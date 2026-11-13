---
type: auth-flow
title: 会话与令牌生命周期
description: 双令牌模型、JWT claims 与 purpose、cookie 路径、带吊销原因墓碑的会话存储、refresh 轮换与 2 分钟宽限期重用检测、refresh 失败认证事件、419/409/401 语义、登出吊销以及会话管理端点。
tags: [auth, jwt, refresh, sessions, cookies]
sources:
  - id: openwiki-source-d30e65659972429c2c50cf5a
    resource: repo://internal/auth/cookies.go
  - id: openwiki-source-be8c32ef9eb79a8172d00695
    resource: repo://internal/auth/jwt.go
  - id: openwiki-source-95acc6f0848493b78312ab3f
    resource: repo://internal/cron/cron.go
  - id: openwiki-source-314bd77139a7d3c62ed804bf
    resource: repo://internal/handler/auth.go
  - id: openwiki-source-3f2beed769b653e945288445
    resource: repo://internal/model/authaudit.go
  - id: openwiki-source-c04b809e997b6644e3320acf
    resource: repo://internal/model/model.go
  - id: openwiki-source-b5df54b08f2ac1045e8ab7c9
    resource: repo://internal/repository/session.go
  - id: openwiki-source-f7acf9d278285ec2e6b81113
    resource: repo://internal/service/auth_service.go
  - id: openwiki-source-27784338c57fd749bd43470e
    resource: repo://internal/service/refresh.go
generated: { by: "zcode", at: "2026-11-13T18:41:36.261Z" }
verified:
  - by: openwiki/0.7.2
    at: 2026-11-13T18:41:36.261Z
---

# 会话与令牌生命周期

认证使用两个生命周期与 purpose 不同的 JWT，以及一个只存储 refresh 令牌哈希的 `usersessions` 文档。Access 令牌是无状态的；refresh 令牌是有状态的，并在每次使用时轮换。

## 令牌形态

- **Access 令牌**：HS256，默认 15 分钟，purpose `access`，claim `id` 为用户 hex ID，以及由 24 个随机字节生成的 `jti`。
- **Refresh 令牌**：HS256，默认 7 天，purpose `refresh`，claim `id`，以及由 24 个随机字节生成的 `jti`。
- **特殊 purpose 令牌**：`2fa-challenge`（5 分钟）、`reset-password`、`email-change`（1 小时）与 `sse-ticket`（30 秒）。它们使用同一签名器，但 `Protect` 会因其 purpose 不是 `access` 而拒绝。（历史 `device-verify` purpose 随设备验证码链移除而停用。）

Claims 包含 `id`、可选 `purpose`、`jti`、`newEmail`、`type`、`deviceLoginCode`，以及标准 `iat`/`exp`（秒）。`Verify` 强制 HS256，过期映射为 `ErrTokenExpired`，其他失败映射为 `ErrInvalidToken`。

## Cookie

`SetAuthCookies` 写入：

| Cookie | 路径 | 生命周期 | HttpOnly | SameSite | Secure |
|---|---|---|---|---|---|
| `accessToken` | `/` | 15 分钟 | 是 | Lax | 仅生产 |
| `refreshToken` | `/api/auth` | 7 天 | 是 | Lax | 仅生产 |
| `fdt-device` | `/` | 365 天 | 是 | Lax | 仅生产 |

refresh cookie 的窄路径意味着刷新与登出请求必须位于 `/api/auth` 下。`fdt-device` 设备令牌 cookie 是"已知设备"识别的唯一依据：浏览器自动携带，会话文档存其 sha256 哈希；User-Agent 客户端完全可控、可枚举重放（CWE-290），仅作展示字段。清除鉴权 cookie 会为两个鉴权路径以及遗留 `token` cookie 写入过期 cookie。

## 会话存储

`usersessions` 文档包含用户 ID、refresh 令牌的 SHA-256 hex 哈希（`refreshTokenHash`）、access 令牌的 SHA-256 hex 哈希（`tokenHash`）、设备令牌的 SHA-256 hex 哈希（`deviceTokenHash`，已知设备识别依据）、设备信息、IP、`isActive`、`loginAt`、`lastActiveAt`、`logoutAt` 与 `revokeReason`。只存哈希，不存令牌原文。`auth.HashToken` 是普通 SHA-256 hex 摘要，不能改变，否则既有哈希无法比较。

`revokeReason` 是会话被置为 inactive 的原因墓碑（取值见 `model.Revoke*` 常量），覆盖登出（`logout`）、refresh 轮换（`rotated`，正常不是吊销）、改密（`password_changed`）、密码重置（`password_reset`）、改绑邮箱（`email_changed`）、refresh 重用检测（`refresh_reuse`）、用户自助下线（`user_revoked`/`user_revoked_all`）、管理员强制下线（`admin_revoked`）与超期清理（`expired`）。它只存在于库与日志，不进 API 响应——排障时直接查 `usersessions` 或用 `make diag` 即可知道会话"为什么"被置废。

`Protect` 对带 `jti` 的 access 令牌按 `tokenHash` 反查会话；会话被登出、改密或管理员下线后，access 令牌立即失效。没有 `jti` 的历史令牌跳过该检查，最多再存活一个 access TTL。

## 签发

登录或设备确认成功后，`IssueSession`：

