---
type: integration
title: 外部服务
description: Altcha 工作量证明集成及其密钥派生、带缓存上限与失败回退的 ipapi.co 地域查询，以及只暴露配置却从不用于发送的 VAPID 设置。
tags: [integration, altcha, geolocation, vapid, external-apis]
sources:
  - id: openwiki-source-a8910515ddd14810ad43f5c1
    resource: repo://internal/config/config.go
  - id: openwiki-source-7e5389f01ee56b40448faee1
    resource: repo://internal/handler/notifications.go
  - id: openwiki-source-5892c79bee89b56ec4322f3f
    resource: repo://internal/handler/reports.go
  - id: openwiki-source-9d45be1491b0521011ea6e6a
    resource: repo://internal/ipregion/client.go
  - id: openwiki-source-f7acf9d278285ec2e6b81113
    resource: repo://internal/service/auth_service.go
  - id: openwiki-source-1994cba008c20be8b9db7693
    resource: repo://internal/service/login.go
generated: { by: "opencode", at: "2026-11-03T16:50:44.301Z" }
verified:
  - by: openwiki/0.7.2
    at: 2026-11-03T16:50:44.301Z
---

# 外部服务

代码中可见三个外部集成：Altcha 工作量证明校验、通过 `ipapi.co` 的 IP 地域查询，以及 VAPID Web Push 配置。只有前两个会发起网络请求，第三个是配置加一个公钥端点。

## Altcha 工作量证明

Altcha 是注册、登录、密码重置、邮箱重发、邮箱变更请求、后台登录与友链申请的验证码机制，使用 `github.com/altcha-org/altcha-lib-go/v2` 实现。

挑战生成使用固定参数：

- 算法 `SHA-256`；
- cost `10000`；
- 密钥长度 32；
- 密钥前缀 `"00"`；
- 五分钟过期。

校验流程：

1. 如果配置了 `DEV_API_TOKEN` 且请求的 `x-dev-token` 头完全匹配，立即通过。该绕过用于开发与测试。
2. 否则把 base64 payload 解码为 challenge/solution。
3. 使用 `DeriveKeySHA` 与配置的 HMAC 密钥校验 solution。

HMAC 密钥在设置 `ALTCHA_HMAC_KEY` 时使用它，否则使用 `"altcha-" + JWT_SECRET` 的 hex SHA-256。派生规则固定；修改会让变更前签发的挑战失效，不过它们五分钟就过期。

挑战签名的 canonical JSON 形态与部分前端实现不同，但后端只校验自己生成的挑战，因此不影响运行。

失败返回 400 与固定消息“验证码错误或已过期”。后台管理域为自己的登录路由保留了一份独立的校验辅助函数。

## IP 地域查询

`ipregion` 把客户端 IP 解析为可读地域，用于登录历史与新设备邮件展示。

- 本地地址 `127.0.0.1`、`::1`、`::ffff:127.0.0.1` 直接返回 `本地`，不发网络请求。
- 其他地址调用 `https://ipapi.co/<ip>/json/`，使用五秒上下文超时与自定义 User-Agent。
- 响应字段 `country_name`、`region`、`city` 用 ` · ` 连接，忽略空部分。
- 请求、解码或空结果失败都返回 `未知`。

缓存使用 `hashicorp/golang-lru`，最多 1000 条，TTL 24 小时。过期在读取时检查；过期条目会被移除并重新请求。LRU 上限意味着流量 IP 很分散时可能提前淘汰热点条目。

查询是同步的：登录、注册、设备确认与 2FA 登录都会在保存用户前内联调用 `GetRegion`。因此 `ipapi.co` 响应慢时，缓存未命中的这些请求最多可能增加五秒。没有熔断或后台预取。

由于是第三方 API，缓存未命中时 IP 地址会离开服务器。代码中没有退出开关。

## VAPID 与 Web Push

`VAPIDConfig` 持有 `PublicKey`、`PrivateKey` 与 `Subject`，由 `[vapid]` ini 段或 `VAPID_PUBLIC_KEY`、`VAPID_PRIVATE_KEY`、`VAPID_SUBJECT` 填充。

- `GET /notifications/vapid-public-key` 公开，只有当公钥与私钥都配置时才返回 `{"publicKey": "<key>"}`，否则为 `null`。这样前端不会在缺少私钥、无法真正推送时误以为 Web Push 可用。
- 推送订阅文档存储 endpoint 与密钥，由通知域管理。
- 私钥与 subject 没有被任何发送器使用。

Web Push 投递尚未实现：每个领域的 `sendPushToUser` 辅助函数都是显式空实现。配置 VAPID 目前只允许客户端创建会被存储、之后可删除的订阅，而不会真正使用。

## 翻译提供方

翻译域也会调用外部服务：配置时用 Azure Translator，然后 MyMemory，再并行 Google 与 Bing。这些提供方及回退记录在工具页面的翻译 API 部分，不在此处展开。

## 运维注意点

- Altcha 与 ipapi.co 失败都是降级而非崩溃：验证码失败会阻止请求，地域失败回退 `未知`。
- ipapi.co 没有重试、退避或共享 HTTP 客户端调优；它使用 `http.DefaultClient` 加每请求上下文超时。
- 两个集成都通过可注入函数测试（邮件用 `SetSendMail`，ipregion 用 `NewClient(fetch)`），因此单元测试不需要网络。

## 相关阅读

- [配置与环境变量](../architecture/configuration.md)——密钥与 VAPID 配置。
- [注册与登录流程](../auth/login-and-registration.md)——验证码与地域查询所在位置。
- [通知与实时推送](../api/notifications-and-realtime.md)——推送订阅及其当前空发送器。
