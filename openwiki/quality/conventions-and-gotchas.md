---
type: quality
title: 约定与有意保留的怪癖
description: Go 后端编码规则、新增端点/集合/定时任务/配置项的配方、不得擅自修复的既有兼容性怪癖，以及近期 ADR（ADR-013 至 ADR-017）的取舍。
tags: [conventions, gotchas, compatibility, code-style]
sources:
  - id: openwiki-source-193053215eb2d471ac4f675e
    resource: repo://docs/contributing.md
  - id: openwiki-source-5e8cfa6ef22542c9ea8aaf4a
    resource: repo://docs/decisions.md
  - id: openwiki-source-7bd911fdd3026b7b031a01e3
    resource: repo://go.mod
  - id: openwiki-source-c167b655890cdbda616b14d7
    resource: repo://internal/auth/totp.go
  - id: openwiki-source-6ca2ae0d44448102cf549f8a
    resource: repo://internal/code/store.go
  - id: openwiki-source-9002449d5a813a8783166c8a
    resource: repo://internal/errors/apperror.go
  - id: openwiki-source-f89e324c07065d291ea97e16
    resource: repo://internal/handler/episodes.go
  - id: openwiki-source-dd013bc1bb1449fcd2111f58
    resource: repo://internal/handler/twofactor.go
  - id: openwiki-source-f7cdef93f6fa51dc3ffc0594
    resource: repo://internal/middleware/ratelimit.go
  - id: openwiki-source-6e8f4534cd26274b5794f0ca
    resource: repo://internal/ratelimit/limiter.go
  - id: openwiki-source-bd4263ae6284e6c27650b1e8
    resource: repo://internal/repository/idutil.go
  - id: openwiki-source-724dd1d09cfbc9ac4d7a0d9e
    resource: repo://internal/router/mount_test.go
  - id: openwiki-source-621b1cb81d01767478ec2637
    resource: repo://internal/router/mount.go
  - id: openwiki-source-a8ba835229d442d09305d33e
    resource: repo://internal/security/xss.go
  - id: openwiki-source-ebbcecfb3e8c07ba5e940ccd
    resource: repo://internal/server/register_routes.go
  - id: openwiki-source-540faaa12781d81a1f3eb2aa
    resource: repo://internal/service/passkey.go
  - id: openwiki-source-3f2309f0c3b4e4f8068472e9
    resource: repo://internal/service/register.go
generated: { by: "zcode", at: "2026-11-13T18:41:36.261Z" }
verified:
  - by: openwiki/0.7.2
    at: 2026-11-13T18:41:36.261Z
---

# 约定与有意保留的怪癖

本项目刻意保留旧实现的行为。若干看起来像缺陷的地方其实是兼容契约。经验法则是：任何会改变 HTTP 响应、状态码、message key、限流 key 或存储格式的改动都是行为变更，需要明确决策。固定文案同样属于契约；最近新增的例子是集数称呼——站内通知、Push、邮件、RSS 与更新日历统一经 `episodeRefText`（`internal/handler/episodes.go`）输出，避免各处各自拼「第N集」。近期决策见 `docs/decisions.md`：ADR-013（客户端 IP 统一走 gin `c.ClientIP()`，代理标头信任策略可配置）、ADR-014（2FA 加固：enable 不降级 / 备份码哈希 / TOTP 一次性消费）、ADR-015（refresh 独立限流 + 并发宽限放宽到 2 分钟）、ADR-016（认证排障：会话墓碑 + authevents + cmd/diag）、ADR-017（IP 地域解析默认禁用）、ADR-018（IP 地域数据源切换纯真：云市场 API + czdb 离线库可切换/兜底）、ADR-019（认证链路安全加固：注销定点写 / 设备令牌 / 原子限流键 / 码位防覆盖 / 2FA 无凭据恢复）。用户系统重构（UID + 用户名折叠登录、角色化 2FA、Passkey、useradmin CLI）落地在 b8fc8b0。模块路径已迁移至 `github.com/Furry-Drama-Tracker/backend`，全部导入路径与文档同步更新。

## 编码规则

1. **为导出符号写文档。** 每个导出类型与函数都有 godoc 注释。这对审计与回归推理很重要。
2. **使用错误类型。** 领域错误是 `New`、`NewKey` 或 `NewExtra` 构造的 `*errors.AppError`。handler 用 `errors.AbortWithAppError` 渲染，不要为领域失败手写状态码。
3. **保持 `messageKey` 稳定。** 它是前端 i18n 键，改名会破坏翻译。
4. **遵守依赖方向。** 禁止向上导入：handler 不导入会反向依赖它的 service，repository 不导入 handler。认证中间件直接消费 repository 是已接受的例外。
5. **规范化 ID。** 当值可能是来自 JWT claims 的 hex 字符串时，对 `_id` 过滤使用 `repository.ToObjectID`。解析路径参数的 handler 可以用 `primitive.ObjectIDFromHex`，非法值表现为 500。
6. **不要新造基础设施。** 优先使用成熟库；如果必须自实现，在决策日志中记录原因。
7. **跑闸门。** `go test ./...` 是行为回归闸门，`go vet ./...` 与 `go build ./...` 也应通过。

