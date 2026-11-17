---
type: architecture
title: 请求生命周期与中间件管线
description: 固定的 Gin 中间件顺序、引擎级客户端 IP 识别装配、请求体解析与清洗、CSRF 校验、限流挂载语义与 429 诊断日志、单前缀路由、静态上传，以及错误实际如何到达客户端。
tags: [http, middleware, gin, rate-limiting, csrf, routing]
sources:
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
  - id: openwiki-source-6e8f4534cd26274b5794f0ca
    resource: repo://internal/ratelimit/limiter.go
  - id: openwiki-source-a8ba835229d442d09305d33e
    resource: repo://internal/security/xss.go
  - id: openwiki-source-7106c8455bb73d12832c8523
    resource: repo://internal/server/app.go
  - id: openwiki-source-ebbcecfb3e8c07ba5e940ccd
    resource: repo://internal/server/register_routes.go
generated: { by: "zcode", at: "2026-11-17T17:20:26.784Z" }
verified:
  - by: openwiki/0.7.2
    at: 2026-11-17T17:20:26.784Z
---

# 请求生命周期与中间件管线

每个 HTTP 请求都由 `server.NewApp` 构建的单一 Gin 引擎处理。中间件顺序被视为契约：`app.go` 与 `register_routes.go` 的注释要求不要重排。本页沿该顺序跟踪请求，并记录行为微妙之处。

## 管线

```
gin.CustomRecovery（panic → JSON 500 SYS0500，替换纯文本 Recovery）
  -> RequestRef（最前：生成参考号，X-Ref-ID 响应头 + refID 上下文）
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
       -> 领域路由（单前缀挂载在 /api 下）
  -> /uploads 静态路由
  -> NoRoute：404 SYS0003「接口不存在」
  -> errors.Handler（最后注册）
```

在中间件之前，`NewApp` 还做三件装配：调用 `errors.SetLogger(log)` 把 slog 注入 errors 包（5xx ERROR 日志的输出器）；构造 `authlog.Recorder`（stderr 结构化日志 + `authevents` 落库）供鉴权链与限流器使用；并按 `[server]` 配置装配客户端 IP 识别（详见下文）。

### 参考号（RequestRef）

`middleware.RequestRef` 是全局链的第一环：用 `crypto/rand` 生成 0-999999999 的十进制参考号，写入 Gin 上下文（键 `errors.ContextRefID`）并设置 `X-Ref-ID` 响应头。errors 渲染器、`RequestLogger` 与 panic 恢复都读取同一参考号，因此响应头、响应体 `ref` 字段与日志行可以用同一个值关联（排障流程见[错误信封与错误码契约](../api/error-envelope.md)）。它必须保持最前——后续所有中间件与错误渲染都依赖它已写入。

### 客户端 IP 识别（引擎级装配）

全仓库的客户端 IP 统一来自 gin 的 `c.ClientIP()`，不再有各中间件/`handler` 手写的 X-Real-IP/XFF 解析。信任策略由 `server/app.go` 按 `[server]` 配置装配到引擎上：

- `trust_proxy_headers`：缺省沿用历史行为（生产信任、开发不信任）；关闭时 `ForwardedByClientIP=false`，`c.ClientIP()` 一律返回 RemoteAddr；
- `trusted_proxies`：配置后经 `SetTrustedProxies` 校验请求来源，非受信来源直连伪造的代理标头被整体忽略（缺省不配 = 信任全部，与旧行为一致）；
- `proxy_ip_headers`：写入引擎的 `RemoteIPHeaders`，缺省 `CF-Connecting-IP,X-Real-IP,X-Forwarded-For`（ESA/CF 边缘覆盖写入的 CF 头优先）；
- `proxy_trusted_platform`：设置 gin `TrustedPlatform`（单一无条件最高优先标头，非空即信，默认不启用）。

依赖客户端 IP 的消费方包括：限流键（`spec.Name + IPKey(c.ClientIP())`）、请求日志、SSE 每 IP 连接上限、观看冷却、审计与认证事件记录。

### Recovery、压缩与响应头

