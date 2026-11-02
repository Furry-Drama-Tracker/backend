---
type: architecture
title: 请求生命周期与中间件管线
description: 固定的 Gin 中间件顺序、请求体解析与清洗、CSRF 校验、限流挂载语义、双版本路由、静态上传，以及错误实际如何到达客户端。
tags: [http, middleware, gin, rate-limiting, csrf, routing]
verified:
  - by: openwiki/0.7.2
    at: 2026-11-02T17:41:59.592Z
sources:
  - id: openwiki-source-3a7c36409770ce04e5cdeef4
    resource: repo://internal/handler/translate.go
  - id: openwiki-source-8cc3b2cd34ec48fbcbb82fd1
    resource: repo://internal/middleware/apitracker.go
  - id: openwiki-source-dbaf2c53f5cefea8a8bb2ff3
    resource: repo://internal/middleware/bodyparse.go
  - id: openwiki-source-0d3ceab7fddae0013568ba2c
    resource: repo://internal/middleware/cors.go
  - id: openwiki-source-a847b7dda9c7ea38cfd0250b
    resource: repo://internal/middleware/csrf.go
  - id: openwiki-source-f7cdef93f6fa51dc3ffc0594
    resource: repo://internal/middleware/ratelimit.go
  - id: openwiki-source-40b293d8b99aed27fbe9db06
    resource: repo://internal/middleware/security.go
  - id: openwiki-source-7106c8455bb73d12832c8523
    resource: repo://internal/server/app.go
generated: { by: "opencode", at: "2026-11-02T17:41:59.592Z" }
---

# 请求生命周期与中间件管线

每个 HTTP 请求都由 `server.NewApp` 构建的单一 Gin 引擎处理。中间件顺序被视为契约：`app.go` 与 `register_routes.go` 的注释要求不要重排。本页沿该顺序跟踪请求，并记录行为微妙之处。

## 管线

```
gin.Recovery
  -> Gzip
  -> SecurityHeaders
  -> CORS
  -> Timeout（30 秒上下文截止）
  -> SlowLogger
  -> BodyParse（1 MB，备份导入 50 MB）
  -> SanitizeInput
  -> SanitizeHeaders
  -> GET /api/csrf-token（注册在 CSRF 校验之前）
  -> CSRF（非 GET）
  -> APITracker
  -> RequestLogger
  -> /api 组：全局限流（300/分钟）
       -> GET /api/health
       -> Swagger UI（仅开发环境）
       -> 领域路由（每个同时挂载 /api 与 /api/v1）
  -> /uploads 静态路由
  -> errors.Handler（最后注册）
```

### Recovery、压缩与响应头

`gin.Recovery` 把 panic 转为 500 响应。Gzip 在客户端接受 gzip 时压缩响应，排除已压缩的图片扩展名（含 webp）。`SecurityHeaders` 为每个响应设置 CSP、一年含子域的 HSTS、`X-Frame-Options: SAMEORIGIN`、`nosniff`、`Referrer-Policy: no-referrer`、`X-XSS-Protection: 0` 及相关头。

### CORS

没有 `Origin` 头的请求直接放行且不设 CORS 头。允许列表中的源会得到 `Access-Control-Allow-Origin` 与凭据支持；预检（`OPTIONS` 加 `Access-Control-Request-Method`）以 204 短路并返回允许的方法与头。不在列表中的源返回 **403** `CORS policy denied`，而不是浏览器式静默失败。比较前会去掉尾部斜杠。

### 超时

`Timeout` 给请求附加 30 秒上下文截止时间。它依赖 handler 与 repository 消费 `c.Request.Context()`，不会强制关闭已开始写入的响应。

### 请求体解析

`BodyParse` 只解析 JSON（`application/json` 或 `application/*+json`）与表单编码请求体。其他内容类型原样通过，特别是 multipart 上传，稍后由上传 handler 读取。

- 普通上限：1 MB。
- `/api/backup/import` 与 `/api/v1/backup/import`（及子路径）为 50 MB，但仅限 JSON。
- 超限返回 413 `request entity too large`。
- JSON 语法错误返回 400。
- 解析后的请求体存入 Gin 上下文供后续中间件使用，原始字节会还原到 `c.Request.Body`，使 handler 可以再次绑定。

### 输入清洗

`SanitizeInput` 在请求体解析之后运行：

- JSON 请求体用 `UseNumber` 解码、递归清洗、重新序列化并替换。
- 查询值被清洗并重写；以 `$` 开头或包含 `.` 的键会被丢弃，防止 NoSQL 操作符注入。
- 路由参数值就地清洗。
- 密码类字段（`password`、`newPassword`、`currentPassword`、`confirmPassword`、`oldPassword`）跳过 XSS 重写。

清洗器会剥除不允许的 HTML 标签而不是转义，保留文本内容。这是相对旧 `xss` 库的有意偏离。

`SanitizeHeaders` 只清洗 `Referer` 头。它刻意不碰 `X-Forwarded-For` 与 `X-Real-IP`，因为限流键依赖它们。

### CSRF

`GET /api/csrf-token` 注册在 CSRF 中间件之前，因此永远可访问。handler 签发非 HttpOnly 的 `XSRF-TOKEN` cookie 并返回 token。对于每个非 GET 请求，`CSRF` 比较 cookie 与 `X-XSRF-TOKEN` 头：

| Cookie | Header | 结果 |
|---|---|---|
| 存在 | 存在且相等 | 放行 |
| 存在 | 存在但不同 | 403 `CSRF token mismatch` |
| 存在 | 缺失 | 403 缺少 header |
| 缺失 | 任意 | 403 缺少 cookie |

