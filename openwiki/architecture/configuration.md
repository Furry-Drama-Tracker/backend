---
type: config
title: 配置与环境变量
description: neo-server 如何加载默认值、按 OS 区分的 ini、可选 .env 与真实环境变量，哪些值是密钥，以及 IsDev、代理标头信任策略、IP 地域开关、启动致命校验与安全告警如何影响运行时行为。
tags: [configuration, environment, ini, secrets, startup]
sources:
  - id: openwiki-source-0f6ed11169bd8e6b34b44280
    resource: repo://cmd/server/main.go
  - id: openwiki-source-6e11d7a3d9cd222e5ed01aed
    resource: repo://deploy/furry-drama-be-neo.ini
  - id: openwiki-source-883d642125c25de6f4b25f17
    resource: repo://internal/auth/fieldcrypto.go
  - id: openwiki-source-a8910515ddd14810ad43f5c1
    resource: repo://internal/config/config.go
  - id: openwiki-source-b10e065a15408ca747b6184f
    resource: repo://internal/email/client.go
  - id: openwiki-source-da21c4a01f369bb3503d0dfb
    resource: repo://internal/handler/csrf.go
  - id: openwiki-source-7106c8455bb73d12832c8523
    resource: repo://internal/server/app.go
  - id: openwiki-source-ebbcecfb3e8c07ba5e940ccd
    resource: repo://internal/server/register_routes.go
  - id: openwiki-source-f7acf9d278285ec2e6b81113
    resource: repo://internal/service/auth_service.go
generated: { by: "zcode", at: "2026-11-13T18:41:36.261Z" }
verified:
  - by: openwiki/0.7.2
    at: 2026-11-13T18:41:36.261Z
---

# 配置与环境变量

所有运行时配置都通过 `config.Load(path)` 进入系统。没有配置服务，也没有热加载；`*config.Config` 在 `cmd/server/main.go` 中创建一次，并通过 `server.Deps` 注入到每一层。

## 优先级

从高到低：

1. **真实环境变量**——供 systemd、容器与 CI 使用。
2. **工作目录下的 `.env` 文件**——通过 `godotenv.Load()` 加载，不会覆盖已存在的真实环境变量。
3. **ini 配置文件**——通过 `--config` 显式指定，否则使用 OS 默认路径。
4. **`defaults()` 中的内置默认值**。

顺序之所以成立，是因为 `.env` 在读取 ini 之前加载，而环境变量覆盖最后应用。缺失 `.env` 是静默的。

## 加载顺序

`Load(path)` 依次执行：

1. 初始化默认值（监听 `tcp:127.0.0.1:5000`、端口 5000、`NODE_ENV=development`、前端/站点 URL `http://localhost:3000`、上传目录 `./uploads`、图标目录 `./uploads/icons`、CORS 源留空、代理标头清单 `CF-Connecting-IP,X-Real-IP,X-Forwarded-For`、IP 地域解析关闭且首选数据源 `aliyun`、改名冷却 7 天、旧名保留 90 天、数据库连接池 10、access TTL 15 分钟、refresh TTL 168 小时、邮件端口 465、登录限制 5 次/30 分钟、CSRF 有效期 24 小时、演示邮箱 `demo@furry09.com`）。
2. 从当前目录加载 `.env`。
3. 解析配置路径：显式路径必须存在且可解析，默认路径缺失时静默跳过。
4. 如果存在 ini，应用到配置。
5. 应用环境变量覆盖。
6. 派生 `IsDev`。
7. 执行 CORS 源兜底（见下节）。
8. 执行 WebAuthn 兜底（见下节）。
9. 执行致命校验。

CORS 源兜底发生在 `IsDev` 派生之后、校验之前：如果 `allow_origins` 仍为空，开发环境填入 localhost 3000/3001/3002/5000 白名单；生产环境则从 `frontend_url` 与 `site_url` 去掉尾部斜杠后派生，避免漏配 `ALLOWED_ORIGINS` 时把所有同源 POST 都拒绝。

WebAuthn 兜底紧随其后：`[webauthn].rp_id` 为空时从 `frontend_url`（缺省再退 `site_url`）的 host 推导（去 scheme/端口，转小写）；`origins` 为空时从两个站点地址推导，开发环境自动追加 `http://localhost:3000`/`3001`/`3002`（对齐 CORS 默认源）；`rp_name` 为空时取 `email.from_name`，再退 `rp_id`。Passkey 只在 RP ID 域名（含子域）下有效，因此站点裸域名是正确的 rp_id。