panic 恢复用 `gin.CustomRecovery` 替换了 gin 默认的纯文本 500（后者会绕过统一 JSON 信封契约）：panic 渲染为 JSON 500，错误码 `SYS0500`、带参考号，stack 只进日志（`panic recovered`，带 ref/path/stack）；开发模式额外在响应体回显 panic 值与 stack。Gzip 在客户端接受 gzip 时压缩响应，排除已压缩的图片扩展名（含 webp）。`SecurityHeaders` 为每个响应设置 CSP（`img-src` 允许 `https:` 以支持外链封面）、一年含子域的 HSTS、`X-Frame-Options: SAMEORIGIN`、`nosniff`、`Referrer-Policy: no-referrer`、`Permissions-Policy`（关闭相机/麦克风/定位/支付）、`X-XSS-Protection: 0` 及相关头。

### CORS

没有 `Origin` 头的请求直接放行且不设 CORS 头。允许列表中的源会得到 `Access-Control-Allow-Origin` 与凭据支持；预检（`OPTIONS` 加 `Access-Control-Request-Method`）以 204 短路并返回允许的方法与头。不在列表中的源返回 **403** `CORS policy denied`，而不是浏览器式静默失败。比较前会去掉尾部斜杠。

### 超时

`Timeout` 给请求附加 30 秒上下文截止时间。它依赖 handler 与 repository 消费 `c.Request.Context()`，不会强制关闭已开始写入的响应。

### 请求体解析

`BodyParse` 先给**所有**请求体套上 `http.MaxBytesReader` 硬上限，再决定是否解析 JSON/form。这一层也覆盖 multipart 与未知 Content-Type，防止后续 `SanitizeInput` 或 `FormFile` 无上限读取内存。

- 全局硬上限：12 MB；备份导入路径为 51 MB。
- JSON/form 解析上限：1 MB；`/api/backup/import`（及子路径）的 JSON 为 50 MB。
- 超限返回 413 `request entity too large`（信封码 `BDP0002`）；读取失败 400 `BDP0001`。
- JSON 语法错误返回 400（`BDP0003`，message 固定为 `encoding/json` 错误原文）；urlencoded 解析失败 400 `BDP0004`。
- **非对象 JSON（数组/标量）返回 400 `SYS0002`「请求格式错误」**（2026-11 起）：此前标量/数组体被静默丢弃、handler 读到零值后表现为误导性业务错误，现在统一按请求格式错误拒绝。
- 解析后的请求体存入 Gin 上下文供后续中间件使用，原始字节会还原到 `c.Request.Body`，使 handler 可以再次绑定。
- 非 JSON/form 内容类型原样通过，特别是 multipart 上传，稍后由上传 handler 读取。

### 输入清洗

`SanitizeInput` 在请求体解析之后运行：

- JSON 请求体用 `UseNumber` 解码、递归清洗、重新序列化并替换。
- 查询字符串只丢弃以 `$` 开头的键，防止 NoSQL 操作符注入；查询**值不做 HTML 转义**，否则 URL、搜索词和链接里的 `&` 会被改成 `&amp;`。含 `.` 的键不再被丢弃。
- 路由参数不做清洗：它们是固定 ID/枚举，转义会破坏合法字符。
- 密码类字段（`password`、`newPassword`、`currentPassword`、`confirmPassword`、`oldPassword`）跳过 XSS 重写。

清洗器会剥除不允许的 HTML 标签而不是转义，保留文本内容。这是相对旧 `xss` 库的有意偏离。

`SanitizeHeaders` 只清洗 `Referer` 头。它刻意不碰 `X-Forwarded-For` 与 `X-Real-IP`，因为引擎级 `c.ClientIP()` 配置（标头清单与受信代理校验）要原样读取它们。

### CSRF

`GET /api/csrf-token` 注册在 CSRF 中间件之前，因此永远可访问。handler 签发非 HttpOnly 的 `XSRF-TOKEN` cookie 并返回 token。对于每个非 GET 请求，`CSRF` 比较 cookie 与 `X-XSRF-TOKEN` 头：

| Cookie | Header | 结果 |
|---|---|---|
| 存在 | 存在且相等 | 放行 |
| 存在 | 存在但不同 | 403 `CSRF token mismatch`（`CSR0001`） |
| 存在 | 缺失 | 403 缺少 header（`CSR0002`） |
| 缺失 | 任意 | 403 缺少 cookie（`CSR0003`） |

三种 403 的 message 文本保持原契约值不变，信封的 `error`/`ref`/`status` 字段由 `codedBody` 补齐。

### 统计与日志

