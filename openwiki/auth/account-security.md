---
type: security
title: 账号安全能力
description: 密码哈希与强度规则、密码修改与重置、带备用码的 TOTP 双因素认证、加密字段存储、邮箱验证与变更、通知偏好、账号注销以及个人数据导出。
tags: [security, password, 2fa, encryption, email-verification, account-deletion]
verified:
  - by: openwiki/0.7.2
    at: 2026-11-02T17:41:59.592Z
sources:
  - id: openwiki-source-00f455935383887d3bff9d53
    resource: repo://internal/auth/bcrypt.go
  - id: openwiki-source-883d642125c25de6f4b25f17
    resource: repo://internal/auth/fieldcrypto.go
  - id: openwiki-source-c167b655890cdbda616b14d7
    resource: repo://internal/auth/totp.go
  - id: openwiki-source-dd013bc1bb1449fcd2111f58
    resource: repo://internal/handler/twofactor.go
  - id: openwiki-source-98ac6fe2e30d23a1386f80db
    resource: repo://internal/handler/users.go
  - id: openwiki-source-2f0a9f6450f4986a4c7d8914
    resource: repo://internal/service/account.go
  - id: openwiki-source-f7acf9d278285ec2e6b81113
    resource: repo://internal/service/auth_service.go
  - id: openwiki-source-66efbc3c5d8f48ad1fd5a0d0
    resource: repo://internal/service/email_change.go
  - id: openwiki-source-fc787e4ad0e9fcb4f4bf6b66
    resource: repo://internal/service/export.go
  - id: openwiki-source-1994cba008c20be8b9db7693
    resource: repo://internal/service/login.go
  - id: openwiki-source-ca7fc5574740b07d87bb29b5
    resource: repo://internal/service/password.go
generated: { by: "opencode", at: "2026-11-02T17:41:59.592Z" }
---

# 账号安全能力

账号安全主要分布在 `internal/auth`（密码学原语）、`internal/service`（密码、邮箱、账号、导出流程）以及认证 handler（`device.go`、`email.go`、`twofactor.go`、`users.go`）。

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

## 双因素认证

2FA 基于 TOTP，参数必须保持稳定以兼容既有注册：

- HMAC-SHA1、30 秒步长、6 位数字；
- 验证窗口为 ±1 步；
- secret 存储为 20 个随机字节的标准 base64 字符串（160 位）；
- pquerna 的 HOTP 内部期望 base32，因此实现把同样的字节重新编码为 base32，不改变字节；
- 十个备用码，每个是 8 字节随机值的 16 位 hex 字符串。

端点：

| 路由 | 行为 |
|---|---|
| `POST /2fa/enable` | 生成 secret 与备用码，加密存储，返回 secret、备用码与 otpauth URL，但仍保持 2FA 关闭 |
| `POST /2fa/verify-enable` | 要求有效 TOTP，然后设置 `twoFactorEnabled` |
| `POST /2fa/verify` | 校验 TOTP 或备用码；使用过的备用码会被消费 |
| `POST /2fa/disable` | 要求 TOTP 或备用码，若使用备用码则消费，然后关闭 2FA |
| `POST /auth/login-2fa` | 使用 2FA 挑战令牌完成登录（见登录流程） |

otpauth URL 使用 account ID 与 issuer `FurryDrama`。启用、验证与关闭都会写审计记录。备用码比较是普通字符串相等，而登录路径使用常量时间比较。

## 加密字段

敏感字段在应用层加密，而不是由 MongoDB 加密：

- 算法：AES-256-CBC 加 PKCS#7 填充；
- 密钥：设置了 `ENCRYPTION_KEY` 时用 `sha256(ENCRYPTION_KEY)`，否则用 `sha256(JWT_SECRET)`——二选一，绝不拼接；
- 密钥材料以 64 位 hex 字符串传递；
- 输出格式：`enc:<iv hex>:<密文 hex>`，iv 为随机 16 字节；
- 已以 `enc:` 开头的值与空值原样返回，因此加密是幂等的；
- 解密失败时原样返回输入而不是报错。

