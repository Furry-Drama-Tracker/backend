---
type: security
title: 鉴权中间件与角色
description: Protect 中间件的准确失败结果、角色常量与角色路由、默认超管邮箱的 RequireEmailChanged、可选鉴权、CSRF 双提交 cookie，以及 SSE 票据校验。
tags: [auth, authorization, roles, middleware, csrf, cookies]
sources:
  - id: openwiki-source-d30e65659972429c2c50cf5a
    resource: repo://internal/auth/cookies.go
  - id: openwiki-source-314bd77139a7d3c62ed804bf
    resource: repo://internal/handler/auth.go
  - id: openwiki-source-7e5389f01ee56b40448faee1
    resource: repo://internal/handler/notifications.go
  - id: openwiki-source-a759557a449edc99daccb392
    resource: repo://internal/middleware/auth.go
  - id: openwiki-source-a847b7dda9c7ea38cfd0250b
    resource: repo://internal/middleware/csrf.go
  - id: openwiki-source-886aab7e2aac16b6c69a5090
    resource: repo://internal/middleware/optional_auth.go
  - id: openwiki-source-7106c8455bb73d12832c8523
    resource: repo://internal/server/app.go
generated: { by: "opencode", at: "2026-11-03T16:50:44.301Z" }
verified:
  - by: openwiki/0.7.2
    at: 2026-11-03T16:50:44.301Z
---

# 鉴权中间件与角色

鉴权由 `middleware.Auth` 实施，这是一个持有 repository 聚合与 JWT 签名器的小容器。它暴露三个守卫：`Protect`、`RequireEmailChanged` 与 `OptionalAuth`。令牌提取与 cookie 行为位于 `internal/auth`。

## 令牌提取

`auth.GetAccessToken` 按固定顺序读取：

1. 以 `Bearer ` 开头的 `Authorization` 头；
2. 路径 `/` 的 `accessToken` cookie；
3. 路径 `/` 的遗留 `token` cookie。

`auth.GetRefreshToken` 只读取路径 `/api/auth` 的 `refreshToken` cookie。

`auth.Signer.Verify` 强制 HS256，过期返回 `ErrTokenExpired`，其他情况返回 `ErrInvalidToken`。Claims 包含 `id`、可选 `purpose`、`jti`、`newEmail`、`type` 与 `deviceLoginCode`。

## Protect 的结果

`Protect(allowedRoles...)` 返回一个 Gin handler，有六种失败模式。状态码与 messageKey 是固定契约：

| 条件 | 状态码 | 响应体 |
|---|---|---|
| 无令牌 | 401 | `{"message":"Not authorized, no token","messageKey":"auth.noToken"}` |
| 令牌过期 | 419 | `{"message":"Access token expired","messageKey":"auth.accessTokenExpired"}` |
| 令牌非法、签名错误或 purpose 非 access | 401 | `invalidToken` |
| 用户已不存在 | 401 | `auth.userNotFound` |
| 令牌带 `jti` 但会话已失效、不存在或不属于该用户 | 401 | `{"message":"Not authorized, token revoked","messageKey":"auth.invalidToken"}` |
| 角色不在允许列表 | 403 | `{"message":"Not authorized","messageKey":"auth.forbidden"}` |

空 purpose 被视为 access，以兼容旧令牌；其他任何 purpose 都会被拒绝。成功时把用户存入 `auth.user`、原始令牌存入 `auth.token`，然后启动一个 fire-and-forget 的会话活跃时间更新。

带 `jti` 的 access 令牌会额外查询 `usersessions.tokenHash`：登出、改密或管理员下线让会话失活后，access 令牌立即失效，而不是等到 15 分钟自然过期。历史上没有 `jti` 的旧令牌跳过该检查，最多再存活一个 access TTL，用于平滑过渡。

419 不是标准 HTTP 状态，但它是前端调用 `/api/auth/refresh` 的信号。

## 角色

角色常量是 `user`、`creator`、`admin` 与 `superadmin`。路由组一致地使用它们：