默认 ini 路径在包初始化时按 OS 选择：

| OS | 路径 |
|---|---|
| Linux | `/etc/furry-drama-tracker/backend.ini` |
| macOS | `/Library/Application Support/furry-drama-tracker/backend.ini` |
| Windows | `C:\ProgramData\furry-drama-tracker\backend.ini` |

## ini 分段

ini 文件分为 `[server]`、`[database]`、`[jwt]`、`[email]`、`[vapid]`、`[webauthn]`、`[oauth]` 与 `[security]`。部署模板记录了每个键：

| 段 | 键 |
|---|---|
| `server` | `listen`、`node_env`、`frontend_url`、`site_url`、`uploads_dir`、`icons_dir`、`allow_origins`、`port`、`trust_proxy_headers`、`proxy_ip_headers`、`trusted_proxies`、`proxy_trusted_platform` |
| `database` | `uri`、`name`、`pool_size` |
| `jwt` | `secret`、`encryption_key`、`altcha_hmac_key`、`access_ttl`、`refresh_ttl`、`dev_api_token`、`demo_emails` |
| `email` | `host`、`port`、`user`、`pass`、`from_name` |
| `vapid` | `public_key`、`private_key`、`subject` |
| `webauthn` | `rp_id`、`rp_name`、`origins` |
| `oauth` | `qq_app_id`、`qq_app_key` |
| `security` | `login_max_attempts`、`login_lock_minutes`、`csrf_max_age_h`、`username_blacklist`、`login_name_cooldown_days`、`login_name_reserve_days`、`ip_region_enabled`、`ip_region_provider`、`ip_region_appcode`、`ip_region_czdb_path_v4`、`ip_region_czdb_path_v6`、`ip_region_czdb_key` |

时长使用 Go 语法（`15m`、`168h`）。非法时长会被忽略，保留之前的值。

`[server]` 的代理标头四键决定客户端 IP 识别策略（限流、审计与登录记录都依赖）：

- `trust_proxy_headers`：是否信任反向代理标头；省略时沿用历史行为——生产（`!IsDev`）信任、开发不信任。
- `proxy_ip_headers`：按序尝试的标头清单，缺省 `CF-Connecting-IP,X-Real-IP,X-Forwarded-For`（ESA/CF 边缘覆盖写入的 CF 头最优先；X-Real-IP 可能被中间层写成本地/节点 IP，仅作兜底）。
- `trusted_proxies`：受信代理的 IP/CIDR 清单；不配 = gin 默认信任全部来源（与旧手写解析一致）。配置后（如 ESA → 本机反代拓扑填 `127.0.0.1,::1`），非受信来源直连伪造的代理标头会被整体忽略。
- `proxy_trusted_platform`：单一无条件最高优先标头（gin TrustedPlatform），非空即信、不校验来源与格式，仅应在前置网关保证覆盖写入时启用；默认不启用。

`[security]` 的用户名策略三键服务于改名/注册校验（`service/loginname.go`）：`username_blacklist` 是黑名单补充词表（逗号分隔，与内置词表合并做子串匹配，内置含 admin/root/system/api 等）；`login_name_cooldown_days` 是改名冷却天数（默认 7）；`login_name_reserve_days` 是旧名保留天数（默认 90，期间他人不可注册该名）。

`[security].ip_region_enabled` 控制 IP → 地域解析（登录/注册/设备确认记录与提醒邮件），缺省 `false`（ADR-017）：该能力会把客户端 IP 发给所选数据源，合规要求下保持关闭。开启后由 ADR-018 的双数据源机制决定行为：

- `ip_region_provider`（默认 `aliyun`）选择首选数据源：`aliyun`=阿里云云市场纯真 IP 地域 API（`cz88geo.market.alicloudapi.com`，AppCode 鉴权，境内服务，区县级精度，按量计费）；`czdb`=纯真离线库（本地查询无外呼，需人工下载/更新 .czdb 库文件）。
- `ip_region_appcode` 是云市场 API（cmapi00053387）的鉴权凭证；`ip_region_czdb_path_v4`/`ip_region_czdb_path_v6` 分别是 IPv4/IPv6 库文件路径，`ip_region_czdb_key` 是两库共用的解密密钥。
- 另一数据源配置齐全时自动兜底：首选返回「未知」时改走它（封装在 `ipregion.NewConfiguredClient`）。仅配一个协议的 czdb 库时，另一协议 IP 记「未知」（可由 aliyun 兜底）。