`APITracker` 统计路径以 `/api/` 开头的请求，键为 `METHOD 路由模板` 与 UTC 日期。它缓冲 50 条或 60 秒后把计数 upsert 到 `apiusages`。单次刷新失败静默。

`RequestLogger` 只记录非 GET 请求、状态至少 400 的响应或超过一秒的请求（其签名已简化为 `RequestLogger(log)`，不再接收 trustXFF 参数，IP 取 `c.ClientIP()`）。`SlowLogger` 额外标记开发环境慢请求，并记录生产环境超过三秒的请求。日志经 slog 写入 stdout；邮件日志写入 stderr。

## 限流

全局限流器挂在 `/api` 路由组，因此覆盖包括 `/api/health` 在内的所有 API 路由。默认预算为每分钟 300 次。它显式跳过以 `/api/translate` 与 `/api/auth/captcha` 开头的路径。

端点限流器通过工厂在注册时调用 `middleware.RateLimit(spec, opts)` 挂到具体路由。挂载语义：

- 无 mounts 的 spec 是全局型。
- 有 mounts 的 spec 精确匹配 `/api/<mount>`，并以 `/api/<mount>/...` 前缀匹配子路径。
- 端点型 spec 精确匹配 `/api/<mount>`，并以 `/api/<mount>/...` 前缀匹配子路径；它不会匹配 `/api/v1/` 或其他前缀。
- `AuthSpec` 仅在 `IsDev` 且 `SKIP_RATE_LIMIT=1` 时跳过。

两个后果容易被忽略：

1. 每次 `RateLimit` 调用都会创建带独立内存计数的新限流器。但 `RegisterRoutes` 用 `spec.Name|window|max` 做键缓存限流器，因此同一 spec 的所有路由共享同一计数器与预算。例如 TwoFactorSpec 的 Mounts 清单为 6 条路由（`/auth/login-2fa`、`/2fa/enable`、`/2fa/verify-enable`、`/2fa/disable`、`/2fa/verify`、`/auth/2fa/email-otp/send`），它们共用同一个 5 次/15 分钟桶——`/2fa/enable` 于 2026-11 补入挂载清单：它是植入持久凭据（第二因素）的端点，此前缺项导致实际只剩全局 300/min 兜底；`/auth/refresh` 不在此列，它使用独立的 `RefreshSpec`（60 次/15 分钟）。Passkey 仪式有独立的 `PasskeySpec`（10 次/15 分钟，覆盖 `/passkeys/register/*` 与 `/auth/passkey/*`）。
2. 动态路由如 `/episodes/:id/view` 无法用挂载匹配表达，因此其配置的限流器永远不会触发。

限流器使用 `ulule/limiter` 与内存存储，因此计数是固定窗口且进程本地的。响应包含 draft-6 `RateLimit-*` 头，429 时包含 `Retry-After`。

限流键在凭证/邮件类 spec 上绑定目标账号（ADR-019）：`auth`、`twoFactor`、`passwordReset`、`emailVerify` 四个 spec 在请求体带认证标识时改用 `spec:sha256(标识小写)` 计数（`keyDigest` 把攻击者可控的键分量压缩为定长摘要——语义不变，但内存 store 中计数条目的驻留体积不再随请求体长度线性增长，修复 CWE-770 无上限资源分配）。**键取自 `emailFromBody`：优先请求体 `identifier` 字段，回退 `email`**（2026-11 ADR-022 修复）——login / login-2fa 的实际认证标识是 `identifier`（email 仅旧客户端回退），若按 email 取键，攻击者用 identifier 认证的同时轮换随机 email，每个猜测请求各得一个新桶，`AuthSpec`/`TwoFactorSpec` 的 5 次/15min 即告失效（实测 900× 放大）；passwordReset / emailVerify 等无 identifier 的端点行为不变。来源 IP 可经伪造代理标头轮换（CWE-348），保留 IP 分量会让这些 spec 在 IP 轮换下退化为无限流；绑定标识后，对同一账号的猜测无论换多少来源 IP 都共享同一计数桶。请求体无标识的挂载（`reset-password`、`change-password`、`2fa/*` 等无邮箱体端点）回退到原 `spec:IP` 键，与旧行为一致。**`/api/auth/verify-email` 是固定例外**：该端点按 code 解析验证条目，请求体 `email` 只是与被保护资源无绑定的可选匹配谓词——按 email 取键会让每次换随机 email 的猜测各得一个新桶，5/h spec 对爆破完全失效，因此该挂载固定回退 IP key（需配合正确的 `trusted_proxies` 配置）。`requestEmailChange` 仍同时按来源 IP 与目标新邮箱小写值限流，避免用不同邮箱刷验证邮件。Passkey 仪式请求体不含 email，天然走 IP 键（仪式本身有 ceremony 会话一次性约束）。

