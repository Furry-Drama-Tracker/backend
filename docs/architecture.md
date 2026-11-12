# 架构导览 (Architecture)

本文帮助新维护者从零理解 neo-server：包怎么分层、请求怎么走完一条链路、中间件按什么顺序执行。**先读 README.md（快速开始）+ AGENTS.md（接口契约）再读本文**。

## 分层与依赖方向

```
cmd/server (入口)
   │  解析 --config/--listen → config.Load → repository.Connect → NewRepos → NewSigner
   ▼
server (装配 Gin)                     ← 唯一知道所有依赖的地方
   │  NewApp(Deps{Config, DB, Repos, Signer})
   │  RegisterRoutes → 构造 email/ipregion/svc/amw/handler → 挂路由
   ▼
handler (HTTP 层，薄)  ──→  service (领域逻辑)  ──→  repository (mongo 访问)
   │  解析请求/组装响应            │  登录分支流/轮换/2FA/导出         │  FindByEmail/IncLoginAttempts...
   ▼                              ▼                               ▼
middleware (横切)              model (struct 映射)            mongo-driver (官方 v1)
   CORS/CSRF/Protect/限流          bson 标签与既有数据形态一致    无 ODM
```

**依赖只允许向下**：middleware → handler → service → repository → model。禁止反向。包内导出符号都有 godoc 注释。

## 包职责（`internal/`）

| 包 | 职责 | 关键导出 |
|---|---|---|
| `config` | ini+env 加载、致命校验、`IsDev` 派生 | `Load` |
| `server` | Gin 装配（中间件顺序）、路由注册表 | `NewApp`、`Deps`、`RegisterRoutes` |
| `middleware` | CORS、CSRF、`Auth.Protect`（鉴权五档）、限流、gzip、bodyparse、sanitize、apiTracker、requestLogger | `NewAuth`、`RateLimit`、`CSRF`、`CORS` |
| `router` | 领域路由单前缀挂载（/api） | `Mount` |
| `handler` | 薄 HTTP 层：认证五文件 + usersession/twofactor/users | `NewAuth` |
| `service` | 领域逻辑：`AuthService`（登录/注册/refresh 轮换/设备码链/2FA/邮箱变更/注销/导出） | `NewAuthService` |
| `repository` | mongo 仓储：`Repos` 聚合 + 各 collection 的 repo | `NewRepos`、`Connect`、`ToObjectID` |
| `model` | struct 映射（bson 标签与既有集合数据形态一致） | User/UserSession/... |
| `auth` | 密码学/令牌原语：jwt/cookies/bcrypt/totp/fieldcrypto/parseUserAgent | `NewSigner`、`HashToken`、`RandomHex` |
| `security` | XSS 清洗（bluemonday）+ 密码强度 | `Sanitize`、`SanitizeValue`、`ValidatePassword` |
| `ratelimit` | 命名限流器定义（Spec）+ IP key | `AuthSpec`、`IPKey` |
| `code` | 验证码内存存储（接口化，可换 Redis） | `NewStore`、`GenerateCode` |
| `email` | SMTP（go-mail）+ 模板 + 目标限流 + SiteContent 配置 | `NewClient` |
| `ipregion` | IP 地域解析：默认 `NewDisabled`（不外呼，ADR-017）；开启后按 provider 装配 aliyun 云市场纯真 API / czdb 离线库（可切换+兜底，ADR-018）+ LRU | `NewConfiguredClient`、`NewClient`、`NewDisabled` |
| `upload` | 图片落盘（魔数校验/大小/类型） | `SaveImage`、`RemoveFile` |
| `pagination` | 分页参数（page/limit 上限 100） | `Parse` |
| `errors` | AppError + 全局错误处理 | `New`、`NewKey`、`AbortWithAppError` |
| `logging` | slog 封装（dev/prod 级别） | `New` |
| `migrate` | 启动迁移（accountId/role/creatorProfile） | `Run` |
| `indexes` | 启动时确保索引 | `Ensure` |
| `cron` | 定时任务（会话/通知清理） | `Start*` |

## 依赖注入链（一次请求前如何组装）

```
main.go
  config.Load()                          → *config.Config
  repository.Connect(uri)                → *mongo.Database
  repository.NewRepos(db, ...)           → *repository.Repos（含全部 repo）
  auth.NewSigner(secret)                 → *auth.Signer（JWT 签发/校验）
  server.NewApp(Deps{Config, DB, Repos, Signer})
    └ 装配全局中间件（顺序见下）
    └ 受限流路由组 /api（挂 globalLimiter）
    └ RegisterRoutes(d, api, opts)
        └ email.NewClient + ipregion.NewDisabled/NewClient（按 ip_region_enabled）+ service.NewAuthService
        └ middleware.NewAuth(repos, signer)
        └ handler.NewAuth(svc, amw, cfg)
        └ Mount 挂 /auth /user-sessions /2fa /users
```

