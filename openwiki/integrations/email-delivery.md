---
type: integration
title: 邮件发送
description: go-mail SMTP 客户端、配置来源、按收件人限流、事务邮件模板、发送方法、测试邮件路径，以及仅写入 stderr 的操作日志。
tags: [email, smtp, notifications, templates, rate-limiting]
verified:
  - by: openwiki/0.7.2
    at: 2026-11-02T17:41:59.592Z
sources:
  - id: openwiki-source-7cbcc11e6fe12221a8c16292
    resource: repo://internal/email/client_test.go
  - id: openwiki-source-b10e065a15408ca747b6184f
    resource: repo://internal/email/client.go
  - id: openwiki-source-f13722fd0b53f3b3409ea7b2
    resource: repo://internal/email/senders.go
  - id: openwiki-source-883605882209a03e3ce21f2b
    resource: repo://internal/email/sitetest.go
  - id: openwiki-source-d96b6441d6f178ecb494fa45
    resource: repo://internal/email/targetrate.go
  - id: openwiki-source-1af801b89db284f73af11010
    resource: repo://internal/email/templates.go
generated: { by: "opencode", at: "2026-11-02T17:41:59.592Z" }
---

# 邮件发送

邮件在 `internal/email` 中实现，是对 `wneessen/go-mail` 的薄封装，外加模板与按收件人限流。handler 与 service 直接调用发送方法，并把投递视为尽力而为：大多数发送是 fire-and-forget goroutine，其结果不影响 HTTP 响应。

## 配置

SMTP 配置只来自 `config.Email`，由 ini `[email]` 段或 `EMAIL_*` 环境变量填充。数据库邮件设置已不再读取；`site-content/email` 键只是运行时配置的只读诊断视图。

`GetConfig` 仅在 host、user、password 都非空时返回可用配置。端口默认 465，from name 未设置时默认 `兽剧聚合平台`。

## 传输行为

`sendViaGoMail`：

- 设置收件人、主题并发送 HTML 正文；
- 应用 15 秒超时；
- 465 端口使用隐式 SSL；
- localhost 主机禁用 TLS；
- 其他情况使用机会性 TLS；
- 仅当用户名存在时启用 plain SMTP auth。

配置缺失或发送被拒绝/失败时返回 false，而不是错误。

## 按收件人限流

`TargetRate` 是以小写收件人地址为键的内存限流器。默认每个地址每小时 **10 封**。`Send` 在访问 SMTP 之前检查，因此超限消息会被跳过并记录日志。`RegisterRoutes` 启动的清理 goroutine 每 10 分钟移除过期时间戳。与其他内存状态一样，该限制是进程本地的。

## 日志

所有邮件操作通过专用 slog logger 写入 **stderr**，与 stdout 上的请求日志分离。每次发送恰好一条：

- `sent`，带收件人、主题、host、port 与耗时；
- `skip`，限流或 SMTP 未配置时；
- `send failed`，带错误；
- `send rejected`，传输返回 false 但没有错误时。

密码与邮件正文从不记录。

## 模板

`BuildEmailHTML` 把正文包进带 preheader、Logo 与页脚的品牌 HTML 布局。品牌数据来自站点内容：

- `settings` 提供站点名与导航 Logo（缺失时回退默认图标）；
- `about` 提供 ICP、公安备案与版本号用于页脚。

两个文档都缓存五分钟，可用 `ClearCache` 失效。布局链接到 GitHub 项目并声明许可证。

两个辅助函数导出给领域正文使用：

- `EmailButton(text, url, variant)`，支持 primary、success、danger、secondary 样式；
- `EmailInfoBox(content, variant)`，支持 info、warning、danger、success 样式。

## 发送方法

| 方法 | 用途 |
|---|---|
| `SendVerificationCodeEmail(to, code, scene)` | 注册、登录或邮箱变更的六位验证码；提示 10 分钟有效 |
| `SendPasswordResetEmail(to, token)` | 一小时有效的密码重置链接 |
| `SendNewDeviceLoginEmail(to, device, ip, region, code, time)` | 设备详情加验证码，并附 Apple 版本号免责声明 |
| `SendNotificationEmail(to, subject, html, preheader)` | 领域自建 HTML 的通用包装 |

其他领域构建自己的 HTML 片段并调用 `SendNotificationEmail`；例如剧集更新、审核结果、反馈回复、公告与创作者主页审核通知。

## 偏好门控

邮件客户端不知道通知偏好。调用方在发送前检查用户的 `emailNotificationPrefs` 与 `isEmailVerified`。例如剧集更新邮件检查 `episodeUpdate`，反馈回复检查 `feedbackReply`，审核结果检查 `reviewResult`。站内通知不受邮件偏好影响，仍会创建。

## 注入点

`Client.SetSendMail(fn)` 替换 SMTP 传输函数。测试用它断言收件人、主题与成功行为，无需服务器。默认实现是 `sendViaGoMail`。管理端测试邮件端点还有第二条传输路径，用调用方提供的凭据构造自己的客户端，使运维能在保存配置前验证 SMTP 设置。

## 测试邮件

`POST /site-content/test-email` 接收 host、port、user、password、from name 与收件人，构建测试邮件并通过 `SendSiteTestEmail` 发送。它绕过按收件人限流与已配置的 SMTP 设置，写入 stderr 日志，并把错误返回给 handler 渲染为 400。该端点仅超管可用。

## 失败行为

- SMTP 未配置：发送被跳过并返回 false；登录用户重发验证路径会提示“邮件服务未配置，请联系管理员”。
- 收件人限流：跳过并警告；调用方看到 false。
- 发送失败：记录错误；调用方通常忽略。
- 需要确认的邮箱变更在邮件不可用时返回 503，因为流程无法继续。

## 相关阅读

<!-- openwiki: broken internal link [/openwiki/api/notifications-and-realtime.md] link "/openwiki/api/notifications-and-realtime.md" is root-absolute, which no real consumer resolves against the repository root (not a coding agent reading the page, not GitHub's Markdown renderer, not a local viewer); use a path relative to this file instead. Fix the href or restore the target, then delete this comment. -->
- [通知与实时推送](/openwiki/api/notifications-and-realtime.md)——偏好键与站内通知。
<!-- openwiki: broken internal link [/openwiki/auth/account-security.md] link "/openwiki/auth/account-security.md" is root-absolute, which no real consumer resolves against the repository root (not a coding agent reading the page, not GitHub's Markdown renderer, not a local viewer); use a path relative to this file instead. Fix the href or restore the target, then delete this comment. -->
- [账号安全能力](/openwiki/auth/account-security.md)——验证与重置流程。
<!-- openwiki: broken internal link [/openwiki/operations/observability-and-background-jobs.md] link "/openwiki/operations/observability-and-background-jobs.md" is root-absolute, which no real consumer resolves against the repository root (not a coding agent reading the page, not GitHub's Markdown renderer, not a local viewer); use a path relative to this file instead. Fix the href or restore the target, then delete this comment. -->
- [可观测性与后台任务](/openwiki/operations/observability-and-background-jobs.md)——stdout 与 stderr 日志分流。
