# 架构决策记录 (ADR)

本文档记录 neo-server 的关键架构决策与取舍，供维护者理解"为什么这样做"。格式：决策 → 背景 → 替代方案 → 取舍。**每个决策都是权衡，不是绝对正确**——改动前请先读对应决策。

---

## ADR-001 独立仓库 + AGPL 许可

- **决策**：neo-server 是独立 git 仓库（非 furry-drama-tracker 子目录模块），license AGPL-3.0-or-later，module `github.com/Furry-Drama-Tracker/backend`。
- **背景**：后端独立于前端仓库演进，用户指定独立仓库、继续 AGPL。
- **取舍**：独立仓库避免污染其他项目的 git 历史；代价是与前端仓库的接口变更需手动同步。

---

## ADR-002 分层架构（handler / service / repository / model）

- **决策**：请求链路严格四层：`middleware`（HTTP 横切）→ `handler`（HTTP 解析/响应组装，薄）→ `service`（领域逻辑）→ `repository`（mongo 访问）+ `model`（struct 映射）。
- **背景**：逻辑堆在少数大文件里难以测试和维护，按职责切分后每层可独立演进。
- **取舍**：每层职责单一，可单测（repository 可 mock）；代价是代码量增加、需要依赖注入（见 ADR-003）。

## ADR-003 依赖注入（Deps → 显式构造，无全局单例）

- **决策**：`server.Deps{Config, DB, Repos, Signer}` 在 `main.go` 组装，经 `server.NewApp` → `RegisterRoutes` 传给 handler/service/middleware。无 `sync.Once` 全局单例。
- **背景**：模块级全局单例（如 User 模型全局共享）让测试难以隔离。
- **取舍**：显式注入让测试可替换依赖（如 mail 用 `SetSendMail` 注入 fake）；代价是构造代码略啰嗦。

## ADR-004 ini 配置为主 + 环境变量覆盖

- **决策**：配置优先级从高到低：**真实环境变量 > `.env` 文件 > ini 配置文件 > 默认值**。ini 默认按 OS 读取（Linux: `/etc/furry-drama-tracker/backend.ini`，macOS: `/Library/Application Support/furry-drama-tracker/backend.ini`，Windows: `C:\ProgramData\furry-drama-tracker\backend.ini`），`--config=` 覆盖。启动时自动加载当前目录的 `.env` 文件（仅为未在真实环境中设置的变量注入值），兼容 `.env` 惯例。启动致命校验：缺 JWT_SECRET（或 <32 字符）/MONGO_URI 退出。
- **背景**：用户明确反感几十个环境变量矩阵（"这么多环境变量真是烦死了"），要求 ini 分 section。同时希望保留"放一个 `.env` 就能跑"的开发体验，避免开发时还要手敲 `--config=`。
- **取舍**：ini 好读好改（生产）；`.env` 零配置启动（开发）；保留 env 覆盖供 systemd 注入密钥、CI 注入 DEV_API_TOKEN。`IsDev` = `NODE_ENV != production`，控制 cookie secure、错误栈、CORS 信任 XFF 等。

## ADR-005 行为契约与有意偏离清单（改动前必读）

- **决策**：HTTP 行为以既有契约为准：响应结构、状态码、错误 key、限流窗口与文案都是**对外承诺**，不能凭直觉"修正"。已知的怪癖集中在下表与 ADR-011，改动任何一项都视为行为变更。
- **背景**：API 文档与实际行为存在历史偏差，实际响应才是前端依赖的真值。
- **取舍**：容忍怪癖、保住兼容。典型的有意怪癖：
  - `requestEmailChange` 限流 key 由来源 IP 与目标邮箱小写值组成——防止用不同邮箱刷验证邮件，**勿简化回单一 IP**；
  - XSS 白名单外标签剥除而非转义——见 ADR-008。

## ADR-006 认证：双 token + refresh 轮换 + jti

- **决策**：access JWT 15min（无状态，不查库）+ refresh JWT 7d（轮换，**必须带随机 jti**）。refresh 原子取用并作废，重用检测：2min 并发宽限内 → 409（不吊销），超期 → 吊销全部会话。
- **背景**：**关键教训：若 refresh 不带 jti，同一秒内轮换会签发相同 token → 重用检测失效**。jti（`randomBytes(24).hex`）保证唯一。
- **取舍**：jti 是安全关键，不可省略。409 并发宽限是 UX 权衡（多标签页并发刷新不误伤），但超期重用必须吊销。

