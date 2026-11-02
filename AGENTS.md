# neo-server — AGENTS.md

Go/Gin 版兽剧聚合平台后端，前端零改动对接。

- 独立 git 仓库，license AGPL-3.0-or-later（见 LICENSE.md）
- module `github.com/xiedada05/furry-drama-be-neo`，Go 1.26
- 官方 `go.mongodb.org/mongo-driver`（v1，无 ODM），手写 repository

## Commands

```bash
make build                         # = go build -o bin/server ./cmd/server 的便捷入口
make run / test / vet / fmt / docs / clean
nmake /f Makefile.win              # Windows 等价（build/run/test/vet/fmt/docs/clean）

go build ./...                    # 编译全部
go build -o bin/server ./cmd/server
go vet ./...                      # 静态检查
go test ./...                     # 单元 + 集成测试（需 mongod 于 127.0.0.1:27017）
go run ./cmd/server --config=dev.ini --listen=tcp:0.0.0.0:5000
go run ./cmd/server --listen=unix:/run/furry-drama-be-neo.sock   # Unix socket
```

## 配置

优先级从高到低：**真实环境变量 > `.env` 文件 > ini 配置文件 > 默认值**。

- **`.env` 文件**：启动时自动读取当前目录的 `.env`（如果存在），仅为未在真实环境中设置的变量注入值。兼容 dotenv 格式（`KEY=VALUE`、`#` 注释、`export` 前缀、引号包裹）。
- **ini 配置文件**：默认按 OS 读取（Linux: `/etc/furry-drama-tracker/backend.ini`，macOS: `/Library/Application Support/furry-drama-tracker/backend.ini`，Windows: `C:\ProgramData\furry-drama-tracker\backend.ini`），`--config=` 覆盖。section：`[server] [database] [jwt] [email] [vapid] [security]`。
- **环境变量**：同名环境变量优先于 ini 和 `.env`（供 systemd 注入密钥、CI 注入 DEV_API_TOKEN 等）。兼容 `.env` 变量名：`JWT_SECRET MONGO_URI ENCRYPTION_KEY ALTCHA_HMAC_KEY FRONTEND_URL SITE_URL ALLOWED_ORIGINS EMAIL_* VAPID_* DEMO_EMAILS DEV_API_TOKEN PORT NODE_ENV SKIP_RATE_LIMIT`。

启动致命校验：缺 `JWT_SECRET`/`MONGO_URI` 或 secret < 32 字符 → 退出 1。

## 目录结构

```
cmd/server/           入口（--config/--listen、TCP/Unix 监听、优雅关停）
internal/
  config/             ini+env 加载与校验
  server/             Gin 装配（中间件管线顺序见 app.go 注释）
  middleware/         CORS/CSRF/security/ratelimit/apitracker/requestlogger/bodyparse/...
  router/             双版本挂载（MountDual）+ 路由注册表
  handler/            薄 HTTP 层（auth/{session,device,password,email,account}、usersession、twofactor、users、csrf、health）
  service/            领域逻辑（登录分支流、refresh 轮换、设备码链、导出）
  repository/         mongo repo（Repos 聚合注入）
  model/              struct 映射（bson 标签与既有集合数据形态一致）
  auth/               jwt/cookies/bcrypt/totp/fieldcrypto/lockout
  security/           XSS 清洗（bluemonday）+ 密码强度
  altcha/             Altcha v2 PoW
  ratelimit/          限流（Store interface，内存滑动窗口）
  code/               验证码内存存储（CodeStore interface）
  email/              SMTP 客户端 + 模板
  ipregion/           ipapi.co 外呼 + LRU
  upload/             图片落盘（魔数/大小/类型校验）
  pagination/ errors/ logging/ audit/ migrate/ indexes/ cron/
docs/                 swaggo 产物 + 维护文档
deploy/               systemd unit + ini 模板 + run.sh
```

## 接口契约（固定，勿改签名——并行 subagent 依赖）

