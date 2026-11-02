---
type: api-domain
title: 通知与实时推送
description: 通知文档与端点、SSE 流的票据认证与连接上限、邮件通知偏好，以及已存储但不会发送的 Web Push 订阅。
tags: [api, notifications, sse, push, email]
verified:
  - by: openwiki/0.7.2
    at: 2026-11-02T17:41:59.592Z
sources:
  - id: openwiki-source-314bd77139a7d3c62ed804bf
    resource: repo://internal/handler/auth.go
  - id: openwiki-source-f89e324c07065d291ea97e16
    resource: repo://internal/handler/episodes.go
  - id: openwiki-source-7e5389f01ee56b40448faee1
    resource: repo://internal/handler/notifications.go
  - id: openwiki-source-5892c79bee89b56ec4322f3f
    resource: repo://internal/handler/reports.go
  - id: openwiki-source-b9d3a8c329294b54d48d5a73
    resource: repo://internal/handler/review.go
  - id: openwiki-source-c04b809e997b6644e3320acf
    resource: repo://internal/model/model.go
  - id: openwiki-source-bb3d20ad5ebcfd9197f76c8a
    resource: repo://internal/repository/push_subscription.go
  - id: openwiki-source-f7acf9d278285ec2e6b81113
    resource: repo://internal/service/auth_service.go
generated: { by: "opencode", at: "2026-11-02T17:41:59.592Z" }
---

# 通知与实时推送

通知投递分为持久化的站内通知，以及两种完成度不同的传输方式：目前只维护连接的 SSE 流，和没有发送器的 Web Push 订阅登记。

## 通知文档

`model.Notification` 映射 `notifications` 集合：`userId`、可选的 `episodeId` 与剧集标题、`type`、`link`、`message`、`metadata`、`isRead` 和 `createdAt`。响应组装把 `episodeId` 输出为 hex 字符串或 `null`，`metadata` 为空时输出 `{}`。各领域 handler 直接通过 `Repos.Notifications` 创建通知，没有统一的通知服务。

代码中可见的通知类型包括：

| 类型 | 生产者 |
|---|---|
| `new_episode` | 剧集创建、集数增加、审核通过 |
| `review_result` | 剧集审核决定 |
| `report_result` | 举报处理 |
| `profile_review` | 创作者主页审核 |
| `friend_link_apply` | 友链申请（发给超管） |
| `feedback_reply` | 管理员反馈回复 |
| `announcement` | 公告广播 |

## 端点

除 `/stream` 与 `/vapid-public-key` 外，通知路由都需要认证。

| 路由 | 行为 |
|---|---|
| `GET /list` | 按 `page`/`limit` 分页，每页最多 100 |
| `GET /unread-count` | 未读通知数量 |
| `PUT /read-all` | 全部未读标记为已读 |
| `PUT /read-episode/:episodeId` | 某剧集的未读通知全部标记为已读 |
| `PUT /read/:id` | 单条标记已读，限定调用者 |
| `DELETE /clear-read` | 删除全部已读通知 |
| `DELETE /:id` | 删除单条，限定调用者 |
| `POST /subscribe-reminder` | 若不存在追番关系则创建 |
| `GET /vapid-public-key` | 公开 VAPID 公钥，未配置时为 `null` |
| `POST /push/subscribe` | 为调用者 upsert 推送订阅 |
| `POST /push/unsubscribe` | 按 endpoint 删除订阅 |

`subscribe-reminder` 复用追番机制而不是独立订阅存储：订阅更新提醒本质上就是“确保追番关系存在”。

分页使用类似 JavaScript 的转换：`page` 默认 1 并强制为正；`limit` 默认 20，负值变为 1，零回退默认值，超过 100 被钳制。

## SSE 流

`GET /notifications/stream` 在路由层是公开的，因为它使用短期票据而不是 access token 认证。`GET /auth/sse-ticket`（需认证）签发有效 30 秒、purpose 为 `sse-ticket` 的 JWT。流 handler 校验 JWT 签名与 purpose，并把 subject 当作 user ID。

