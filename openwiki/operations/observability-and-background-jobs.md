---
type: operations
title: 可观测性与后台任务
description: 写入 stdout 的结构化日志、请求与慢请求日志、API 调用统计聚合、审计日志来源、清理定时任务，以及启动时未接入的迁移与索引包。
tags: [operations, logging, metrics, audit, cron, jobs]
verified:
  - by: openwiki/0.7.2
    at: 2026-11-02T17:41:59.592Z
sources:
  - id: openwiki-source-6ca2ae0d44448102cf549f8a
    resource: repo://internal/code/store.go
  - id: openwiki-source-95acc6f0848493b78312ab3f
    resource: repo://internal/cron/cron.go
  - id: openwiki-source-b10e065a15408ca747b6184f
    resource: repo://internal/email/client.go
  - id: openwiki-source-1649edbcafe0eab861fed46a
    resource: repo://internal/handler/backup.go
  - id: openwiki-source-3a7c36409770ce04e5cdeef4
    resource: repo://internal/handler/translate.go
  - id: openwiki-source-a5147957bc4eb2270289c610
    resource: repo://internal/logging/logging.go
  - id: openwiki-source-8cc3b2cd34ec48fbcbb82fd1
    resource: repo://internal/middleware/apitracker.go
  - id: openwiki-source-ec085cff2976e238109f19cf
    resource: repo://internal/middleware/requestlogger.go
  - id: openwiki-source-8121abc37fb11ac1dc1948a2
    resource: repo://internal/repository/misc_repos.go
  - id: openwiki-source-ebbcecfb3e8c07ba5e940ccd
    resource: repo://internal/server/register_routes.go
  - id: openwiki-source-f7acf9d278285ec2e6b81113
    resource: repo://internal/service/auth_service.go
  - id: openwiki-source-ca7fc5574740b07d87bb29b5
    resource: repo://internal/service/password.go
generated: { by: "opencode", at: "2026-11-02T17:41:59.592Z" }
---

# 可观测性与后台任务

可观测性刻意保持轻量：结构化文本日志、一个 API 调用计数器，以及一个审计集合。除 `/api/health` 外，没有 metrics 端点、链路追踪或健康看板。

## 日志

`logging.New(isDev)` 返回写入 **stdout** 的 `slog` 文本 handler：

- 开发环境：Debug 级别；
- 生产环境：Info 级别。

Gin 在开发环境使用 debug 模式，生产环境使用 release 模式。因此请求日志出现在 stdout，可与普通应用流一起采集。

## 请求日志

两个中间件以不同阈值记录请求。

`SlowLogger` 在链中较早运行：

- 开发环境：状态至少 400、耗时超过一秒，或路径包含 `/list`、`/histories`、`/follows` 时记录；超过一秒会附加 `level=SLOW`；
- 生产环境：只以 WARN 记录超过三秒的请求。

`RequestLogger` 在鉴权之后运行，当方法不是 GET、状态至少 400 或耗时超过一秒时记录。字段包括 method、path、status、duration、解析后的客户端 IP，以及上下文中的 user ID。5xx 为 ERROR，4xx 或慢请求为 WARN，其余为 INFO。

由于一秒以内且状态低于 400 的 GET 请求会被跳过，普通读取不会记录。这降低了日志量，但也意味着成功的公开流量在请求日志中不可见。

## 邮件日志

邮件包创建自己的 `slog` 文本 handler，写入 **stderr**。每次发送产生一条：sent、skipped（限流或 SMTP 未配置）、rejected 或 failed，带收件人、主题、host、port 与耗时。这种分离让运维可以独立采集邮件诊断信息。密码与正文从不记录。

## API 调用统计

`APITracker` 统计 URL 路径以 `/api/` 开头的请求，记录 `METHOD <路由模板>`（404 时为原始路径）与 UTC 日期。它在内存中缓冲计数，当待处理请求达到 50 条或每 60 秒刷新一次，先到者触发。刷新按 endpoint 与日期 upsert，对 `count` 执行 `$inc`，对 method 执行 `$setOnInsert`。

刷新失败会被忽略，因此数据库故障会静默丢失缓冲计数。`GET /api/rss/api-usage` 向超管暴露聚合数据。由于缓冲是每进程的，多实例计数只能通过 MongoDB 的 `$inc` 累加，而刷新节奏因实例而异。

## 审计日志

