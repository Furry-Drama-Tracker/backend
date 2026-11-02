---
type: guide
title: 快速开始
description: neo-server 是什么、最短的构建与运行路径、无法启动时缺失的配置，以及按任务查阅后续页面。
tags: [quickstart, setup, navigation]
verified:
  - by: openwiki/0.7.2
    at: 2026-11-02T17:41:59.592Z
sources:
  - id: openwiki-source-0f6ed11169bd8e6b34b44280
    resource: repo://cmd/server/main.go
  - id: openwiki-source-6ca2ae0d44448102cf549f8a
    resource: repo://internal/code/store.go
  - id: openwiki-source-a8910515ddd14810ad43f5c1
    resource: repo://internal/config/config.go
  - id: openwiki-source-da21c4a01f369bb3503d0dfb
    resource: repo://internal/handler/csrf.go
  - id: openwiki-source-7e5389f01ee56b40448faee1
    resource: repo://internal/handler/notifications.go
  - id: openwiki-source-7106c8455bb73d12832c8523
    resource: repo://internal/server/app.go
  - id: openwiki-source-a8fb1ba11cf3dbe31498bcae
    resource: repo://internal/server/integration_test.go
  - id: openwiki-source-012f2c78e3b1446dfc35803f
    resource: repo://Makefile
generated: { by: "opencode", at: "2026-11-02T17:41:59.592Z" }
---

# 快速开始

neo-server 是兽剧聚合平台的 Go/Gin 后端，用于聚合与追踪兽剧内容。它是单一 HTTP 服务，后端使用 MongoDB；前端 React 应用位于另一个仓库，通过 `/api` 调用并依赖既有响应契约。

## 前置条件

- Go 1.26 或更高版本。
- 可访问的 MongoDB。
- 至少 32 字符的 `JWT_SECRET` 与 `MONGO_URI`。缺少任一或 secret 过短时进程立即退出。
- 可写的上传目录（默认 `./uploads` 不会自动创建）。

## 构建与运行

```bash
cd backend
make build
./bin/furry-drama-be-neo
```

开发时创建 `backend/.env`，至少包含：

```
JWT_SECRET=replace-with-at-least-32-random-characters
MONGO_URI=mongodb://127.0.0.1:27017/furry_drama_tracker
NODE_ENV=development
```

`.env` 会从工作目录自动加载。真实环境变量覆盖它，两者都覆盖 ini 文件。默认监听 `tcp:0.0.0.0:5000`；Unix socket 使用 `--listen=unix:/run/furry-drama-be-neo.sock`，生产配置使用 `--config=/path/to/backend.ini`。

## 验证

- MongoDB 可访问时 `GET /api/health` 返回 `status: ok`，否则 503。
- `GET /api/csrf-token` 设置 CSRF cookie；非 GET 请求需要匹配的 `X-XSRF-TOKEN` 头。
- `GET /api/docs` 仅在开发环境提供 Swagger UI。

## 测试

```bash
cd backend
go test ./...    # 单元与集成测试；集成测试需要 127.0.0.1:27017 上的 mongod
go vet ./...
```

`go test ./...` 是行为回归闸门。没有 CI 流水线，因此必须本地运行。

## 接下来读什么

