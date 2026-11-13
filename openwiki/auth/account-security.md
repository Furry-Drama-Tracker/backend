---
type: security
title: 账号安全能力
description: 密码哈希与强度规则、密码修改与重置、角色化 2FA（TOTP/邮箱 OTP/备份码、Base32 展示格式、管理员强制与无凭据恢复）、Passkey/WebAuthn 注册与单因素登录、加密字段存储、邮箱验证与变更、账号注销的定点写入以及个人数据导出。
tags: [security, password, 2fa, passkey, webauthn, encryption, email-verification, account-deletion]
sources:
  - id: openwiki-source-d32d831ce74c9c76796fd705
    resource: repo://internal/auth/backupcode.go
  - id: openwiki-source-00f455935383887d3bff9d53
    resource: repo://internal/auth/bcrypt.go
  - id: openwiki-source-883d642125c25de6f4b25f17
    resource: repo://internal/auth/fieldcrypto.go
  - id: openwiki-source-c167b655890cdbda616b14d7
    resource: repo://internal/auth/totp.go
  - id: openwiki-source-95acc6f0848493b78312ab3f
    resource: repo://internal/cron/cron.go
  - id: openwiki-source-945636a752d43231c84fd6af
    resource: repo://internal/handler/passkey.go
  - id: openwiki-source-dd013bc1bb1449fcd2111f58
    resource: repo://internal/handler/twofactor.go
  - id: openwiki-source-98ac6fe2e30d23a1386f80db
    resource: repo://internal/handler/users.go
  - id: openwiki-source-b5df54b08f2ac1045e8ab7c9
    resource: repo://internal/repository/session.go
  - id: openwiki-source-2f0a9f6450f4986a4c7d8914
    resource: repo://internal/service/account.go
  - id: openwiki-source-f7acf9d278285ec2e6b81113
    resource: repo://internal/service/auth_service.go
  - id: openwiki-source-219229851f95542ab08870c4
    resource: repo://internal/service/device.go
  - id: openwiki-source-66efbc3c5d8f48ad1fd5a0d0
    resource: repo://internal/service/email_change.go
  - id: openwiki-source-fc787e4ad0e9fcb4f4bf6b66
    resource: repo://internal/service/export.go
  - id: openwiki-source-1994cba008c20be8b9db7693
    resource: repo://internal/service/login.go
  - id: openwiki-source-540faaa12781d81a1f3eb2aa
    resource: repo://internal/service/passkey.go
  - id: openwiki-source-ca7fc5574740b07d87bb29b5
    resource: repo://internal/service/password.go
  - id: openwiki-source-d97c9077329c5637a8dbc18d
    resource: repo://internal/service/purge.go
generated: { by: "zcode", at: "2026-11-13T18:41:36.261Z" }
verified:
  - by: openwiki/0.7.2
    at: 2026-11-13T18:41:36.261Z
---

# 账号安全能力

账号安全主要分布在 `internal/auth`（密码学原语）、`internal/service`（密码、邮箱、账号、登录名、Passkey、导出流程）以及认证 handler（`device.go`、`email.go`、`twofactor.go`、`users.go`、`passkey.go`）。

## 密码哈希与强度

密码使用固定 cost 12 的 bcrypt 哈希，存储为标准 `$2a$` 前缀。`auth.Compare` 使用 bcrypt 的常量时间比较，非法哈希返回 false。旧实现的 `$2a$` 与 `$2b$` 哈希可原样校验。

密码强度由固定函数强制，消息也是固定的中文文案：

1. 长度至少 8 个 UTF-16 码元（代理对计 2，匹配 JavaScript `.length`）；
2. 至少一个 ASCII 字母；
3. 至少一个 ASCII 数字。

“密码长度至少8位”、“密码必须包含至少一个字母”、“密码必须包含至少一个数字”是契约字符串，不得本地化或改写。

