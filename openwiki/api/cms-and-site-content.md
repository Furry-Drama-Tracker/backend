---
type: api-domain
title: CMS 与站点内容 API
description: 公告、壁纸、友情链接、主题、SVG 图标、站点内容键、创作者主页、动态流与反馈的建模、权限门控，与邮件和通知的联动，以及图标上传端点的停用状态。
tags: [api, cms, themes, icons, announcements, creator, feedback]
verified:
  - by: openwiki/0.7.2
    at: 2026-11-11T16:33:08.526Z
sources:
  - id: openwiki-source-b10e065a15408ca747b6184f
    resource: repo://internal/email/client.go
  - id: openwiki-source-85aef808e803b1323b13e891
    resource: repo://internal/handler/activity.go
  - id: openwiki-source-3527f438e382b053fb995986
    resource: repo://internal/handler/announcements.go
  - id: openwiki-source-8c579850c9dce2d1167ef9a0
    resource: repo://internal/handler/creatorprofile.go
  - id: openwiki-source-f89e324c07065d291ea97e16
    resource: repo://internal/handler/episodes.go
  - id: openwiki-source-c71d9937fb8fe9a87857856e
    resource: repo://internal/handler/feedback.go
  - id: openwiki-source-d96aab034efe83a94d9977ae
    resource: repo://internal/handler/friendlinks.go
  - id: openwiki-source-72eac37f3c5cb254f4c498e7
    resource: repo://internal/handler/icons.go
  - id: openwiki-source-2705037b16fa0486414860f8
    resource: repo://internal/handler/sitecontent.go
  - id: openwiki-source-15ee0d2d5ffabd3428b4285a
    resource: repo://internal/handler/sitestats.go
  - id: openwiki-source-eb2a947fd0238814b1b0335d
    resource: repo://internal/handler/themes.go
  - id: openwiki-source-35d6e9a38d44a46e10d5f6cf
    resource: repo://internal/handler/wallpapers.go
  - id: openwiki-source-0b6d7f0465da2820a1a264ef
    resource: repo://internal/model/theme.go
generated: { by: "zcode", at: "2026-11-11T16:33:08.526Z" }
---

# CMS 与站点内容 API

这些域共享 `cms_helpers.go` 工具，并遵循同一形态：公开读取面、登录用户自有资源，以及超管或管理员管理端点。所有路由都通过 `router.Mount` 挂在 `/api` 下（单前缀，无 v1 镜像），并且每个请求都先经过全局中间件管线。

## 站点内容

`/site-content` 是 `sitecontents` 集合之上的键值存储。默认键顺序为 `privacy`、`terms`、`about`、`settings`，`List` 会惰性创建缺失的默认文档，因此后台总能看到四个键。

- `GET /:key` 公开，但 `key=email` 例外：它由内联超管校验保护。`email` 键由运行时配置合成而非存储：host、port、user、from name 和 `enabled` 标志，密码始终为空。
- `PUT /:key` 仅超管可用，并直接拒绝 `email`，因为 SMTP 凭据通过 ini 或环境变量配置，而不是数据库。更新 `about` 与 `settings` 时 `content` 必须是合法 JSON。
- `GET /`（管理员）返回全部键，缺失时按需创建默认值。
- `POST /upload` 接收最大 5 MB 的图片并返回 `/uploads/...` URL。
- `POST /test-email` 使用请求体提供的凭据发送测试邮件。
- `GET /pwa-manifest` 公开，根据 `settings` 内容 JSON 推导 Web App Manifest（名称、短名、图标、颜色）。

读取 `privacy`、`terms`、`about`、`settings` 时，如果存储内容不是合法 JSON 而 HTML 反转义后合法，会返回反转义结果以兼容旧版全局 HTML 转义造成的损坏；该修复只在读取时生效，不写回数据库。`email` 键响应带 `Cache-Control: no-store`。

`settings` 文档还支撑事务邮件的品牌信息：邮件客户端从 `settings` 读取站点名与导航 Logo，从 `about` 读取 ICP、公安备案与版本号，两者缓存五分钟。这是 CMS 内容与出站邮件之间的主要耦合。

## 公告

公告由超管管理，所有路由（包括读）都附加 `RequireEmailChanged`。公开面是 `GET /active`（可按 `popup` 或 `banner` 渠道过滤，由 repository 限制条数）和 `GET /:id`。

创建接受四种固定类型（`info`、`warning`、`maintenance`、`update`），其他类型会落到通用 500 路径。`link` 为空或必须是 `http`/`https`，否则返回 400。新建公告在 active 且 `publishAt` 不在未来时会立即广播。更新时，如果 `active`、`sendNotification` 或 `sendEmail` 从 false 变为 true，会先重置对应的已发送标志再重新广播，避免被去重逻辑压制。删除公告时也会删除引用它的通知中心条目。