`[webauthn]` 服务 Passkey（WebAuthn）：`rp_id` 是 Relying Party ID（站点裸域名，不含 scheme/端口）；`rp_name` 是注册时展示的站点名；`origins` 是允许的仪式来源（完整 origin）。留空时按上文 WebAuthn 兜底推导。

`[oauth]` 是外部身份提供方配置，目前仅 QQ 互联预留（`qq_app_id`/`qq_app_key`）：未配置凭据时 `/api/auth/qq/*` 一律返回 501，待接入 SDK。

## 环境变量覆盖

`applyEnv` 只识别固定集合：`JWT_SECRET`、`MONGO_URI`、`ENCRYPTION_KEY`、`ALTCHA_HMAC_KEY`、`FRONTEND_URL`、`SITE_URL`、`ICONS_DIR`、`ALLOWED_ORIGINS`、`EMAIL_*`（HOST/PORT/USER/PASS/FROM_NAME）、`VAPID_PUBLIC_KEY`、`VAPID_PRIVATE_KEY`、`VAPID_SUBJECT`、`DEMO_EMAILS`、`DEV_API_TOKEN`、`PORT`、`NODE_ENV`、`SKIP_RATE_LIMIT`、`TRUST_PROXY_HEADERS`、`PROXY_IP_HEADERS`、`TRUSTED_PROXIES`、`PROXY_TRUSTED_PLATFORM`，以及认证重构新增的 `IP_REGION_ENABLED`、`IP_REGION_PROVIDER`、`IP_REGION_APPCODE`、`IP_REGION_CZDB_PATH_V4`、`IP_REGION_CZDB_PATH_V6`、`IP_REGION_CZDB_KEY`、`USERNAME_BLACKLIST`、`LOGIN_NAME_COOLDOWN_DAYS`、`LOGIN_NAME_RESERVE_DAYS`、`WEBAUTHN_RP_ID`、`WEBAUTHN_RP_NAME`、`WEBAUTHN_ORIGINS`、`QQ_APPID` 与 `QQ_APPKEY`。

行为细节：

- 空值不会覆盖已解析的值；`setStr` 忽略空字符串。
- `ALLOWED_ORIGINS` 与 `DEMO_EMAILS` 会替换整个列表，按逗号切分并去除空白。
- `SKIP_RATE_LIMIT` 只有在值恰好为 `1` 时生效。
- `PORT` 会更新 `Server.Port`，但**监听器不使用 `Server.Port`**，而是使用 `Server.Listen`。只改 `PORT` 不会改变地址，除非同时设置 `--listen` 或 ini 的 `listen`。
- `uploads_dir` 没有环境变量覆盖，只能通过 ini 或默认值设置。

## 派生 IsDev

`IsDev` 等于 `NODE_ENV != "production"`（不区分大小写），因此未设置环境时是开发模式。它控制：

- Gin debug 还是 release 模式；
- 鉴权 cookie 是否 secure；
- 是否挂载 Swagger UI 到 `/api/docs`；
- 代理标头信任开关的**缺省值**（生产信任、开发不信任；`trust_proxy_headers`/`TRUST_PROXY_HEADERS` 显式设置时覆盖，实际的标头解析统一走 gin `c.ClientIP()`，见下文代理标头四键）；
- 5xx 响应是否暴露内部消息与堆栈；
- 是否启用演示邮箱跳过验证。

## 启动校验与告警

以下情况 `validate()` 返回错误，`main` 以状态码 1 退出：

- 缺少 `JWT_SECRET`；
- `JWT_SECRET` 短于 32 字符（`MinSecretLen`）；
- 设置了 `ENCRYPTION_KEY` 但短于 32 字符；
- 缺少 `MONGO_URI`；
- `trusted_proxies` 中存在既不是合法 IP 也不是 CIDR 段的项——这是启动致命校验，避免到装配阶段才发现（`SetTrustedProxies` 失败会静默退回信任全部来源的错误状态）。

其余都是告警而非致命，便于已有服务拉取新代码后平滑升级：