## 错误契约

`AppError` 携带状态、消息、可选 message key、cause、operational 标志与 extra 标志。渲染规则：

- 状态 0 变为 500；
- 生产环境中状态 500 及以上始终返回“服务器内部错误”，忽略内部消息；
- 开发环境中 5xx 可以包含由 cause 派生的 `stack`；
- 设置了 `messageKey` 时会包含；
- extra 字段合并进响应体，但不能覆盖 `message` 与 `messageKey`；
- 上传错误渲染为 400，并带“文件上传错误: ...”前缀。

`errors.Handler` 作为全局兜底存在，但它注册在路由之后，因此实践中领域 handler 必须自行渲染错误。不要假设未处理错误会被转换。

## 新增端点

既有的四步模式：

1. 添加 handler 方法：解析请求、调用 service 或 repository、写响应。存在 service 时把领域决策留在 service。
2. 只有该领域有 service 时才加 service 逻辑；大多数领域直接调用 repository。
3. 在 `internal/server/register_routes.go` 的对应 `router.Mount` 块中注册路由。需要认证就加 `amw.Protect(...)`，需要限流就加 `rl(ratelimit.XxxSpec)`。路由只会出现在 `/api` 下（单前缀，无 v1 镜像）。
4. 如果端点要出现在 API 文档中，添加 Swagger 注解并用 `make docs` 重新生成 `docs/`。

记住端点限流只匹配 `/api/<mount>` 精确与子路径，且 `RegisterRoutes` 会按 spec 缓存限流器，因此共享同一 spec 的路由共用计数器。

## 新增集合

1. 在 `internal/model` 添加模型结构体，BSON 标签匹配存储文档形态，JSON 标签匹配响应契约。
2. 在 `internal/repository` 添加 repository 文件与接收 Mongo 集合的构造函数。
3. 在 `repository.NewRepos` 中把 repository 字段注册到 `Repos`。
4. 如果唯一性、TTL 或查询性能需要，把索引加到 `internal/indexes`——`Ensure` 在启动时执行，失败只告警。
5. 如果既有文档需要转换，在 `internal/migrate` 添加带 `settings` 标记的幂等迁移；它同样在启动时执行。

## 新增后台任务

遵循 cron 包模式：goroutine、对齐的首次运行、ticker、上下文取消与日志。然后在 `cron.StartSessions` 中注册；该函数已由 `main` 在开始监听前启动。新增任务时注意首个执行点的语义，以及 ctx 取消时 goroutine 能及时退出。

## 新增配置项

1. 在 `config.Config` 的对应段结构体中添加字段。
2. 在 `applyINI` 中读取（如果省略键时应保留默认值，用 `firstNonEmpty`）。
3. 如果需要环境变量覆盖，在 `applyEnv` 中读取。
4. 加到 `deploy/furry-drama-be-neo.ini`。
5. 记录优先级以及它是否为密钥。

注意 `applyINI` 中的无条件赋值：`listen`、数据库 URI/name、JWT 密钥与邮件凭据即使 ini 省略也会被覆盖。

## 有意保留的怪癖