1. 签发带新 `jti` 的 access 令牌；
2. 签发带新 `jti` 的 refresh 令牌；
3. 生成 256 位随机设备令牌，插入包含 access 哈希、refresh 哈希、设备令牌哈希、设备信息、IP 与时间戳的会话；
4. 设置鉴权 cookie 与 `fdt-device` 设备 cookie；
5. 异步写审计记录。

## 轮换与重用检测

`POST /api/auth/refresh` 读取 refresh cookie 并调用 `VerifyRefreshToken`：

1. 缺少令牌 → 401 `auth.noRefreshToken`。
2. 签名非法 → 401 `auth.invalidToken`；过期 → 401 `auth.refreshTokenExpired`。
3. purpose 错误 → 401 `auth.invalidToken`。
4. 通过 `findOneAndUpdate({refreshTokenHash, isActive:true}, {$set:{isActive:false, logoutAt:now, revokeReason:"rotated"}})` 原子占用会话。只有一个并发请求能成功。

该端点使用独立的 `RefreshSpec` 限流器（60 次/15 分钟），不再与 2FA 桶（5/15min）共享配额——正常轮换流量与 TOTP 试错共桶曾让运营商 NAT 共享出口 IP 的用户被 429 打成登出（ADR-015）。

如果原子占用失败，`handleRefreshMiss` 会查询同一哈希（包括非活跃会话）并区分两种情况：

- 会话在 **2 分钟**（`ConcurrentRefreshGrace`）内被置为无效 → **409** `auth.concurrentRefresh`。这被视为合法并发刷新（多标签页、弱网重发），并记录 `refresh_grace` 事件。handler **不会**清除 cookie，也**不会**吊销会话。宽限期从旧值 30 秒放宽，避免移动端后台挂起后迟到的旧 token 被误判为重用而吊销全部会话。
- 否则 → **401** `auth.refreshTokenReuse`。该用户所有活跃会话都会被置为无效（`revokeReason="refresh_reuse"`），强制重新登录，并记录 `refresh_reuse` 事件。

成功占用后加载用户并轮换：带新 `jti` 的新 access 令牌、带新 `jti` 的新 refresh 令牌、继承设备信息与原登录时间且记录两者哈希的新会话，以及新 cookie。旧会话保持非活跃。

`jti` 是安全关键。没有它，同一秒内的两次轮换可能签发出字节完全相同的 refresh 令牌，存储哈希也会与新会话匹配，重用检测会静默失效。

### 状态语义

| 状态码 | 含义 | Cookie 处理 |
|---|---|---|
| 419 | `Protect` 期间 access 令牌过期 | 前端调用 refresh |
| 409 | 宽限期内并发刷新 | 保留 cookie，不吊销 |
| 401 | refresh 缺失、非法、过期或重用 | handler 清除 cookie，重用时吊销 |

除 409 外，handler 会为所有 refresh 错误清除鉴权 cookie，并按错误 messageKey 记录 `refresh_invalid` 诊断事件（detail 区分 cookie missing / expired / invalid）——409 与 401 在响应上都是 4xx，但排障含义完全不同。

## 登出

`POST /api/auth/logout` 受保护，因此带有效 access 令牌。`DeactivateForLogout` 的行为：

1. 哈希 refresh cookie 并查找匹配会话：**会话已是 inactive = 令牌重用信号**（该令牌已被他人先行轮换/吊销）——按重用语义吊销该用户全部活跃会话（`revokeReason="refresh_reuse"`）并记录 `refresh_reuse` 认证事件，防止先行轮换的窃贼会话在受害者登出后继续存活；会话仍活跃则按 ID 停用（`revokeReason="logout"`）。
2. 哈希 access 令牌并停用任何遗留 `tokenHash` 匹配的会话（`revokeReason="logout"`）。
3. 清除鉴权 cookie。没有匹配时静默。

## 会话管理

`/user-sessions` 域让用户查看与管理设备：

- `POST /create` 与 `POST /heartbeat` 维护遗留的 `tokenHash` 会话模型；
- `GET /my` 按登录时间返回用户会话；
- `PUT /:id/name` 重命名设备；
- `DELETE /:id` 停用单个会话；
- `DELETE /my/all` 停用除当前令牌外的所有会话；
- 超管路由可以列出全部会话，并按 ID 或按用户删除会话。

`Protect` 还会在每个认证请求上通过异步更新刷新 `lastActiveAt`，这正是“在线用户”统计读取的数据。

## 会话清理

`internal/cron` 包含一个任务，把 `lastActiveAt` 超过 30 天的活跃会话标记为无效（`revokeReason="expired"`），每小时运行。`cron.StartSessions` 已由 `cmd/server/main.go` 在开始监听之前启动，因此清理会随进程运行；进程退出时通过 context 取消。cron 包中记录的不活跃窗口同样是 30 天。

## 多实例影响

会话存储在 MongoDB 中，因此令牌校验与轮换可跨实例工作。重用宽限期也有效，因为它来自持久化的 `logoutAt`。**不**跨实例工作的内存状态包括验证码、限流计数、SSE 连接簿记与剧集缓存。

## 相关阅读

- [注册与登录流程](login-and-registration.md)——会话最初如何创建。
- [鉴权中间件与角色](middleware-and-authorization.md)——419 处理与守卫行为。
- [通知与实时推送](../api/notifications-and-realtime.md)——SSE 票据变体。
