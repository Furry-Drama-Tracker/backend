---
type: architecture
title: 架构总览
description: neo-server 的分层包结构、从 main 开始的显式依赖注入链、各 internal 包的职责，以及文档所述分层比理想情况更松散的地方。
tags: [architecture, layers, dependency-injection, packages, gin]
sources:
  - id: openwiki-source-0f6ed11169bd8e6b34b44280
    resource: repo://cmd/server/main.go
  - id: openwiki-source-f89e324c07065d291ea97e16
    resource: repo://internal/handler/episodes.go
  - id: openwiki-source-a759557a449edc99daccb392
    resource: repo://internal/middleware/auth.go
  - id: openwiki-source-6b98ba42af046aa58117648f
    resource: repo://internal/middleware/cache.go
  - id: openwiki-source-47b21f10ba3dc863e7bae4e5
    resource: repo://internal/repository/repos.go
  - id: openwiki-source-621b1cb81d01767478ec2637
    resource: repo://internal/router/mount.go
  - id: openwiki-source-7106c8455bb73d12832c8523
    resource: repo://internal/server/app.go
  - id: openwiki-source-ebbcecfb3e8c07ba5e940ccd
    resource: repo://internal/server/register_routes.go
  - id: openwiki-source-f7acf9d278285ec2e6b81113
    resource: repo://internal/service/auth_service.go
  - id: openwiki-source-62a9837f2b45c92ae81ca775
    resource: repo://internal/upload/upload.go
generated: { by: "opencode", at: "2026-11-04T03:17:38.088Z" }
verified:
  - by: openwiki/0.7.2
    at: 2026-11-04T03:17:38.088Z
---

# 架构总览

neo-server 是一个 Go 模块（`github.com/xiedada05/furry-drama-be-neo`），使用 Gin 与官方 MongoDB 驱动。没有 ORM、没有全局单例、没有插件系统。二进制是由一组固定 internal 包装配而成的单一 HTTP 服务。

## 分层

预期形态是：

```
cmd/server            入口：参数、配置、Mongo 连接、监听、关停
    |
server                Gin 引擎装配、中间件顺序、路由注册
    |
middleware  handler  <-- HTTP 关注点
    |          |
    |        service   <-- 认证/账号领域逻辑
    |          |
    +-------> repository <-- MongoDB 访问
                  |
                model   <-- BSON/JSON 映射
```

阅读代码时需要注意两点限定：

1. **`middleware` 会直接访问 `repository`。** 认证中间件直接加载用户并更新会话活跃时间，不经过 service。因此依赖规则更准确地说是“禁止向上导入”，而不是严格的线性管线。
2. **大多数 handler 并不薄。** 认证 handler 委托给 `service.AuthService`，但内容、CMS 与管理 handler 经常直接查询 repository、聚合数据并在内联做业务决策。仅 `internal/handler/episodes.go` 就超过 2000 行。应把 service 层理解为认证专用，而不是通用业务层。

`internal/server` 是唯一知道所有依赖的包。它以下的包不会自行构造协作者，只有少数有记录的例外（通知 handler 从数据库句柄自行构造推送订阅 repository）。

## 启动链

`cmd/server/main.go` 只做这些事：

1. 解析 `--config` 与 `--listen`。
2. `config.Load`——缺少、过短或不合规的密钥会致命退出；示例/历史弱密钥与占位生产 CORS 源只打印 `[SECURITY WARN]`。
3. `repository.Connect`——用 15 秒上下文连接并 ping MongoDB。
4. `repository.NewRepos`——针对命名集合构造全部 repository。
5. `auth.NewSigner`——用配置的 secret 构造 JWT 签名器。
6. 运行启动迁移与索引创建；失败只打印 `[WARN]` 并继续，避免已有数据导致服务无法启动。
7. 启动后台清理任务（会话、已读通知、到期注销账号）。
8. 配置上传与图标目录，并创建两个目录。
9. `server.NewApp(server.Deps{...})`——装配中间件与路由。
10. 创建 TCP 或 Unix 监听器，开始服务，等待 SIGTERM/SIGINT 并执行 10 秒优雅关停。

迁移、索引与定时任务都已接线，但采用“失败告警继续”的平滑升级策略；详见运维页面。

## 依赖注入

`server.Deps` 携带四个值：

```go
type Deps struct {
    Config *config.Config
    DB     *mongo.Database
    Repos  *repository.Repos
    Signer *auth.Signer
}
```

随后 `RegisterRoutes` 只构造一次请求期协作者：