## ADR-007 字段加密 AES-256-CBC 兼容既有数据

- **决策**：敏感字段（twoFactorSecret/backupCodes、SiteContent email.pass）用 AES-256-CBC，key=`sha256(ENCRYPTION_KEY 或 JWT_SECRET)`（二选一非拼接），格式 `enc:iv:ct`。
- **背景**：必须能解密既有加密数据（2FA secret 是 base64 存储的 20 字节，email.pass 用独立的 `sha256(JWT_SECRET)` 解密——**注意 email.pass 与字段加密用不同 key 派生**，见 `internal/email/client.go decryptEmailPass`）。
- **取舍**：算法与 key 派生固定不变以兼容既有数据；未来如改 key 需迁移。

## ADR-008 换库决策（尽量不造轮子）

用户要求用成熟库，当前已替换的自实现：

| 模块 | 现用库 | 替换的自实现 | 关键取舍 |
|---|---|---|---|
| altcha PoW | `altcha-org/altcha-lib-go/v2` | 此前自实现的 920+ 行移植代码 | KDF 与前端 widget 完全一致（跨实现验证过）；挑战 signature 的 canonical JSON 形态与前端不同，但**不影响实际使用**（后端只用自生成挑战，前端只负责求解） |
| TOTP | `pquerna/otp`（hotp 底层） | 手写 RFC6238 | **secret 保持 base64 存储格式**（兼容既有数据），内部字节转 base32 供 pquerna（保字节不变） |
| IP 地域缓存 | `hashicorp/golang-lru` | 手写 map+order | 上限 1000 + 24h TTL（命中时淘汰过期） |
| 限流 | `ulule/limiter` | 手写滑动窗口 | **固定窗口语义**（沿用既定行为）；Reset/Retry-After 为近似值（客户端不依赖其精确值）；挂载规则/IP key/skip 是应用层逻辑仍自持 |
| XSS | `microcosm-cc/bluemonday` | 此前 920 行逐字节移植的清洗实现 | **行为契约：白名单外标签【剥除】而非转义**（转义会输出 `&lt;...&gt;`）；script 连同内容删除、javascript: URL/事件属性过滤。已接受此偏离（用户"向下兼容不求完美"） |

**保留的自实现（无成熟等价或标准库更合适）**：
- `internal/code/`（验证码内存存储）：进程内 Map + TTL + 尝试上限，无成熟等价库，已接口化（未来可换 Redis）。
- AES 字段加密：标准库 `crypto/aes`。
- `parseUserAgent`：业务解析（浏览器/OS/设备），标准正则。
- JWT/cookies：`golang-jwt` + `net/http`。

## ADR-009 单实例内存语义

- **决策**：验证码（emailVerify/deviceLogin）、LRU、限流计数均为**进程内存**，但写成小接口（`code.Store`、`ratelimit.Spec`）便于未来换 Redis。
- **背景**：内存状态不跨实例共享，多实例部署会破坏行为（验证码分发到另一实例失效）。
- **取舍**：保持单实例语义；多实例需显式迁移到 Redis（届时改接口实现，不动业务代码）。

## ADR-010 移除双版本挂载（v1 已删除）

- **决策**：不再提供 `/api/v1` 镜像。所有领域端点只在 `/api/X` 注册一次；`router.Mount` 只做单前缀挂载，不再注入 `Deprecation`/`Sunset` 头。
- **背景**：v1 是旧后端对外承诺的弃用镜像，但唯一客户端前端从不使用 v1。它只是同一 handler 的第二条 URL，无法独立演进，还带来 refresh cookie 路径不匹配、限流/统计/文档分裂与单边漏挂风险。
- **取舍**：这是一次有意的破坏性变更，放弃 2027-01-01 的 Sunset 承诺。若未来确实需要版本化，应通过独立 handler 或转换层实现，而不是再挂一条相同路由。
- **历史**：旧实现为 `router.MountDual` + `/api/v1` + 固定 `Deprecation`/`Sunset` 头；相关不对称规则随 v1 一并删除。

## ADR-011 接受的行为偏离清单（与其他实现约定不完全一致处）