`service.ValidatePassword` 与 `security.ValidatePassword` 都存在且规则相同；注册与管理员创建账号使用 service 版本。

## 密码修改与重置

`PUT /api/auth/change-password` 需要认证与当前密码。成功后依次：

1. 校验新密码强度；
2. 校验当前密码；
3. 写入新哈希与 `passwordChangedAt`；
4. 吊销该用户全部会话；
5. 清除鉴权 cookie；
6. 写入审计记录。

`POST /api/auth/forgot-password` 校验 Altcha 后，无论账号是否存在都返回同一消息——“如果该邮箱已注册，重置链接已发送至邮箱”。存在账号时签发一小时有效的 `reset-password` 令牌并发送链接。

`POST /api/auth/reset-password` 校验令牌 purpose，通过 `usedtokens` 集合拒绝已使用令牌，校验新密码，标记令牌已用，更新哈希，吊销全部会话并审计。过期、格式非法、purpose 错误与重复使用的令牌都返回 400，但消息不同。

## 角色化 2FA 策略

2FA 是创作者及以上才开放的能力，策略由角色对比落地：

- **普通用户禁开**：`POST /2fa/enable` 与 `POST /2fa/verify-enable` 对 `role=user` 直接 403「普通用户不可开启两步验证」；
- **管理员强制**：admin/superadmin 必须开启 2FA——登录/refresh/`/me` 响应携带 `force2FASetup` 标记，前端拦截强制设置流程；同时 `POST /2fa/disable` 对 admin/superadmin 返回 403「管理员账号强制开启两步验证，不可自行关闭」。管理员丢失验证器被锁在强制设置流程外时，只能用 `cmd/useradmin clear-2fa` 带外恢复（清 2FA + 吊销全部会话，重新走强制设置）；
- **降级联动**：后台 `UpdateRole` 把开启 2FA 的账号降级为 `user` 时自动关闭 2FA、吊销全部会话并写 `ROLE_DOWNGRADE_2FA_OFF` 审计（见管理与审核 API 页）。

## 双因素认证（TOTP / 备份码 / 邮箱 OTP）

2FA 基于 TOTP，参数必须保持稳定以兼容既有注册：

- HMAC-SHA1、30 秒步长、6 位数字；
- 验证窗口为 ±1 步；
- secret 存储为 20 个随机字节的标准 base64 字符串（160 位）；
- pquerna 的 HOTP 内部期望 base32，因此实现把同样的字节重新编码为 base32，不改变字节；
- 十个备用码，每个是 8 字节随机值的 16 位 hex 字符串。

**Base32 展示格式**：otpauth URL 的 `secret` 参数按 otpauth:// 规范必须是 RFC 4648 Base32（无填充大写）。内部存储格式保持标准 base64（既有格式勿改），`POST /2fa/enable` 在响应中同时返回 `secret`（base64，既有字段，保持兼容）与 `secretBase32`（2FA 工具识别二维码与手动输入用的格式），otpauth URL 使用转换后的 Base32 与 issuer `FurryDrama`。

端点：

| 路由 | 行为 |
|---|---|
| `POST /2fa/enable` | **角色门控**（user 403）；**已启用时返回 400**；未启用时生成 secret 与备用码，secret 加密、备用码哈希存储，返回 `secret`/`secretBase32`/`backupCodes`/`otpauthUrl`，但仍保持 2FA 关闭 |
| `POST /2fa/verify-enable` | 角色门控；要求有效 TOTP，然后设置 `twoFactorEnabled` |
| `POST /2fa/verify` | 校验 TOTP 或备用码；使用过的备用码会被消费（剩余数组以哈希格式落库） |
| `POST /2fa/disable` | 角色门控（admin/superadmin 403）；要求 TOTP 或备用码；**无凭据恢复通道**（见下） |
| `POST /auth/login-2fa` | 使用 2FA 挑战令牌完成登录，`method` 决定第二因素：`totp`（默认，TOTP 码或备份码）或 `emailOtp`（邮箱验证码） |
| `POST /auth/2fa/email-otp/send` | 凭 2FA 挑战令牌向账号邮箱发送第二因素验证码（仅已验证邮箱的账号） |

