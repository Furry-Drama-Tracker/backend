---
type: auth-flow
title: 注册与登录流程
description: 注册与登录的有序校验分支、Altcha 工作量证明、账号锁定、注销清理、邮箱与设备验证、2FA 挑战、会话签发，以及串联这些流程的内存验证码存储。
tags: [auth, login, registration, altcha, device-verification, 2fa]
sources:
  - id: openwiki-source-6ca2ae0d44448102cf549f8a
    resource: repo://internal/code/store.go
  - id: openwiki-source-314bd77139a7d3c62ed804bf
    resource: repo://internal/handler/auth.go
  - id: openwiki-source-f9cd993b1da0967b3ad454b7
    resource: repo://internal/repository/user.go
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
generated: { by: "opencode", at: "2026-11-04T03:17:38.088Z" }
verified:
  - by: openwiki/0.7.2
    at: 2026-11-04T03:17:38.088Z
---

# 注册与登录流程

注册与登录是服务中最敏感的契约路径。分支顺序与状态码被视为固定，因为前端依赖响应标志。`POST /auth/register` 与 `POST /auth/login` 是 `service.AuthService` 之上的薄 handler；下面的顺序就是实际实现顺序。

注册、登录、忘记密码与 2FA 登录都会先对邮箱做 `strings.ToLower(strings.TrimSpace(...))`，因此数据库中的邮箱视为大小写不敏感；新增入口必须复用同样的规范化，否则会查不到既有账号。

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
3. account ID 长度 3–20。
4. account ID 字符：仅字母、数字、下划线。
5. 用户名长度 1–20。
6. 密码强度。
7. 演示邮箱保护：`DEMO_EMAILS` 中的地址不能新注册。
8. 邮箱唯一性——失败返回 `emailTaken: true` 供前端引导。
9. account ID 唯一性。

邮箱在校验前已被 trim 并转为小写，因此 `Foo@Example.com` 与 `foo@example.com` 被视为同一账号。

随后哈希密码、解析 User-Agent、构建设备信息、解析 IP 地域，创建 `role: "user"`、`isEmailVerified: false`、带默认通知与背景偏好、空壁纸数组的用户。六位验证码被存储并异步发送邮件。

成功返回 200 与 `{message, email, needVerification: true}`。重复键竞态映射为 400“该信息已被使用”。

## 登录分支顺序

`service.Login` 严格按以下顺序执行：

1. **Altcha**——失败返回 400“验证码错误或已过期”。
2. **按邮箱查找账号**——邮箱不存在时返回通用 400“用户名或密码错误”，不再附带 `accountNotFound`，避免账号枚举。
3. **锁定检查**——返回通用凭据错误而不是锁定专用消息，避免暴露锁定状态。
4. **注销宽限期**——如果已过七天删除截止时间，调用 `PurgeUserData` 做级联物理清理，然后返回通用凭据错误。如果清理失败，流程继续，因此清理失败不会阻塞登录。
5. **密码校验**——失败递增 `loginAttempts` 并返回通用 400。
6. **重置失败计数**——密码正确时清零。
7. **邮箱验证**——未验证（且未跳过）时生成并发送验证码，结果为 `NeedVerification`。
8. **演示/跳过自动验证**——开发环境中演示邮箱自动标记为已验证。
9. **新设备检测**——如果没有任何活跃会话使用相同 User-Agent，生成并发送登录码，结果为 `NeedDeviceVerify`，并携带解析出的浏览器、操作系统与 IP。这里不再要求“用户已有会话”；只要邮件服务可用，任何未识别设备都要验证。SMTP 未配置时（`GetConfig` 失败）退化为仅密码登录，避免整站无法登录。
10. **2FA**——已启用时签发五分钟的 `2fa-challenge` 令牌并返回 `Need2FA`。
11. **成功**——保存设备信息、最后登录时间/IP/地域，签发会话，并在启用时异步发送新设备通知邮件。

