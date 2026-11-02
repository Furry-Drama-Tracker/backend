---
type: config
title: 配置与环境变量
description: neo-server 如何加载默认值、按 OS 区分的 ini、可选 .env 与真实环境变量，哪些值是密钥，以及 IsDev 与启动致命校验如何影响运行时行为。
tags: [configuration, environment, ini, secrets, startup]
verified:
  - by: openwiki/0.7.2
    at: 2026-11-02T17:41:59.592Z
sources:
  - id: openwiki-source-6e11d7a3d9cd222e5ed01aed
    resource: repo://deploy/furry-drama-be-neo.ini
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
generated: { by: "opencode", at: "2026-11-02T17:41:59.592Z" }
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

1. 初始化默认值（监听 `tcp:0.0.0.0:5000`、端口 5000、`NODE_ENV=development`、前端/站点 URL `http://localhost:3000`、上传目录 `./uploads`、图标目录 `./uploads/icons`、localhost 3000/3001/3002/5000 的 CORS 白名单、数据库连接池 10、access TTL 15 分钟、refresh TTL 168 小时、邮件端口 465、登录限制 5 次/30 分钟、CSRF 有效期 24 小时、演示邮箱 `demo@furry09.com`）。
2. 从当前目录加载 `.env`。
3. 解析配置路径：显式路径必须存在且可解析，默认路径缺失时静默跳过。
4. 如果存在 ini，应用到配置。
5. 应用环境变量覆盖。
6. 派生 `IsDev`。
7. 执行致命校验。

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

## 致命校验

以下情况启动返回错误，`main` 以状态码 1 退出：

- 缺少 `JWT_SECRET`；
- `JWT_SECRET` 短于 32 字符（`MinSecretLen`）；
- 缺少 `MONGO_URI`。

其他情况都不致命。特别是 SMTP、VAPID 或上传目录未配置不会阻止进程启动，相关功能在运行时降级。

## 密钥

敏感值为 `JWT.Secret`、`JWT.EncryptionKey`、`JWT.AltchaHMACKey`、`JWT.DevAPIToken`、`Email.Pass` 与 `VAPID.PrivateKey`。部署模板建议文件权限 `600`，并通过 systemd 环境变量传递密钥而不是写入 ini。`EncryptionKey` 缺省时回退到 `JWT.Secret` 以兼容遗留加密字段，`AltchaHMACKey` 缺省时回退到由 `JWT_SECRET` 派生。

## 认证之外的消费者

多个非认证子系统直接读取配置：

- **邮件**：`[email]` 与 `EMAIL_*` 提供给 SMTP 客户端；数据库中的邮件配置已不再读取。
- **VAPID**：`[vapid]` 与 `VAPID_*` 返回给客户端并随推送订阅存储，尽管推送发送尚未实现。
- **上传**：`uploads_dir` 与 `icons_dir` 选择存储位置；服务器从上传目录提供 `/uploads`。
- **CORS**：`allow_origins` 加默认 localhost 列表。
- **限流**：`SKIP_RATE_LIMIT=1` 设置 `security.rate_limit_skip` 并传给中间件选项。
- **CSRF**：`security.csrf_max_age_h` 设置 `XSRF-TOKEN` cookie 生命周期。
- **站点 URL**：`frontend_url` 与 `site_url` 用于邮件和生成链接。

## 怪癖

- `applyINI` 会无条件从对应段赋值 `listen`、`database.uri`、`database.name`、`jwt.secret`、`jwt.encryption_key`、`jwt.altcha_hmac_key` 以及邮件 host/user/pass/from-name。省略这些键的 ini 会清空之前的默认值，而不是保留。缺失 secret 或 URI 随后会失败校验；缺失 `listen` 会留下空监听地址。
- `database.min`、`database.select` 与 `database.sock` 存在于结构体和 ini 模板中，但没有任何消费者。
- `server.port` 会被解析，但监听器不使用。
- 由于 `.env` 是非覆盖语义，shell 导出的变量总是优先，这正是 CI 与 systemd 覆盖机制成立的原因。

## 相关阅读

<!-- openwiki: broken internal link [/openwiki/operations/deployment-and-runtime.md] link "/openwiki/operations/deployment-and-runtime.md" is root-absolute, which no real consumer resolves against the repository root (not a coding agent reading the page, not GitHub's Markdown renderer, not a local viewer); use a path relative to this file instead. Fix the href or restore the target, then delete this comment. -->
- [部署与运行时](/openwiki/operations/deployment-and-runtime.md)——加载后的值如何被使用。
<!-- openwiki: broken internal link [/openwiki/auth/account-security.md] link "/openwiki/auth/account-security.md" is root-absolute, which no real consumer resolves against the repository root (not a coding agent reading the page, not GitHub's Markdown renderer, not a local viewer); use a path relative to this file instead. Fix the href or restore the target, then delete this comment. -->
- [账号安全能力](/openwiki/auth/account-security.md)——加密密钥派生。
<!-- openwiki: broken internal link [/openwiki/integrations/external-services.md] link "/openwiki/integrations/external-services.md" is root-absolute, which no real consumer resolves against the repository root (not a coding agent reading the page, not GitHub's Markdown renderer, not a local viewer); use a path relative to this file instead. Fix the href or restore the target, then delete this comment. -->
- [外部服务](/openwiki/integrations/external-services.md)——Altcha 与 VAPID 使用。