### 邮箱 OTP 第二因素

`emailOtp` 是用户系统重构新增的第二因素方法。登录响应 `Need2FA` 附带 `twoFactorMethods` 列表告知前端可用方法：`totp` 恒有（TOTP 密钥在 2FA 开启时必已注册），`emailOtp` 仅邮箱已验证时可用。

- 发送（`Send2FAEmailOTP`）校验 `2fa-challenge` 挑战令牌、2FA 已启用与邮箱已验证，然后向独立的 `EmailOTPCodes` store 写入六位码并发送邮件。该 store 与注册/登录验证码存储共享六位命名空间但独立实例，避免同码跨流程误消费；签发走 `code.Store.Mint`，保证不覆盖任何未过期条目（ADR-019）。
- 校验（`consumeEmailOTP`）一次性消费并限五次尝试；码属于别的账号时按校验失败处理，不消费、不留痕（防跨账号枚举）。
- 任一方法的校验失败都走 `rejectTwoFactor`：递增 `loginAttempts`，达到阈值返回 423 锁定。

### 2FA 无凭据恢复通道（ADR-019）

`/2fa/disable` 在存储的 secret 与备份码均为空（配置完整性损坏，如历史版本曾对投影加载的 user 做整文档 Save 把 `twoFactorSecret` 清空）时，允许持有效会话的账户所有者直接关闭 2FA 重新启用，审计事件注明 "no verifiable credential stored"；正常账户（secret 或备份码非空）行为不变，仍必须通过 TOTP/备份码校验。此前该状态只能靠 pquerna hotp 对空密钥的 HMAC 恰好校验通过的“意外后门”恢复。

### 2FA 加固（ADR-014，响应字段不变）

- **enable 不再降级**：旧实现不检查状态，对已启用账号调用 `/2fa/enable` 会把 `twoFactorEnabled` 写回 `false`——一个有效会话即可在无 TOTP 的情况下关闭 2FA，比要求验证码的 `/2fa/disable` 更弱。现在已启用时直接 400，重置必须先走 `/2fa/disable`。
- **备份码单向哈希存储**：`auth.HashBackupCodes` 以 `bc:<sha256 hex>` 前缀存储（备份码是 64 位高熵随机值，快哈希即可；bcrypt 会让单次登录多出数秒）。旧数据（AES 密文 `enc:` 或明文）在首次命中时由 `auth.ConsumeBackupCode` 整组就地升级为哈希格式，比对使用常量时间比较。
- **TOTP 一次性消费**：`auth.VerifyTOTPStep` 返回命中的时间步（窗口顺序 `-1 → 0 → +1` 固定契约，未命中返回 -1），登录成功后 `users.twoFactorLastStep` 以原子条件更新标记该步；同一步重复提交按校验失败处理并记录 `twofa_replay` 事件（NIST SP 800-63B 一次性 OTP）。仅作用于 `/auth/login-2fa`，避免“开启 2FA 用的码不能立刻用于登录”的体验倒退。

## Passkey（WebAuthn）

Passkey 是用户系统重构新增的认证方式（`internal/service/passkey.go`、`internal/handler/passkey.go`、`passkeys` 集合）：

| 路由 | 行为 |
|---|---|
| `POST /passkeys/register/options` | protect + **密码再认证**（`reauthPassword`）：生成注册仪式 options，返回 `publicKey` 与一次性 `ceremonyKey` |
| `POST /passkeys/register/verify?ceremonyKey=&name=` | 完成注册仪式，落库凭据（credentialId base64url 唯一、COSE 公钥、attestation 类型、传输方式、签名计数器、备份状态） |
| `GET /passkeys` | 列出当前用户的凭据 |
| `DELETE /passkeys/:id` | 删除凭据（属主校验在 repo filter） |
| `POST /auth/passkey/options` | 开始登录仪式；`identifier` 为空走**可发现凭据（无用户名）模式**，非空限定用户 |
| `POST /auth/passkey/verify` | 完成登录仪式并签发会话 |