429 命中时，响应体走带码信封（`RLM0001`，message 为 spec 的固定文案），并设置 draft-6 `RateLimit-*` 头与 `Retry-After`。中间件还会通过 `RateLimitOpts.Log` 输出一条认证诊断事件：`event=ratelimited`，带限流器名（spec 名写入 reason）、路径、IP 与 retry_after 秒数——在此之前日志里只有一个 429 状态码，无法区分是登录、refresh 还是全局限流命中。

## 单前缀路由

`router.Mount(parent, "/x", register)` 只调用一次 `register(parent.Group("/x"))`，把领域 handler 注册到 `/api/x`。不存在 `/api/v1` 镜像，也不注入 `Deprecation`/`Sunset` 头（ADR-010 记录了这一破坏性变更）。`router.Mount` 的测试锁定 `/api/v1/ping` 必须返回 404，防止镜像被重新引入。

领域 handler 常把根路径注册两次（`""` 与 `"/"`），以避免 Gin 自动尾斜杠重定向，因为接口契约要求两种形式都直接匹配而不返回 307。

## 静态上传

`/uploads/*filepath` 注册在引擎上、`/api` 组之外，因此不被 API 统计且不受全局限流影响。支持 GET 与 HEAD。只服务 `.jpg`、`.jpeg`、`.png`、`.gif`、`.webp`、`.svg` 六种扩展名；包含 `..` 的路径、目录、其他扩展名与缺失文件返回 404。存在文件获得七天不可变缓存、`nosniff`、`noindex`、inline disposition 与 sandbox CSP，确保直接打开 SVG 也不会执行脚本。

## 错误渲染

4xx/5xx 响应的完整渲染契约在[错误信封与错误码契约](../api/error-envelope.md)页。本管线视角的要点：

- handler 主要通过 `errors.AbortWithAppError` / `Abort500C` / `AbortCoded` 显式渲染错误：响应体为 `{message, error, ref, status}`，message 纯文案、生产 5xx 掩码「服务器内部错误」，5xx 额外打一行 ERROR 日志（code/ref/path/site/cause）。
- 中间件层的固定契约响应（CSRF、限流、bodyparse）用 `codedBody` 在原 message 上补 `status`/`error`/`ref`。
- 未命中路由渲染 404 `SYS0003`「接口不存在」（`NoRoute`）；panic 渲染 500 `SYS0500`；漏网错误由最后注册的 `errors.Handler` 兜底渲染 `SYS0501`。

`errors.Handler` 在 `NewApp` 最后通过 `r.Use` 注册，位于所有路由之后。由于 Gin 在注册每个路由时快照中间件链，这次最终注册不会包裹此前注册的业务路由。实践中全局兜底渲染不覆盖这些路由，正确性依赖显式 abort 调用。之后新增的路由会有不同行为，扩展引擎时需要留意该顺序。

## 修改时的顺序规则

- `RequestRef` 必须保持全局链最前：后续中间件与错误渲染都读取它写入的参考号。
- 不要把 CSRF 校验移到 csrf-token 路由之前。
- 不要把全局限流器移出 `/api` 组；`/api/health` 与所有领域路由依赖它。
- 请求体解析必须早于清洗，因为清洗期望可读的请求体。
- 安全头要尽早设置，确保错误响应也带有它们。
- 新增中间件时，决定它属于路由前（全引擎）、`/api` 组（受限流）还是领域组（带守卫）。

## 相关阅读

- [错误信封与错误码契约](../api/error-envelope.md)——4xx/5xx 响应体、错误码与参考号契约。
- [架构总览](overview.md)——包边界。
- [鉴权中间件与角色](../auth/middleware-and-authorization.md)——`Protect` 行为。
- [会话与令牌生命周期](../auth/session-and-token-lifecycle.md)——cookie 与令牌语义。
