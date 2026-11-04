---
type: api-domain
title: 管理与审核 API
description: 后台管理、剧集审核与回收站、举报、统计、分类管理、自动任务、版本历史与审计日志的角色门槛和副作用。
tags: [api, admin, moderation, review, audit, statistics]
verified:
  - by: openwiki/0.7.2
    at: 2026-11-04T15:34:40.691Z
sources:
  - id: openwiki-source-44e96c77b4637e4ff46c5518
    resource: repo://internal/handler/admin.go
  - id: openwiki-source-039537f045c759e7b68c50fa
    resource: repo://internal/handler/auditlogs.go
  - id: openwiki-source-2796508087fca859a79d72b7
    resource: repo://internal/handler/autostatus.go
  - id: openwiki-source-f89e324c07065d291ea97e16
    resource: repo://internal/handler/episodes.go
  - id: openwiki-source-c8c9d34df7c2b365c89ecb02
    resource: repo://internal/handler/episodetrash.go
  - id: openwiki-source-5892c79bee89b56ec4322f3f
    resource: repo://internal/handler/reports.go
  - id: openwiki-source-b9d3a8c329294b54d48d5a73
    resource: repo://internal/handler/review.go
  - id: openwiki-source-8bf4cd0f9bad70a28fad32ec
    resource: repo://internal/handler/stats.go
  - id: openwiki-source-54e2988cad872a1deb6494f6
    resource: repo://internal/repository/episode_trash.go
generated: { by: "zcode", at: "2026-11-04T15:34:40.691Z" }
---

# 管理与审核 API

管理面分散在多个挂载路由组中，而不是集中在单一包内。它们都位于受限流的 `/api` 组下，并通过 `router.Mount` 以单前缀挂载；每个 handler 都是薄 HTTP 层，直接调用 repository 或委托 service。鉴权通过 `middleware.Auth.Protect(...)` 的角色列表表达，部分域再叠加内联角色校验。

## 鉴权层级

| 挂载点 | 读权限 | 写权限 |
|---|---|---|
| `/admin` | `verify`、`pending-counts`、`verify-password` 与 `logout` 允许 `creator` / `admin` / `superadmin`；`creators` 仅 `admin` / `superadmin`；账号列表与创作者主页管理仅 `superadmin` | 仅 `superadmin`，且变更路由附加 `RequireEmailChanged` |
| `/review` | 先经 `Protect` 允许 `creator` / `admin` / `superadmin`，再由 `adminOnlyReview` 收窄为 `admin` / `superadmin` | 同上 |
| `/episode-trash` | 先经 `Protect`，再由 `adminOnlyReview` 收窄 | 同上 |
| `/reports` | 提交：任意登录用户；列表：`admin` / `superadmin` | 处理：`admin` / `superadmin` |
| `/stats` | overview/heatmap/lifecycle/realtime：`admin` / `superadmin`；calendar 与相关推荐公开；个性化推荐需登录 | 无 |
| `/categories`、`/banners` | 公开列表 | `admin` / `superadmin` |
| `/auto-status` | 无 | `superadmin` |
| `/versions` | `creator` / `admin` / `superadmin` | 同上 |
| `/audit-logs` | `superadmin` | 无 |

新增路由时有两个细节需要注意：

- `adminOnlyReview` 是内联 handler 校验，不是 `Protect` 角色列表。非 `admin` / `superadmin` 的调用者会收到 403，因此“创作者可进入的 `Protect` 列表 + 该内联校验”最终等价于“仅管理员可访问”。
- `RequireEmailChanged` 会拦截默认超管邮箱（`admin@furry09.com`）发出的非 GET 请求，直到邮箱被修改。它挂在 `/admin` 的变更类 `superadmin` 路由上，所以刚初始化的超管可以查看但不能修改。

## 后台管理

`Admin.Register` 挂载后台端点。后台没有独立登录入口：

1. 管理员使用公开的 `POST /api/auth/login` 完成统一登录分支（Altcha、锁定、邮箱验证、设备验证与 2FA 全部生效）。
2. 登录成功后调用 `GET /api/admin/verify` 校验后台权限并返回当前管理员信息；角色不足时由 `Protect` 返回 403。
3. `POST /api/admin/logout` 与前台登出一致，吊销当前 refresh/access 会话并清除鉴权 cookie。