机制要点：

- **WebID = UID**：WebAuthn user handle 是 `users.uid` 的大端 8 字节编码，绑定在用户侧而非凭据侧；`WebAuthnName` 使用 `accountId`。RP ID/名称/允许来源由 `config.Load` 兜底推导（见配置页），WebAuthn 引擎构造失败仅告警不致命——Passkey 路由回 503，其余认证方式不受影响。
- **仪式会话**是进程内 `ceremonyStore`，有效期 5 分钟（`PasskeyCeremonyTTL`），key 为随机 ceremony ID，取用即删（超期未完成的仪式作废）。与验证码存储一样是单实例状态，多实例部署需换共享存储。
- **单因素登录**：Passkey 登录直接通过，即使账号开启 2FA 也不再挑战第二因素（抗钓鱼的硬件级因素视为足够）。通过后走共享门槛：注销宽限检查、邮箱未验证 → 发送验证码并返回 `NeedVerification`；随后签发会话（审计事件 `LOGIN_PASSKEY`）并对未识别设备发提醒邮件。
- 仪式限流为 `PasskeySpec`（10 次/15 分钟，IP 键）。

## 加密字段

敏感字段在应用层加密，而不是由 MongoDB 加密：

- 算法：AES-256-CBC 加 PKCS#7 填充；
- 密钥：设置了 `ENCRYPTION_KEY` 时用 `sha256(ENCRYPTION_KEY)`，否则用 `sha256(JWT_SECRET)`——二选一，绝不拼接；
- 密钥材料以 64 位 hex 字符串传递；
- 输出格式：`enc:<iv hex>:<密文 hex>`，iv 为随机 16 字节；
- 已以 `enc:` 开头的值与空值原样返回，因此加密是幂等的；
- 解密失败时原样返回输入而不是报错。

`EncryptArray`/`DecryptArray` 对元素逐个应用同一方案。加密字段包括 2FA secret、2FA 备份码列表以及遗留的站点内容邮件密码。算法与密钥派生不能改变，否则既有密文无法读取。

## 邮箱验证

`POST /api/auth/verify-email` 消费内存验证码存储中的六位验证码：

- 验证码十分钟后过期；
- 超过五次失败尝试会删除验证码；
- 可选的 email 参数必须与验证码条目匹配；
- 用户当前邮箱必须仍等于签发验证码时捕获的邮箱；
- 成功后把邮箱标记为已验证并删除验证码。

登录用户（`POST /auth/resend-verification`）与带 Altcha 的邮箱方式（`POST /auth/resend-verification-by-email`）都可以重发。后者始终返回统一消息，避免泄露账号是否存在。SMTP 未配置时，登录用户重发路径返回“邮件服务未配置，请联系管理员”。

## 邮箱变更

有两条不同流程：

- **普通用户**：`POST /auth/request-email-change` 需要当前密码与 Altcha，检查锁定状态，拒绝相同地址或已被使用的地址，并签发一小时有效、携带新地址的 `email-change` 令牌。确认链接发送到新地址。`POST /auth/verify-email-change` 校验 purpose，通过 `usedtokens` 强制一次性使用，重新检查唯一性，把新地址设为已验证，并吊销全部会话。
- **超管强制变更**：`PUT /auth/change-email` 仅限超管并附加 `RequireEmailChanged`。它校验密码，用定点更新 `SetEmailPendingVerification` 把新地址设为未验证（不再整文档替换，避免清空 2FA 密文），发送验证码，审计，吊销全部会话并清除 cookie。该流程用于让刚初始化的超管替换默认邮箱。

## 通知偏好

