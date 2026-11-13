---
type: auth-flow
title: 注册与登录流程
description: 注册与登录的有序校验分支、Altcha 工作量证明、登录标识符折叠（用户名/邮箱）、UID 发号、账号锁定、注销清理、验证码 Mint 防覆盖、带邮箱 OTP 方法的 2FA 挑战、设备令牌判定的新设备提醒，以及串联这些流程的内存验证码存储。
tags: [auth, login, registration, altcha, identifier, 2fa, email-otp, device-token]
sources:
  - id: openwiki-source-6ca2ae0d44448102cf549f8a
    resource: repo://internal/code/store.go
  - id: openwiki-source-314bd77139a7d3c62ed804bf
    resource: repo://internal/handler/auth.go
  - id: openwiki-source-f9cd993b1da0967b3ad454b7
    resource: repo://internal/repository/user.go
  - id: openwiki-source-ebbcecfb3e8c07ba5e940ccd
    resource: repo://internal/server/register_routes.go
  - id: openwiki-source-f7acf9d278285ec2e6b81113
    resource: repo://internal/service/auth_service.go
  - id: openwiki-source-219229851f95542ab08870c4
    resource: repo://internal/service/device.go
  - id: openwiki-source-1994cba008c20be8b9db7693
    resource: repo://internal/service/login.go
  - id: openwiki-source-ca7fc5574740b07d87bb29b5
    resource: repo://internal/service/password.go
  - id: openwiki-source-3f2309f0c3b4e4f8068472e9
    resource: repo://internal/service/register.go
generated: { by: "zcode", at: "2026-11-13T18:41:36.261Z" }
verified:
  - by: openwiki/0.7.2
    at: 2026-11-13T18:41:36.261Z
---

# 注册与登录流程

注册与登录是服务中最敏感的契约路径。分支顺序与状态码被视为固定，因为前端依赖响应标志。`POST /auth/register` 与 `POST /auth/login` 是 `service.AuthService` 之上的薄 handler；下面的顺序就是实际实现顺序。

登录标识符（`identifier` 字段）支持用户名（`accountId`）与邮箱：含 `@` 按邮箱查找（大小写不敏感），否则按 `accountIdLower` 折叠小写查找（GitHub 式大小写策略——存储保留原样，登录/唯一性按折叠判定）。登录前统一 `strings.ToLower(strings.TrimSpace(...))`，兼容旧客户端的 `email` 字段回退。

## Altcha 工作量证明

注册与登录在一切之前先校验 Altcha payload。校验辅助函数：

- 配置了 `DEV_API_TOKEN` 且 `x-dev-token` 头匹配时接受开发绕过；
- base64 解码 payload 并反序列化 challenge/solution；
- 用配置的 HMAC 密钥以 SHA-256 校验 solution。

挑战由 `altcha-lib-go` 生成：`SHA-256`、cost `10000`、密钥长度 32、密钥前缀 `"00"`、五分钟过期。HMAC 密钥在配置了 `ALTCHA_HMAC_KEY` 时使用它，否则使用 `"altcha-" + JWT_SECRET` 的 hex SHA-256。这些参数与前端求解器共享，不能修改。

## 注册顺序

`POST /auth/register` 按以下顺序校验，首个失败返回 400：

1. Altcha。
2. 邮箱正则。
3. **用户名（accountId）规则**（`ValidateLoginName`）：3–32 字符，`^[A-Za-z_][A-Za-z0-9_-]{2,31}$`（首字符字母或下划线）；显式拒绝 `.`/`..`；内置黑名单子串匹配（admin/root/system/api 等，可与 `[security].username_blacklist` 合并）；保留名精确匹配（me/self/login 等）。存储保留原样大小写，唯一性按折叠小写判定。
4. 昵称长度 1–20。
5. 密码强度。
6. 演示邮箱保护：`DEMO_EMAILS` 中的地址不能新注册（仅当该邮箱尚不存在）。
7. 邮箱唯一性——失败返回 `emailTaken: true` 供前端引导。
8. 账号 ID 唯一性（折叠小写比较）。
9. **旧名保留期**：`loginnamereservations` 中处于保留期（默认 90 天）的旧用户名不可注册（按折叠小写匹配，注册者无自身豁免），防止改名释放的旧名被冒充。