广播有两条独立通道：

- 通知按每批 1000 条通过 `insertMany` 写给所有用户。
- 邮件只发给邮箱已验证且未关闭公告偏好的用户，每批 50 封，并统计成功数。

手动 `POST /:id/send-email` 与 `POST /:id/send-notification` 要求对应开关已启用，并持久化 `emailSent`/`emailSentAt`/`emailSentCount` 或 `notificationSent`。

## 壁纸

壁纸分为两个互不相关的存储：

- **系统壁纸**位于 `systemwallpapers` 集合。公开列表返回启用的条目；`/system/all` 向超管暴露全部。上传接受最大 8 MB 图片，`enabled` 除非表单值恰好为 `"false"` 否则为 true，`sortOrder` 使用类似 JavaScript `parseInt` 的语义解析。删除时同时移除磁盘文件。
- **个人壁纸**内嵌在用户文档的 `personalWallpapers` 数组中。上传为 8 MB 图片，每位用户最多 20 张；超出上限会删除刚写入的文件并返回 400。按 URL 删除时同时移除数组项与文件。

## 友情链接

公开 `GET /` 与超管 `GET /all` 只在过滤上不同。`POST /apply` 使用 `OptionalAuth`：匿名访客可以申请，登录申请人会被记录。Altcha 始终必需（开发 token 照常可绕过），站点 URL 与 Logo 必须是 `http`/`https`。

申请创建为 `status=pending`、`isActive=false`。成功后 handler 会向所有超管发送站内通知与邮件。审批通过超管 CRUD 端点（`POST /`、`PUT /:id`、`DELETE /:id`）完成，没有单独的 approve 端点。`GET /my-applications` 让登录申请人跟踪自己的提交。

## 主题

主题是壁纸和/或图标映射组成的外观包，不包含按钮配色。`Theme.ThemeType()` 根据存在的部分推导 `full`、`wallpaper`、`icons` 或 `legacy`。

主题有两类归属和一套状态生命周期：

- 系统主题（`isSystem=true`）对所有人可见，由 `enabled` 控制。
- 个人主题属于单个用户，默认 `draft`，可以提交审核进入 `pending`。通过后 `isSystem=true`、`status=approved`、`enabled=true`；拒绝则保持个人主题，`status=rejected` 并带审核备注。超管也可以通过管理更新端点直接转换，个人转系统会被视为已通过。
- 站点默认主题只能有一个。`SetDefault` 要求是启用的系统主题，清除其他主题的默认标记，并允许完全取消默认（站点可以没有默认主题）。

用户选择采用双槽：`themeWallpaperId` 与 `themeIconsId` 相互独立，因此可以组合主题 A 的壁纸与主题 B 的图标。`GET /my/selection` 在槽未设置时回退到站点默认主题，报告该槽是否为默认兜底，并把遗留的单 `themeId` 与 `applyIcons`/`applyWallpaper` 标志迁移到两个槽，同时清除失效引用。`PUT /selection` 接受双槽请求体或遗留单主题请求体；修改壁纸槽时还会同步用户背景偏好，使选择在多端生效。

主题资源必须是站内 `/uploads/` 路径或 `http(s)` URL，图标键必须匹配组件键正则，最多 64 个图标映射，`misc.logo` 不允许被主题覆盖。壁纸与图标上传上限为 8 MB。

**图标上传已停用（主题只保留壁纸）：** `POST /api/themes/upload-icon` 的路由注册被注释掉，端点对客户端表现为不存在，上传目录不再被写入。壁纸上传（`POST /api/themes/upload-wallpaper`）、主题选择（`GET /my/selection`、`PUT /selection`）与既有图标主题的读取（主题 JSON 的 `icons` 字段、`GET /themes/list`）全部保持原样，线上已有图标主题不受影响。恢复时取消 `Themes.Register` 中对应那一行注释即可。

## SVG 图标

`/icons` 管理存储在可配置 `IconsDir`（默认 `./uploads/icons`）下的 SVG 文件。公开 `GET /list` 返回启用图标以及从组件键到 URL 的扁平 `mappings` 对象，供前端图标引擎消费。

**图标包上传已停用：** `POST /api/icons/upload` 的路由注册被注释掉（与主题图标上传同一次决策——主题只保留壁纸），端点对客户端表现为不存在，`IconsDir` 不再被新上传写入。读取链路（`GET /icons/list`、`GET /icons/all`）与管理端点（`PUT /:id`、`DELETE /:id`，仍要求超管与 `RequireEmailChanged`）照常挂载，线上既有图标文件与图标映射不受影响。恢复时取消 `Icons.Register` 中对应那一行注释即可。