| 偏离 | 原因 | 影响 |
|---|---|---|
| XSS 白名单外标签**剥除**而非转义 | 换 bluemonday（ADR-008） | 前端展示差异（用户输入含 HTML 时） |
| 限流窗口语义**固定窗口**（ulule） | 换库 | Reset/Retry-After 头近似；极端突发行为略异 |
| `personalWallpapers` nil → 输出 `[]` | JSON nil 序列化差异 | 已显式归一化 |
| JWT claims.id 是 hex 字符串需转 ObjectID 查库 | JSON 载荷无原生 ObjectID 类型 | 已集中到 `repository.ToObjectID` |

## ADR-012 删除独立管理员登录入口

- **决策**：移除 `POST /api/admin/login`、`Admin.Login`、`AdminAuthSpec` 以及只服务于它的 `AdminFindByIdentifierWithAuth` / `AdminUpdateLastLogin`。管理员统一走 `POST /api/auth/login`，之后用 `/api/admin/verify` 校验后台权限。
- **背景**：旧 Express 后端有独立管理员表与登录入口；迁移后管理员已是 `users` 集合中带 `admin`/`superadmin` 角色的普通账号，原入口没有独立数据模型支撑。现有前端从不调用该路径（`apiEndpoints.js` 里的 `ADMIN.LOGIN` 常量已删除）。
- **取舍**：删除重复且更弱的认证路径——它缺少新设备验证、2FA 与注销宽限检查，仅以“邮箱已验证 + 未开启 2FA”作为放行条件。任何未知外部调用方会收到 404；已确认无此类客户端。

## ADR-013 客户端 IP 识别统一走 gin `c.ClientIP()`（配置化代理标头）

- **决策**：删除 4 处手写的 clientIP 解析（`middleware/ratelimit.go`、`service/auth_service.go`、`handler/episodes.go`、`handler/notifications.go`），全仓库统一用 gin 原生 `c.ClientIP()`；标头信任策略由引擎装配（`server/app.go`）按 `[server]` 配置决定：`trust_proxy_headers`（*bool，缺省=沿用 `!IsDev`：生产信任/开发不信任）、`proxy_ip_headers`（缺省 `CF-Connecting-IP, X-Real-IP, X-Forwarded-For`）、`trusted_proxies`（IP/CIDR 清单，缺省不配=gin 默认信任全部来源）、`proxy_trusted_platform`（单一无条件最高优先标头，默认不启用）。同名环境变量 `TRUST_PROXY_HEADERS` / `PROXY_IP_HEADERS` / `TRUSTED_PROXIES` / `PROXY_TRUSTED_PLATFORM` 可覆盖；`trusted_proxies` 格式错误属启动致命校验。
- **背景**：生产部署为访客 → 阿里云 ESA（「托管转换 → 添加真实客户端 IP 标头」自定义标头名 `CF-Connecting-IP`，边缘覆盖写入真实访客 IP）→ 源站本机反代（Caddy/Nginx）→ Go。旧实现 `X-Real-IP` 优先级最高，但本机反代写出的 X-Real-IP 是它看到的 TCP 对端（=ESA 节点 IP），导致真实 IP 被"莫名其妙覆盖"成 CDN 节点地址；且旧实现不校验请求来源，直连即可伪造标头绕过限流。gin v1.12 的 ClientIP 机制（来源 ∈ trustedCIDRs 才读标头、重复同名标头合并后从右往左回溯取非受信值）正面解决这两个问题。
- **取舍 / 行为变化**：
  - 生产默认解析顺序由 `X-Real-IP > XFF 首值 > CF` 改为 **`CF > X-Real-IP > XFF 首值`**（本项目部署下即修复节点 IP 覆盖问题）；
  - auth_service 开发环境从"无条件信头"改为默认不信（可 `TRUST_PROXY_HEADERS=1` 打开）；
  - 非法 IP 格式的标头值被 gin `validateHeader` 拒绝（旧实现原样采信）；IPv4-mapped IPv6 归一化输出（`::ffff:1.2.3.4` → `1.2.3.4`）；
  - `trusted_proxies` 配置后 XFF 从右往左取第一个非受信代理 IP（未配置时信任全部=取首值，与旧语义一致）；`RateLimit` 限流 key、viewLog 去重、SSE per-IP 计数随之变化属于预期修正；
  - `proxy_trusted_platform` 分支不校验来源与格式（非空即信），仅应在前置网关保证覆盖写入该头时启用；
  - `requestEmailChange` 限流 key 仍为 IP+目标邮箱（ADR-005 契约不变）。

## ADR-014 2FA 加固：enable 不降级 / 备份码哈希 / TOTP 一次性消费

