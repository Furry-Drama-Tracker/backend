---
type: quality
title: 测试策略
description: Go 单元与集成测试套件、MongoDB 依赖、两种应用夹具模式、带 CSRF 与 cookie 的 HTTP 辅助工具、覆盖范围以及仍存在的缺口。
tags: [testing, go-test, integration, mongodb, regression]
sources:
  - id: openwiki-source-193053215eb2d471ac4f675e
    resource: repo://docs/contributing.md
  - id: openwiki-source-605d1503b82e25824739c4d3
    resource: repo://internal/handler/episodes_test.go
  - id: openwiki-source-e6c7522152e60d391a17f718
    resource: repo://internal/indexes/indexes_test.go
  - id: openwiki-source-c7ecdff94f5112a400a92cb2
    resource: repo://internal/middleware/csrf_test.go
  - id: openwiki-source-a5ee09c1401361b766b6b860
    resource: repo://internal/middleware/middleware_test.go
  - id: openwiki-source-fce8bcc8408620f767d87b7a
    resource: repo://internal/migrate/migrate_test.go
  - id: openwiki-source-a8fb1ba11cf3dbe31498bcae
    resource: repo://internal/server/integration_test.go
  - id: openwiki-source-6070436ccd2d188f29f21465
    resource: repo://internal/server/series_test.go
  - id: openwiki-source-012f2c78e3b1446dfc35803f
    resource: repo://Makefile
generated: { by: "opencode", at: "2026-11-03T16:50:44.301Z" }
verified:
  - by: openwiki/0.7.2
    at: 2026-11-03T16:50:44.301Z
---

# 测试策略

仓库没有专门测试运行器或框架，只使用标准 `go test`。33 个 `_test.go` 文件中共有约 179 个测试函数。大多数行为要么通过小型 Gin 路由的聚焦单元测试验证，要么通过基于真实 MongoDB 装配真实应用的 HTTP 集成测试验证。

## 前置条件与命令

```bash
cd backend
go test ./...        # 完整套件；需要 127.0.0.1:27017 上的 mongod
go test ./internal/server -run TestRegisterLoginMeRefresh -v
go vet ./...
go build ./...
```

没有 CI 流水线。`go test ./...` 是记录在案的行为回归闸门，贡献指南要求在影响 HTTP 行为的改动后运行它，外加 `go vet` 与构建。

需要 MongoDB 的测试连接 `mongodb://127.0.0.1:27017`；不可用时失败而不是跳过。不访问数据库的单元测试可以单独运行，但通常以 `./...` 调用，因此本地 `mongod` 是整套测试的实际基线。

## 单元级测试

以下包测试纯逻辑或可注入逻辑，不需要 MongoDB：

- `internal/middleware`：安全头、CORS、请求体解析、CSRF、限流、API 统计、超时与日志辅助，使用 `httptest`。
- `internal/auth`：bcrypt 哈希与比较、字段加密往返与兼容性、TOTP 向量。
- `internal/code`：验证码 TTL、尝试次数与清理。
- `internal/cron`：调度对齐辅助函数。
- `internal/email`：目标限流、未配置 SMTP 行为与注入发送点。
- `internal/ipregion`：本地 IP 处理与注入 fetch 的缓存行为。
- `internal/security`：XSS 清洗与密码校验。
- `internal/ratelimit`：spec 与 IP key 行为。
- `internal/upload`：图片校验、魔数、SVG 规则与路径安全。
- `internal/altcha` 行为通过 service 测试覆盖：用官方库创建、求解并验证挑战。

这些测试快速且确定；外部网络调用由注入函数替换。

## 数据库测试

`internal/indexes` 与 `internal/migrate` 有自己的 MongoDB 夹具。它们创建隔离数据库，执行操作，断言索引或迁移后的文档，并在清理时删除数据库。

`internal/handler` 等领域包包含集成式测试，用相关 handler 与真实 `Repos` 构建小型 Gin 应用：

- 剧集、收藏夹、追番/收藏、管理及对应路由组；
- 夹具在测试前后删除专用数据库，清空全局剧集缓存，设置临时上传目录，并只安装该领域需要的中间件（recovery、请求体解析、清洗、CSRF）。

`internal/server` 包含端到端测试，用完整生产装配调用 `server.NewApp`，因此真实中间件链、路由表与 `/api` 加 `/api/v1` 挂载都会被覆盖。覆盖包括注册/登录/me/refresh、错误密码、2FA 登录、refresh 重用、通知与 SSE、历史与评分、举报/审核/统计、CMS 域、系列、RSS、翻译校验与备份导出/导入。

## 夹具模式

共存两种数据库命名策略：

- `integration_test.go` 使用固定名 `neo_integration_test`，在测试开始与清理时删除，因此可重复但无法并行执行。
- `series_test.go` 及部分较新测试使用唯一名称如 `neo_series_test_<UnixNano>`，在运行之间隔离而无需全局删除。

清理始终使用 `context.Background()` 而不是 `t.Context()`，因为测试上下文在清理阶段已取消，删除会失败。

## HTTP 测试客户端

集成辅助函数构建带 cookie jar 与 CSRF 引导的 `http.Client`：

1. GET `/api/csrf-token` 获取 cookie 与 token；
2. 为每个非 GET 请求附加 `X-XSRF-TOKEN`；
3. 通过 jar 自动携带鉴权 cookie；
4. 可选使用 bearer token 与 `x-dev-token` 头绕过 Altcha。

角色专用辅助函数会注册、登录并直接在数据库中提升用户，然后返回该用户 ID 与独立客户端，避免 cookie jar 相互覆盖。

## 覆盖较好的部分

- 认证分支行为：错误密码、2FA、refresh 重用、重复注册、密码修改。
- CSRF 与中间件契约细节。
- 剧集可见性、审核门控、观看计数、单集通知、重新提交与删除。
- 收藏夹分享、归属校验与未分类行为。
- 管理级联、角色门控、审计日志列表。
- 备份导入/导出与覆盖语义。
- CMS 公告广播行为与友链申请。
- 加密兼容性：bcrypt 前缀、字段加密格式、TOTP 向量、Altcha 往返。

## 缺口

- 没有测试断言 `cron.StartSessions`、`indexes.Ensure` 或 `migrate.Run` 已在 `main` 中接线；这些启动路径仍靠人工检查启动日志。
- Web Push 发送无法测试，因为它是空实现。
- SSE 测试覆盖票据认证、连接与读取流程；10 秒轮询广播依赖时间，缺少确定性测试。
- 翻译测试避免真实提供方；Google/Bing/Azure 分支没有集成测试。
- 没有覆盖率阈值，Makefile 中也没有 race detector 配置。

## 相关阅读

- [快速开始](../quickstart.md)——本地设置。
- [约定与有意保留的怪癖](conventions-and-gotchas.md)——测试保护的兼容规则。
- [请求生命周期与中间件管线](../architecture/request-lifecycle.md)——被断言的中间件。
