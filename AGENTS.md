# neo-server — AGENTS.md

Go/Gin 版兽剧聚合平台后端，前端零改动对接。

- 独立 git 仓库，license AGPL-3.0-or-later（见 LICENSE.md）
- module `github.com/Furry-Drama-Tracker/backend`，Go 1.26
- 官方 `go.mongodb.org/mongo-driver`（v1，无 ODM），手写 repository

## Commands

```bash
make build                         # = go build -o bin/furry-drama-be-neo ./cmd/server（Windows 目标带 .exe）
make release                       # 发布构建：-trimpath -ldflags "-s -w"（剥离符号表 + 调试信息）
make run / test / vet / fmt / docs / clean
make release GOOS=linux GOARCH=arm64   # 交叉编译：GOOS/GOARCH 经环境变量传给 go
nmake /f Makefile.win              # Windows 等价（build/release/run/seed/test/vet/fmt/docs/clean）

GitHub Actions: .github/workflows/release.yml — 每次推送构建 8 目标
（linux/windows/darwin × 386/amd64/arm64，macOS 无 32 位；无 CGO，参数同 make release）。
main 分支推送额外把产物覆盖发布到 tag=latest 的滚动 Release（含 checksums.txt）。
Windows 产物自动链接已提交的 cmd/server/rsrc_windows_*.syso（图标+版本元数据）。

go build ./...                    # 编译全部
go build -o bin/furry-drama-be-neo ./cmd/server
go vet ./...                      # 静态检查
go test ./...                     # 单元 + 集成测试（需 mongod 于 127.0.0.1:27017）
go run ./cmd/server --config=dev.ini --listen=tcp:0.0.0.0:5000
go run ./cmd/server --listen=unix:/run/furry-drama-be-neo.sock   # Unix socket
```

## 配置

优先级从高到低：**真实环境变量 > `.env` 文件 > ini 配置文件 > 默认值**。

- **`.env` 文件**：启动时自动读取当前目录的 `.env`（如果存在），仅为未在真实环境中设置的变量注入值。兼容 dotenv 格式（`KEY=VALUE`、`#` 注释、`export` 前缀、引号包裹）。
- **ini 配置文件**：默认按 OS 读取（Linux: `/etc/furry-drama-tracker/backend.ini`，macOS: `/Library/Application Support/furry-drama-tracker/backend.ini`，Windows: `C:\ProgramData\furry-drama-tracker\backend.ini`），`--config=` 覆盖。section：`[server] [database] [jwt] [email] [vapid] [security]`。
- **环境变量**：同名环境变量优先于 ini 和 `.env`（供 systemd 注入密钥、CI 注入 DEV_API_TOKEN 等）。兼容 `.env` 变量名：`JWT_SECRET MONGO_URI ENCRYPTION_KEY ALTCHA_HMAC_KEY FRONTEND_URL SITE_URL ALLOWED_ORIGINS EMAIL_* VAPID_* DEMO_EMAILS DEV_API_TOKEN PORT NODE_ENV SKIP_RATE_LIMIT`。客户端 IP 识别（代理标头信任策略，ADR-013）：`TRUST_PROXY_HEADERS PROXY_IP_HEADERS TRUSTED_PROXIES PROXY_TRUSTED_PLATFORM`。IP 地域解析默认关闭（数据出境合规，ADR-017/ADR-018）：`IP_REGION_ENABLED`（=ini `[security].ip_region_enabled`）仅在显式开启时启用；数据源由 `IP_REGION_PROVIDER`（`aliyun` 默认=阿里云云市场纯真 API / `czdb`=纯真离线库，另一源配置齐全时自动兜底）选择，凭证 `IP_REGION_APPCODE`、离线库 `IP_REGION_CZDB_PATH_V4`/`IP_REGION_CZDB_PATH_V6`/`IP_REGION_CZDB_KEY`。

启动致命校验：缺 `JWT_SECRET`/`MONGO_URI` 或 secret < 32 字符 → 退出 1。示例/历史弱密钥、生产 CORS 占位域名仅告警不阻止启动，便于已有服务平滑升级；应尽快轮换/修正。

## 目录结构