- 任意登录用户：无参数的 `Protect()`（追番、收藏、历史、通知、用户自助）；
- creator 及以上：`Protect("creator", "admin", "superadmin")`（剧集提交、系列、创作中心、版本）；
- admin 及以上：`Protect("admin", "superadmin")`（举报、统计、反馈管理、分类）；
- 仅 superadmin：`Protect("superadmin")`（后台管理、审计日志、备份、系统壁纸、自动任务、站点内容）。

部分领域在创作者可进入的守卫之上再叠加内联校验。审核与剧集回收站路由使用 `Protect(creator, admin, superadmin)` 加 `adminOnlyReview`，后者在角色不是 admin 或 superadmin 时返回 403。即使中间件列表看起来更宽，实际访问仍是仅管理员。

## RequireEmailChanged

该守卫针对邮箱仍为 `admin@furry09.com` 的新初始化超管。当登录用户是超管且邮箱恰为该地址时：

- GET 请求放行；
- 包含 `change-email`、`logout` 或 `verify` 的路径放行；
- 其他所有请求返回 403 与 `{"forceEmailChange": true}`。

它挂在变更类超管路由上，例如管理员账号创建/删除、角色变更、创作者主页审核、公告以及备份相关管理操作。它不是通用的邮箱验证检查。

## 可选鉴权

`OptionalAuth` 用于对登录用户行为不同的公开端点，例如剧集详情与友链申请。它尝试相同的 access token 提取与校验，但在缺失、非法、purpose 错误或带 `jti` 的令牌对应会话已失效时静默放行。与 `Protect` 不同，它：

- 不设置原始令牌上下文键；
- 不更新活跃时间；
- 从不返回 401。

成功时会设置 `auth.user`，使下游 handler 能区分访客与成员。

## CSRF

CSRF 使用双提交 cookie：

- `GET /api/csrf-token` 设置 `XSRF-TOKEN` 并在 JSON 中返回相同值。cookie **不是** HttpOnly（前端必须读取），`SameSite=Strict`，路径 `/`，生命周期为配置的 `csrf_max_age_h`（默认 24 小时）。
- 非 GET 请求由 `CSRF` 中间件比较 cookie 与 `X-XSRF-TOKEN` 头。不匹配、缺少头、缺少 cookie 各返回不同的 403 消息。
- token 端点注册在校验中间件之前，因此即使其他路由会因 CSRF 失败，它也始终可访问。

由于 CSRF cookie 非 HttpOnly 且校验是普通相等比较，该方案依赖浏览器同源 cookie 策略与 CORS 限制，而不是服务端令牌存储。

## SSE 票据认证

SSE 通知流不使用 `Protect`，因为 EventSource 不能设置 `Authorization` 头。取而代之：

1. `GET /auth/sse-ticket`（受保护）签发 30 秒、purpose 为 `sse-ticket` 的 JWT。
2. `GET /notifications/stream?ticket=...` 在通知 handler 中直接校验签名与 purpose，并把 claim 的 `id` 当作 user ID。

这是与 `Protect` 分开的校验路径；它不检查角色，也不加载用户文档。

## Cookie 属性

- Access 令牌：HttpOnly、`SameSite=Lax`、15 分钟、路径 `/`、仅生产环境 secure。
- Refresh 令牌：HttpOnly、`SameSite=Lax`、7 天、路径 `/api/auth`、仅生产环境 secure。
- 清除鉴权 cookie 时会为两个路径以及遗留 `token` cookie 写入过期 HttpOnly cookie；生产环境清除时使用 `SameSite=Strict`。

## 相关阅读

- [请求生命周期与中间件管线](../architecture/request-lifecycle.md)——CSRF 与守卫在链中的位置。
- [会话与令牌生命周期](session-and-token-lifecycle.md)——refresh 轮换与 419 处理。
- [管理与审核 API](../api/administration-and-moderation.md)——角色门控的管理路由。