- `JWT_SECRET` 是示例或历史弱密钥（`change-me`、含 `example`、`secret`、`jwt-secret`、`changeme`、`password`、`furry-drama-tracker-secret-key-2024` 等）时，`Cfg.JWT.WeakSecret` 置位，`main` 打印 `[SECURITY WARN]` 并提示轮换前先设置 `ENCRYPTION_KEY=旧密钥` 以兼容既有 2FA/邮件加密数据。
- 生产环境下 `allow_origins` 为空，或仍含 `localhost`/`127.0.0.1`/`example.com` 时，`Cfg.Server.InsecureOrigins` 置位，`main` 打印 `[SECURITY WARN]`。
- IP 地域解析开关会在启动时打印状态：开启时打 `[SECURITY WARN]` 并注明首选数据源（czdb 提示查询走本地库、未命中按配置兜底；aliyun 提示认证流程会把客户端 IP 发送至该 API，非必要保持关闭）；首选为 aliyun 但未配置 AppCode 时追加一条「aliyun 数据源不可用」告警。关闭时打印提示「LastLoginRegion 记为 未知」。
- 迁移与索引创建失败只打印 `[WARN]` 并继续启动，首次部署或数据重复时应尽快人工处理后重启。
- SMTP、VAPID 或上传目录未配置不会阻止进程启动，相关功能在运行时降级。

`internal/config/config_test.go` 用历史生产配置（弱密钥 + 占位 CORS 域）断言 `validate()` 不报错且两个告警标志都置位，作为平滑升级行为的回归闸门。

## 密钥

敏感值为 `JWT.Secret`、`JWT.EncryptionKey`、`JWT.AltchaHMACKey`、`JWT.DevAPIToken`、`Email.Pass` 与 `VAPID.PrivateKey`。部署模板建议文件权限 `600`，并通过 systemd 环境变量传递密钥而不是写入 ini。`EncryptionKey` 缺省时回退到 `JWT.Secret` 以兼容遗留加密字段，`AltchaHMACKey` 缺省时回退到由 `JWT_SECRET` 派生。

弱密钥不再阻止启动，但会触发 `Cfg.JWT.WeakSecret` 与启动告警。轮换顺序很重要：先把 `ENCRYPTION_KEY` 设为旧密钥，再替换 `JWT_SECRET`，否则既有 2FA secret 与加密邮件字段将无法解密。

## 认证之外的消费者

多个非认证子系统直接读取配置：

- **邮件**：`[email]` 与 `EMAIL_*` 提供给 SMTP 客户端；数据库中的邮件配置已不再读取。
- **VAPID**：`[vapid]` 与 `VAPID_*` 返回给客户端并随推送订阅存储，尽管推送发送尚未实现。
- **上传**：`uploads_dir` 与 `icons_dir` 选择存储位置；服务器从上传目录提供 `/uploads`。
- **CORS**：`allow_origins` 非空时整体替换默认列表；为空时按环境兜底（开发用 localhost 列表，生产从 `frontend_url`/`site_url` 派生）。
- **限流**：`SKIP_RATE_LIMIT=1` 设置 `security.rate_limit_skip` 并传给中间件选项。
- **CSRF**：`security.csrf_max_age_h` 设置 `XSRF-TOKEN` cookie 生命周期。
- **站点 URL**：`frontend_url` 与 `site_url` 用于邮件和生成链接。

## 怪癖

- `applyINI` 会无条件从对应段赋值 `listen`、`database.uri`、`database.name`、`jwt.secret`、`jwt.encryption_key`、`jwt.altcha_hmac_key`、邮件 host/port/user/pass/from_name 以及 vapid 与 webauthn/oauth 的全部键。省略这些键的 ini 会清空之前的默认值，而不是保留。缺失 secret 或 URI 随后会失败校验；缺失 `listen` 会留下空监听地址。
- 内置默认监听是回环地址 `tcp:127.0.0.1:5000`。要从外部访问必须显式设置 `listen = tcp:0.0.0.0:5000`（ini）或 `--listen=tcp:0.0.0.0:5000`。
- `database.min`、`database.select` 与 `database.sock` 只存在于结构体（`DatabaseConfig` 字段），ini 模板与 `applyINI`/`applyEnv` 都不解析它们，没有任何消费者。
- `server.port` 会被解析，但监听器不使用。
- 由于 `.env` 是非覆盖语义，shell 导出的变量总是优先，这正是 CI 与 systemd 覆盖机制成立的原因。

## 相关阅读

- [部署与运行时](../operations/deployment-and-runtime.md)——加载后的值如何被使用。
- [账号安全能力](../auth/account-security.md)——加密密钥派生。
- [外部服务](../integrations/external-services.md)——Altcha 与 VAPID 使用。
