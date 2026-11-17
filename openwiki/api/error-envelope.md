---
type: api-contract
title: 错误信封与错误码契约
description: 4xx/5xx 统一错误信封 {message, error, ref, status} 的渲染管线、错误码构造器家族、参考号中间件、机器注册表生成与 ESA 边缘信封。
tags: [api, errors, error-codes, ref-id, envelope, observability, esa]
sources:
  - id: openwiki-source-0f6ed11169bd8e6b34b44280
    resource: repo://cmd/server/main.go
  - id: openwiki-source-e02064da87fd6ca4ba71ae39
    resource: repo://docs/errors.json
  - id: openwiki-source-9e1e456a1a4263c13511b8b3
    resource: repo://docs/esa-error.schema.json
  - id: openwiki-source-dd3141877f40c4fa42a48212
    resource: repo://docs/esa-errors.csv
  - id: openwiki-source-1c4889e4ab93c54a50468bdf
    resource: repo://docs/gen_errors_json.py
  - id: openwiki-source-9002449d5a813a8783166c8a
    resource: repo://internal/errors/apperror.go
  - id: openwiki-source-f89e324c07065d291ea97e16
    resource: repo://internal/handler/episodes.go
  - id: openwiki-source-33d886b205383a9cd6ea57b4
    resource: repo://internal/middleware/refid.go
  - id: openwiki-source-ec085cff2976e238109f19cf
    resource: repo://internal/middleware/requestlogger.go
  - id: openwiki-source-7106c8455bb73d12832c8523
    resource: repo://internal/server/app.go
generated: { by: "zcode", at: "2026-11-17T17:20:26.784Z" }
verified:
  - by: openwiki/0.7.2
    at: 2026-11-17T17:20:26.784Z
---

# 错误信封与错误码契约

2026-11 起所有 4xx/5xx 响应体统一为四字段信封（`internal/errors` 包渲染，契约章节见 backend/AGENTS.md「错误码与参考号」）：

```json
{"message": "该邮箱已注册", "error": "USR0007", "ref": 123456789, "status": 400}
```

- **error** 是 `XXXYYYY` 错误码：3 字母模块前缀 + 文件内顺序号。**码即注册表**——`grep -rn "USR0007" backend/` 直达唯一调用点；新站点取号 = 同前缀仓库内当前最大序号 + 1，前缀不得跨模块复用。
- **ref** 是每请求一个的 0-999999999 随机十进制参考号（`crypto/rand`），响应头 `X-Ref-ID` 携带同值。
- **status** 是 HTTP 状态码整数，信封自描述（与 axios `err.response.status` 恒等）。
- **message 恒为纯文案**：编号只经 `error`/`ref` JSON 字段传递，后端绝不把编号拼进 message；展示格式 `USR0007-000123456` 由前端从字段拼装。生产环境 5xx 的 message 一律掩码为「服务器内部错误」。

例外：`/api/health` 的 503 独立信封与 oauth 501（业务性"未开放"）不按本契约编码。

## 参考号中间件

`middleware.RequestRef` 是全局中间件链的第一环（panic 恢复之后立即注册）：生成参考号写入 `gin.Context`（键 `errors.ContextRefID`）并设置 `X-Ref-ID` 响应头。`internal/errors` 渲染器与 `RequestLogger` 都从这里读取同一参考号——因此一次请求的响应头、响应体与日志行可以用同一个 ref 关联。参考号由 `errors.NewRefID` 用 `crypto/rand` 生成（0-999999999 的整数，`ref` 在 JSON 里是整数而非字符串）；渲染时若上下文没有参考号（单测直连引擎）会现场生成一个兜底值。

## 渲染管线

错误响应的统一出口是 `internal/errors` 包，按入口分四层：

1. **AppError 渲染**：service 层返回的 `*AppError` 经 `AbortWithAppError` → `abortAppError` 渲染。`abortAppError` 按状态码组装响应体：生产 5xx 掩码 message；`messageKey`、`Extra` 标志字段（如 `needVerification`、`accountNotFound`）原样并入；状态 ≥ 400 时补 `error`/`ref`/`status` 三字段——`error` 取 `AppError.Code`，未编码时按状态兜底（400 → `SYS0002`、404 → `SYS0003`、其余 → `SYS0001`）。`Extra` 不允许覆盖 `message`/`messageKey`/`error`/`ref`/`status` 五个保留字段。
2. **handler 直写**：handler 不经 AppError 也能直写带码响应——`Abort500C`（500 标准）、`Abort500SiteC`（同 Abort500C 但 site 显式传入）、`AbortErrC`（502/504 等变体）、`AbortCoded`（4xx）、`AbortKeyed`（4xx + messageKey）。
3. **panic 恢复**：`gin.CustomRecovery` 替换了 gin 默认的纯文本 Recovery——panic 渲染为 JSON 500，错误码 `SYS0500`，stack 只进日志；开发模式额外回显 panic 值与 stack。
4. **全局兜底 `Handler`**：最后注册的全局错误中间件把 gin `c.Errors` 里漏网的错误渲染为 `SYS0501`；上传错误（`UploadError`）在此（及 `AbortWithAppError`）被识别并映射为 `UPL0001`（大小）/`UPL0002`（类型）/`UPL0003`（魔数）/`UPL0004`（其它），message 前缀「文件上传错误: 」保留。