历史上曾有独立的 `POST /api/admin/login`，但它缺少新设备验证与 2FA，只以“邮箱已验证且未开启 2FA”放行；该入口、`Admin.Login`、`AdminAuthSpec` 及只服务于它的 repository 方法均已移除（ADR-012），管理员统一走前台登录。后台登录不再是单独的锁定分支。

账号管理分为三类角色：

- `RegisterAccount` 只接受 `admin` 和 `creator` 角色，使用共享密码强度规则，推导或校验唯一 `accountId`，将用户创建为邮箱已验证；创作者账号会尝试创建 `CreatorProfile`（失败静默），并发送“账号已创建”邮件。
- `DeleteAccount` 拒绝删除自己、拒绝删除最后一个 `superadmin`，且只接受管理或创作者账号。删除委托 `service.PurgeUserData` 做 best-effort 级联清理（单个集合失败不阻断），比 `DeleteUser` 宽松。
- `DeleteUser` 执行完整级联：追番、历史、通知、收藏、收藏夹、反馈、会话、推送订阅与举报（举报同时按遗留字段 `reporter` 与 `reporterId` 清理），删除该用户的评分，然后对每个受影响的剧集重算平均分与评分人数；随后继续清理收藏夹收藏项、主题与创作者主页。任一步失败即返回 500。
- `UpdateRole` 接受 `user`、`creator`、`admin`、`superadmin`；拒绝修改自己、拒绝降级最后一个 `superadmin`，并在账号变为 `creator` 时创建 `CreatorProfile`。
- `ToggleAdminAccess` 在普通 `user` 与 `admin` 之间切换，但拒绝操作 `superadmin` 或 `creator` 账号。

`PendingCounts` 汇总后台待办：待审核剧集、举报、反馈、友链和主题。

### 创作者主页审核

`ApproveCreatorProfile` 把 `pendingChanges` 应用到正式字段，清空待审核块，标记 `reviewStatus=approved`，并为创作者创建 `profile_review` 通知。`RejectCreatorProfile` 保留待审核改动供创作者修改后重提，保存驳回备注并通知创作者。超管通过 `UpdateCreatorProfile` 直接编辑视为终态：清空待审核改动并标记为已通过。通知发送为 fire-and-forget，Web Push 在此处是空实现。

## 剧集审核流程

审核路由最终仅管理员可访问。通过和拒绝的语义取决于剧集是否已审核：

- **已审核剧集有待审核改动时通过**：把 `episode.PendingChanges` 应用到线上字段并清空待审核块。失效 `episode_<id>` 与所有 `episodes_` 前缀的缓存，并通知创作者。
- **新提交的待审核剧集通过**：用单次原子更新把 `reviewStatus` 置为 `approved`，失效缓存并通知创作者。
- **已审核剧集的待审核改动被拒绝**：丢弃 `pendingChanges`，线上内容不变，记录备注与审核人，并通知创作者。
- **从未通过的待审核剧集被拒绝**：标记为 rejected，并把整个文档移入剧集回收站（原因 `rejected`），立即从公开列表与个人列表消失。

审核通过导致 `currentEpisodes` 增加时，`notifyEpisodeNumberChange` 会为每个新增集数向全部追番用户写入一条 `new_episode` 通知，然后向邮箱已验证且开启 `episodeUpdate` 偏好的用户发送更新邮件。邮件按 `(剧集, 集数, 事件)` 去重一小时，使用上限 2000 条的内存映射。

## 剧集回收站

删除剧集或拒绝新提交会把文档移入 `episodetrash` 集合。repository 持有 `episodes`、`episodeversions`、`singleepisodes`、`follows`、`favorites`、`histories`、`ratings` 与 `notifications` 的句柄，因此回收站可以跨集合操作：

- `MoveToTrash` 复制剧集并附加 `trashReason`、`trashNote`、`trashBy`、`trashAt`，然后从 `episodes` 删除。
- `Restore` 用原 `_id` 重新插入 `episodes`（版本历史保持有效）并删除回收站副本；如果删除副本失败，会回滚插入。
- `Purge` 是破坏性路径，删除回收站文档及其关联的单集、版本、追番、收藏、历史、评分和通知。