随后哈希密码、从 `counters` 集合原子发号 UID（首个用户/seed 账号为 0，普通注册从计数器取号）、解析 User-Agent、构建设备信息、解析 IP 地域（默认禁用态下记为「未知」），创建 `role: "user"`、`isEmailVerified: false`、带默认通知与背景偏好、空壁纸数组的用户——`UserRepo.Create` 在 `createdAt` 为零值时补当前时间（UTC 毫秒对齐），因此注册不会再把 `ISODate("0001-01-01")` 落库。六位验证码经 `Mint` 写入并异步发送邮件。

成功返回 200 与 `{message, email, needVerification: true}`。重复键竞态映射为 400“该信息已被使用”。

## 登录分支顺序

`service.Login` 严格按以下顺序执行：

1. **Altcha**——失败返回 400“验证码错误或已过期”。
2. **按标识符查找账号**——用户名折叠或邮箱；不存在时返回通用 400“用户名或密码错误”，不附带 `accountNotFound`，避免账号枚举。
3. **锁定检查**——返回通用凭据错误而不是锁定专用消息，避免暴露锁定状态。
4. **注销宽限期**——如果已过七天删除截止时间，调用 `PurgeUserData` 做级联物理清理，然后返回通用凭据错误。如果清理失败，流程继续，因此清理失败不会阻塞登录。
5. **密码校验**——失败递增 `loginAttempts` 并返回通用 400。
6. **重置失败计数**——密码正确时清零。
7. **邮箱验证**——未验证（且未跳过）时经 `Mint` 生成并发送验证码，结果为 `NeedVerification`。
8. **演示/跳过自动验证**——开发环境中演示邮箱自动标记为已验证（`UpdateVerified` 定点更新，避免整文档替换清空 2FA 密文）。
9. **2FA**——已启用时签发五分钟的 `2fa-challenge` 令牌并返回 `Need2FA`，响应附带 `twoFactorMethods` 列表（`totp` 恒有；`emailOtp` 仅邮箱已验证时可用）。
10. **成功**——定点更新登录态字段（`UpdateDeviceInfoAndLogin`：设备信息、最后登录时间/IP/地域），签发会话，并在未识别设备时异步发送新设备提醒邮件。

**新设备不再拦截登录**（2026-11 重构变更）：旧实现的“新设备 → 设备验证码邮件 → `verify-device`/`confirm-device-login` 确认链”已整体移除（两个路由也已删除）。现在新设备只在登录成功后收到一封提醒邮件（受 `emailNotificationPrefs.newDeviceLogin` 控制），是事后提醒而非屏障。“已知设备”由服务端签发的设备令牌判定（见下节）。

handler 把结果映射为响应：

| 结果 | 状态码 | 响应信号 |
|---|---|---|
| `User != nil` | 200 | 会话用户对象 |
| `NeedVerification` | 403 | `needVerification`、`email` |
| `Need2FA` | 200 | `need2FA`、`email`、`twoFactorChallenge`、`twoFactorMethods` |

2FA 挑战保持 200 而验证分支为 403，这是有意设计。

## 设备令牌与新设备提醒

已知设备的唯一信任依据是服务端签发的设备令牌：

- `IssueSession` 在每次登录成功时生成 256 位随机设备令牌，写入会话文档的 `deviceTokenHash`（sha256），并设置 httpOnly 的 `fdt-device` cookie（secure=生产、sameSite=lax、365 天）。
- `isKnownDevice` 把请求 cookie 的哈希与该用户全部活跃会话的 `deviceTokenHash` 比对；未匹配时发新设备提醒邮件。
- User-Agent 客户端完全可控、可枚举重放（CWE-290），只作展示字段，不再作为信任依据。旧会话无 `deviceTokenHash` 时不匹配（fail-closed），下次登录会收到一次提醒邮件——这是一次性的预期摩擦。

## 账号锁定

`users.loginAttempts` 与 `users.lockUntil` 实现锁定。默认五次尝试、30 分钟，可通过 `[security]` 配置。`repository.IsLocked` 把 `lockUntil`（Unix 毫秒）与当前时间比较。`IncLoginAttempts` 递增并设置锁定；如果已锁定，则重置为一次并清除锁定，这是防御分支。