- **决策**（三项均为纯服务端行为，不改变任何响应字段）：
  1. POST /api/2fa/enable 在 	woFactorEnabled=true 时返回 **400**，不再用新 secret 覆盖既有配置（原实现会顺带写入 	woFactorEnabled=false）；
  2. 备份码改为**单向哈希**存储，格式 `bc:<sha256 hex>`（`internal/auth/backupcode.go`）；旧数据仍是 AES 密文（`enc:`），读取时兼容并在命中时就地升级整个数组；
  3. TOTP 增加**一次性消费**：登录成功后把命中的时间步写入 `users.twoFactorLastStep`，同一步的重复提交按校验失败处理（计入失败次数）——满足 NIST SP 800-63B「同一 OTP 在有效期内只能被接受一次」。
- **背景**：/2fa/enable 原本不检查既有状态，而 `SetTwoFactorSetup` 会写 `twoFactorEnabled=false`——结果是**一个有效会话就能在不知道任何 TOTP 的情况下关掉 2FA**（比需要 TOTP/备份码的 `/2fa/disable` 更弱）。备份码是等效于口令的凭据，可逆加密意味着密钥泄露即全部泄露。TOTP 无步长记录则可被窗口内重放。
- **替代方案**：
  - 备份码用 bcrypt：更慢（10 个码 × cost12 ≈ 数秒/次登录），且备份码是高熵随机值（64 位），无字典攻击面，快哈希已足够（GitHub 同做法）；
  - 给备份码加 pepper（HMAC）：更强，但与 `ENCRYPTION_KEY` 轮换耦合，且当前无独立的 2FA 专用密钥；
  - 挑战令牌一次性消费：**未采纳**，因为客户端需要在同一挑战内重试 TOTP（改为"成功才消费"仍需客户端契约确认，见待办）。
- **取舍 / 兼容性**：
  - 已启用 2FA 的用户若再次点"开启"，现在收到 400 而非静默重置；前端只应展示开启入口一次（现有 UI 已按状态切换）；
  - 旧备份码在**首次使用**时自动升级，未使用的旧码保持密文直到被使用；
  - 一次性消费只作用于 `/auth/login-2fa`（认证入口）；`/2fa/disable`、`/2fa/verify`、`/2fa/verify-enable` 不记步长——避免"启用 2FA 的码不能立刻用于登录"这类体验倒退；
  - `TwoFactorLastStep` 是瞬时反重放计数，不参与备份导出白名单（`handler/backup.go`），恢复后会失效（可接受）。
## ADR-015 refresh 独立限流 + 并发宽限放宽到 2min

- **决策**：
  1. /api/auth/refresh 从 TwoFactorSpec（5 次/15min）中摘出，改用新增的 RefreshSpec（**60 次/15min**，atelimit.RefreshName）；TwoFactorSpec 保留其余 6 个 2FA/设备端点；
  2. service.ConcurrentRefreshGrace 由 **30s** 放宽到 **2min**；同时删除 middleware 包里那份从未被使用、且已与 service 版本重复定义的 ConcurrentRefreshGrace（消除常量漂移源）。
- **背景**：/auth/refresh 被挂在 2FA 暴力破解限流器上，等于让"正常令牌轮换"与"TOTP 试错"共享 5 次/15min 配额。access 只有 15min，单用户约 4 次/小时刷新；移动网络大量用户共享运营商 NAT 出口 IP，任一用户把配额用掉，整片区用户在剩余窗口内全部 429 —— 而前端把 refresh 失败当作登出处理，表现为"用着用着突然掉登录"（并连带触发重新登录 → 新设备验证码）。
- **替代方案**：
  - 按用户而非 IP 限流：能精确隔离，但 middleware.RateLimit 的 key 生成是 IP 语义，改造面更大，留待后续；
  - 直接关闭 refresh 限流：失去 DoS 兜底，不采纳；
  - 宽限期保持 30s：会把"移动端后台挂起后迟到的请求"误判为 token 重用并吊销全部会话，正是要修的问题。
- **取舍 / 兼容性**：
  - 响应契约不变（409/401 语义、messageKey 均未改），仅阈值与时限变化；
  - 60/15min 仍保留 DoS 约束：refresh token 是高熵随机串，不存在被爆破的现实风险，故按可用性优先；
  - 宽限期 2min 远小于 access TTL（15min），不影响"真实重用必须吊销全部会话"的语义；
  - 新增回归测试 TestRefreshBurstNotRateLimited（连续 8 次轮换不得 429），旧配置下第 6 次即失败。
