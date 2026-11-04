---
type: api-domain
title: 内容目录 API
description: 剧集与系列目录、审核门控的写入路径、观看计数与缓存，以及用户追番、收藏、收藏夹、历史与评分域的行为。
tags: [api, episodes, series, follows, favorites, folders, histories, ratings]
sources:
  - id: openwiki-source-f89e324c07065d291ea97e16
    resource: repo://internal/handler/episodes.go
  - id: openwiki-source-3272d4a2a73dae56315eac5a
    resource: repo://internal/handler/folders.go
  - id: openwiki-source-a2c91d36f26b79bf16108ec2
    resource: repo://internal/handler/follows.go
  - id: openwiki-source-2e2a241e8abdb3a5ebecb00e
    resource: repo://internal/handler/histories.go
  - id: openwiki-source-2fd6aa6286220db4ac2e224f
    resource: repo://internal/handler/series.go
  - id: openwiki-source-6b98ba42af046aa58117648f
    resource: repo://internal/middleware/cache.go
  - id: openwiki-source-7dbb03889219204710573384
    resource: repo://internal/model/misc.go
generated: { by: "opencode", at: "2026-11-03T16:50:44.301Z" }
verified:
  - by: openwiki/0.7.2
    at: 2026-11-04T03:17:38.088Z
---

# 内容目录 API

内容目录是产品的核心面。它横跨多个挂载路由组，共享 `episodes` 集合与一个公共缓存：`/episodes`、`/series`、`/follows`、`/favorites`、`/folders`、`/saved-folders`、`/histories` 与 `/ratings`。handler 直接调用 repository；只有认证与导出路径使用 service 层。

## 剧集目录与单集

剧集文档包含多语言标题与简介、封面、集数计数、状态（`ongoing`、`completed`、`upcoming`）、分类与标签、平台链接、首播日期、审核字段（`reviewStatus`、`reviewNote`、`reviewedBy`、`reviewedAt`）、待审核改动字段，以及创作者/编辑者引用。剧集引用的单集存放在独立的 `singleepisodes` 集合中，并在详情响应里内嵌。

可见性受审核门控：

- 公开列表与搜索只包含 `reviewStatus` 为 `approved` 或缺失（遗留文档早于该字段）的文档。
- `GET /episodes/:id` 使用 `OptionalAuth`。未审核剧集返回 404，除非调用者是创作者、授权编辑、自定义作者或管理员；有权查看者还会收到 `X-Robots-Tag: noindex, nofollow`。
- 追番、收藏与评分写入会以 403 拒绝未审核剧集，用户无法与未发布内容交互。

### 列表、搜索与缓存

`GET /episodes` 支持分类、标签、状态、年份（含 2016 年之前的特殊 `earlier` 桶）、最低评分、全文搜索、排序（`latest`、`views`、`premiere`、`rating`）与方向。分页是可选行为：只有同时提供 `page` 和 `limit` 时响应才包含分页字段；否则返回 `episodes` 与 `total`，默认上限 100 且没有页码上限。

响应缓存在进程级 LRU 缓存（`EpisodeCache`，TTL 五分钟，上限 200 条）中。缓存键拼接所有过滤条件以及 page/limit。任何剧集写入、观看递增、评分变化或审核决定都会删除单键 `episode_<id>` 与所有 `episodes_` 前缀键。

`GET /episodes/search` 是轻量端点，最多返回 50 条，按观看量排序，只返回 `_id`、标题、封面、分类和平均分。`search-suggestions` 返回标题和标签，`popular-tags` 聚合标签。

### 观看计数

`PUT /episodes/:id/view` 先确认剧集已审核通过才计数。随后按 `episodeId + clientIP` 应用内存冷却：十分钟内重复观看直接返回当前剧集而不递增。观看日志会择机清理，且是进程本地的，因此不会跨实例去重。

该路由注册了 `ratelimit.ViewSpec`，但端点限流只匹配字面挂载路径，而实际路径包含 `:id` 段，所以限流不会触发。十分钟的 handler 内冷却才是有效保护。

## 写入路径与审核交互

- `POST /episodes` 需要 creator、admin 或 superadmin。创作者提交会创建为 `reviewStatus=pending`；管理员与超管提交立即通过。
- `PUT /episodes/:id` 会先把编辑前文档快照写入 `episodeversions`。版本历史上限 50 条，超出时丢弃最旧记录。
- 创作者编辑已通过剧集时，改动存入 `pendingChanges`，线上文档继续提供旧内容，直到管理员批准。创作者编辑待审核或已拒绝剧集时，字段直接写入且状态回到 `pending`。管理员绕过待审核机制。
- `POST /episodes/:id/resubmit` 只对已拒绝剧集有效，会把审核字段重置为 pending。
- `DELETE /episodes/:id` 不做物理删除，而是以原因 `deleted` 把文档移入剧集回收站；只有回收站彻底删除才会清理关联数据。创作者只能删除自己创建或被授权管理的剧集，管理角色不受此限。
- 单集更新与删除同样校验归属：创作者只能操作其有权编辑的父剧集下的单集，越权返回 403。
- 创建与更新会校验资源地址：`coverImage` 必须是 `/uploads/` 或 `http(s)`，`platformLinks` 经过清洗，`qqGroupLink` 必须是 `http(s)`。