- `email.NewClient`（SMTP、模板、收件人限流）；
- `ipregion.NewClient`（IP 地域查询与 LRU 缓存）；
- `service.NewAuthService`（持有 repository、签名器、邮件、IP 地域、配置以及两个内存验证码存储）；
- `middleware.NewAuth`（JWT 校验与角色检查）；
- 每个挂载路由组一个 handler 容器，接收 repository、配置、认证中间件与限流器工厂。

这样做的理由是便于测试：测试可以用假实现或测试数据库构造 `Deps`，得到真实路由表。代价是 `register_routes.go` 中一段很长的构造代码。

没有包使用 `sync.Once` 或包级可变单例保存领域状态。例外是有意为之的进程级基础设施：剧集缓存、上传/SVG 目录全局变量以及限流存储。

## 包职责

| 包 | 职责 |
|---|---|
| `config` | 默认值、ini、`.env`、环境覆盖、致命校验、`IsDev` |
| `server` | Gin 引擎、中间件管线、静态上传、路由注册 |
| `router` | 通过 `Mount` 把领域路由挂载到 `/api/<path>` 单前缀（无 v1 镜像、无弃用头） |
| `middleware` | recovery、gzip、安全头、CORS、超时、日志、请求体解析、清洗、CSRF、鉴权、限流、API 统计、缓存 |
| `handler` | HTTP 解析与响应组装；40 多个按领域分组的文件 |
| `service` | 认证/账号领域逻辑：登录分支、refresh 轮换、设备链、2FA、邮箱变更、导出 |
| `repository` | MongoDB 访问，按集合区域一文件，`Repos` 聚合，ObjectID 辅助 |
| `model` | 带 BSON 标签的文档结构与面向 JSON 的子文档 |
| `auth` | JWT 签名、cookie、bcrypt、TOTP、字段加密、User-Agent 解析 |
| `security` | bluemonday XSS 清洗与密码强度 |
| `errors` | `AppError`、上传错误、全局错误渲染 |
| `ratelimit` | 命名限流器 spec 与挂载元数据 |
| `code` | 带 TTL 与尝试上限的内存验证码存储 |
| `email` | go-mail 客户端、模板、收件人限流、stderr 日志 |
| `ipregion` | `ipapi.co` 查询，带 LRU 上限与 24 小时 TTL |
| `upload` | 图片与 SVG 存储、校验、删除 |
| `pagination` | 共享 `page`/`limit` 解析与结果形状 |
| `logging` | debug 或 info 级别的 slog 文本日志 |
| `migrate` | 幂等启动数据迁移（启动时调用，失败告警继续） |
| `indexes` | 幂等索引创建（启动时调用，单个冲突不阻断其余） |
| `cron` | 会话、已读通知与到期注销账号清理任务 |

`internal/audit` 与 `internal/testutil` 是空目录；审计日志在 repository/model 层实现，测试辅助函数位于各测试文件内。

## 请求期装配

路由注册遵循一致模式：

```
handler.NewX(repos, cfg, authMiddleware, rateLimiterFactory, optionalMail)
    └ Register(group)
        └ g.GET/POST/PUT/DELETE(path, guards..., handler)
router.Mount(apiGroup, "/x", handler.Register)
```

`Mount` 把一组 handler 注册到 `/api/x` 一次。守卫是普通 Gin handler，通常是 `Protect(roles...)` 与限流包装。当前实现已移除 v1 镜像与 `Deprecation`/`Sunset` 头，所有领域端点只存在于 `/api` 下（ADR-010）。

## 数据归属

`repository.Repos` 是一个扁平的 repository 指针结构体，每个集合或一组内聚集合一个指针。构造函数接收 Mongo 集合，方法返回 model 结构体或 repository 专用行类型。service 与 handler 都接收 `*repository.Repos`；除少数专用流程（备份导入、回收站搬移）传入 `mongo.SessionContext` 外，没有额外事务抽象。

## 为什么是这个形态

ADR 记录了取舍：独立仓库与 AGPL 许可、显式四层分离、构造注入而非全局、ini 优先配置。分层是务实的而非教条的：它让 HTTP 与持久化可分离、可测试，同时允许复杂领域留在 handler 文件中，而不是为每个功能强行造一个 service 对象。

## 相关阅读

- [请求生命周期与中间件管线](request-lifecycle.md)——准确的中间件顺序。
- [持久化与数据模型](persistence-and-data-model.md)——集合与 ObjectID 规则。
- [约定与有意保留的怪癖](../quality/conventions-and-gotchas.md)——不能被“清理”的规则。