## ADR-016 认证排障：会话墓碑 + 认证事件（authevents）+ cmd/diag

- **决策**：
  1. 会话置废时写入 `usersessions.revokeReason`（取值见 `model.Revoke*`：logout / rotated / password_changed / password_reset / email_changed / refresh_reuse / user_revoked / user_revoked_all / admin_revoked / expired）；
  2. 认证链路的**失败与吊销类**事件写双出口：stderr 结构化日志（`event=<类型>`，journalctl 可直接 grep）＋ `authevents` 集合（`at` 建 30 天 TTL 索引）；
  3. 429 命中时记录 `event=ratelimited spec=<限流器名>`（原本日志里只有状态码，无法区分是哪个限流器打的）；
  4. 新增只读排障命令 `cmd/diag`（`make diag ARGS="-user=..."`），按邮箱/`_id` 输出会话时间线与事件，并给出"掉登录"结论。
- **背景**：用户报"用着用着掉登录"时，日志只有 `method/path/status`，既不知道 429 是哪个限流器、也不知道 401 是因为登出、改密、refresh 重用还是 cookie 根本没发出来；`sessions` 也只有 `isActive/logoutAt`，看不出**为什么**被置废。排查只能靠猜。
- **替代方案**：
  - 只写日志不落库：journald 未开持久化或日志轮转后就查不到了（用户通常隔天才报障）；
  - 只落库不写日志：现场 `tail -f` / `journalctl -f` 看不到实时抖动；
  - 复用 `auditlogs`：那份记业务操作（谁改了谁），语义不同且查询路径不同，混在一起反而更难查。
- **取舍 / 兼容性**：
  - `authevents` 只记失败/吊销（成功登录不记），量级很小；TTL 30 天自动清理，无需人工归档；
  - 落库是 fire-and-forget（3s 超时 + 失败只记一行 warn），**不影响响应**，Mongo 抖动也不会让登录失败；
  - API 响应**未新增字段**：`handler.sessionJSON` 是显式字段集，`revokeReason` 只存在于库与日志中（避开客户端契约冻结）；
  - 新增集合意味着备份/恢复白名单不含它（`handler/backup.go`）——排障数据不入备份，属预期。
- **怎么用**：
  - 现场：`journalctl -u <unit> | grep 'event='`（看实时，含 spec/reason/detail）；
  - 事后：`make diag ARGS="-user=someone@example.com -days=7"`（结论直接给"第几秒被谁以什么原因吊销"）；
  - 面：`make diag ARGS="-recent -hours=6"`（按类型统计，判断是否大面积）。

## ADR-017 IP 地域解析默认禁用（数据出境合规）+ 可替换的本地实现预留

- **决策**：
  1. `internal/ipregion` 新增 `NewDisabled()`：只识别本地 IP（`本地`），其余 IP 一律返回 `未知`，**不发起任何网络外呼**；
  2. `RegisterRoutes` 默认装配 `NewDisabled()`，仅当 `[security].ip_region_enabled = true`（环境变量 `IP_REGION_ENABLED`，优先级同其它配置）时才装配原来的 ipapi.co 客户端；
  3. 开关缺省 **false**——不依赖运维改配置，拉到新代码即停止出境；`deploy/furry-drama-be-neo.ini` 显式写出并注明合规原因。
- **背景**：登录、注册、设备确认、2FA 登录会把客户端 IP 内联发给第三方 `https://ipapi.co/<ip>/json/`（同步、5s 超时）来拼 `LastLoginRegion`。IP 属个人信息，向境外第三方 API 传递构成数据出境，需按合规要求先停用；后续计划换成本地离线库（如 ip2region 一类）查询。
- **替代方案**：
  - 直接删掉 `ipregion` 包与外呼代码：最彻底，但未来接本地库要重写四处调用点与字段语义；保留注入点更省事；
  - service 里传 `nil` 客户端：也能走 `未知` 兜底，但调用点分散在 4 处、`!= nil` 分支长期不被执行，容易在后续改动中被"顺手清理"掉；用显式 `NewDisabled()` 让同一条代码路径保持“活着”；
  - 默认开启 + 用 ini 关掉：需要每个部署改配置，漏改即继续出境，不合规风险高。
