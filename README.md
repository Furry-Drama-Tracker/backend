<div align="center">

<img src="FDT-Icon.png" alt="兽剧通" width="128" height="128" />

<h1>兽剧通</h1>

<h3>兽剧聚合平台（Furry Drama Tracker） · 后端服务</h3>

<p>
  <a href="https://github.com/Furry-Drama-Tracker/backend/releases/latest"><img src="https://img.shields.io/badge/release-latest-2ea44f" alt="Release"></a>
  <img src="https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white" alt="Go 1.26">
  <img src="https://img.shields.io/badge/platform-Linux%20%7C%20Windows%20%7C%20macOS-6b7280" alt="Platform">
  <a href="LICENSE.md"><img src="https://img.shields.io/badge/license-AGPL--3.0--or--later-0f9d58" alt="License AGPL-3.0-or-later"></a>
  <a href="https://fdt.xiedada.net/"><img src="https://img.shields.io/badge/website-fdt.xiedada.net-ff69b4" alt="Website"></a>
</p>

<p>
  <a href="https://fdt.xiedada.net/"><b>🌐 打开网站</b></a> ·
  <a href="#普通用户请看这里">普通用户</a> ·
  <a href="#想自己搭建一套">自己搭建</a> ·
  <a href="#常见问题">常见问题</a> ·
  <a href="https://github.com/Furry-Drama-Tracker/backend/issues">反馈问题</a> ·
  <a href="#开发者文档导航">开发者文档</a>
</p>

</div>

---

