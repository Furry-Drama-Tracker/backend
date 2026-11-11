---
type: architecture
title: 持久化与数据模型
description: neo-server 如何在不使用 ODM 的情况下使用官方 MongoDB 驱动、集合与 BSON 模型如何组织（含会话吊销原因与认证事件）、ID 与错误约定，以及启动时接线的索引与迁移如何容错执行。
tags: [mongodb, persistence, repositories, models, indexes, migrations]
verified:
  - by: openwiki/0.7.2
    at: 2026-11-11T16:33:08.526Z
sources:
  - id: openwiki-source-f89e324c07065d291ea97e16
    resource: repo://internal/handler/episodes.go
  - id: openwiki-source-90472325131c7abb6569a18c
    resource: repo://internal/indexes/indexes.go
  - id: openwiki-source-3ee71ed54dce769eb3daf020
    resource: repo://internal/migrate/migrate.go
  - id: openwiki-source-073b77f6a5791985babef0c1
    resource: repo://internal/model/episode.go
  - id: openwiki-source-c04b809e997b6644e3320acf
    resource: repo://internal/model/model.go
  - id: openwiki-source-d398759a1a3f0969ca03110a
    resource: repo://internal/repository/db.go
  - id: openwiki-source-bd4263ae6284e6c27650b1e8
    resource: repo://internal/repository/idutil.go
  - id: openwiki-source-bb3d20ad5ebcfd9197f76c8a
    resource: repo://internal/repository/push_subscription.go
  - id: openwiki-source-47b21f10ba3dc863e7bae4e5
    resource: repo://internal/repository/repos.go
  - id: openwiki-source-f9cd993b1da0967b3ad454b7
    resource: repo://internal/repository/user.go
generated: { by: "zcode", at: "2026-11-11T16:33:08.526Z" }
---

# 持久化与数据模型

neo-server 通过官方 Go 驱动（`go.mongodb.org/mongo-driver` v1）访问 MongoDB，没有 ODM，也没有驱动层 schema 校验。数据形态由 `internal/model` 中的 BSON 结构标签与 `internal/repository` 中的查询共同定义。

## 连接

`repository.Connect(ctx, uri, name, pool)` 构造客户端并设置：

- `SetMaxPoolSize(pool)`（非正数时默认 10）；
- 10 秒服务器选择超时；
- 45 秒 socket 超时；
- 返回前对集群执行一次 Ping。

`name` 为空时从 URI 路径提取数据库名，去掉查询参数，缺省为 `furry_drama_tracker`。Ping 失败会返回给 `main` 并退出。

## 集合与 repository

`repository.NewRepos(db, loginMaxAttempts, loginLockMinutes)` 构造一个扁平的 `Repos` 结构体，每个集合或一组内聚集合一个字段。集合名是旧系统使用的小写复数形式，不能随意修改：

| Repository 字段 | 集合 |
|---|---|
| `Users` | `users` |
| `Sessions` | `usersessions` |
| `UsedTokens` | `usedtokens` |
| `Notifications` | `notifications` |
| `AuditLogs` | `auditlogs` |
| `AuthEvents` | `authevents` |
| `ApiUsage` | `apiusages` |
| `SiteContents` | `sitecontents` |
| `Settings` | `settings` |
| `Follows` | `follows` |
| `Histories` | `histories` |
| `Favorites` | `favorites` |
| `Ratings` | `ratings` |
| `Reports` | `reports` |
| `Feedbacks` | `feedbacks` |
| `CreatorProfiles` | `creatorprofiles` |
| `Episodes` | `episodes` |
| `SingleEpisodes` | `singleepisodes` |
| `EpisodeVersions` | `episodeversions` |
| `EpisodeTrash` | `episodetrash` 及来源与关联集合 |
| `Folders` / `SavedFolders` | `folders` / `savedfolders` |
| `Series` | `series` |
| `Categories` / `Banners` | `categories` / `banners` |
| `Announcements` / `Wallpapers` / `FriendLinks` | `announcements` / `systemwallpapers` / `friendlinks` |
| `Themes` / `Icons` | `themes` / `icons` |