**重要**：`service.AuthService` 持有 `Repos`/`Signer`/`Mail`/`IPRegion`/`Config` 的引用，是认证逻辑的枢纽。handler 只做"解析 body → 调 service → 按结果组装响应"。

## 中间件管线顺序（`server/app.go`）

```
Recovery → Gzip → SecurityHeaders → CORS → Timeout(30s) → SlowLogger →
BodyParse(1mb/50mb) → SanitizeInput → SanitizeHeaders →
GET /api/csrf-token (单独注册，豁免限流) →
CSRF 校验(非 GET) → APITracker → RequestLogger →
[受限流路由组 /api] globalLimiter(300/min) → health → 业务路由 →
全局错误处理(最后)
```

关键点：
- **CSRF**：非 GET 请求校验 `XSRF-TOKEN` cookie 与 `X-XSRF-TOKEN` header 双拷贝相等（三态 403）。
- **限流**：globalLimiter 挂 `/api` 路由组（含 health）；per-endpoint 限流在具体路由上（`rl(spec)`），只匹配 `/api/<mount>`。
- **SanitizeInput**：读 body 剥离以 `$` 开头的键（防 NoSQL 操作符注入），字符串值不再做 HTML 转义；query 同样只剥离 `$` 键且不改写值；Referer 头仍经 bluemonday 清洗。

## 请求生命周期示例：POST /api/auth/login

```
1. 中间件管线：CORS → ... → BodyParse(解析 JSON body) → SanitizeInput(清洗) → CSRF 校验
   → APITracker → RequestLogger → globalLimiter(计数) → authLimiter(登录专用 5/15min)
2. handler.Auth.Login
   ├ 解析 body → service.LoginInput{Email, Password, DeviceInfo, Altcha, Ua, IP...}
   └ svc.Login(c, in)
3. service.AuthService.Login（登录分支流，顺序严格，见 AGENTS.md 铁律）
   ├ VerifyAltcha(altcha, dev-token)          // 绕过口令或 PoW 校验
   ├ Repos.Users.FindByEmailWithAuth(email)   // 含 password/loginAttempts/lockUntil
   ├ repository.IsLocked(user)                // 5 次失败锁 30min
   ├ 注销宽限检查 → purgeUser 物理删除
   ├ auth.Compare(user.Password, password)    // bcrypt cost 12
   ├ 邮箱未验证 → 发验证码 → 403 needVerification
   ├ 新设备检测 → 发设备码 → 403 needDeviceVerify
   ├ 2FA 已开 → 签发 challenge → 200 need2FA
   └ 成功：BuildDeviceInfo → Save → IssueSession
4. service.IssueSession
   ├ Signer.Sign(userID, "access", 15min)     // access token
   ├ Signer.Sign(userID, "refresh", 7d, {jti})// refresh token（带随机 jti！）
   ├ Repos.Sessions.Create(新会话, refreshTokenHash)
   └ auth.SetAuthCookies(c, access, refresh)  // cookie: accessToken(/), refreshToken(/api/auth)
5. handler 按 LoginResult 分支响应：
   - User != nil → 200 SessionUserJSON(user)
   - NeedVerification → 403 {message, needVerification, email}
   - NeedDeviceVerify → 403 {message, needDeviceVerify, email, deviceInfo}
   - Need2FA → 200 {need2FA, email, twoFactorChallenge}
6. 响应带 Set-Cookie；审计日志 fire-and-forget 写 auditlogs
```

## 数据流与集合

第一段涉及的 collection（Mongo 小写复数）：`users`、`usersessions`、`usedtokens`、`notifications`、`auditlogs`、`apiusages`、`sitecontents`、`settings`、`follows`、`histories`、`favorites`、`ratings`、`reports`、`feedbacks`、`creatorprofiles`、`episodes`（导出 populate 用基础字段）。

每个集合一个 repo 文件（`internal/repository/`），方法按业务动词命名（`FindByEmail`/`IncLoginAttempts`/`FindAndDeactivateRefresh`），返回 `*model.X` 或 `[]model.X`。ID 查询统一过 `repository.ToObjectID`（hex 字符串 → ObjectID，见 ADR-011）。
