---
type: operations
title: 部署与运行时
description: 构建目标与 CI 滚动发布、认证排障命令 make diag、准确启动顺序、TCP 与 Unix socket 监听、服务器超时、优雅关停、目录初始化、systemd unit 与 ini 模板，以及多实例后果。
tags: [deployment, runtime, systemd, startup, shutdown, ci]
verified:
  - by: openwiki/0.7.2
    at: 2026-11-11T16:33:08.526Z
sources:
  - id: openwiki-source-4d1d392666be6dfdd7a91a2e
    resource: repo://.github/workflows/release.yml
  - id: openwiki-source-0f6ed11169bd8e6b34b44280
    resource: repo://cmd/server/main.go
  - id: openwiki-source-6e11d7a3d9cd222e5ed01aed
    resource: repo://deploy/furry-drama-be-neo.ini
  - id: openwiki-source-965e9675b33e3892958fab10
    resource: repo://deploy/furry-drama-be-neo.service
  - id: openwiki-source-523bc741245510a618f572a9
    resource: repo://deploy/run.sh
  - id: openwiki-source-da80fdae2220fc6ad9a470b7
    resource: repo://internal/upload/svg.go
  - id: openwiki-source-012f2c78e3b1446dfc35803f
    resource: repo://Makefile
  - id: openwiki-source-1ffe96277832af9ddb17aa8f
    resource: repo://winres/winres.json
generated: { by: "zcode", at: "2026-11-11T16:33:08.526Z" }
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

`GOOS`/`GOARCH` 通过环境变量传给 `go`，因此 `make release GOOS=linux GOARCH=arm64` 可直接交叉编译，输出名按目标平台决定是否带 `.exe`。`Makefile.win` 为 NMAKE 镜像这些目标（含 `release`、`seed` 与 `diag`）。模块要求 Go 1.26 与可访问的 MongoDB。

Windows 可执行文件内嵌应用图标与版本元数据：`FDT-Icon.ico` 经 go-winres 由 `winres/winres.json` 编译为 `cmd/server/rsrc_windows_{amd64,386,arm64}.syso`，任意 Windows 目标的 `go build` 自动链接（非 Windows 目标按文件名后缀忽略）。资源含 RT_GROUP_ICON（应用图标）与 RT_VERSION（双语产品名"兽剧通后端服务"/英文、AGPL-3.0 版权、仓库地址；版本号 0.0.0.0 占位）。更换图标后重跑 `go run github.com/tc-hib/go-winres@latest make --in winres/winres.json --out cmd/server/rsrc --arch amd64,386,arm64`。

## CI 滚动发布

`.github/workflows/release.yml` 是仓库唯一 CI 文件（只有构建与发布，不跑测试）：

- 每次推送触发 8 目标交叉构建：linux/windows/darwin × 386/amd64/arm64（macOS 无 32 位），`CGO_ENABLED=0`，参数与 `make release` 一致（`-trimpath -ldflags "-s -w"`）。
- `main` 分支推送额外把产物覆盖发布到 tag=`latest` 的滚动 Release：先删后建（`gh release delete latest --cleanup-tag`），含 `checksums.txt`；`concurrency: release, cancel-in-progress` 保证只保留最新一次。
- CI 干净检出使 Go 自动烙入 `vcs.revision`，无 `+dirty`，产物随代码变化可复现。

## 启动顺序

`cmd/server/main.go` 按顺序执行：

1. 解析 `--config` 与 `--listen`。
2. `config.Load`——缺少或过短的 `JWT_SECRET`、过短的 `ENCRYPTION_KEY`，或缺少 `MONGO_URI`，以状态码 1 退出；弱密钥与占位 CORS 源只打印 `[SECURITY WARN]`；随后打印 IP 地域解析开关状态（误开时打 `[SECURITY WARN]` 提示数据出境）。
3. `repository.Connect`，15 秒上下文——连接或 ping 失败即退出。
4. 构造 `Repos` 与 JWT 签名器。
5. 运行 `migrate.Run` 与 `indexes.Ensure`（60 秒上下文）；失败只打印 `[WARN]` 并继续启动。
6. 启动 `cron.StartSessions`：过期会话、已读通知、到期注销账号清理。
7. 应用上传与图标目录配置，并用 `MkdirAll` 创建两个目录；失败只告警。
8. 构建 Gin 应用。
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

默认是 `tcp:127.0.0.1:5000`（仅回环）。缺少 flag 时使用 ini 的 `listen`，但要注意省略该键的 ini 会清空默认值而不是保留。要从外部访问必须显式设置 `tcp:0.0.0.0:5000`。

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

若要改为 TCP，把 `ExecStart` 中的 `--listen` 改为 `tcp:0.0.0.0:5000` 并去掉 socket 依赖。反向代理应终止 TLS，通过 `CF-Connecting-IP`、`X-Real-IP` 或 `X-Forwarded-For` 转发真实客户端 IP，并单独服务前端。

## ini 模板

`deploy/furry-drama-be-neo.ini` 记录了每个段与面向生产的默认值。使用前：

1. 复制到 OS 默认路径，或让 `--config` 指向它；
2. 把 `[jwt].secret` 设为至少 32 个随机字符；
3. 设置 `[database].uri` 与可选 `name`；
4. 把 `uploads_dir` 设为持久可写路径；
5. 如需邮件，配置 `[email]`；
6. 把 `allow_origins` 设为真实前端源；按反代拓扑核对 `[server]` 代理标头四键（`trust_proxy_headers`、`proxy_ip_headers`、`trusted_proxies`、`proxy_trusted_platform`，ESA → 本机反代拓扑建议 `trusted_proxies = 127.0.0.1, ::1`）；
7. 保持 `[security].ip_region_enabled = false`（数据出境合规，ADR-017）；
8. 可选地显式设置 `encryption_key` 与 `altcha_hmac_key`。

该文件包含密钥，建议 `chmod 600`。

## run.sh

`deploy/run.sh` 是无 systemd 主机上的最小备选。尽管注释提到 nohup，它当前在前台 `exec` 二进制，并期望外部守护进程将其转入后台。二进制缺失时快速失败，使用 `CONFIG` 与可选的 listen 参数。

## 多实例影响

在负载均衡后运行多个实例会改变行为，因为多个存储是进程本地的：

- 验证码与设备登录码；
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
