# 架构决策记录 (ADR)

本文档记录 neo-server 的关键架构决策与取舍，供维护者理解"为什么这样做"。格式：决策 → 背景 → 替代方案 → 取舍。**每个决策都是权衡，不是绝对正确**——改动前请先读对应决策。

---

## ADR-001 独立仓库 + AGPL 许可

- **决策**：neo-server 是独立 git 仓库（非 furry-drama-tracker 子目录模块），license AGPL-3.0-or-later，module `github.com/xiedada05/furry-drama-be-neo`。
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

- **决策**：access JWT 15min（无状态，不查库）+ refresh JWT 7d（轮换，**必须带随机 jti**）。refresh 原子取用并作废，重用检测：30s 并发宽限内 → 409（不吊销），超期 → 吊销全部会话。
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