连接簿记在进程内并由互斥锁保护：

- 每个客户端 IP 最多 10 条并发连接；
- 全局最多 500 条并发连接；
- 每个用户最多 5 条连接，第 6 条会取消最旧的连接。

handler 写入 SSE 头（`text/event-stream`、`no-cache`、`keep-alive`、`X-Accel-Buffering: no`），发出 `connected` 事件，然后循环等待四种条件：上下文取消、客户端关闭、30 分钟连接超时、30 秒心跳（`:heartbeat`）。超限时发送 `error` 事件并结束流；每 IP 检查发生在 `connected` 之前，全局上限检查发生在它之后。

当前的重要限制：连接登记只用于计数与驱逐。没有任何 handler 向 `sseClients` 广播通知负载，因此流永远不会投递真实通知事件。客户端仍需轮询 `/list` 或 `/unread-count` 获取内容。由于登记是进程本地的，上限与驱逐也不跨实例生效。

## 邮件通知偏好

用户文档带有七个布尔偏好，默认全部为 true：

`episodeUpdate`、`newDeviceLogin`、`feedbackReply`、`friendLinkStatus`、`friendLinkApply`、`announcement`、`reviewResult`。

这些偏好不会阻止通知创建，只门控对应的邮件发送；邮件由生产领域异步发送，且仅在邮箱已验证时发送。例如剧集更新邮件检查 `episodeUpdate`，反馈回复检查 `feedbackReply`，审核结果检查 `reviewResult`。

偏好通过 `PUT /api/auth/email-notification-prefs` 管理，并出现在 `/auth/me` 响应中。

## Web Push

推送订阅是 `pushsubscriptions` 集合中的真实文档，逻辑上以 `userId + endpoint` 为键，存储浏览器 endpoint、`p256dh` 与 `auth` 密钥、User-Agent 和创建时间。该 repository 不在 `Repos` 聚合内，由通知 handler 直接用数据库句柄构造。

订阅端点可用：subscribe 按 endpoint upsert，unsubscribe 幂等删除，账号删除会级联移除该用户的全部订阅。

发送不可用：每个领域的 `sendPushToUser` 辅助函数都是显式空实现，VAPID 配置只作为公钥暴露给客户端。因此即使订阅存在，也永远不会投递 Web Push 消息。

## 与其他领域的关系

- 剧集、审核、举报、主页、友链、反馈与公告流程都会写通知；具体消息与副作用见内容、管理、CMS 与反馈页面。
- 邮件传输、模板与按收件人限流记录在邮件集成页面。
- SSE 票据使用与认证相同的签名器与 purpose 机制，校验规则遵循令牌生命周期页面。

## 相关阅读

<!-- openwiki: broken internal link [/openwiki/auth/session-and-token-lifecycle.md] link "/openwiki/auth/session-and-token-lifecycle.md" is root-absolute, which no real consumer resolves against the repository root (not a coding agent reading the page, not GitHub's Markdown renderer, not a local viewer); use a path relative to this file instead. Fix the href or restore the target, then delete this comment. -->
- [会话与令牌生命周期](/openwiki/auth/session-and-token-lifecycle.md)——令牌 purpose 与签名。
<!-- openwiki: broken internal link [/openwiki/integrations/email-delivery.md] link "/openwiki/integrations/email-delivery.md" is root-absolute, which no real consumer resolves against the repository root (not a coding agent reading the page, not GitHub's Markdown renderer, not a local viewer); use a path relative to this file instead. Fix the href or restore the target, then delete this comment. -->
- [邮件发送](/openwiki/integrations/email-delivery.md)——实际邮件传输与偏好检查。
<!-- openwiki: broken internal link [/openwiki/integrations/external-services.md] link "/openwiki/integrations/external-services.md" is root-absolute, which no real consumer resolves against the repository root (not a coding agent reading the page, not GitHub's Markdown renderer, not a local viewer); use a path relative to this file instead. Fix the href or restore the target, then delete this comment. -->
- [外部服务](/openwiki/integrations/external-services.md)——VAPID 配置。