- **取舍 / 兼容性**：
  - 响应契约不变：`LastLoginRegion` 落库值变为 `未知`（本就是外呼失败时的兜底值），前端/API 无字段变化；
  - 新设备登录提醒邮件里的地域一行显示 `未知`，不影响发送与其它内容；
  - 关闭后不再有 5s 外呼，认证路径少一个外部故障点（缓存未命中时的登录延迟随之消失）；
  - 少量历史数据仍是真实地域，不做回填（回填反而需要再次出境查询）。
- **怎么接到本地实现**：把 `register_routes.go` 里 `ipregion.NewDisabled()` 换成 `ipregion.NewClient(本地查询)`（`fetch` 签名 `func(ctx, ip) string`，返回 `country · region · city` 风格的字符串），并为本地查询函数补单测；`NewClient` 的 LRU（1000 条 / 24h TTL）会继续生效，service 侧零改动。

## ADR-018 IP 地域数据源切换纯真：云市场 API + czdb 离线库（可切换/兜底）

- **决策**：
  1. 外呼数据源由 ipapi.co 换成**阿里云云市场纯真 IP 地域 API**（cmapi00053387，`GET https://cz88geo.market.alicloudapi.com/search/ip/geo?ip=<ip>`，鉴权 `Authorization: APPCODE <code>`，境内服务）；AppCode 走 `[security].ip_region_appcode` / `IP_REGION_APPCODE`；
  2. 新增**纯真离线库 czdb** 数据源（`github.com/tagphi/czdb-search-golang`，MEMORY 模式，构造期预热避免懒加载竞态后即释放文件句柄），`[security].ip_region_czdb_path_v4` + `ip_region_czdb_path_v6` 分别指定 IPv4/IPv6 的 .czdb 库文件路径，密钥 `ip_region_czdb_key` 两库共用；本地查询**无网络外呼**，查询按 IP 协议路由到对应库；
  3. `[security].ip_region_provider`（`IP_REGION_PROVIDER`，缺省 `aliyun`）选择首选数据源；**另一数据源配置齐全时自动兜底**——首选返回 `未知`（外呼失败/IP 未命中）时改走它，封装在 `ipregion.NewConfiguredClient`；
  4. 输出统一为 `country · province · city · districts` 风格。实测官方 czdb 输出为 `国家–省–市[–区]<TAB>归属描述`（– 为 en-dash U+2013，列选择为 0 时 lib 直接返回 otherData）：取首个 tab 段按 – 拆段、丢弃归属描述；兼容 geo mapping 列形态（tab 分隔列取前 4 列非空）。阿里云 API 取 data 四字段，缺失的区县会返回字面 `未知`，与空值一并跳过。
- **背景**：ADR-017 停用的 ipapi.co 是无 SLA 的免费境外服务；需求回归后选定纯真系数据源——云市场 API 精度高（区县级）但按量计费且依赖外呼，czdb 离线库免费无外呼但需人工下载/更新库文件。两者互补，故做成可切换 + 兜底而非二选一。
- **替代方案**：
  - 只接云市场 API：外呼失败/欠费即全量 `未知`，无兜底；
  - 只接 czdb：v4/v6 双库、需定期更新，忘记维护则长期陈旧；
  - 保持 ipapi.co：境外服务，数据出境合规问题依旧。
- **取舍 / 兼容性**：
  - `ip_region_enabled` 语义与缺省值不变（ADR-017 仍成立）：默认关闭，开启才外呼/查库；开启但未配置任何数据源时保持禁用态并告警；
  - `LastLoginRegion` 格式仍是 `·` 拼接字符串，service/handler/API 契约零变化；区县级比旧 ipapi.co 多一列区县；
  - czdb 仅配一个协议的库时，另一协议 IP 记 `未知`（可由 aliyun 兜底）；
  - czdb 搜索器生命周期与进程一致（MEMORY 模式全量入内存，v4+v6 约几十 MB），不做热更新——更新库文件后重启服务生效；
  - 单行形态的启发式（首段含 `–` 或 tab 段 <3）用于区分实测形态与假想的 geo mapping 列形态，若未来换用列形态库版本需以真实样本复核 `formatCzdb`。
- **怎么配**：ini `[security]` 段（模板见 `deploy/furry-drama-be-neo.ini`）或同名大写环境变量；AppCode 在云市场控制台获取（https://market.aliyun.com/detail/cmapi00053387），czdb 密钥随库文件由纯真官方下发。真实数据源冒烟：`internal/ipregion/local_smoke_test.go`（环境变量门控，默认跳过）。