未命中路由（`NoRoute`）也走同一信封：404 `SYS0003`「接口不存在」，替换 gin 默认的纯文本 404。`middleware.codedBody` 服务于中间件层的固定契约响应（CSRF 三态、限流 429、bodyparse 413/400 等）：在既有 `gin.H` 上补 `status`/`error`/`ref` 三字段，message 文本保持原契约值不变。

## 构造器选型

`internal/errors` 的构造器按调用层分流：

- **service 层**：`NewC(code, status, message)`、`NewKeyC`（+messageKey 供前端 i18n）、`NewExtraC`（+布尔标志字段）、`WrapC`（包裹底层 cause）。不带码的旧构造器（`New`/`NewKey`/`NewExtra`/`Wrap`）仍存在，渲染时回退兜底码。
- **handler 直写**：`Abort500C`/`Abort500SiteC`/`AbortErrC`/`AbortCoded`/`AbortKeyed`（见上）。
- **middleware 固定契约**：`codedBody(c, status, code, gin.H{...})`。

带码构造器通过导出的 `CallerSite`（`runtime.Caller` 包装）自动抓取构造点 `file:line`（路径裁剪到 `backend/` 之后）存入 `AppError.site`——该值**不进响应体**，仅用于 5xx 日志定位调用点。skip 语义同 `runtime.Caller`，构造器内部统一 `CallerSite(2)`（2026-11 ADR-021 修正：此前 `callerSite(1)` 自指，抓到的是 errors 包自身行号）。经共享错误助手转发时助手帧会挡住真实调用点——此时应改用 `Abort500SiteC` 显式传入：`episodes.go` 的 `serverError`（全模块共用的 500 兜底，`EPS0037`）即以 `CallerSite(2)` 抓取自身调用方后经 `Abort500SiteC` 转递，site 为空时 5xx 日志省略该字段。

## 5xx 日志与排障

`abortAppError` 在渲染 5xx 时额外打一行 stdout ERROR 日志（`log5xx`）：字段为 `code`/`ref`/`path`/`site`（错误构造点）/`cause`（底层错误文本，不进响应体）。2026-11 ADR-021 修正后 `site` 可信地指向**真实调用点**（skip=2 修正 + `Abort500SiteC` 显式转递）：此前的 skip 自指使 5xx 日志 site 恒为 `apperror.go` 行号，`EPS0037` 这类大流量兜底码无法定位到具体端点——现在经 `serverError` 转发的 5xx 日志 site 形如 `episodes.go:1072`，且 nil-cause 调用点已清零。排障流程由此闭合：拿到用户报告的错误码 → grep 源码定位唯一调用点；拿到 ref 或 `X-Ref-ID` → grep 服务日志定位该次请求（`RequestLogger` 的每一行也带 `ref`）。panic 恢复日志（`panic recovered`）同样带 ref 与 stack。

## Swagger 与机器注册表

- `errors.ErrorBody` 是信封的 Swagger 契约模型；全部 4xx/5xx `@Failure` 注解统一引用它（主描述在 `cmd/server/main.go`）。改注解后需 `make docs`（`swag init -g cmd/server/main.go --output docs`）重新生成 `docs/docs.go`/`swagger.json`。
- `docs/errors.json` 是机器可读注册表（756 条，41 个前缀；每条含 code/kind/status/message/file/line），`docs/errors.schema.json` 是其 JSON Schema。生成器 `docs/gen_errors_json.py` 扫描 `internal/{handler,service,middleware,server,errors}` 中全部带码构造器/渲染调用点（含 `Abort500SiteC`、渲染器兜底 `SYS0001` 与 panic `SYS0500`；`Abort500SiteC` 的参数比 `Abort500C` 整体后移一位）——新增错误码后重跑即可，输出字节稳定可直接提交；未提交的 WIP 修改按 HEAD 内容扫描，注册表只反映已提交契约。
- `refSpec` 元数据描述 ref 的取值范围与前端展示格式，与代码契约同步。

## ESA 边缘信封

阿里云 ESA 边缘拦截（地区限制等）在请求到达后端之前就返回错误，因此走独立信封（Schema `docs/esa-error.schema.json`）：

```json
{"message": "此服务在您所在地区不可用。", "error": "ESA0001", "ref": 0,
 "esa_request_id": "<UUID>", "esa_rule": 123, "status": 451}
```

与后端信封的差异：`ref` **恒为 0**——这是"边缘响应"的标记，因为无服务端请求日志，按 ref grep 不到属预期；排障改用 `esa_request_id`（ESA 控制台查边缘日志）与 `esa_rule`（命中的拦截规则 ID）。错误 ID 注册表在 `docs/esa-errors.csv`，新 ID 取 CSV 内下一序号；已登记 `ESA0001` 地区限制（451 Unavailable For Legal Reasons）。ESA0001 同时在后端注册表中预留，避免源码侧误用该前缀。

## 相关阅读

- [请求生命周期](../architecture/request-lifecycle.md)——中间件顺序（panic 恢复与 RequestRef 的位置）与 bodyparse/CSRF/限流的带码响应。
- [约定与陷阱](../quality/conventions-and-gotchas.md)——错误码取号与 message 纯文案等铁律。
- [内容目录 API](content-catalog.md)——消费信封的典型业务域。
- [可观测性与后台任务](../operations/observability-and-background-jobs.md)——RequestLogger 与 5xx 日志的落点。
