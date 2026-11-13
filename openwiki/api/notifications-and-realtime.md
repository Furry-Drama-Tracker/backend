---
type: api-domain
title: 通知与实时推送
description: 通知文档与端点、SSE 流的票据认证与连接上限、邮件通知偏好，以及已存储但不会发送的 Web Push 订阅。
tags: [api, notifications, sse, push, email]
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
generated: { by: "zcode", at: "2026-11-13T18:41:36.261Z" }
verified:
  - by: openwiki/0.7.2
    at: 2026-11-13T18:41:36.261Z
---

# 通知与实时推送

通知投递分为持久化的站内通知，以及两种完成度不同的传输方式：通过数据库轮询推送新通知的 SSE 流，和没有发送器的 Web Push 订阅登记。

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

追番更新类通知（`new_episode`）的站内文案与 Push 正文按 `episodeRefText` 解析集数称呼——单集自定义 `episodeLabel` 非空时用它，否则回退「第N集」；label 非空时 metadata 还会携带 `episodeLabel`。

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

每 IP 上限中的客户端 IP 统一取自 gin 的 `c.ClientIP()`：SSE 流不再维护手写的 `clientIP` 方法，标头信任策略由 `[server]` 配置在引擎级装配（见请求生命周期页面）。上限数值本身不变。

handler 写入 SSE 头（`text/event-stream`、`no-cache`、`keep-alive`、`X-Accel-Buffering: no`），清除服务端 WriteTimeout 对该连接的写截止时间，发出 `connected` 事件，然后循环等待五种条件：上下文取消、客户端关闭、30 分钟连接超时、30 秒心跳（`:heartbeat`）、以及每 10 秒一次的通知轮询。超限时发送 `error` 事件并结束流；每 IP 检查发生在 `connected` 之前，全局上限检查发生在它之后。

通知轮询以数据库为唯一事实来源：每 10 秒读取当前用户最近 50 条通知与未读数，把 `createdAt` 晚于连接游标的条目作为 `event: notification` 事件推送，负载包含 `notification` 与 `unreadCount`。因此多实例与重启后仍能工作，代价是每连接每 10 秒一次数据库查询。连接登记本身仍是进程本地的，上限与驱逐不跨实例生效。

## 邮件通知偏好

用户文档带有七个布尔偏好，默认全部为 true：

`episodeUpdate`、`newDeviceLogin`、`feedbackReply`、`friendLinkStatus`、`friendLinkApply`、`announcement`、`reviewResult`。

这些偏好不会阻止通知创建，只门控对应的邮件发送；邮件由生产领域异步发送，且仅在邮箱已验证时发送。例如剧集更新邮件检查 `episodeUpdate`，反馈回复检查 `feedbackReply`，审核结果检查 `reviewResult`。

偏好通过 `PUT /api/auth/email-notification-prefs` 管理，并出现在 `/auth/me` 响应中。`/auth/me` 与登录/refresh 响应的用户对象是固定字段契约（`勿增删`）：登录/refresh 用 `SessionUserJSON`，`/auth/me` 用 `MeJSON`（额外带 `avatar` 与偏好对象）；两者当前都包含 `_id`、`uid`（数字用户锚）、`accountId`、`username`、`email`、`role`、`twoFactorEnabled`（前端 2FA 状态展示的权威来源）、`force2FASetup`（admin/superadmin 未开 2FA 时的强制设置标记）与 `forceEmailChange`（超管未改默认邮箱标记）。

## Web Push

推送订阅是 `pushsubscriptions` 集合中的真实文档，逻辑上以 `userId + endpoint` 为键，存储浏览器 endpoint、`p256dh` 与 `auth` 密钥、User-Agent 和创建时间。该 repository 不在 `Repos` 聚合内，由通知 handler 直接用数据库句柄构造。

订阅端点可用：subscribe 按 endpoint upsert，unsubscribe 幂等删除，账号删除会级联移除该用户的全部订阅。

发送不可用：每个领域的 `sendPushToUser` 辅助函数都是显式空实现，VAPID 配置只在公钥与私钥都已配置时通过 `/vapid-public-key` 暴露给客户端。因此即使订阅存在，也永远不会投递 Web Push 消息。

## 与其他领域的关系

- 剧集、审核、举报、主页、友链、反馈与公告流程都会写通知；具体消息与副作用见内容、管理、CMS 与反馈页面。
- 邮件传输、模板与按收件人限流记录在邮件集成页面。
- SSE 票据使用与认证相同的签名器与 purpose 机制，校验规则遵循令牌生命周期页面。

## 相关阅读

- [会话与令牌生命周期](../auth/session-and-token-lifecycle.md)——令牌 purpose 与签名。
- [邮件发送](../integrations/email-delivery.md)——实际邮件传输与偏好检查。
- [外部服务](../integrations/external-services.md)——VAPID 配置。
- [请求生命周期与中间件管线](../architecture/request-lifecycle.md)——客户端 IP 的统一解析策略。
