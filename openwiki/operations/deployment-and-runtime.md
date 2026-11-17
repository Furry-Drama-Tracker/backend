---
type: operations
title: 部署与运行时
description: 构建目标与 CI 滚动发布、认证排障命令 make diag、准确启动顺序、TCP 与 Unix socket 监听、服务器超时、优雅关停、目录初始化、systemd unit 与 ini 模板，以及多实例后果。
tags: [deployment, runtime, systemd, startup, shutdown, ci]
sources:
  - id: openwiki-source-4d1d392666be6dfdd7a91a2e
    resource: repo://.github/workflows/release.yml
  - id: openwiki-source-0f6ed11169bd8e6b34b44280
    resource: repo://cmd/server/main.go
  - id: openwiki-source-85047375915a57a29b05d0cd
    resource: repo://cmd/useradmin/main.go
  - id: openwiki-source-6e11d7a3d9cd222e5ed01aed
    resource: repo://deploy/furry-drama-be-neo.ini
  - id: openwiki-source-965e9675b33e3892958fab10
    resource: repo://deploy/furry-drama-be-neo.service
  - id: openwiki-source-db33e0b2a6bda7efb94d4efc
    resource: repo://deploy/nginx.conf.example
  - id: openwiki-source-523bc741245510a618f572a9
    resource: repo://deploy/run.sh
  - id: openwiki-source-f9cd993b1da0967b3ad454b7
    resource: repo://internal/repository/user.go
  - id: openwiki-source-f7acf9d278285ec2e6b81113
    resource: repo://internal/service/auth_service.go
  - id: openwiki-source-540faaa12781d81a1f3eb2aa
    resource: repo://internal/service/passkey.go
  - id: openwiki-source-da80fdae2220fc6ad9a470b7
    resource: repo://internal/upload/svg.go
  - id: openwiki-source-012f2c78e3b1446dfc35803f
    resource: repo://Makefile
  - id: openwiki-source-1ffe96277832af9ddb17aa8f
    resource: repo://winres/winres.json
generated: { by: "zcode", at: "2026-11-17T17:20:26.784Z" }
verified:
  - by: openwiki/0.7.2
    at: 2026-11-17T17:20:26.784Z
---

# 部署与运行时

后端是一个 Go 二进制，加上配置和两个可写目录。仓库有一个构建与滚动发布的 CI 流水线（不跑测试）；部署就是构建、配置文件和进程守护。schema 迁移与索引创建内置在启动流程中。模块路径为 `github.com/Furry-Drama-Tracker/backend`。

## 构建

Makefile 提供标准目标：

| 目标 | 动作 |
|---|---|
| `make build` | `go build -o bin/furry-drama-be-neo ./cmd/server`（Windows 目标带 `.exe`） |
| `make release` | `go build -trimpath -ldflags "-s -w" -o bin/furry-drama-be-neo ./cmd/server` |
| `make run` | `go run ./cmd/server` |
| `make seed` | `go run ./cmd/seed-admin` 创建初始超管 |
| `make test` | `go test ./...` |
| `make vet` | `go vet ./...` |
| `make fmt` | `gofmt -l -w .` |
| `make docs` | 重新生成 Swagger 到 `docs/`（需要 `swag`） |
| `make diag` | 认证排障（只读）：`go run ./cmd/diag $(ARGS)`，如 `make diag ARGS="-user=<邮箱或ObjectID> -days=7"` 或 `make diag ARGS="-recent -hours=6"` |
| `make clean` | 删除二进制 |

`GOOS`/`GOARCH` 通过环境变量传给 `go`，因此 `make release GOOS=linux GOARCH=arm64` 可直接交叉编译，输出名按目标平台决定是否带 `.exe`。`Makefile.win` 为 NMAKE 镜像这些目标（含 `release`、`seed` 与 `diag`）。模块要求 Go 1.26 与可访问的 MongoDB。另有第四个入口 `cmd/useradmin`（无 make 目标）：`go run ./cmd/useradmin <命令>`，见下文「useradmin 维护 CLI」。

Windows 可执行文件内嵌应用图标与版本元数据：`FDT-Icon.ico` 经 go-winres 由 `winres/winres.json` 编译为 `cmd/server/rsrc_windows_{amd64,386,arm64}.syso`，任意 Windows 目标的 `go build` 自动链接（非 Windows 目标按文件名后缀忽略）。资源含 RT_GROUP_ICON（应用图标）与 RT_VERSION（双语产品名"兽剧通后端服务"/英文、AGPL-3.0 版权、仓库地址；版本号 0.0.0.0 占位）。更换图标后重跑 `go run github.com/tc-hib/go-winres@latest make --in winres/winres.json --out cmd/server/rsrc --arch amd64,386,arm64`。

