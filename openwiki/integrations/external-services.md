---
type: integration
title: 外部服务
description: Altcha 工作量证明集成及其密钥派生、默认禁用（可选开启 ipapi.co）的 IP 地域解析与本地离线库替换缝，以及只暴露配置却从不用于发送的 VAPID 设置。
tags: [integration, altcha, geolocation, vapid, external-apis]
sources:
  - id: openwiki-source-a8910515ddd14810ad43f5c1
    resource: repo://internal/config/config.go
  - id: openwiki-source-7e5389f01ee56b40448faee1
    resource: repo://internal/handler/notifications.go
  - id: openwiki-source-4b6649913d9eb04add9a5c21
    resource: repo://internal/handler/oauth.go
  - id: openwiki-source-5892c79bee89b56ec4322f3f
    resource: repo://internal/handler/reports.go
  - id: openwiki-source-9d45be1491b0521011ea6e6a
    resource: repo://internal/ipregion/client.go
  - id: openwiki-source-ab5c4b1bca556999a73341b0
    resource: repo://internal/model/authfactors.go
  - id: openwiki-source-f7acf9d278285ec2e6b81113
    resource: repo://internal/service/auth_service.go
  - id: openwiki-source-1994cba008c20be8b9db7693
    resource: repo://internal/service/login.go
  - id: openwiki-source-da44a950f6747f4662d4d376
    resource: repo://internal/service/oauth.go
  - id: openwiki-source-3f2309f0c3b4e4f8068472e9
    resource: repo://internal/service/register.go
generated: { by: "zcode", at: "2026-11-13T18:41:36.261Z" }
verified:
  - by: openwiki/0.7.2
    at: 2026-11-13T18:41:36.261Z
---

# 外部服务

代码中可见四个外部集成：Altcha 工作量证明校验、双数据源的 IP 地域查询（aliyun 云市场 API 与 czdb 离线库）、QQ 互联 OAuth 预留，以及 VAPID Web Push 配置。前两者会发起网络请求（IP 地域默认关闭），QQ 预留恒 501，VAPID 只是配置加一个公钥端点。

## Altcha 工作量证明

Altcha 是注册、登录、密码重置、邮箱重发、邮箱变更请求与友链申请的验证码机制，使用 `github.com/altcha-org/altcha-lib-go/v2` 实现。

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

失败返回 400 与固定消息“验证码错误或已过期”。所有入口共用认证服务的同一套校验实现；独立的 `/api/admin/login` 及其辅助函数已随 ADR-012 删除。

## IP 地域查询（默认禁用，ADR-017/ADR-018）

`ipregion` 把客户端 IP 解析为可读地域，用于登录历史与新设备邮件展示。按数据出境合规要求，该能力**默认禁用**（ADR-017）：

- 默认装配走 `ipregion.NewDisabled()`：本地地址 `127.0.0.1`、`::1`、`::ffff:127.0.0.1` 仍返回 `本地`，其余 IP 一律返回 `未知`，**不发任何网络请求**。登录、注册、Passkey 登录与 2FA 登录不再把客户端 IP 发给第三方。
- 仅当 `cfg.Security.IPRegionEnabled` 为 true 时，`register_routes.go` 才用 `ipregion.NewConfiguredClient` 按 `[security].ip_region_provider` 装配双数据源（ADR-018）。
- `cmd/server/main.go` 启动时打印开关状态：开启时打 `[SECURITY WARN]` 并注明首选数据源。

### 双数据源与自动兜底（ADR-018）

- **aliyun（默认首选）**：阿里云云市场纯真 IP 地域 API（`cz88geo.market.alicloudapi.com/search/ip/geo`，cmapi00053387），鉴权 `Authorization: APPCODE <code>`（`[security].ip_region_appcode` / `IP_REGION_APPCODE`），5 秒超时。取响应 `data` 的 country/province/city/districts 四字段用 ` · ` 连接（缺失与字面「未知」的区县跳过），区县级精度、按量计费、依赖外呼。
- **czdb**：纯真离线库（`github.com/tagphi/czdb-search-golang`，MEMORY 模式）。`ip_region_czdb_path_v4`/`ip_region_czdb_path_v6` 分别指定 IPv4/IPv6 的 .czdb 库文件路径，`ip_region_czdb_key` 两库共用。本地查询**无网络外呼**，查询按 IP 协议路由到对应库；MEMORY 模式构造期预热全量入内存后即释放文件句柄，之后查询为纯内存读、可安全并发。库文件损坏/密钥错误/协议放反时构造报错（调用方记告警并继续其它数据源）；仅配一个协议的库时另一协议 IP 记 `未知`。更新库文件后需重启生效（不做热更新）。
- **自动兜底**：`NewConfiguredClient` 把两个数据源用 `WithFallback` 串起来——首选返回「未知」（外呼失败/未命中）时改走另一数据源；任一未配置则退化为单源；两个都不可用时返回 nil，调用方保持 `NewDisabled` 并告警。
- **输出统一**为 `country · province · city · districts` 风格。czdb 官方库实测输出为 `国家–省–市[–区]<TAB>归属描述`（– 为 en-dash）：`formatCzdb` 取首个 tab 段按 – 拆段、丢弃归属描述；同时兼容 tab 分隔列形态（geo mapping 列，取前 4 列非空）。区分启发式：首段含 – 或 tab 段不足 3 列按单行形态处理。