**一句话**：[兽剧通](https://fdt.xiedada.net/) 是用来「追兽剧」的网站，这个仓库是它在服务器上运行的那半边程序（后端服务）。

> 💡 **只是来看剧、追剧、评分的普通访客，不需要下载或安装任何东西** —— 直接打开网站就行，读「[普通用户请看这里](#普通用户请看这里)」就够，后面的内容都是给要自己搭建的人看的。

---

## 普通用户请看这里

### 怎么用

1. 浏览器打开 **<https://fdt.xiedada.net/>**（手机、电脑都可以）。
2. 注册一个账号，就能浏览剧集、搜索、关注创作者、收藏和评分。
3. 手机上可以点浏览器的「添加到主屏幕」，用起来和 App 差不多 —— **兽剧通没有单独的 App**，网站本身就是入口。

不需要购买、不需要安装、也不需要懂任何技术。你在网站上看到的每一部剧、每一条收藏，背后都由本仓库这套程序提供。

### 遇到问题怎么办

| 情况 | 该怎么办 |
|---|---|
| 忘了密码 | 回到网站登录页，点「忘记密码」，按邮件里的链接重设 |
| 收不到验证邮件 | 先查垃圾箱/广告箱；仍收不到就等几分钟再试，或用网站内的反馈入口联系运营方 |
| 网站打不开、转圈 | 先换个网络或浏览器试试；都打不开说明是运营方服务器的问题，等一等或反馈 |
| 想注销账号、找回数据 | 通过网站内的「反馈」联系运营方处理（账号数据在运营方的服务器上，不在本仓库里） |
| 看到违规内容、想举报 | 使用网站内的「举报」入口 |
| 想提建议、报 bug | 到 [Issues](https://github.com/Furry-Drama-Tracker/backend/issues) 提，**但千万别把密码、验证码贴到公开页面** |

> 这个仓库里装的是「服务器程序」本身，**不包含任何用户数据**。你的账号、收藏、评分都存放在运营方的服务器里，由运营方维护。

---

## 想自己搭建一套

想在自己服务器上跑一套兽剧通？可以，但**需要一点命令行基础**（装数据库、填配置、启动程序），大约 15 分钟。不熟悉的话用现成网站就好。

1. **准备**：一台服务器（Linux / Windows 均可）+ 一个 MongoDB（本机安装，或用 [MongoDB Atlas](https://www.mongodb.com/atlas) 免费版）。网页界面在[前端仓库](https://github.com/furry09shou/furry-drama-tracker-frontend)，本仓库只有后端。
2. **下载**：到 [Releases](https://github.com/Furry-Drama-Tracker/backend/releases/latest) 取对应系统的单文件程序（Linux / macOS 记得先 `chmod +x`）。
3. **配置**：复制 [`.env.example`](.env.example) 为 `.env`，至少填 `MONGO_URI` 和至少 32 位的随机 `JWT_SECRET`（`openssl rand -hex 32`；Windows 用 `[guid]::NewGuid().ToString('N') + [guid]::NewGuid().ToString('N')`）。
4. **启动**：运行程序，浏览器打开 <http://localhost:5000/api/health>，看到 `"status": "ok"` 就成功了。默认只监听 `127.0.0.1`，对外访问请用 Nginx / Caddy 反向代理并配 HTTPS。
5. **建管理员**：`make seed`（或 `go run ./cmd/seed-admin`，需 Go；按报错填 `SUPERADMIN_*` 环境变量）。

> 启动失败多半是 `JWT_SECRET` 太短或 MongoDB 没启动。完整步骤、systemd、备份与排错见 [docs/contributing.md](docs/contributing.md) 和 [`deploy/`](deploy/)。

---

## 它能做什么

- **追剧**：搜索、按分类/标签筛选剧集，看简介、集数与更新状态。
- **追更**：关注剧集和创作者，收藏到自己的文件夹，用「更新日历」看接下来哪天更新。
- **打分与记录**：评分、写记录，历史记录随时翻。
- **账号体系**：邮箱验证、两步验证（2FA）、设备管理、密码找回。
- **后台管理**：剧集/分类/轮播图维护、内容审核、举报处理、数据统计、审计日志。

---

## 常见问题

**有手机 App 吗？**
没有独立 App。网站支持「添加到主屏幕」（PWA），图标和体验接近 App，离线也能打开部分页面。

**我不懂技术，能用这个吗？**
能，而且你就是主要用户 —— 直接开网站用。本仓库只是网站的后台程序，你不需要碰它。

**网站和这个仓库是什么关系？**
<https://fdt.xiedada.net/> 是运营方用这套程序搭起来的线上站。同一套程序可以部署到任意服务器，数据各自独立。

**开源吗？我能改成自己的站吗？**
可以，代码以 **AGPL-3.0-or-later** 发布：自建、修改都行，但对外提供服务时要保留版权声明并公开你的修改。

**为什么仓库里只有后端，没有网页？**
前后端是各自独立的仓库。网页界面在[前端仓库](https://github.com/furry09shou/furry-drama-tracker-frontend)，两边通过 `/api` 接口对接。

**以后换服务器，我的收藏会丢吗？**
收藏跟着数据库走。把数据库和 `uploads_dir` 上传目录一起带走，换机器也能原样恢复。

---

## 开发者文档导航

| 文档 | 内容 |
|---|---|
| [docs/architecture.md](docs/architecture.md) | 分层架构 + 依赖注入链 + 请求生命周期（从零理解） |
| [docs/decisions.md](docs/decisions.md) | 架构决策记录 ADR（每个取舍的 Why + 替代方案） |
| [docs/contributing.md](docs/contributing.md) | 扩展标准模式 + 运行调试 + 代码规范（人工维护指南） |
| [docs/API.md](docs/API.md) | 手写 API 参考（全部端点 + 错误码 + cookie + 数据结构） |
| [/api/docs](http://localhost:5000/api/docs) | Swagger UI（运行时，覆盖全部端点，非生产） |
| [AGENTS.md](AGENTS.md) | 接口契约 + 行为铁律（并行开发锚点） |

## 开发与构建

```bash
go build ./...                     # 编译全部
make build                         # 或：go build -o bin/furry-drama-be-neo ./cmd/server
make release                       # 发布构建：-trimpath + 剥离符号表/调试信息
make run                           # go run ./cmd/server
```

| 目标 | 作用 |
|---|---|
| `make build` | 编译到 `bin/furry-drama-be-neo`（Windows 目标带 `.exe`） |
| `make release` | 发布构建：`-trimpath` 并剥离符号表/调试信息（`-ldflags "-s -w"`） |
| `make run` | `go run ./cmd/server` |
| `make test` / `make vet` / `make fmt` | 测试 / 静态检查 / 格式化 |
| `make docs` | 重新生成 Swagger 产物（需要 swag） |
| `make clean` | 清理构建产物 |

`GOOS`/`GOARCH` 会作为环境变量传给 `go`，可直接交叉编译：
`make release GOOS=linux GOARCH=arm64`（非 Windows 目标的输出不带 `.exe`）。
需要不同文件名时覆盖 `BINARY`，例如
`make release GOOS=windows GOARCH=amd64 BINARY=bin/server.exe`。
Windows 下等价写法为 `nmake /f Makefile.win release GOOS=linux GOARCH=arm64`。

单元 + 集成测试需本地 mongod 于 `127.0.0.1:27017`，是行为回归闸门：

```bash
go test ./...
go vet ./...
```

## 配置

支持三种配置来源，优先级从高到低：**真实环境变量 > `.env` 文件 > ini 配置文件 > 默认值**。

### `.env` 文件（开发推荐）

启动时自动读取当前目录的 `.env` 文件（如果存在），将其中**未在真实环境中设置**的变量注入。格式兼容 dotenv（`KEY=VALUE`，支持 `#` 注释、`export` 前缀、引号包裹）。模板见 [.env.example](.env.example)。

### ini 配置文件（生产推荐）

默认按 OS 读取对应路径的 ini 文件，`--config=` 覆盖：

| OS | 默认路径 |
|---|---|
| Linux | `/etc/furry-drama-tracker/backend.ini` |
| macOS | `/Library/Application Support/furry-drama-tracker/backend.ini` |
| Windows | `C:\ProgramData\furry-drama-tracker\backend.ini` |

section：`[server] [database] [jwt] [email] [vapid] [security]`。模板见 [`deploy/furry-drama-be-neo.ini`](deploy/furry-drama-be-neo.ini)。

### 环境变量

**同名环境变量优先于 ini 和 .env**（供 systemd 注入密钥、CI 注入 DEV_API_TOKEN 等）。兼容 `.env` 变量名：`JWT_SECRET MONGO_URI ENCRYPTION_KEY ALTCHA_HMAC_KEY FRONTEND_URL SITE_URL ALLOWED_ORIGINS EMAIL_* VAPID_* DEMO_EMAILS DEV_API_TOKEN PORT NODE_ENV SKIP_RATE_LIMIT`。客户端 IP 识别（代理标头信任策略，ADR-013）：`TRUST_PROXY_HEADERS PROXY_IP_HEADERS TRUSTED_PROXIES PROXY_TRUSTED_PLATFORM`。IP 地域解析默认关闭（数据出境合规，ADR-017）：`IP_REGION_ENABLED`。

启动致命校验：缺 `JWT_SECRET`（或 < 32 字符）/ `MONGO_URI` → 退出 1。示例/历史弱密钥与生产 CORS 占位域名仅输出安全告警，不阻止已有服务启动；升级后应尽快轮换/修正。

## 技术架构

```
cmd/server/            入口（--config/--listen、TCP/Unix 监听、优雅关停）
cmd/diag/              认证排障 CLI（会话墓碑 + authevents 时间线，只读）
internal/
  config/              ini+env 加载与校验
  server/              Gin 装配（中间件管线顺序）+ 路由注册表
  middleware/          CORS/CSRF/Protect 鉴权/ratelimit/apitracker/requestlogger/bodyparse/sanitize...
  router/              领域路由挂载辅助（/api 单前缀）
  handler/             薄 HTTP 层（auth + usersession/twofactor/users + 内容域 episodes/series/follows/favorites/folders/histories/ratings + 管理/审核/站点长尾/通知/工具）
  service/             领域逻辑（登录分支流、refresh 轮换+重用检测、设备登录码链、2FA、导出）
  repository/          mongo 仓储（Repos 聚合，ToObjectID 规范化）
  model/               struct 映射（bson 标签与既有集合数据形态一致）
  auth/                jwt/cookies/bcrypt(cost12)/totp/backupcode/fieldcrypto(AES-256-CBC)/parseUserAgent
  authlog/             认证诊断事件（结构化 stderr event=... + authevents 落库）
  security/            XSS 清洗（bluemonday）+ 密码强度
  altcha/              Altcha v2 PoW
  ratelimit/           Store 接口 + 内存滑动窗口 + 命名限流器
  code/                验证码内存存储（CodeStore 接口）
  email/               go-mail 封装 + 模板 + 目标限流
  ipregion/           IP 地域解析（默认禁用；开启后 ipapi.co 外呼 + 24h LRU）
  upload/              图片落盘（魔数/大小/类型校验）
  pagination/ errors/ logging/ audit/ migrate/ indexes/ cron/
docs/                  swagger 产物 + 维护文档
deploy/                systemd unit + ini 模板 + run.sh
```

## 认证与安全模型

- **双 token**：access 15m（无状态 JWT）+ refresh 7d（轮换，含随机 jti）。`refreshToken` cookie path=`/api/auth`。
- **419** = access 过期（`auth.accessTokenExpired`），前端自动调 `/api/auth/refresh`。
- **refresh 重用检测**：原子取用并作废；2min 并发宽限内 → 409（不吊销），超期 → 401 且吊销全部会话。
- **CSRF**：非 GET 请求校验 `XSRF-TOKEN` cookie 与 `X-XSRF-TOKEN` header 双拷贝相等（三态 403）。
- **Altcha PoW 验证码**：SHA-256 cost 10000；`DEV_API_TOKEN` + `x-dev-token` 头可绕过（仅开发）。
- **账号锁定**：5 次失败锁 30 分钟（`loginAttempts`/`lockUntil`）。
- **字段加密**：AES-256-CBC，`enc:iv:ct`，key=`sha256(ENCRYPTION_KEY 或 JWT_SECRET)`，兼容既有加密数据。
- **限流**：全局 300/min + 各端点专用（login 5/15min、register 3/h 等）；per-endpoint 只匹配 `/api/<mount>`。
- **认证排障**：会话置废写 `revokeReason`；失败/吊销类事件写 `authevents`（30 天 TTL）并输出 `event=...` 结构化日志；`make diag ARGS="-user=someone@example.com"` 直接给出会话时间线与"为什么掉登录"的结论（`-recent -hours=6` 看全局面）。

## 部署

- `deploy/furry-drama-be-neo.service`：systemd unit（Unix socket / TCP）。
- `deploy/run.sh`：nohup 备选。
- 优雅关停：SIGTERM/SIGINT → 10s 内排空在途请求。

## 功能覆盖

全部业务域已实现：

- **认证+用户**：auth（session/device/password/email/account）、user-sessions、2fa、users
- **内容域**：episodes、series、follows、favorites、folders、saved-folders、histories、ratings
- **通知**：notifications（含 PushSubscription）
- **管理**：admin、audit-logs、stats、reports、review
- **内容管理**：categories、banners、auto-status、versions
- **站点长尾**：creator-profile、activity、creator、feedback、site-content
- **公告/壁纸/友链**：announcements、wallpapers、friend-links
- **工具**：rss、translate、backup

全部端点经 `router.Mount` 挂载在 `/api` 下（v1 镜像已移除）。

## 许可证

以 [AGPL-3.0-or-later](LICENSE.md) 发布。自建、修改均可；对外提供服务时需保留版权声明并公开修改后的源码。

Copyright (c) 2026 Furry Drama Tracker。