## CI 滚动发布

`.github/workflows/release.yml` 是仓库唯一 CI 文件（只有构建与发布，不跑测试）：

- 每次推送触发 8 目标交叉构建：linux/windows/darwin × 386/amd64/arm64（macOS 无 32 位），`CGO_ENABLED=0`，参数与 `make release` 一致（`-trimpath -ldflags "-s -w"`）。
- `main` 分支推送额外把产物覆盖发布到 tag=`latest` 的滚动 Release：先删后建（`gh release delete latest --cleanup-tag`），含 `checksums.txt`；`concurrency: release, cancel-in-progress` 保证只保留最新一次。
- CI 干净检出使 Go 自动烙入 `vcs.revision`，无 `+dirty`，产物随代码变化可复现。

改动错误码后的发布前检查：新增/调整任何带码错误响应后重跑 `python3 docs/gen_errors_json.py` 刷新机器注册表 `docs/errors.json`（输出字节稳定可直接提交），改了 Swagger 注解则重跑 `make docs`——两者都是已提交契约的一部分，未提交的 WIP 修改不会进入注册表（详见[错误信封与错误码契约](../api/error-envelope.md)）。

## 启动顺序

`cmd/server/main.go` 按顺序执行：

1. 解析 `--config` 与 `--listen`。
2. `config.Load`——缺少或过短的 `JWT_SECRET`、过短的 `ENCRYPTION_KEY`，或缺少 `MONGO_URI`，以状态码 1 退出；弱密钥与占位 CORS 源只打印 `[SECURITY WARN]`；生产环境信任代理标头但 `trusted_proxies` 未配置时同样只打 `[SECURITY WARN]`（2026-11 ADR-022 新增，见下文「反向代理加固」）；随后打印 IP 地域解析开关状态（开启时按首选数据源打 `[SECURITY WARN]`：aliyun 提示会把客户端 IP 发送至该 API，czdb 提示查询走本地库；首选为 aliyun 且未配 AppCode 时追加不可用告警；关闭时提示记「未知」）。
3. `repository.Connect`，15 秒上下文——连接或 ping 失败即退出。
4. 构造 `Repos` 与 JWT 签名器。
5. 运行 `migrate.Run` 与 `indexes.Ensure`（60 秒上下文）；失败只打印 `[WARN]` 并继续启动。
6. 启动 `cron.StartSessions`：过期会话、已读通知、到期注销账号清理。
7. 应用上传与图标目录配置，并用 `MkdirAll` 创建两个目录；失败只告警。
8. 构建 Gin 应用——`server.NewApp` 在装配时调用 `errors.SetLogger(log)` 把 slog 注入 errors 包（5xx ERROR 日志输出器），注册 JSON panic 恢复（`SYS0500`）与 `NoRoute` 404 信封（`SYS0003`）。
9. 创建监听器；失败即退出。
10. 开始服务。
11. 等待 SIGTERM/SIGINT 或服务错误。
12. 用 10 秒超时关停并退出。

迁移、索引与清理任务已接线，但启动失败不会阻止进程继续，以便已有服务平滑升级。首次部署或数据重复时应检查启动日志并人工修复后重启。

## 可写目录

- `uploads_dir`（默认 `./uploads`）是图片写入位置，启动时用 `MkdirAll` 创建（失败只告警），也是 `/uploads` 服务的根。
- `icons_dir`（默认 `./uploads/icons`）在启动时与 SVG 上传前创建。返回 URL 始终为 `/uploads/icons/...`，因此上传根之外的自定义目录不会被内置静态路由服务。

两者都应可被服务用户写入，并与二进制独立持久化。

## 监听模式

`--listen` 接受：

- `tcp:HOST:PORT` 或裸 `HOST:PORT` → TCP；
- `unix:/path/to.sock` → Unix domain socket，先删除残留文件并设置权限 `0660`；
- `unix:@name` → Linux abstract socket，无文件或权限。

默认是 `tcp:127.0.0.1:5000`（仅回环）。缺少 flag 时使用 ini 的 `listen`，但要注意省略该键的 ini 会清空默认值而不是保留。要从外部访问必须显式设置 `tcp:0.0.0.0:5000`——**勿绑 `0.0.0.0` 直曝公网**（2026-11 ADR-022 部署加固口径：`run.sh` 默认值与 systemd 注释均已从 `0.0.0.0:5000` 收紧为 `127.0.0.1:5000`），对外访问统一经反向代理（见下文「反向代理加固」）。