`service/handler/API` 契约零变化：`LastLoginRegion` 仍是 ` · ` 拼接字符串。真实数据源冒烟测试在 `internal/ipregion/local_smoke_test.go`（环境变量门控，默认跳过）。

开启时的缓存行为不变：`hashicorp/golang-lru`，最多 1000 条，TTL 24 小时。过期在读取时检查；过期条目会被移除并重新请求。LRU 上限意味着流量 IP 很分散时可能提前淘汰热点条目。

查询是同步的：登录、注册、Passkey 登录与 2FA 登录都会在保存/签发会话前内联调用 `GetRegion`。因此显式开启后首选数据源响应慢时，缓存未命中的这些请求最多可能增加五秒。没有熔断或后台预取。

## QQ 互联 OAuth（预留，501）

`/api/auth/qq/*`（`status`/`start`/`callback`）是外部身份提供方的预留路由。`OAuthConfig`（`[oauth]` 的 `qq_app_id`/`qq_app_key` 或 `QQ_APPID`/`QQ_APPKEY`）配置齐全时 provider 视为启用，但当前后端实现仍返回 501「QQ 登录尚未开放」，待接入 SDK。`GET /api/auth/qq/status` 返回 `{"enabled": <bool>}` 供前端探测。`externalidentities` 集合已建好（`(provider, openid)` 唯一索引，provider 预留 `"qq"`），上线前不会有数据；注册路径已为「OAuth 绑定、无邮箱」的外部身份预留分支。

## VAPID 与 Web Push

`VAPIDConfig` 持有 `PublicKey`、`PrivateKey` 与 `Subject`，由 `[vapid]` ini 段或 `VAPID_PUBLIC_KEY`、`VAPID_PRIVATE_KEY`、`VAPID_SUBJECT` 填充。

- `GET /notifications/vapid-public-key` 公开，只有当公钥与私钥都配置时才返回 `{"publicKey": "<key>"}`，否则为 `null`。这样前端不会在缺少私钥、无法真正推送时误以为 Web Push 可用。
- 推送订阅文档存储 endpoint 与密钥，由通知域管理。
- 私钥与 subject 没有被任何发送器使用。

Web Push 投递尚未实现：每个领域的 `sendPushToUser` 辅助函数都是显式空实现。配置 VAPID 目前只允许客户端创建会被存储、之后可删除的订阅，而不会真正使用。

## 翻译提供方

翻译域也会调用外部服务：配置时用 Azure Translator，然后 MyMemory，再并行 Google 与 Bing。这些提供方及回退记录在工具页面的翻译 API 部分，不在此处展开。

## 运维注意点

- Altcha 与地域数据源失败都是降级而非崩溃：验证码失败会阻止请求，地域失败回退 `未知`（aliyun 失败时由 czdb 兜底，反之亦然）。
- IP 地域解析默认关闭，认证路径少一个外部故障点；`LastLoginRegion` 与新设备提醒邮件的地域一行显示 `未知`（本就是外呼失败的兜底值），响应字段不变。
- 显式开启后：aliyun API 按量计费且无重试/退避（`http.DefaultClient` 加每请求 5 秒上下文超时）；czdb 免费无外呼但库文件需人工定期更新，忘记维护则数据陈旧。
- 两个集成都通过可注入函数测试（邮件用 `SetSendMail`，ipregion 用 `NewClient(fetch)`/`NewAliyunClient`/`NewCzdbClient` 与 `NewDisabled()`），因此单元测试不需要网络。

## 相关阅读

- [配置与环境变量](../architecture/configuration.md)——密钥与 VAPID 配置。
- [注册与登录流程](../auth/login-and-registration.md)——验证码与地域查询所在位置。
- [通知与实时推送](../api/notifications-and-realtime.md)——推送订阅及其当前空发送器。
