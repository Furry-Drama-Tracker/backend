---
type: architecture
title: 持久化与数据模型
description: neo-server 如何在不使用 ODM 的情况下使用官方 MongoDB 驱动、集合与 BSON 模型如何组织、ID 与错误约定，以及存在但未接入启动流程的索引与迁移包。
tags: [mongodb, persistence, repositories, models, indexes, migrations]
verified:
  - by: openwiki/0.7.2
    at: 2026-11-02T17:41:59.592Z
sources:
  - id: openwiki-source-0f6ed11169bd8e6b34b44280
    resource: repo://cmd/server/main.go
  - id: openwiki-source-90472325131c7abb6569a18c
    resource: repo://internal/indexes/indexes.go
  - id: openwiki-source-3ee71ed54dce769eb3daf020
    resource: repo://internal/migrate/migrate.go
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
generated: { by: "opencode", at: "2026-11-02T17:41:59.592Z" }
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
- 部分文档携带只读不写的遗留字段，例如 `themeId` 与 `themeApplyIcons`/`themeApplyWallpaper`，读取时会迁移到双槽主题模型。

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
- `usersessions`：`userId+isActive`、`isActive+lastActiveAt`、唯一稀疏 `refreshTokenHash`；
- `usedtokens`：唯一 `tokenHash`、`expiresAt` TTL；
- `histories`：`lastWatched` TTL（365 天）、`userId+lastWatched`；
- `notifications`：`userId+isRead`、`createdAt` TTL（90 天）；
- `ratings`：唯一 `userId+episodeId`、`episodeId`；
- `follows` 与 `favorites`：唯一 `userId+episodeId`；
- `auditlogs`：`createdAt`、`adminId`、`userId`；
- `apiusages`：唯一 `endpoint+date`；
- `sitecontents`：唯一 `key`；
- `folders`：`userId+type`。

创建按名称幂等：已存在则跳过，"already exists" 错误视为容忍。

**接线缺口：** `indexes.Ensure` 没有从 `main` 或任何地方调用。迁移与 cron 同样如此。因此新数据库只有外部创建的索引，依赖唯一索引的重复键处理（追番、收藏、评分、用户、会话）在索引建立前不会按预期工作。

## 迁移

`internal/migrate` 实现三个幂等、带标记的迁移，标记存储在 `settings` 集合：

1. `accountId_migration_v1`——为缺失 `accountId` 的用户回填：把用户名中非单词字符替换为 `_`、转小写，冲突时追加 `_N`。
2. `role_migration_v1`——把遗留 `adminAccess: true` 用户转为 `role: "admin"`，并移除所有用户的 `adminAccess` 字段。
3. `creatorProfile_migration_v1`——删除旧 `adminId_1` 索引，把 `adminId` 重命名为 `creatorId`，并为缺少主页的 creator、admin、superadmin 用户创建初始主页。

每个迁移先检查 `settings.GetFlag`，成功后写入 `{done: true}`。`migrate.Run` 在首个错误处停止。与索引一样，当前入口不会调用它，因此需要这些转换的遗留数据必须外部迁移。

## 运维影响

- 由于索引创建与迁移目前是手动的，部署手册必须在依赖重复拒绝、会话清理或历史/通知过期之前创建唯一与 TTL 索引并运行迁移。
- 模型描述的历史 TTL 与通知 TTL 依赖索引；没有索引时文档不会自动删除。
- 回收站搬移与备份导入是仅有的多集合事务路径；大多数 repository 操作是单文档的，依靠唯一索引或原子 `findOneAndUpdate` 保证正确性。

## 相关阅读

<!-- openwiki: broken internal link [/openwiki/api/content-catalog.md] link "/openwiki/api/content-catalog.md" is root-absolute, which no real consumer resolves against the repository root (not a coding agent reading the page, not GitHub's Markdown renderer, not a local viewer); use a path relative to this file instead. Fix the href or restore the target, then delete this comment. -->
- [内容目录 API](/openwiki/api/content-catalog.md)——剧集、系列与用户内容库文档的使用。
<!-- openwiki: broken internal link [/openwiki/architecture/overview.md] link "/openwiki/architecture/overview.md" is root-absolute, which no real consumer resolves against the repository root (not a coding agent reading the page, not GitHub's Markdown renderer, not a local viewer); use a path relative to this file instead. Fix the href or restore the target, then delete this comment. -->
- [架构总览](/openwiki/architecture/overview.md)——包边界与依赖注入。
<!-- openwiki: broken internal link [/openwiki/operations/observability-and-background-jobs.md] link "/openwiki/operations/observability-and-background-jobs.md" is root-absolute, which no real consumer resolves against the repository root (not a coding agent reading the page, not GitHub's Markdown renderer, not a local viewer); use a path relative to this file instead. Fix the href or restore the target, then delete this comment. -->
- [可观测性与后台任务](/openwiki/operations/observability-and-background-jobs.md)——cron 与索引接线状态。