HTTP 层只暴露列表、版本日志、恢复与彻底删除，四者均为管理员专属。

## 举报

任何登录用户都可以举报 `episode` 或 `creator`。原因必须是 `inappropriate`、`copyright`、`spam`、`misleading` 或 `other`，描述限制为 2000 个 UTF-16 码元。同一举报者对同一目标的第二条待处理举报会以 `Already reported` 拒绝。

列表与处理仅管理员可用。处理接受 `resolved` 或 `dismissed`，保存处理人与备注，并向举报者写入 `report_result` 通知。Web Push 是空实现，因此只送达站内通知。

## 统计

`Stats` 持有一个 `middleware.Cache`，TTL 五分钟、上限 200 条。`overview`、`calendar`、`recommendations/:episodeId`（相关推荐）、`activity-heatmap` 与 `episode-lifecycle` 按完整请求 URI 缓存；`collaborative`、`personalized` 与 `/stats/realtime` 不走缓存，直接查询实时数据。

- `overview` 接受 `7d`/`30d` 周期，聚合剧集、用户、追番、评分与举报计数，并返回高分、最多观看、最多追番榜单与活跃度趋势。
- `realtime` 把最近五分钟活跃的会话计为在线用户，把本地午夜以来活跃的会话计为今日访问，并返回今日新增用户与新增剧集。
- `episode-lifecycle` 根据剧集总观看量推导每周曲线，因此是近似值而非真实历史序列。
- `calendar` 与 `recommendations/:episodeId` 公开；协同与个性化推荐需要登录。更新日历响应在 `episodeNumber` 之外还带 `episodeLabel`：单集自定义显示序号非空时用它，否则回退为「第N集」（经 `episodeRefText` 解析）。

## 分类与轮播图

两个域都为 GET 和 POST 同时注册 `""` 与 `"/"`，以避免 Gin 自动尾斜杠重定向，因为接口契约要求两种形式都直接匹配。

- 分类：公开列表按 `order` 再按创建时间排序。管理员创建时拒绝重名；缺失名称会落到通用 500 路径，这是既有设计。更新与删除仅管理员可用。
- 轮播图：公开列表只返回 `active` 记录，按 `order` 升序、创建时间降序。`/banners/all` 返回全部但需要管理员权限。链接只允许 `http`/`https`，缺失 `title` 或 `image` 同样落到通用 500 路径。

## 自动任务

`/auto-status` 暴露两个仅超管可调用的维护触发器，在旧系统中它们本是后台任务：

- `auto-complete` 扫描连载中的剧集，当 `currentEpisodes > 0`、`totalEpisodes > 0` 且 `currentEpisodes >= totalEpisodes` 时把 `status` 置为 `completed`。
- `check-premieres` 发布到期的单集，把 `isUpcoming` 置为 `false`，并令 `releaseDate = premiereDate`。

两者都返回被修改的文档数。它们是手动触发器，没有调度器调用。

## 版本历史

`/versions` 读取 `episodeversions` 集合，对创作者和管理员开放：

- 分页列出版本（`page` 默认 1，`limit` 默认 20，无上限钳制）；
- 获取单个版本快照；
- 对比两个版本，按固定 DTO 字段顺序遍历，客户端依赖该顺序；
- 回滚到指定版本。

有一个固定怪癖：`limit` 为 0 时，列表响应的 `totalPages` 序列化为 `null` 而不是数字。

## 审计日志

`/audit-logs` 仅超管可访问，返回按 `action`、`admin`（操作者名）和 `user`（被操作用户名）过滤的审计文档，过滤使用转义后的不区分大小写正则。响应键严格为 `logs`、`total`、`page`、`totalPages`，没有 `limit` 键。

审计记录只在登录会话签发成功和备份导入时写入。日常管理变更（改角色、删除账号、审核决定）目前不会产生审计条目，因此该日志不是完整的管理操作轨迹。

## 相关阅读

- [内容目录 API](content-catalog.md)——被审核的剧集文档。
- [鉴权中间件与角色](../auth/middleware-and-authorization.md)——`Protect` 状态码与 `RequireEmailChanged`。
- [邮件发送](../integrations/email-delivery.md)——审核与追番通知邮件。
