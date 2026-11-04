---
type: config
title: 配置与环境变量
description: neo-server 如何加载默认值、按 OS 区分的 ini、可选 .env 与真实环境变量，哪些值是密钥，以及 IsDev、启动致命校验与安全告警如何影响运行时行为。
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
  - id: openwiki-source-f7acf9d278285ec2e6b81113
    resource: repo://internal/service/auth_service.go
generated: { by: "opencode", at: "2026-11-03T16:50:44.301Z" }
verified:
  - by: openwiki/0.7.2
    at: 2026-11-04T03:17:38.088Z
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

1. 初始化默认值（监听 `tcp:127.0.0.1:5000`、端口 5000、`NODE_ENV=development`、前端/站点 URL `http://localhost:3000`、上传目录 `./uploads`、图标目录 `./uploads/icons`、CORS 源留空、数据库连接池 10、access TTL 15 分钟、refresh TTL 168 小时、邮件端口 465、登录限制 5 次/30 分钟、CSRF 有效期 24 小时、演示邮箱 `demo@furry09.com`）。
2. 从当前目录加载 `.env`。
3. 解析配置路径：显式路径必须存在且可解析，默认路径缺失时静默跳过。
4. 如果存在 ini，应用到配置。
5. 应用环境变量覆盖。
6. 派生 `IsDev`。
7. 执行 CORS 源兜底（见下节）。
8. 执行致命校验。

CORS 源兜底发生在 `IsDev` 派生之后、校验之前：如果 `allow_origins` 仍为空，开发环境填入 localhost 3000/3001/3002/5000 白名单；生产环境则从 `frontend_url` 与 `site_url` 去掉尾部斜杠后派生，避免漏配 `ALLOWED_ORIGINS` 时把所有同源 POST 都拒绝。

默认 ini 路径在包初始化时按 OS 选择：

| OS | 路径 |
|---|---|
| Linux | `/etc/furry-drama-tracker/backend.ini` |
| macOS | `/Library/Application Support/furry-drama-tracker/backend.ini` |
| Windows | `C:\ProgramData\furry-drama-tracker\backend.ini` |

## ini 分段

ini 文件分为 `[server]`、`[database]`、`[jwt]`、`[email]`、`[vapid]` 与 `[security]`。部署模板记录了每个键：

| 段 | 键 |
|---|---|
| `server` | `listen`、`node_env`、`frontend_url`、`site_url`、`uploads_dir`、`icons_dir`、`allow_origins`、`port` |
| `database` | `uri`、`name`、`pool_size` |
| `jwt` | `secret`、`encryption_key`、`altcha_hmac_key`、`access_ttl`、`refresh_ttl`、`dev_api_token`、`demo_emails` |
| `email` | `host`、`port`、`user`、`pass`、`from_name` |
| `vapid` | `public_key`、`private_key`、`subject` |
| `security` | `login_max_attempts`、`login_lock_minutes`、`csrf_max_age_h` |

时长使用 Go 语法（`15m`、`168h`）。非法时长会被忽略，保留之前的值。

## 环境变量覆盖

`applyEnv` 只识别固定集合：`JWT_SECRET`、`MONGO_URI`、`ENCRYPTION_KEY`、`ALTCHA_HMAC_KEY`、`FRONTEND_URL`、`SITE_URL`、`ICONS_DIR`、`ALLOWED_ORIGINS`、`EMAIL_HOST`、`EMAIL_PORT`、`EMAIL_USER`、`EMAIL_PASS`、`EMAIL_FROM_NAME`、`VAPID_PUBLIC_KEY`、`VAPID_PRIVATE_KEY`、`VAPID_SUBJECT`、`DEMO_EMAILS`、`DEV_API_TOKEN`、`PORT`、`NODE_ENV` 与 `SKIP_RATE_LIMIT`。

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
- 限流与请求日志是否在生产环境信任 `X-Forwarded-For`；
- 5xx 响应是否暴露内部消息与堆栈；
- 是否启用演示邮箱跳过验证。

## 启动校验与告警

以下情况 `validate()` 返回错误，`main` 以状态码 1 退出：

- 缺少 `JWT_SECRET`；
- `JWT_SECRET` 短于 32 字符（`MinSecretLen`）；
- 设置了 `ENCRYPTION_KEY` 但短于 32 字符；
- 缺少 `MONGO_URI`。

其余都是告警而非致命，便于已有服务拉取新代码后平滑升级：

- `JWT_SECRET` 是示例或历史弱密钥（`change-me`、含 `example`、`secret`、`jwt-secret`、`changeme`、`password`、`furry-drama-tracker-secret-key-2024` 等）时，`Cfg.JWT.WeakSecret` 置位，`main` 打印 `[SECURITY WARN]` 并提示轮换前先设置 `ENCRYPTION_KEY=旧密钥` 以兼容既有 2FA/邮件加密数据。
- 生产环境下 `allow_origins` 为空，或仍含 `localhost`/`127.0.0.1`/`example.com` 时，`Cfg.Server.InsecureOrigins` 置位，`main` 打印 `[SECURITY WARN]`。
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

- `applyINI` 会无条件从对应段赋值 `listen`、`database.uri`、`database.name`、`jwt.secret`、`jwt.encryption_key`、`jwt.altcha_hmac_key` 以及邮件 host/user/pass/from-name。省略这些键的 ini 会清空之前的默认值，而不是保留。缺失 secret 或 URI 随后会失败校验；缺失 `listen` 会留下空监听地址。
- 内置默认监听是回环地址 `tcp:127.0.0.1:5000`。要从外部访问必须显式设置 `listen = tcp:0.0.0.0:5000`（ini）或 `--listen=tcp:0.0.0.0:5000`。
- `database.min`、`database.select` 与 `database.sock` 存在于结构体和 ini 模板中，但没有任何消费者。
- `server.port` 会被解析，但监听器不使用。
- 由于 `.env` 是非覆盖语义，shell 导出的变量总是优先，这正是 CI 与 systemd 覆盖机制成立的原因。

## 相关阅读

- [部署与运行时](../operations/deployment-and-runtime.md)——加载后的值如何被使用。
- [账号安全能力](../auth/account-security.md)——加密密钥派生。
- [外部服务](../integrations/external-services.md)——Altcha 与 VAPID 使用。