同一锁定也适用于 2FA 登录和邮箱变更请求（423）；管理员与普通用户共用同一个登录入口，因此也走这里的通用凭据错误，而不再有返回 423 的独立后台登录分支。修改密码时输入错误的当前密码同样会递增 `loginAttempts`，成功后才清零，因此反复猜当前密码也会触发锁定。

## 2FA 登录

`POST /auth/login-2fa` 接收 `identifier`（用户名或邮箱，兼容旧 `email` 字段）、`twoFactorChallenge`、`method` 与 `twoFactorToken`。它校验挑战令牌（五分钟、purpose `2fa-challenge`），按折叠标识符加载带 2FA 敏感字段的用户，确认挑战 subject 与用户匹配，重新检查锁定与邮箱验证，然后按 `method` 校验第二因素：

- **`emailOtp`**——校验并一次性消费独立 `EmailOTPCodes` store 中的六位码（限五次尝试，码属他人按失败处理不留痕）。
- **`totp`（默认）**——解密 secret 后接受有效 TOTP 或备份码。TOTP 命中的时间步通过 `users.twoFactorLastStep` 原子标记做一次性消费——同一步重复提交按校验失败处理并记录 `twofa_replay` 诊断事件（仅作用于本端点，“开启 2FA 用的码不能立刻用于登录”的体验不受影响）。使用过的备用码由 `auth.ConsumeBackupCode` 移除，剩余数组就地升级为哈希格式落库。

任一方法失败都会递增尝试次数（`rejectTwoFactor`），达到阈值时返回 423。成功后登录态字段定点更新（`UpdateDeviceInfoAndLogin`，备份码消费单独定点落库），签发会话并按设备令牌判定发提醒邮件。

## Passkey 登录

`POST /auth/passkey/options` + `POST /auth/passkey/verify` 提供 Passkey 登录仪式（identifier 可空走可发现凭据模式）。Passkey 是单因素登录——即使账号开启 2FA 也不再挑战第二因素；通过后仍走注销宽限与邮箱验证门槛。完整机制见[账号安全能力](account-security.md)。

## 验证码存储

邮箱验证、密码重置与邮箱变更使用 `internal/code.Store`：

- 进程内 map，以六位随机码为键；
- 每条包含用户 ID、可选邮箱、过期时间、尝试次数与可选 2FA 标志；
- 默认 TTL 十分钟；
- 60 秒清理 goroutine 删除过期条目；
- 调用方负责尝试上限与消费后删除。

**签发走 `Mint`/`SetFresh`（ADR-019）**：六位码是全用户共享的 900k 命名空间，旧的 `Set` 会静默覆盖同码未过期条目，把攻击者已知的 code 解析到别的账号（设备码链可据此接管）；`SetFresh` 在同码已有未过期条目时拒绝，`Mint` 循环生成直到无冲突（连续 100 次冲突回退 `Set` 保可用性）。原地写回（尝试计数等）仍走 `Set`。

认证服务持有两个独立 store 实例：`EmailVerifyCodes`（注册/登录验证码）与 `EmailOTPCodes`（2FA 邮件 OTP 第二因素）——共享六位命名空间但独立实例，避免同码跨流程误消费。

这是单实例状态。多实例会生成只存在于某一个进程的验证码，因此水平扩展需要用共享实现替换该存储。存储已接口化，但仓库中没有 Redis 实现。

## 会话签发

所有成功登录路径最终汇聚到 `IssueSession`：签发 15 分钟 access 令牌（带随机 `jti`）、带随机 `jti` 的 7 天 refresh 令牌，把 access 与 refresh 的 SHA-256 哈希连同设备令牌哈希（`deviceTokenHash`）、设备信息与 IP 写入 `usersessions` 文档，设置鉴权 cookie 与 `fdt-device` 设备 cookie，并异步写审计记录。access 令牌的 `tokenHash` 让中间件可以在会话被吊销后拒绝仍然有效的 access 令牌。

## 相关阅读

- [会话与令牌生命周期](session-and-token-lifecycle.md)——轮换与重用检测、设备令牌。
- [账号安全能力](account-security.md)——角色化 2FA 策略、Passkey 与邮箱 OTP 细节。
- [外部服务](../integrations/external-services.md)——Altcha 与 IP 地域行为。