有两个 repository 不在聚合内：推送订阅（`pushsubscriptions`）由通知 handler 构造；回收站 repository 持有多个集合句柄，因为搬移与彻底删除剧集跨集合操作。

## 模型映射

模型是带 `bson` 标签与响应组装用 JSON 标签的普通结构体。约定：

- `_id` 为 `primitive.ObjectID` 加 `omitempty`，插入时可提供 ID 或让 Mongo 生成。
- ID 在 JSON 中序列化为 24 位 hex 字符串，消费方依赖这一格式。
- 敏感字段除 handler 侧 DTO 外还使用 `json:"-"`：`password`、`twoFactorSecret`、`twoFactorBackupCodes`、`loginAttempts` 与 `lockUntil`。
- MongoDB 的 `__v` 版本键建模为 `VersionKey int`，标签为 `bson:"__v,omitempty"`，响应中常输出 `__v`。
- 遗留默认值通过显式构造函数表达，例如七个邮件通知偏好默认 true、背景偏好默认空图片与 opacity 30/blur 0、个人壁纸归一化为 `[]` 而非 `null`。
- `singleepisodes` 的 `episodeLabel` 是显示用集数序号（自由字符串）：存量文档缺省为空串，读取时由响应层经 `episodeRefText` 回退为「第N集」，因此该字段无需数据迁移。
- 部分文档携带只读不写的遗留字段，例如 `themeId` 与 `themeApplyIcons`/`themeApplyWallpaper`，读取时会迁移到双槽主题模型。

认证链路新增三个存储契约：

- `usersessions.revokeReason`（`model.Revoke*` 常量）是会话被置为 inactive 的原因墓碑：`logout`（登出）、`rotated`（refresh 轮换，正常）、`password_changed`、`password_reset`、`email_changed`、`refresh_reuse`（重用检测命中）、`user_revoked`/`user_revoked_all`（用户自助下线）、`admin_revoked`（管理员强制下线）与 `expired`（30 天无活跃被 cron 清理）。它只存在于库与日志，不进 API 响应。
- `users.twoFactorLastStep` 记录最近被接受的 TOTP 时间步（Unix 秒 / 30），`UserRepo.MarkTOTPStepUsed` 以原子条件更新实现一次性消费，用于拒绝同一时间步重放。
- `UserRepo.Create` 在 `createdAt` 为零值时补当前时间（UTC、毫秒对齐），显式赋值不受影响；这修复了 `/api/auth/register` 把 `ISODate("0001-01-01")` 落库的问题。

模型文件按领域分组（`model.go` 放用户、会话、通知、审计、API 统计；另有 `episode.go`、`series.go`、`folder.go`、`theme.go` 等）。没有从标签推导 schema 的迁移框架。

## ID 规范化

JWT claims 以字符串携带用户 ID。`repository.ToObjectID(id any)` 把 hex 字符串转为 ObjectID，非法 hex 或非字符串原样返回。repository 对 `_id` 过滤应用它，使字符串 claim 与 ObjectID 路径参数行为一致。直接解析路径参数的 handler 通常调用 `primitive.ObjectIDFromHex`，非法 ID 表现为 500 `Server error`，与旧 API 一致。

## 错误约定

repository 层定义三个哨兵：

- `ErrNotFound` 与 `IsNotFound(err)` 表示文档不存在；
- 内部重复键包装与 `IsDuplicateKey(err)` 表示唯一索引 E11000 冲突；
- `repository.IsLocked(user)` 把 `lockUntil` 与当前 Unix 毫秒时间比较。

repository 使用共享的 10 秒 `contextTimeout`，每个操作创建子上下文，因此卡住的查询不会无限期占用请求。

## 索引