`auditlogs` 文档包含操作者或用户 ID 与名称、action、target、details、IP、User-Agent 与创建时间。仅超管可访问的 `/audit-logs` 端点按 action、操作者名与被操作用户名过滤（转义后的不区分大小写正则），返回 `logs`、`total`、`page` 与 `totalPages`。

写入审计条目的路径包括：

- 认证会话签发（`LOGIN`、`LOGIN_2FA` 等）；
- 密码修改与密码重置；
- 2FA 设置、启用与关闭；
- 邮箱变更与账号注销申请/取消；
- 备份导入（`数据恢复`）。

审计写入是尽力而为的，部分路径异步。值得注意的是，日常管理变更——创建或删除账号、角色变更、创作者主页通过、公告、审核决定——目前**不会**写审计条目。因此审计日志是认证与数据导入轨迹，不是完整的管理轨迹。

## 定时任务

`internal/cron` 定义两个任务：

| 任务 | 计划 | 动作 |
|---|---|---|
| 会话清理 | 每小时整点 | 把 `lastActiveAt` 超过 30 天的活跃会话标记为不活跃 |
| 已读通知清理 | 每天本地时间 03:00 | 删除超过 30 天的已读通知 |

每个任务运行在独立 goroutine 中，先对齐到下一次执行时间，然后用 ticker 驱动。取消上下文会停止循环。cron 包还记录了两个未实现的 TODO：宽限期后物理删除账号，以及自动完结系列并通知追番用户。

通知 TTL 在索引包中还表达为 90 天索引，这与 cron 任务的 30 天已读清理是不同保留窗口。

## 实际启动的内容

启动时以下 goroutine 会运行：

- API 使用统计刷新循环，在构造 `APITracker` 中间件时创建；
- 邮件目标限流清理，由 `RegisterRoutes` 每 10 分钟启动；
- 验证码清理循环，在认证服务创建两个 code store 时启动；
- 翻译批处理 worker，由 translate handler 构造函数启动。

以下内容**不会**启动，因为没有任何调用：

- `cron.StartSessions`；
- `indexes.Ensure`；
- `migrate.Run`。

因此文档与模型描述的清理、索引与迁移行为，当前入口永远不会激活。需要 30 天会话过期、30 天已读通知清理、TTL 索引或遗留数据迁移的部署，必须外部执行或把它们接线。

## 健康检查

`GET /api/health` 位于受限流的 `/api` 组内。它用两秒超时 ping MongoDB，成功返回 200 与 `{status:"ok", timestamp, db:"connected"}`，失败返回 503 与 `db:"disconnected"`。时间戳为 ISO-8601 UTC，毫秒精度。

## 运维影响

- 数据库压力下，API 统计缓冲丢失与审计静默失败是预期行为。
- 成功的 GET 流量按设计在日志中体现不足。
- 内存缓冲与计数在重启后重置。
- 没有 cron 与索引接线时，数据保留完全依赖外部维护。

## 相关阅读

<!-- openwiki: broken internal link [/openwiki/operations/deployment-and-runtime.md] link "/openwiki/operations/deployment-and-runtime.md" is root-absolute, which no real consumer resolves against the repository root (not a coding agent reading the page, not GitHub's Markdown renderer, not a local viewer); use a path relative to this file instead. Fix the href or restore the target, then delete this comment. -->
- [部署与运行时](/openwiki/operations/deployment-and-runtime.md)——进程守护与启动顺序。
<!-- openwiki: broken internal link [/openwiki/architecture/persistence-and-data-model.md] link "/openwiki/architecture/persistence-and-data-model.md" is root-absolute, which no real consumer resolves against the repository root (not a coding agent reading the page, not GitHub's Markdown renderer, not a local viewer); use a path relative to this file instead. Fix the href or restore the target, then delete this comment. -->
- [持久化与数据模型](/openwiki/architecture/persistence-and-data-model.md)——索引与迁移定义。
<!-- openwiki: broken internal link [/openwiki/api/administration-and-moderation.md] link "/openwiki/api/administration-and-moderation.md" is root-absolute, which no real consumer resolves against the repository root (not a coding agent reading the page, not GitHub's Markdown renderer, not a local viewer); use a path relative to this file instead. Fix the href or restore the target, then delete this comment. -->
- [管理与审核 API](/openwiki/api/administration-and-moderation.md)——审计日志端点的访问规则。