| 怪癖 | 原因 | 后果 |
|---|---|---|
| 端点限流只匹配 `/api/<mount>` 与子路径 | 单前缀路由（v1 镜像已移除） | 其他前缀（包括 `/api/v1`）不会触发端点限流 |
| 共享 spec 的路由共用限流器 | `RegisterRoutes` 按 spec 名/窗口/上限缓存 | 2FA 预算在 Mounts 五条路由间共享（`/2fa/enable` 调用了同一 spec 工厂但不在 Mounts 内，不计数）；refresh 用独立 `RefreshSpec`（60/15min），见 ADR-015 |
| 凭证/邮件类限流键绑定目标邮箱（请求体 email 小写） | 来源 IP 可经伪造代理标头轮换，IP 键退化为无限流（ADR-019） | auth/twoFactor/passwordReset/emailVerify 四个 spec 在带 email 请求体时改用 `spec:邮箱` 计数；无邮箱体端点回退 IP 键；改 key 组成会重置既有计数 |
| otpauth secret 展示格式是 RFC 4648 Base32，内部存储保持标准 base64 | otpauth:// 规范要求 Base32（无填充大写），既有密文是 base64 | `/2fa/enable` 同时返回 `secret`（base64，既有字段）与 `secretBase32`（工具识别用）；两格式勿混改 |
| 验证码签发走 `Mint`/`SetFresh`，不覆盖未过期同码条目 | 六位码是全用户共享命名空间，静默覆盖会把攻击者已知的 code 解析到别的账号（ADR-019） | 原地写回（尝试计数）仍走 `Set`；Mint 连续 100 次冲突回退 `Set` 保可用性 |
| 已知设备判定只比对 fdt-device cookie ↔ 会话 deviceTokenHash | User-Agent 客户端完全可控、可枚举重放（CWE-290，ADR-019） | 旧会话无 deviceTokenHash 时不匹配（fail-closed），下次登录多收一封新设备提醒邮件——一次性预期摩擦 |
| 对投影加载的 user 禁止整文档 Save，登录态/注销/邮箱变更用定点 `$set` | `publicProjection` 不含 password/2FA 密文/锁定计数，整文档替换会覆写成零值静默销毁凭据（ADR-019） | 新增写路径必须用 `UserRepo` 的定点方法（`SetDeletionRequestedAt`、`UpdateDeviceInfoAndLogin`、`SetEmailPendingVerification` 等） |
| 客户端 IP 解析统一走 gin `c.ClientIP()` | 删除 4 处手写 X-Real-IP/XFF 解析（ADR-013） | 标头信任策略由 `[server]` 配置在引擎级装配；改限流/审计 key 组成会重置既有计数 |
| `ulule/limiter` 的固定窗口计数 | 替换旧滑动窗口的库选型 | `RateLimit-*` 与 `Retry-After` 是近似值；窗口边界的突发行为不同 |
| `requestEmailChange` 的键是 IP + 小写 `newEmail` | 防止用不同邮箱刷验证邮件 | 修改 key 组成会重置既有计数 |
| XSS 清洗剥除不允许标签而不是转义 | 用 `bluemonday` 替换旧 `xss` 库 | 用户 HTML 会丢失标签，而不是显示转义标记 |
| 查询字符串只剥离以 `$` 开头的键，值不做 HTML 转义 | 避免把 `&` 改成 `&amp;` 破坏 URL/搜索词 | 含 `.` 的键不再被丢弃；路由参数完全不清洗 |
| 登录/注册邮箱先 trim 再转小写；用户名存储保留原样、唯一性按 accountIdLower 折叠 | 避免大小写不同的重复账号与登录失败（GitHub 式大小写策略） | 新增入口必须复用同一规范化；`Admin` 与 `admin` 不可并存 |
| 动态观看路由永远不命中其配置的限流器 | 挂载匹配基于前缀，无法表达 `:id` | 十分钟的每 IP 观看冷却是真正的去重机制 |
| `personalWallpapers` 的 nil 序列化为 `[]` | 遗留输出形状 | 客户端可以始终按数组处理 |
| `_id` 始终是 hex 字符串 | JSON 没有 ObjectID 类型 | 字符串 ID 在 Mongo 查询前必须规范化 |
| `backgroundPrefs` 默认空图片、关闭、opacity 30、blur 0 | 遗留默认值 | 缺失字段不能被解释为零值 |
| 一秒以内的成功 GET 不出现在请求日志 | 日志量控制 | 流量分析应使用 API 统计而非日志 |
| 验证码、Passkey 仪式会话与限流在内存中 | 单实例设计 | 多实例部署会破坏验证码/仪式并使限制按实例生效 |

## 启动时接线的行为

- `indexes.Ensure` 在开始监听前执行；`users`、`usersessions`、`histories`、`notifications` 等集合的唯一与 TTL 索引会自动创建。失败只打印 `[WARN]` 并继续启动。
- `migrate.Run` 同样在启动时执行，五项迁移（account ID 回填、角色迁移、创作者主页迁移、UID 派发、用户名折叠回填）会自动运行；失败告警后继续。
- `cron.StartSessions` 启动会话清理、已读通知清理与到期注销账号清理。
- Web Push 发送仍是空实现，尽管订阅会被存储。
- SSE 流每 10 秒轮询数据库并向连接推送 `notification` 事件，但连接簿记仍是进程本地的。

## 兼容性检查清单

修改行为前问自己：

1. 是否改变状态码、消息、message key 或响应字段？
2. 是否改变限流器名称、key、窗口或挂载？
3. 是否改变存储格式（加密、哈希、BSON 标签、集合名）？
4. 是否改变登录或注册分支顺序？
5. 是否改变 CSRF、cookie 或令牌语义？
6. 是否改变 `/api/<mount>` 的挂载，或让 `/api/v1` 镜像重新出现？

任一答案为是，就把它当作有意的 API 变更，并运行完整测试套件。

## 相关阅读

- [架构总览](../architecture/overview.md)——这些规则保护的分层。
- [请求生命周期与中间件管线](../architecture/request-lifecycle.md)——限流与 CSRF 机制。
- [测试策略](testing.md)——回归闸门。