## HTTP 服务器超时

服务器配置为：

- 读头超时 65 秒（慢客户端防护）；
- 读超时 30 秒；
- 写超时 30 秒；
- 空闲超时 5 秒。

这些与中间件应用的 30 秒请求上下文截止时间是分开的。

## 优雅关停

收到 SIGTERM 或 SIGINT 时，进程记录信号，调用带 10 秒上下文的 `Shutdown`，并记录完成或超时。systemd unit 在 SIGKILL 前允许 15 秒（`TimeoutStopSec=15`）。在途请求获得关停窗口；更长请求会被截断。

## systemd unit

`deploy/furry-drama-be-neo.service` 是标准 simple unit：

- `User=www-data`、`Group=www-data`；
- `WorkingDirectory=/opt/furry-drama-tracker/neo-server`；
- `ExecStart=.../bin/furry-drama-be-neo --config=/etc/furry-drama-tracker/backend.ini --listen=unix:/run/furry-drama-be-neo.sock`；
- `Restart=on-failure`、`RestartSec=2`；
- 加固：`UMask=0077`、`NoNewPrivileges`、`PrivateTmp`、`ProtectSystem=full`、`ProtectHome`；
- `RuntimeDirectory=furry-drama-be-neo`，模式 `0750`，用于 socket；
- `TimeoutStartSec=30` 与 `TimeoutStopSec=15`。

注释掉的 `Environment=` 行展示了预期的密钥注入方式：`JWT_SECRET` 与 `MONGO_URI`。

若要改为 TCP，把 `ExecStart` 中的 `--listen` 改为 `tcp:127.0.0.1:5000`（勿绑 `0.0.0.0` 直曝公网，unit 注释已按此口径更新）并去掉 socket 依赖。反向代理应终止 TLS，并按「反向代理加固」一节覆盖写入或只放行受信来源的代理标头，同时单独服务前端。

## ini 模板

`deploy/furry-drama-be-neo.ini` 记录了每个段与面向生产的默认值，共八段：`server`（含代理标头四键注释块）、`database`、`jwt`、`email`、`vapid`、`webauthn`（Passkey：`rp_id`/`rp_name`/`origins`，留空自动推导）、`oauth`（QQ 预留）与 `security`（登录锁定、用户名策略三键、`ip_region_enabled = false` 及 ADR-018 双数据源键：`ip_region_provider`/`ip_region_appcode`/`ip_region_czdb_path_v4`/`ip_region_czdb_path_v6`/`ip_region_czdb_key`）。使用前：

1. 复制到 OS 默认路径，或让 `--config` 指向它；
2. 把 `[jwt].secret` 设为至少 32 个随机字符；
3. 设置 `[database].uri` 与可选 `name`；
4. 把 `uploads_dir` 设为持久可写路径；
5. 如需邮件，配置 `[email]`；
6. 把 `allow_origins` 设为真实前端源；按反代拓扑核对 `[server]` 代理标头四键（`trust_proxy_headers`、`proxy_ip_headers`、`trusted_proxies`、`proxy_trusted_platform`，ESA → 本机反代拓扑建议 `trusted_proxies = 127.0.0.1, ::1`）——注意 `trusted_proxies` 只校验直连对端，还须按「反向代理加固」一节在反代层处理代理标头，否则生产启动会打 trust-all `[SECURITY WARN]`（未配置时）或伪造标头仍被透传采信（已配置但反代未覆盖写入时）；
7. 保持 `[security].ip_region_enabled = false`（数据出境合规，ADR-017/ADR-018）；开启才需配置 provider 与对应数据源凭据；
8. Passkey 默认从 `frontend_url`/`site_url` 推导 RP；站点走裸域名下子域部署时按需显式设置 `[webauthn].rp_id`；
9. 可选地显式设置 `encryption_key` 与 `altcha_hmac_key`。

该文件包含密钥，建议 `chmod 600`。

## run.sh

`deploy/run.sh` 是无 systemd 主机上的最小备选。尽管注释提到 nohup，它当前在前台 `exec` 二进制，并期望外部守护进程将其转入后台。二进制缺失时快速失败，使用 `CONFIG` 与可选的 listen 参数——默认监听自 2026-11 起收紧为 `tcp:127.0.0.1:5000`（仅回环），对外访问须经反代（见下节）。

## 反向代理加固（nginx.conf.example）