```
cmd/server/           入口（--config/--listen、TCP/Unix 监听、优雅关停）
cmd/seed-admin/       初始超管创建 CLI（全库无用户时 UID=0；否则走计数器取号）
cmd/useradmin/        用户维护 CLI（救砖：重置密码/清2FA/解锁/改角色/改名/吊销会话）
cmd/diag/             认证排障 CLI（会话墓碑 + authevents 时间线，只读）
internal/
  config/             ini+env 加载与校验
  server/             Gin 装配（中间件管线顺序见 app.go 注释）
  middleware/         CORS/CSRF/security/ratelimit/apitracker/requestlogger/bodyparse/...
  router/             领域路由单前缀挂载（Mount）+ 路由注册表
  handler/            薄 HTTP 层（auth/{session,device,password,email,account}、usersession、twofactor、users、csrf、health）
  service/            领域逻辑（登录分支流、refresh 轮换、2FA 多因素（TOTP/邮件OTP/Passkey）、用户名改名、导出）
  repository/         mongo repo（Repos 聚合注入）
  model/              struct 映射（bson 标签与既有集合数据形态一致）
  auth/               jwt/cookies/bcrypt/totp/backupcode/fieldcrypto/lockout
  authlog/            认证诊断事件记录器（结构化 stderr + authevents 落库）
  security/           XSS 清洗（bluemonday）+ 密码强度
  altcha/             Altcha v2 PoW
  ratelimit/          限流（Store interface，内存滑动窗口）
  code/               验证码内存存储（CodeStore interface）
  email/              SMTP 客户端 + 模板
  ipregion/           IP 地域解析（默认禁用不外呼，ADR-017；开启后 aliyun 云市场纯真 API / czdb 离线库可切换+兜底，ADR-018，LRU）
  upload/             图片落盘（魔数/大小/类型校验）
  pagination/ errors/ logging/ audit/ migrate/ indexes/ cron/
docs/                 swaggo 产物 + 维护文档
deploy/               systemd unit + ini 模板 + run.sh
```

## 接口契约（固定，勿改签名——并行 subagent 依赖）

- `config.Load(path string) (*config.Config, error)`；字段 `Cfg.IsDev`、`Cfg.Server.AllowOrigins`、`Cfg.Server.TrustProxyHeaders/ProxyIPHeaders/TrustedProxies/ProxyTrustedPlatform`（代理标头信任策略，缺省沿用 !IsDev）、`Cfg.Security.LoginMaxAttempts/LoginLockMinutes/CSRFMaxAgeHours/IPRegionEnabled/IPRegionProvider/IPRegionAppCode/IPRegionCzdbPathV4/IPRegionCzdbPathV6/IPRegionCzdbKey`、`Cfg.JWT.Secret/EncryptionKey/AltchaHMACKey/DevAPIToken/DemoEmails/AccessTTL/RefreshTTL`
- `repository.Connect(ctx, uri, name string, pool int) (*mongo.Database, error)`
- `repository.NewRepos(db, loginMaxAttempts, loginLockMinutes int) *repository.Repos`
- `auth.NewSigner(secret string) *auth.Signer`；`(*Signer).Sign/Verify`
- `auth.HashToken(token) string`（sha256 hex）
- `auth.SetAuthCookies(c, access, refresh string, isProd bool)`、`auth.ClearAuthCookies(c, isProd bool)`、`auth.SetCSRFCookie(c, isProd bool, maxAgeHours int) string`、`auth.GetAccessToken(c)`、`auth.GetRefreshToken(c)`
- `errors.New/NewKey/NewExtra/Wrap(status, message[, key, cause])`、`errors.AbortWithAppError(c, err, isDev)`（无码旧构造器，保留兼容）
- `errors.NewC/NewKeyC/NewExtraC/WrapC(code, status, message[, key/cause])`、`errors.Abort500C(c, code, msg, cause, isDev)`、`errors.AbortErrC/AbortCoded/AbortKeyed(...)`、`errors.SetLogger(l)`、`errors.NewRefID()`、`errors.ContextRefID`
- `middleware.RequestRef(isDev)`（全局链第一个中间件：生成参考号 ref + `X-Ref-ID` 响应头 + isDev 入 context）
- `pagination.Parse(c) (Query)`、`(*Query).Skip()/TotalPages()`
- `middleware.CORS(allowOrigins []string)`、`middleware.CSRF()`
- `router.Mount(parent *gin.RouterGroup, mountPath string, register func(*gin.RouterGroup))`
- `server.NewApp(server.Deps{Config, DB, Repos, Signer}) *gin.Engine`
- `handler.CSRF(cfg)`、`handler.Health(db)`

## 错误码与参考号（2026-11 契约，勿破坏）

所有 4xx/5xx 响应体统一附带三个信号（200 与 health 503 / oauth 501 / 上传 400 信封除外）：

```json
{"message": "该邮箱已注册", "error": "USR0007", "ref": 123456789}
```

- **error**：`XXXYYYY`——X=3 字母模块前缀，Y=文件（或模块共享文件组）内顺序号。码即注册表：`grep -rn "USR0007" backend/` 直达唯一调用点。**新站点取号 = 同前缀仓库内当前最大序号 +1**（4 位数字，前缀不得复用于不同模块）。
- **ref**：每请求一个 0-999999999 随十进制整数（`crypto/rand`，`middleware.RequestRef` 生成）；响应头 `X-Ref-ID` 同值；`RequestLogger`/5xx 日志行都带 `ref`。
- **message 恒为纯文案**：编号只经 `error`/`ref` JSON 字段传递，后端不拼进 message；出错编号展示与复制由前端从字段拼装（`frontend/src/utils/errorMeta.js`，格式 `USR0007-000123456`）。生产 5xx message 掩码「服务器内部错误」。
- **日志**：5xx 渲染时额外打一行 ERROR（stdout slog）：`code`/`ref`/`path`/`site`（错误构造点 file:line，`runtime.Caller` 自动抓取）/`cause`；panic 恢复走 `SYS0500`（JSON 500 + stack 入日志）。排障：拿用户报的码 grep 源码定位站点，拿 ref grep 日志定位请求。
- **构造器选型**：handler 直写用 `Abort500C/AbortErrC/AbortCoded`；middleware 固定契约响应用 `codedBody(c, code, gin.H{...})`；service 层用 `NewC/NewKeyC/NewExtraC/WrapC`。
- **机器注册表**：`docs/errors.json`（741+ 条目，含 code/kind/status/message/file/line）+ `docs/errors.schema.json`；生成器 `python3 docs/gen_errors_json.py`——新增错误码后重跑，输出字节稳定可提交。
- 例外不加码：`/api/health` 503（独立信封）、oauth 501（业务性未开放）、`UploadError` 400 信封。