### 统计与日志

`APITracker` 统计路径以 `/api/` 开头的请求，键为 `METHOD 路由模板` 与 UTC 日期。它缓冲 50 条或 60 秒后把计数 upsert 到 `apiusages`。单次刷新失败静默。

`RequestLogger` 只记录非 GET 请求、状态至少 400 的响应或超过一秒的请求。`SlowLogger` 额外标记开发环境慢请求，并记录生产环境超过三秒的请求。日志经 slog 写入 stdout；邮件日志写入 stderr。

## 限流

全局限流器挂在 `/api` 路由组，因此覆盖包括 `/api/health` 与 `/api/v1/*` 在内的所有 API 路由。默认预算为每分钟 300 次。它显式跳过以 `/api/translate` 与 `/api/auth/captcha` 开头的路径。

端点限流器通过工厂在注册时调用 `middleware.RateLimit(spec, opts)` 挂到具体路由。挂载语义：

- 无 mounts 的 spec 是全局型。
- 有 mounts 的 spec 精确匹配 `/api/<mount>`，并以 `/api/<mount>/...` 前缀匹配子路径。
- 端点型 spec **永不**匹配 `/api/v1/...`；这一不对称是记录在案的契约，不是 bug。
- `AuthSpec` 仅在 `IsDev` 且 `SKIP_RATE_LIMIT=1` 时跳过。

两个后果容易被忽略：

1. 每次 `RateLimit` 调用都会创建带独立内存计数的新限流器。跨多个路由复用同一 spec 会产生每路由独立计数，而不是共享预算。例如 2FA spec 挂在七个路由上，因此每个路由都有各自的 5 次/15 分钟桶。
2. 动态路由如 `/episodes/:id/view` 无法用挂载匹配表达，因此其配置的限流器永远不会触发。

限流器使用 `ulule/limiter` 与内存存储，因此计数是固定窗口且进程本地的。响应包含 draft-6 `RateLimit-*` 头，429 时包含 `Retry-After`。`requestEmailChange` 限流器刻意以 `[object Object]:<小写 newEmail>` 为键而不是 IP，保留历史怪癖。

## 双版本路由

`router.MountDual(parent, "/x", register)` 调用 `register` 两次：一次在 `/api/x`，一次在 `/api/v1/x`。v1 组添加 `Deprecation: true` 与固定 `Sunset` 日期。handler 完全相同，因此除头与端点限流不对称外，两个版本的响应形状没有差异。

领域 handler 常把根路径注册两次（`""` 与 `"/"`），以避免 Gin 自动尾斜杠重定向，因为接口契约要求两种形式都直接匹配而不返回 307。

## 静态上传

`/uploads/*filepath` 注册在引擎上、`/api` 组之外，因此不被 API 统计且不受全局限流影响。支持 GET 与 HEAD。包含 `..` 的路径、目录与缺失文件返回 404。存在文件获得七天不可变缓存、`nosniff`、`noindex` 与 inline disposition。

## 错误渲染

handler 主要通过 `errors.AbortWithAppError` 显式渲染错误，它映射 `AppError` 状态与消息，附加 `messageKey` 与额外标志，并在生产隐藏 5xx 详情、在开发暴露堆栈。

`errors.Handler` 在 `NewApp` 最后通过 `r.Use` 注册，位于所有路由之后。由于 Gin 在注册每个路由时快照中间件链，这次最终注册不会包裹此前注册的业务路由。实践中全局兜底渲染不覆盖这些路由，正确性依赖显式 abort 调用。之后新增的路由会有不同行为，扩展引擎时需要留意该顺序。

## 修改时的顺序规则

- 不要把 CSRF 校验移到 csrf-token 路由之前。
- 不要把全局限流器移出 `/api` 组；v1 与 health 依赖它。
- 请求体解析必须早于清洗，因为清洗期望可读的请求体。
- 安全头要尽早设置，确保错误响应也带有它们。
- 新增中间件时，决定它属于路由前（全引擎）、`/api` 组（受限流）还是领域组（带守卫）。

## 相关阅读

<!-- openwiki: broken internal link [/openwiki/architecture/overview.md] link "/openwiki/architecture/overview.md" is root-absolute, which no real consumer resolves against the repository root (not a coding agent reading the page, not GitHub's Markdown renderer, not a local viewer); use a path relative to this file instead. Fix the href or restore the target, then delete this comment. -->
- [架构总览](/openwiki/architecture/overview.md)——包边界。
<!-- openwiki: broken internal link [/openwiki/auth/middleware-and-authorization.md] link "/openwiki/auth/middleware-and-authorization.md" is root-absolute, which no real consumer resolves against the repository root (not a coding agent reading the page, not GitHub's Markdown renderer, not a local viewer); use a path relative to this file instead. Fix the href or restore the target, then delete this comment. -->
- [鉴权中间件与角色](/openwiki/auth/middleware-and-authorization.md)——`Protect` 行为。
<!-- openwiki: broken internal link [/openwiki/auth/session-and-token-lifecycle.md] link "/openwiki/auth/session-and-token-lifecycle.md" is root-absolute, which no real consumer resolves against the repository root (not a coding agent reading the page, not GitHub's Markdown renderer, not a local viewer); use a path relative to this file instead. Fix the href or restore the target, then delete this comment. -->
- [会话与令牌生命周期](/openwiki/auth/session-and-token-lifecycle.md)——cookie 与令牌语义。