- `config.Load(path string) (*config.Config, error)`；字段 `Cfg.IsDev`、`Cfg.Server.AllowOrigins`、`Cfg.Security.LoginMaxAttempts/LoginLockMinutes/CSRFMaxAgeHours`、`Cfg.JWT.Secret/EncryptionKey/AltchaHMACKey/DevAPIToken/DemoEmails/AccessTTL/RefreshTTL`
- `repository.Connect(ctx, uri, name string, pool int) (*mongo.Database, error)`
- `repository.NewRepos(db, loginMaxAttempts, loginLockMinutes int) *repository.Repos`
- `auth.NewSigner(secret string) *auth.Signer`；`(*Signer).Sign/Verify`
- `auth.HashToken(token) string`（sha256 hex）
- `auth.SetAuthCookies(c, access, refresh string, isProd bool)`、`auth.ClearAuthCookies(c, isProd bool)`、`auth.SetCSRFCookie(c, isProd bool, maxAgeHours int) string`、`auth.GetAccessToken(c)`、`auth.GetRefreshToken(c)`
- `errors.New/NewKey/Wrap(status, message[, key, cause])`、`errors.AbortWithAppError(c, err, isDev)`
- `pagination.Parse(c) (Query)`、`(*Query).Skip()/TotalPages()`
- `middleware.CORS(allowOrigins []string)`、`middleware.CSRF()`
- `router.MountDual(r *gin.Engine, mountPath string, register func(*gin.RouterGroup))`
- `server.NewApp(server.Deps{Config, DB, Repos, Signer}) *gin.Engine`
- `handler.CSRF(cfg)`、`handler.Health(db)`

## 行为一致性铁律

1. **双 token**：access 15m / refresh 7d，HS256；`refreshToken` cookie path=`/api/auth`；419=access 过期（`auth.accessTokenExpired`）
2. **refresh 轮换 + 重用**：原子 findOneAndUpdate 取用作废；未抢到且 logoutAt 在 30s 并发宽限内 → **409**（`auth.concurrentRefresh`，不清 cookie 不吊销）；否则吊销全部 → 401（`auth.refreshTokenReuse`）
3. **CSRF 三态** 403
4. **登录分支顺序**：altcha→锁定→删除宽限→密码→邮箱(403 needVerification)→设备(403 needDeviceVerify)→2FA(200 need2FA)→成功；状态码不能乱
5. **AES-256-CBC**：key=`sha256(ENCRYPTION_KEY 或 JWT_SECRET)`（二选一非拼接），格式 `enc:iv:ct`，兼容既有加密数据
6. **限流挂载不对称**：per-endpoint 只作用于 `/api/<path>`（不含 `/api/v1/`）；global 作用于全部 `/api/*`
7. **`_id` 输出 hex 字符串**；`backgroundPrefs` 默认 `{image:'',enabled:false,opacity:30,blur:0}`
8. 行为怪癖与有意偏离（XSS 白名单外标签剥除而非转义、固定窗口限流、per-endpoint 限流不作用 v1 等）集中在 `docs/decisions.md`——**改动任何行为前先读**；`go test ./...` 是行为回归闸门

<!-- OPENWIKI:START -->

## OpenWiki

This repository has a generated `openwiki/` evidence index. It is optional just-in-time context, not required startup reading.

- Do not enumerate, preload, or search wikis at task start. Use retrieval when the user asks for it, when unfamiliar architecture or dependency behavior materially affects the task, or when source inspection leaves an important uncertainty. Stop once the question is grounded.
- When those conditions apply and OpenWiki retrieval tools are available, use `openwiki_search` for just-in-time context and `openwiki_read` for the relevant complete sections. If search returns `workspace_required`, ask which listed workspace to use and retry with its ID.
- Use `openwiki_list_workspaces` or `openwiki_list_wikis` when workspace membership itself needs to be discovered.
- If the retrieval tools are unavailable, read `openwiki/quickstart.md` and follow its links to the relevant pages.
- Treat source code and tests as authoritative. A brief's unknowns and review items are verification gaps, not automatic requirements.
- Prefer the narrowest quiet validation that proves the changed behavior. Preserve complete failure output.

The scheduled OpenWiki GitHub Actions workflow refreshes the repository wiki. Do not hand-edit generated OpenWiki pages unless explicitly asked; prefer updating source code/docs and letting OpenWiki regenerate.

<!-- OPENWIKI:END -->