集数计数驱动通知：创建带 `available`、`preview`、`preview_video` 或 `preview_info` 事件的单集时，会为每个追番用户写入一条 `new_episode` 通知；直接编辑导致 `currentEpisodes` 增加时，会为每个新增集数通知追番用户。推送发送是空实现；邮件只发给邮箱已验证且保留 `episodeUpdate` 偏好的用户，并按剧集、集数、事件去重一小时。

## 系列

系列是剧集 ID 的有序数组加多语言名称与简介。列表与详情响应会把这些 ID 填充为剧集投影：列表投影包含标题、封面、计数、状态和评分；详情投影额外包含简介、分类、标签和观看量。系列分页默认每页 50 条（不是共享默认的 20），并钳制到 100。

创建要求名称和 creator 及以上角色。更新会校验归属：创作者只能修改自己创建的系列，管理员与超管可以修改任意系列；删除同样校验归属，创作者删除他人系列返回 403。删除系列只移除系列文档，引用的剧集不受影响。

## 追番

`/follows` 全部需要认证。

- `POST /add` 校验剧集存在且已通过，然后把 `followedAtEpisodes` 存为追番时的当前集数。提供 `folderId` 时必须存在且属于当前用户，否则 400。唯一索引冲突会变成 400 `Already following`。
- `POST /remove` 幂等。
- `GET /list` 使用共享 `pagination` 包分页，并关联剧集与收藏夹对象。按 `name` 或 `rating` 排序时最多加载 `page*limit + 100` 条并在内存排序，因为这些字段位于剧集文档上。
- `GET /check/:episodeId` 返回 `isFollowing`，没有追番时省略 `followedAtEpisodes`。

## 收藏

`/favorites` 与追番类似，但使用独立集合与响应契约。添加时校验剧集已通过，并在提供 `folderId` 时校验收藏夹属于调用者。重复收藏变成 400 `Already favorited`。`GET /counts` 返回聚合计数，`GET /check/:episodeId` 报告成员关系。

## 收藏夹与分享

收藏夹有类型（`follow` 或 `favorite`）并属于单个用户。API 支持列出、创建、重命名/更新、删除、重排、添加与移除条目，以及分享。

- `__unclassified__` 收藏夹是虚拟的：分享它会暴露 `folderId` 为 null 的追番或收藏。
- 分享会惰性生成随机 `shareToken`；撤销会清空它。
- `GET /folders/shared/:shareToken` 公开，返回收藏夹及填充后的剧集，并过滤掉审核状态为 pending 或 rejected 的剧集。
- `POST /folders/share-unclassified` 为未分类桶创建可分享表示。

`/saved-folders` 是独立概念，存储在 `savedfolders` 中：用户可以创建命名保存夹、列出并删除。它们不包含条目，是收藏夹定义的书签。

## 观看历史

`POST /histories/record` 按用户与剧集 upsert 历史文档。请求体需要可解析的 `episodeNumber` 和 `episodeId`；缺失标识返回 404，格式非法返回 500，两者都是有意的契约怪癖。upsert 会把观看集数合并进 `watchedEpisodes`，更新 `lastWatched`，并写入 `lastWatchedEpisodeNumber`。

读取端点为 `GET /continue-watching`（最近 10 条）和 `GET /list`（分页）。两者都关联剧集文档，剧集不存在时输出 `null`。删除单条历史移除一个剧集条目；删除根路径清空该用户全部历史。`histories` 集合在 `lastWatched` 上有 365 天 TTL 索引。

## 评分

`POST /ratings/` 接受 1 到 5 分，目标必须是已通过剧集。输入转换刻意模仿 JavaScript `Number()` 与 `parseInt` 语义，因此 `"4"` 会被接受，`"3abc"` 解析为 `3`。评分按用户与剧集 upsert，然后重算聚合平均分与人数，平均分四舍五入到一位小数，并写回剧集文档。剧集缓存随之失效。

`DELETE /ratings/:episodeId` 移除评分并重算相同聚合。`GET /ratings/check/:episodeId` 返回调用者当前分数。

## 横切行为

- 所有用户内容库端点都需要认证，并且只操作调用者自己的文档。
- 对象 ID 是 hex 字符串；路径或请求体中格式非法的 ID 通常表现为 500 `Server error`，与旧 API 一致。
- 目录变更会使共享剧集缓存失效；部分操作还会触发站内通知与邮件。
- 推送发送在该域中已定义但当前为空实现。

## 相关阅读

- [管理与审核 API](administration-and-moderation.md)——通过/拒绝与回收站语义。
- [上传、RSS、翻译、备份与分页](utilities-and-uploads.md)——封面上传与分页。
- [持久化与数据模型](../architecture/persistence-and-data-model.md)——集合布局与 ObjectID 处理。