## 行为一致性铁律

1. **双 token**：access 15m / refresh 7d，HS256；`refreshToken` cookie path=`/api/auth`；419=access 过期（`auth.accessTokenExpired`）
2. **refresh 轮换 + 重用**：原子 findOneAndUpdate 取用作废；未抢到且 logoutAt 在 **2min** 并发宽限内 → **409**（`auth.concurrentRefresh`，不清 cookie 不吊销）；否则吊销全部 → 401（`auth.refreshTokenReuse`）
3. **CSRF 三态** 403
4. **登录分支顺序**：altcha→锁定→删除宽限→密码→邮箱(403 needVerification)→2FA(200 need2FA)→成功；状态码不能乱。（2026-11 起新设备不再强制验证码——改为事后通知邮件；needDeviceVerify/verify-device/confirm-device-login 全链路已移除）
5. **标识与认证（2026-11 重构）**：
   - `users.uid`：不可变数字锚（首个用户/seed=0，仅注册顺序、无角色语义；`counters` 集合原子发号；登录标识=用户名(accountId)或邮箱）
   - accountId=可改登录用户名（POSIX portable filename 字符集收紧版，**允许大小写**）：3-32 位字母/数字/`_`/`-`+黑名单子串+保留名；**GitHub 式大小写策略**——存储保留原样，登录/唯一性/保留期一律按折叠小写 `users.accountIdLower`（唯一索引）判定；改名 `PUT /users/login-name`（密码再认证，冷却 7d，旧名折叠形式写 `loginnamereservations` 保留 90d TTL，豁免自己——大小写微调合法）；注册/`check-accountId` 均查保留期；迁移 `loginname_lower_backfill_v1` 回填存量 lower（变体撞名给后出现者加 _N）
   - 角色化 2FA：user 禁开（/2fa/enable 403）/creator 可选/admin+superadmin 强制（`middleware.Protect` 内置 force2FASetup 门禁：未开启时拦截非 GET，放行 2fa/logout/verify；`SessionUserJSON`/`MeJSON` 带 `force2FASetup`）；admin 的 /2fa/disable 403（自救走 `cmd/useradmin clear-2fa`）；降级为 user 自动关 2FA+吊销会话
   - 2FA 挑战响应带 `twoFactorMethods`（totp/emailOtp；emailOtp 仅已验证邮箱）；`login-2fa` 带 `method` 字段；发码 `POST /auth/2fa/email-otp/send`（凭 challenge JWT）
   - Passkey（go-webauthn）：`/api/passkeys/*`（管理+注册）与 `/api/auth/passkey/*`（登录）；user handle=UID 大端 8 字节；**Passkey 单因素登录直接通过，跳过 2FA 挑战**；仪式会话存内存（单实例语义）
   - QQ 预留：`externalidentities` 集合 + `OAuthProvider` 接口；`/api/auth/qq/*` 未配置凭据时一律 501
5. **AES-256-CBC**：key=`sha256(ENCRYPTION_KEY 或 JWT_SECRET)`（二选一非拼接），格式 `enc:iv:ct`，兼容既有加密数据
6. **单前缀路由**：所有领域端点只挂 `/api/<path>`；v1 镜像已移除，per-endpoint 限流只匹配 `/api/<mount>`；global 作用于全部 `/api/*`
7. **`_id` 输出 hex 字符串**；`backgroundPrefs` 默认 `{image:'',enabled:false,opacity:30,blur:0}`
8. 行为怪癖与有意偏离（XSS 处理、固定窗口限流等）集中在 `docs/decisions.md`——**改动任何行为前先读**；`go test ./...` 是行为回归闸门
9. **会话墓碑**：任何把会话置为 `isActive=false` 的路径都必须写 `revokeReason`（`model.Revoke*`），不得静默置废——`make diag` 与 `authevents` 排障链依赖它；认证失败/吊销事件统一走 `authlog.Recorder`（stderr `event=` + `authevents`）
10. **维护 CLI**：`go run ./cmd/useradmin <list|show|set-role|reset-password|clear-2fa|unlock|rename|set-email|verify-email|revoke-sessions>`（直连 Mongo，破坏性操作需确认，`--yes` 跳过）——管理员锁死/丢 2FA 的唯一自救通道

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