上传 handler 本身保留（恢复即用）：仅超管可用，接受 `files`（多文件）或 `file`（单文件），单次最多 50 个文件，分类默认 `general`。每个文件走 SVG 专用保存路径，单文件失败会进入 `failed` 数组而不使整批失败。更新会校验名称长度、分类长度、描述长度、映射与启用状态。删除会移除文档及其文件。

## 创作者主页与创作中心

`/creator-profile` 区分公开与私有视图：

- `GET /my-profile` 对 `creator`、`admin`、`superadmin` 开放；首次访问会根据用户名创建默认主页。
- `PUT /my-profile` 从不修改线上字段。它写入 `pendingChanges` 并设置 `reviewStatus=pending`，在超管批准前公开主页保持不变。简介截断为 500 个字符。头像必须是 `/uploads/` 站内路径或 `http(s)` URL，`socialLinks` 会经过清洗与数量/长度校验，`qqGroupLink` 必须是 `http(s)`。
- `GET /public/:id` 与 `GET /by-creator/:creatorId` 公开，只返回已通过的字段以及该创作者的已审核剧集；待审核改动、审核状态和审核备注会被剥离。

`/creator` 工作台暴露 `GET /my-episodes`（用户作为创作者、授权编辑或自定义作者参与的剧集）与 `GET /editable`（用户可编辑的已审核剧集），两者都要求创作者角色。

## 动态流

没有 `activity` 集合。`GET /activity` 为登录用户构建个性化动态，聚合最近 30 天内其追番剧集的：

1. 窗口内发布的新单集；
2. 最近更新的已追剧集；
3. 已追剧集的高分评分（`score >= 4`，最多 50 条）。

条目按日期排序并在内存中分页。新单集条目的集数称呼经 `episodeRefText` 解析——单集自定义显示序号非空时直接用它（英文描述同样用 label，否则 `Ep.N`），否则回退「第N集」；label 非空时还会写入条目 `metadata.episodeLabel`。`GET /activity/public` 无需认证，改为聚合最近 30 个已发布单集、平均分至少 4 的前 10 个已审核剧集，以及最近更新的 10 个已审核剧集，固定最多返回 20 条。

## 反馈

任何登录用户都可以提交反馈，类型为 `suggestion`、`bug`、`complaint` 或 `other`（空值默认 `suggestion`），内容最多 2000 字符。提交按端点半限流为每小时 5 次。

用户可以列出自己的反馈；管理员可以列出全部反馈并更新状态（`pending`、`read`、`replied`）以及最多 1000 字符的回复。保存非空回复会创建带 50 字符预览的 `feedback_reply` 通知，并在用户邮箱已验证且开启 `feedbackReply` 偏好时异步发送邮件。管理员列表的 `limit` 没有上限钳制，这一点与大多数分页端点不同。

## 站点统计

`GET /api/site-stats` 是新增的公开端点，返回首页所需的规模数据：`users`（用户总数）、`episodes`（已审核剧集数）与 `online`（最近 10 分钟活跃会话数）。响应带 15 秒进程内缓存与 `Cache-Control: public, max-age=15`；在线人数查询失败时降级为 0，用户数与剧集数查询失败则返回 500。响应只含聚合计数，不含任何用户标识。

## 横切注意点

- 对于 Gin 默认尾斜杠重定向会破坏契约的公开端点，会有意同时注册 `""` 与 `"/"`。
- 上传 handler 返回本地化错误消息，并依赖 `internal/upload` 做扩展名、MIME、魔数与大小校验。
- 超管写端点（站点内容、公告、壁纸、友链、主题壁纸上传）普遍附加 `RequireEmailChanged` 与上传/写入限流；图标包与主题图标两类上传端点当前已停用（路由注册被注释）。个人壁纸与主题文件删除只允许清理带对应前缀（`wallpaper-`、`theme-wallpaper-`）且未被引用的文件，避免通过 URL 删除任意上传文件。
- 多个 handler 带有 fire-and-forget 的通知与邮件副作用；这些失败不会回滚主写入。

## 相关阅读

- [上传、RSS、翻译、备份与分页](utilities-and-uploads.md)——共享上传实现与落盘安全约束。
- [邮件发送](../integrations/email-delivery.md)——模板辅助函数与按收件人限流。
- [外部服务](../integrations/external-services.md)——友链申请使用的 Altcha 校验。