`PUT /auth/email-notification-prefs` 接受七个固定键的任意子集：`episodeUpdate`、`newDeviceLogin`、`feedbackReply`、`friendLinkStatus`、`friendLinkApply`、`announcement`、`reviewResult`。未知键被忽略。空请求体或全部未知返回 400“没有可更新的偏好设置”。响应包含更新后的完整偏好对象。这些偏好只门控邮件发送，站内通知仍会创建。

## 账号注销

注销是“先软后硬”的两步流程：

1. `POST /auth/request-deletion` 写入 `deletionRequestedAt` 并审计（需当前密码再认证）。第二次请求返回 400。
2. 用户可以用 `POST /auth/cancel-deletion` 取消，或用 `GET /auth/deletion-status` 查看截止时间。

两步都只**定点写** `deletionRequestedAt` 单字段（`UserRepo.SetDeletionRequestedAt`，`$set`/写 null），不再对中间件投影加载的 user 做整文档 Save——`publicProjection` 不含 `password/twoFactorSecret/twoFactorBackupCodes/loginAttempts/lockUntil`，整文档替换会把它们覆写成零值，一次“申请注销”就会静默销毁凭据（ADR-019）。写入失败如实返回 500（旧实现吞错返回 200 但未落库）。

宽限期为七天，有两条路径会真正删除：

1. **定时任务**：`cron.StartSessions` 每 6 小时执行一次注销清理；为避免升级/重启瞬间批量删除历史申请，进程启动后第一次执行延迟 6 小时。
2. **宽限期后登录**：登录流程发现已过期，会先物理删除再返回通用“用户名或密码错误”，不暴露删除事实。

两条路径都调用 `service.PurgeUserData` 做 best-effort 级联清理：追番、历史、通知、收藏、评分、举报、反馈、会话、文件夹、收藏夹收藏项、创作者主页、主题与推送订阅；删除评分后重算受影响剧集的平均分。用户创建的剧集等公共内容保留，因为可能涉及其他协作者。单个集合清理失败不阻断后续步骤，最终以用户删除是否成功为准。

## 用户名（登录名）改名

`PUT /api/users/login-name`（密码再认证 + 规则校验 + 折叠占用检查 + 改名冷却 + 旧名保留期）与 `GET /api/users/check-login-name` 预检的完整流程记录在[工具与上传 API](../api/utilities-and-uploads.md)；改名本身是安全敏感操作：密码再认证使用带哈希的重载（中间件公开投影不含 password），成功后写 `LOGIN_NAME_CHANGED` 审计事件。

## 个人数据导出

`GET /users/export-my-data` 限流为每小时三次，生成可下载文件，包含：

- `exportDate` 与用户摘要（`_id`、UID、账号 ID、用户名、邮箱、验证状态、头像、创建时间）；
- 追番与收藏，带填充后的剧集标题、封面与状态；
- 评分，带填充后的剧集基础信息；
- 观看历史，带填充后的剧集基础信息与已看集数。

`format=csv` 返回带 BOM 的 UTF-8 CSV；其他值返回 JSON。文件名为 `my_data_<YYYY-MM-DD>.json` 或 `.csv`。

## 资料与头像

`PUT /users/profile` 在 trim 与 20 字符长度校验后更新用户名；空名称返回 400。`POST /users/avatar` 接收最大 2 MB 图片，存在旧头像时删除旧文件，保存新 URL 并返回。

## 相关阅读

- [配置与环境变量](../architecture/configuration.md)——加密与 Altcha 密钥来源、WebAuthn RP 推导。
- [注册与登录流程](login-and-registration.md)——登录标识符、设备令牌与 2FA 分支。
- [工具与上传 API](../api/utilities-and-uploads.md)——用户名改名完整流程。
- [管理与审核 API](../api/administration-and-moderation.md)——降级自动关闭 2FA 与 useradmin CLI。
- [邮件发送](../integrations/email-delivery.md)——验证与重置邮件模板。