| 如果你需要…… | 阅读 |
|---|---|
<!-- openwiki: broken internal link [/openwiki/architecture/overview.md] link "/openwiki/architecture/overview.md" is root-absolute, which no real consumer resolves against the repository root (not a coding agent reading the page, not GitHub's Markdown renderer, not a local viewer); use a path relative to this file instead. Fix the href or restore the target, then delete this comment. -->
| 理解包布局与依赖注入 | [架构总览](/openwiki/architecture/overview.md) |
<!-- openwiki: broken internal link [/openwiki/architecture/request-lifecycle.md] link "/openwiki/architecture/request-lifecycle.md" is root-absolute, which no real consumer resolves against the repository root (not a coding agent reading the page, not GitHub's Markdown renderer, not a local viewer); use a path relative to this file instead. Fix the href or restore the target, then delete this comment. -->
| 跟踪请求经过中间件、CSRF 与限流 | [请求生命周期与中间件管线](/openwiki/architecture/request-lifecycle.md) |
<!-- openwiki: broken internal link [/openwiki/architecture/configuration.md] link "/openwiki/architecture/configuration.md" is root-absolute, which no real consumer resolves against the repository root (not a coding agent reading the page, not GitHub's Markdown renderer, not a local viewer); use a path relative to this file instead. Fix the href or restore the target, then delete this comment. -->
<!-- openwiki: broken internal link [/openwiki/operations/deployment-and-runtime.md] link "/openwiki/operations/deployment-and-runtime.md" is root-absolute, which no real consumer resolves against the repository root (not a coding agent reading the page, not GitHub's Markdown renderer, not a local viewer); use a path relative to this file instead. Fix the href or restore the target, then delete this comment. -->
| 配置或部署服务 | [配置与环境变量](/openwiki/architecture/configuration.md) 与 [部署与运行时](/openwiki/operations/deployment-and-runtime.md) |
<!-- openwiki: broken internal link [/openwiki/architecture/persistence-and-data-model.md] link "/openwiki/architecture/persistence-and-data-model.md" is root-absolute, which no real consumer resolves against the repository root (not a coding agent reading the page, not GitHub's Markdown renderer, not a local viewer); use a path relative to this file instead. Fix the href or restore the target, then delete this comment. -->
| 处理 MongoDB 集合、索引或迁移 | [持久化与数据模型](/openwiki/architecture/persistence-and-data-model.md) |
<!-- openwiki: broken internal link [/openwiki/auth/login-and-registration.md] link "/openwiki/auth/login-and-registration.md" is root-absolute, which no real consumer resolves against the repository root (not a coding agent reading the page, not GitHub's Markdown renderer, not a local viewer); use a path relative to this file instead. Fix the href or restore the target, then delete this comment. -->
<!-- openwiki: broken internal link [/openwiki/auth/session-and-token-lifecycle.md] link "/openwiki/auth/session-and-token-lifecycle.md" is root-absolute, which no real consumer resolves against the repository root (not a coding agent reading the page, not GitHub's Markdown renderer, not a local viewer); use a path relative to this file instead. Fix the href or restore the target, then delete this comment. -->
<!-- openwiki: broken internal link [/openwiki/auth/middleware-and-authorization.md] link "/openwiki/auth/middleware-and-authorization.md" is root-absolute, which no real consumer resolves against the repository root (not a coding agent reading the page, not GitHub's Markdown renderer, not a local viewer); use a path relative to this file instead. Fix the href or restore the target, then delete this comment. -->
| 修改登录、刷新或鉴权行为 | [注册与登录流程](/openwiki/auth/login-and-registration.md)、[会话与令牌生命周期](/openwiki/auth/session-and-token-lifecycle.md)、[鉴权中间件与角色](/openwiki/auth/middleware-and-authorization.md) |
<!-- openwiki: broken internal link [/openwiki/auth/account-security.md] link "/openwiki/auth/account-security.md" is root-absolute, which no real consumer resolves against the repository root (not a coding agent reading the page, not GitHub's Markdown renderer, not a local viewer); use a path relative to this file instead. Fix the href or restore the target, then delete this comment. -->
| 修改密码、2FA、邮箱或账号注销 | [账号安全能力](/openwiki/auth/account-security.md) |
<!-- openwiki: broken internal link [/openwiki/api/content-catalog.md] link "/openwiki/api/content-catalog.md" is root-absolute, which no real consumer resolves against the repository root (not a coding agent reading the page, not GitHub's Markdown renderer, not a local viewer); use a path relative to this file instead. Fix the href or restore the target, then delete this comment. -->
<!-- openwiki: broken internal link [/openwiki/api/administration-and-moderation.md] link "/openwiki/api/administration-and-moderation.md" is root-absolute, which no real consumer resolves against the repository root (not a coding agent reading the page, not GitHub's Markdown renderer, not a local viewer); use a path relative to this file instead. Fix the href or restore the target, then delete this comment. -->
| 新增或修改内容、内容库或管理端点 | [内容目录 API](/openwiki/api/content-catalog.md)、[管理与审核 API](/openwiki/api/administration-and-moderation.md) |
<!-- openwiki: broken internal link [/openwiki/api/cms-and-site-content.md] link "/openwiki/api/cms-and-site-content.md" is root-absolute, which no real consumer resolves against the repository root (not a coding agent reading the page, not GitHub's Markdown renderer, not a local viewer); use a path relative to this file instead. Fix the href or restore the target, then delete this comment. -->
| 处理 CMS、主题、图标或创作者主页 | [CMS 与站点内容 API](/openwiki/api/cms-and-site-content.md) |
<!-- openwiki: broken internal link [/openwiki/api/notifications-and-realtime.md] link "/openwiki/api/notifications-and-realtime.md" is root-absolute, which no real consumer resolves against the repository root (not a coding agent reading the page, not GitHub's Markdown renderer, not a local viewer); use a path relative to this file instead. Fix the href or restore the target, then delete this comment. -->
| 处理通知、SSE 或推送 | [通知与实时推送](/openwiki/api/notifications-and-realtime.md) |
<!-- openwiki: broken internal link [/openwiki/api/utilities-and-uploads.md] link "/openwiki/api/utilities-and-uploads.md" is root-absolute, which no real consumer resolves against the repository root (not a coding agent reading the page, not GitHub's Markdown renderer, not a local viewer); use a path relative to this file instead. Fix the href or restore the target, then delete this comment. -->
| 处理上传、RSS、翻译、备份或分页 | [上传、RSS、翻译、备份与分页](/openwiki/api/utilities-and-uploads.md) |
<!-- openwiki: broken internal link [/openwiki/integrations/email-delivery.md] link "/openwiki/integrations/email-delivery.md" is root-absolute, which no real consumer resolves against the repository root (not a coding agent reading the page, not GitHub's Markdown renderer, not a local viewer); use a path relative to this file instead. Fix the href or restore the target, then delete this comment. -->
| 排查邮件投递 | [邮件发送](/openwiki/integrations/email-delivery.md) |
<!-- openwiki: broken internal link [/openwiki/integrations/external-services.md] link "/openwiki/integrations/external-services.md" is root-absolute, which no real consumer resolves against the repository root (not a coding agent reading the page, not GitHub's Markdown renderer, not a local viewer); use a path relative to this file instead. Fix the href or restore the target, then delete this comment. -->
| 理解 Altcha、IP 地域或 VAPID | [外部服务](/openwiki/integrations/external-services.md) |
<!-- openwiki: broken internal link [/openwiki/operations/observability-and-background-jobs.md] link "/openwiki/operations/observability-and-background-jobs.md" is root-absolute, which no real consumer resolves against the repository root (not a coding agent reading the page, not GitHub's Markdown renderer, not a local viewer); use a path relative to this file instead. Fix the href or restore the target, then delete this comment. -->
| 调查日志、计数或后台任务 | [可观测性与后台任务](/openwiki/operations/observability-and-background-jobs.md) |
<!-- openwiki: broken internal link [/openwiki/quality/testing.md] link "/openwiki/quality/testing.md" is root-absolute, which no real consumer resolves against the repository root (not a coding agent reading the page, not GitHub's Markdown renderer, not a local viewer); use a path relative to this file instead. Fix the href or restore the target, then delete this comment. -->
| 编写或运行测试 | [测试策略](/openwiki/quality/testing.md) |
<!-- openwiki: broken internal link [/openwiki/quality/conventions-and-gotchas.md] link "/openwiki/quality/conventions-and-gotchas.md" is root-absolute, which no real consumer resolves against the repository root (not a coding agent reading the page, not GitHub's Markdown renderer, not a local viewer); use a path relative to this file instead. Fix the href or restore the target, then delete this comment. -->
| 避免破坏兼容契约 | [约定与有意保留的怪癖](/openwiki/quality/conventions-and-gotchas.md) |

## 首先要知道的事

- 每个领域路由也挂载在 `/api/v1`，并带固定弃用头。
- 非 GET 请求需要 CSRF，419 表示 access 令牌过期。
- Refresh 令牌带随机 `jti` 轮换；30 秒宽限期外的重用会吊销全部会话。
- 多个子系统已定义但未在启动时接线：索引、迁移、会话/通知清理 cron 以及 Web Push 发送。不要假设它们会运行。
- 验证码、限流计数、剧集缓存与 SSE 连接状态是进程本地的，多实例部署需要共享存储。