`EncryptArray`/`DecryptArray` 对元素逐个应用同一方案。加密字段包括 2FA secret、2FA 备用码列表以及遗留的站点内容邮件密码。算法与密钥派生不能改变，否则既有密文无法读取。

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
- **超管强制变更**：`PUT /auth/change-email` 仅限超管并附加 `RequireEmailChanged`。它校验密码，把新地址设为未验证，发送验证码，审计，吊销全部会话并清除 cookie。该流程用于让刚初始化的超管替换默认邮箱。

## 通知偏好

`PUT /auth/email-notification-prefs` 接受七个固定键的任意子集：`episodeUpdate`、`newDeviceLogin`、`feedbackReply`、`friendLinkStatus`、`friendLinkApply`、`announcement`、`reviewResult`。未知键被忽略。空请求体或全部未知返回 400“没有可更新的偏好设置”。响应包含更新后的完整偏好对象。这些偏好只门控邮件发送，站内通知仍会创建。

## 账号注销

注销是“先软后硬”的两步流程：

1. `POST /auth/request-deletion` 写入 `deletionRequestedAt` 并审计。第二次请求返回 400。
2. 用户可以用 `POST /auth/cancel-deletion` 取消，或用 `GET /auth/deletion-status` 查看截止时间。

宽限期为七天。没有定时任务会删除账号。相反，当宽限期后尝试登录时，登录流程会物理删除用户及关联的追番、历史、通知、收藏、评分、举报、反馈与会话，然后返回通用“用户名或密码错误”，不暴露删除事实。这意味着只有账号在宽限期后再次被使用时，删除才会真正完成。

## 个人数据导出

`GET /users/export-my-data` 限流为每小时三次，生成可下载文件，包含：

- `exportDate` 与用户摘要（`_id`、账号 ID、用户名、邮箱、验证状态、头像、创建时间）；
- 追番与收藏，带填充后的剧集标题、封面与状态；
- 评分，带填充后的剧集基础信息；
- 观看历史，带填充后的剧集基础信息与已看集数。

`format=csv` 返回带 BOM 的 UTF-8 CSV；其他值返回 JSON。文件名为 `my_data_<YYYY-MM-DD>.json` 或 `.csv`。

## 资料与头像

`PUT /users/profile` 在 trim 与 20 字符长度校验后更新用户名；空名称返回 400。`POST /users/avatar` 接收最大 2 MB 图片，存在旧头像时删除旧文件，保存新 URL 并返回。

## 相关阅读

<!-- openwiki: broken internal link [/openwiki/architecture/configuration.md] link "/openwiki/architecture/configuration.md" is root-absolute, which no real consumer resolves against the repository root (not a coding agent reading the page, not GitHub's Markdown renderer, not a local viewer); use a path relative to this file instead. Fix the href or restore the target, then delete this comment. -->
- [配置与环境变量](/openwiki/architecture/configuration.md)——加密与 Altcha 密钥来源。
<!-- openwiki: broken internal link [/openwiki/auth/login-and-registration.md] link "/openwiki/auth/login-and-registration.md" is root-absolute, which no real consumer resolves against the repository root (not a coding agent reading the page, not GitHub's Markdown renderer, not a local viewer); use a path relative to this file instead. Fix the href or restore the target, then delete this comment. -->
- [注册与登录流程](/openwiki/auth/login-and-registration.md)——登录时的物理删除与 2FA 分支。
<!-- openwiki: broken internal link [/openwiki/integrations/email-delivery.md] link "/openwiki/integrations/email-delivery.md" is root-absolute, which no real consumer resolves against the repository root (not a coding agent reading the page, not GitHub's Markdown renderer, not a local viewer); use a path relative to this file instead. Fix the href or restore the target, then delete this comment. -->
- [邮件发送](/openwiki/integrations/email-delivery.md)——验证与重置邮件模板。