`internal/indexes` 包含幂等的 `Ensure(ctx, db)`，会创建以下索引：

- `users`：唯一 `email`、唯一 `accountId`；
- `usersessions`：`userId+isActive`、`isActive+lastActiveAt`、唯一稀疏 `refreshTokenHash`、唯一稀疏 `tokenHash`；
- `usedtokens`：唯一 `tokenHash`、`expiresAt` TTL；
- `histories`：`lastWatched` TTL（365 天）、`userId+lastWatched`；
- `notifications`：`userId+isRead`、`createdAt` TTL（90 天）、`userId+createdAt`；
- `ratings`：唯一 `userId+episodeId`、`episodeId`；
- `follows` 与 `favorites`：唯一 `userId+episodeId`；
- `auditlogs`：`createdAt`、`adminId`、`userId`；
- `authevents`：`userId+at`、`email+at`、`at` TTL（30 天）——认证诊断事件的保留期由 TTL 索引决定；
- `apiusages`：唯一 `endpoint+date`；
- `sitecontents`：唯一 `key`；
- `folders`：`userId+type`；`savedfolders`：`userId`；
- `episodes`：审核状态组合索引、`createdBy`、`allowedEditors`、`customAuthors`、`tags`、`category`、`premiereDate`；
- `series`：`createdBy`、`createdAt`；
- `themes`：`ownerId`、`isSystem+enabled`、`status`、`isDefault`；
- `reports`：`reporterId+status`、`status+createdAt`。

创建按名称幂等：已存在则跳过，"already exists" 错误视为容忍。单个索引失败（常见于历史重复数据）不会阻断其余索引，错误被聚合后返回给调用方。

**启动接线：** `indexes.Ensure` 在 `main` 中、开始监听之前调用。失败只打印 `[WARN]` 并继续启动，因此依赖唯一索引的重复键处理（追番、收藏、评分、用户、会话）在索引冲突未修复前仍可能失效。

## 迁移

`internal/migrate` 实现三个幂等、带标记的迁移，标记存储在 `settings` 集合：

1. `accountId_migration_v1`——为缺失 `accountId` 的用户回填：把用户名中非单词字符替换为 `_`、转小写，冲突时追加 `_N`。
2. `role_migration_v1`——把遗留 `adminAccess: true` 用户转为 `role: "admin"`，并移除所有用户的 `adminAccess` 字段。
3. `creatorProfile_migration_v1`——删除旧 `adminId_1` 索引，把 `adminId` 重命名为 `creatorId`，并为缺少主页的 creator、admin、superadmin 用户创建初始主页。

每个迁移先检查 `settings.GetFlag`，成功后写入 `{done: true}`。`migrate.Run` 在首个错误处停止。它在 `main` 中先于索引创建、先于开始监听执行；失败只打印 `[WARN]` 并继续启动，因此遗留数据转换失败时需要人工处理后重启。

## 运维影响

- 索引创建与迁移在启动时自动执行，但失败只告警；首次部署或历史数据重复时应检查启动日志并人工修复后重启。
- 模型描述的历史 TTL 与通知 TTL 依赖索引；索引创建失败时文档不会自动删除。`authevents` 的 30 天 TTL 同理——只增不改的认证事件靠该索引自动过期，无手动清理路径。
- 回收站搬移与备份导入是仅有的多集合事务路径；大多数 repository 操作是单文档的，依靠唯一索引或原子 `findOneAndUpdate` 保证正确性。
- 账号注销的级联清理由 `service.PurgeUserData` 统一实现，cron 与管理员删号共用；清理是 best-effort，单个集合失败不阻断其余。

## 相关阅读

- [内容目录 API](../api/content-catalog.md)——剧集、系列与用户内容库文档的使用。
- [架构总览](overview.md)——包边界与依赖注入。
- [可观测性与后台任务](../operations/observability-and-background-jobs.md)——cron、迁移与索引接线状态。