`deploy/nginx.conf.example`（2026-11 ADR-022 新增）回答一个核心问题：**让 Go 进程看到的代理标头（`CF-Connecting-IP` / `X-Real-IP` / `X-Forwarded-For`）一定来自可信网关，而不是客户端伪造**——这是 IP 维度限流（登录/注册/验证码/全局限流）与审计归因（`LastLoginIP`、新设备提醒邮件、authevents）可信的前提。仅在 Go 侧配 `trusted_proxies` 封不死伪造路径：socket/loopback 部署下 Go 的直连对端恒为本机反代、永远「受信」，客户端经反代透传的伪造 `CF-Connecting-IP` 仍会被原样采信。示例给出两种互补形态：

- **形态 A（经 CDN/ESA 回源）**：用 `geo` + `allow`/防火墙把源站限制为只接受 CDN 回源网段，让 `CF-Connecting-IP` 只可能由 CDN 边缘覆盖写入；nginx 原样转发该头，**禁加** `proxy_set_header CF-Connecting-IP ...`（会覆盖掉 CDN 写入的真实访客 IP，后端记录到的一律是 CDN 节点 IP）。
- **形态 B（无 CDN，访客直连本机 nginx）**：nginx 用 `$remote_addr` 覆盖写入全部优先标头，客户端自带的任何代理标头都不外传。

配套 Go 侧（backend.ini）：`trust_proxy_headers = true`、`trusted_proxies = 127.0.0.1, ::1`（形态 A 还需追加 CDN 回源网段，否则 XFF 回溯停在 CDN 节点 IP）、`proxy_ip_headers = CF-Connecting-IP,X-Real-IP,X-Forwarded-For`。公共代理段还包括 SSE 需要的 `proxy_buffering off` + 放长读超时 + HTTP/1.1 长连接，以及 `client_max_body_size 10m`（后端另有更严的类型/魔数校验）。

## useradmin 维护 CLI

`cmd/useradmin` 是与 API 并行的带外用户维护通道（"救砖"用），配置与 server 相同（`MONGO_URI`/`JWT_SECRET` 来自 env/`.env`/ini）。典型场景：管理员丢失 2FA 设备被锁在强制设置流程外（`clear-2fa`）、重置密码邮件链路故障（`reset-password`）、密码连错触发锁定（`unlock`）、恢复/移交管理员权限（`set-role`）。

| 命令 | 动作 |
|---|---|
| `list [关键字]` / `show <uid\|accountId\|email\|_id>` | 查询用户（show 支持 UID 数字锚） |
| `set-role <id> <role>` | 改角色 |
| `reset-password <id> [--generate\|--stdin]` | 重置密码 |
| `clear-2fa <id>` | 清除 2FA 并吊销全部会话（账号重新进入强制设置流程） |
| `unlock <id>` | 清除锁定计数 |
| `rename <id> <newName> [--force]` | 改用户名；`--force` 越过黑名单与冷却（不能越过占用检查），旧名照常写入保留期 |
| `set-email <id> <email> [--verify]` | 改邮箱，`--verify` 直接标记已验证 |
| `verify-email <id>` | 标记邮箱已验证 |
| `revoke-sessions <id>` | 吊销全部会话 |

破坏性操作需交互确认，`--yes` 跳过（脚本/CI 用）。`FindByUID` 按 UID 数字锚查找是为该 CLI 新增的 repository 方法。与后台 API 的分工：管理员/超管的 2FA 不能经 API 自行关闭，只能用这里的 `clear-2fa` 恢复。

## 多实例影响

在负载均衡后运行多个实例会改变行为，因为多个存储是进程本地的：

- 验证码（注册/登录验证码与 2FA 邮件 OTP 两个独立 store）；
- Passkey/WebAuthn 仪式会话（5 分钟 TTL 的进程内 ceremonyStore）；
- 限流计数；
- SSE 连接上限与驱逐；
- 剧集缓存；
- 按收件人邮件限流；
- 观看去重日志。

会话、令牌与用户数据在 MongoDB 中，可跨实例工作。实际建议是在这些存储迁移到共享后端之前保持单实例。

## 部署检查清单

1. 构建二进制并放到 `ExecStart` 路径。
2. 编写包含密钥与真实 URL 的 ini。
3. 启动服务并检查 `/api/health` 报告数据库已连接。
4. 检查启动日志中的 `[WARN]`：迁移、索引与目录创建失败不会阻止启动，但需要人工处理。
5. 通过真实源验证 `/api/csrf-token` 与一个非 GET 端点。
6. 确认 `/uploads/<已知文件>` 可访问并带有缓存头。

## 相关阅读

- [配置与环境变量](../architecture/configuration.md)——所有设置与优先级。
- [架构总览](../architecture/overview.md)——启动链的上下文。
- [可观测性与后台任务](observability-and-background-jobs.md)——日志、迁移与清理任务。