handler 把结果映射为响应：

| 结果 | 状态码 | 响应信号 |
|---|---|---|
| `User != nil` | 200 | 会话用户对象 |
| `NeedVerification` | 403 | `needVerification`、`email` |
| `NeedDeviceVerify` | 403 | `needDeviceVerify`、`email`、`deviceInfo` |
| `Need2FA` | 200 | `need2FA`、`email`、`twoFactorChallenge` |

2FA 挑战保持 200 而其他待处理分支为 403，这是有意设计。

## 账号锁定

`users.loginAttempts` 与 `users.lockUntil` 实现锁定。默认五次尝试、30 分钟，可通过 `[security]` 配置。`repository.IsLocked` 把 `lockUntil`（Unix 毫秒）与当前时间比较。`IncLoginAttempts` 递增并设置锁定；如果已锁定，则重置为一次并清除锁定，这是防御分支。

同一锁定也适用于 2FA 登录和邮箱变更请求（423）；管理员与普通用户共用同一个登录入口，因此也走这里的通用凭据错误，而不再有返回 423 的独立后台登录分支。修改密码时输入错误的当前密码同样会递增 `loginAttempts`，成功后才清零，因此反复猜当前密码也会触发锁定。

## 设备验证链

检测到新设备时：

1. 登录返回 403、`needDeviceVerify`，并发送设备码邮件。
2. 邮件包含带 `device-verify` 令牌的链接。`POST /auth/verify-device` 通过 `usedtokens` 检查一次性使用，校验 purpose，把令牌标记为已用 30 分钟，并生成绑定用户的六位设备登录码。
3. `POST /auth/confirm-device-login` 查找六位码，最多允许五次尝试，然后要么返回 2FA 挑战（账号启用 2FA 时，挑战携带设备码），要么保存设备信息并签发会话完成登录。

设备登录码十分钟后过期。已使用或过期的码会被删除。需要 2FA 时，挑战携带设备码，以便 2FA 成功后删除它。

## 2FA 登录

`POST /auth/login-2fa` 接收邮箱、挑战令牌、TOTP 或备用码，以及可选设备信息。它校验令牌 purpose，加载带 2FA 机密的用户，确认挑战 subject 与用户匹配，重新检查锁定与邮箱验证，解密 secret 与备用码，并接受有效 TOTP 或备用码。使用过的备用码会被移除并重新加密剩余数组。失败会递增尝试次数，达到阈值时可能返回 423。成功保存设备信息并签发会话。

## 验证码存储

邮箱验证、设备登录、密码重置与邮箱变更都使用 `internal/code.Store`：

- 进程内 map，以六位随机码为键；
- 每条包含用户 ID、可选邮箱、过期时间、尝试次数与可选 2FA 标志；
- 默认 TTL 十分钟；
- 60 秒清理 goroutine 删除过期条目；
- 调用方负责尝试上限与消费后删除。

这是单实例状态。多实例会生成只存在于某一个进程的验证码，因此水平扩展需要用共享实现替换该存储。存储已接口化，但仓库中没有 Redis 实现。

## 会话签发

所有成功登录路径最终汇聚到 `IssueSession`：签发 15 分钟 access 令牌（带随机 `jti`）、带随机 `jti` 的 7 天 refresh 令牌，把 access 与 refresh 的 SHA-256 哈希连同设备信息与 IP 写入 `usersessions` 文档，设置鉴权 cookie，并异步写审计记录。access 令牌的 `tokenHash` 让中间件可以在会话被吊销后拒绝仍然有效的 access 令牌。

## 相关阅读

- [会话与令牌生命周期](session-and-token-lifecycle.md)——轮换与重用检测。
- [账号安全能力](account-security.md)——密码、2FA 与邮箱变更细节。
- [外部服务](../integrations/external-services.md)——Altcha 与 IP 地域行为。
