---
type: api-domain
title: 上传、RSS、翻译、备份与分页
description: 共享图片与 SVG 上传校验、静态上传服务、RSS 订阅、翻译提供方回退、整库备份导出/导入限制，以及通用分页契约。
tags: [api, uploads, rss, translation, backup, pagination]
sources:
  - id: openwiki-source-1649edbcafe0eab861fed46a
    resource: repo://internal/handler/backup.go
  - id: openwiki-source-12bf4f6da3684593b4c87988
    resource: repo://internal/handler/rss.go
  - id: openwiki-source-3a7c36409770ce04e5cdeef4
    resource: repo://internal/handler/translate.go
  - id: openwiki-source-dbaf2c53f5cefea8a8bb2ff3
    resource: repo://internal/middleware/bodyparse.go
  - id: openwiki-source-4b411986ff37b5fb340aff2a
    resource: repo://internal/pagination/paginate.go
  - id: openwiki-source-7106c8455bb73d12832c8523
    resource: repo://internal/server/app.go
  - id: openwiki-source-da80fdae2220fc6ad9a470b7
    resource: repo://internal/upload/svg.go
  - id: openwiki-source-62a9837f2b45c92ae81ca775
    resource: repo://internal/upload/upload.go
generated: { by: "opencode", at: "2026-11-03T16:50:44.301Z" }
verified:
  - by: openwiki/0.7.2
    at: 2026-11-03T16:50:44.301Z
---

# 上传、RSS、翻译、备份与分页

本页覆盖其他功能依赖的服务型领域：二进制存储、订阅生成、机器翻译、数据可移植性，以及大多数列表端点共用的分页辅助。

## 图片上传

所有图片上传都经过 `upload.SaveImage(c, field, prefix, maxBytes)`。校验顺序属于契约：

1. 请求必须是 `multipart/form-data`，否则返回 `ErrNoFile`。
2. 整个 multipart 请求体被 `http.MaxBytesReader` 包裹在 `maxBytes + 1 MB` 的粗限内。超出返回 `UploadError{Code: "LIMIT_FILE_SIZE"}`。
3. 文件自身大小精确对比 `maxBytes`，因此 multipart 开销不计入文件限制。
4. 小写扩展名必须完整匹配 `.jpg`/`.jpeg`/`.png`/`.gif`/`.webp`（锚定匹配，避免 `.notjpg` 绕过），声明的 `Content-Type` 必须匹配 `^image/(jpeg|png|gif|webp)$`。失败返回 `LIMIT_FILE_TYPE`。
5. 文件写入配置的上传目录，文件名为 `<prefix>-<32 位 hex><原扩展名>`。
6. 前 12 字节经 base64 编码后必须匹配已知魔数前缀；失败会删除文件并返回 `BAD_MAGIC`。

返回 URL 为 `/uploads/<文件名>`。`maxBytes <= 0` 时默认 5 MB。调用方选择不同前缀与限制，例如封面 5 MB、壁纸与主题资源 8 MB、站点内容图片 5 MB。

`RemoveFile` 只接受上传目录下单层路径段。空字符串、`.`、`..`、含分隔符的名称以及包含 `..` 的名称都会被拒绝，防止被污染的数据库值删除目录外文件。文件不存在视为成功。

### 错误渲染

handler 把 `UploadError` 代码翻译为固定用户消息：超限返回 400 与大小提示，类型不符返回 400 与允许图片提示，魔数不符返回 400 “文件内容与类型不匹配”。部分旧 handler 对类型不符刻意返回 500；这一不一致属于既有响应契约。

### 静态服务

`GET`/`HEAD /uploads/*filepath` 注册在 `/api` 组之外，因此不受全局限流或 API 统计影响。handler 规范化路径、拒绝 `..`、对目录与缺失文件返回 404，并对存在文件设置：

- `Cache-Control: public, max-age=604800, immutable`
- `X-Content-Type-Options: nosniff`
- `X-Robots-Tag: noindex`
- `Content-Disposition: inline`
- `Content-Security-Policy: default-src 'none'; img-src 'self' data:; style-src 'unsafe-inline'; sandbox`

只服务 `.jpg`、`.jpeg`、`.png`、`.gif`、`.webp`、`.svg` 六种扩展名，其他历史文件一律 404，避免上传目录中的 HTML/JS 被同源执行。沙箱 CSP 进一步保证直接打开 SVG 时脚本不会运行。

由于文件名是内容不可变的随机名，替换图片会产生新 URL，旧 URL 可以无限缓存。

## SVG 图标

SVG 上传走独立路径，因为它是文本且带脚本风险：

- 最大 1 MB，请求体额外放宽 1 MB 以容纳 multipart 开销。
- 扩展名必须为 `.svg`。
- MIME 可为 `image/svg+xml` 或几种为拖拽上传容忍的 XML/纯文本/二进制类型。
- 去掉 UTF-8 BOM 与首尾空白后，内容必须以 `<?xml` 或 `<svg` 开头。
- 使用 XML 解析器做结构化校验：必须是单一 `<svg>` 根元素；拒绝 `script`、`foreignObject`、`iframe`、`object`、`embed`、`link`、`style`、`animate`、`set`、`discard`；拒绝 `on*` 事件属性、`javascript:`/`vbscript:`/`data:`/`file:` 等危险 URL 与 `@import`；拒绝 `DOCTYPE`/`ENTITY` 及非 `xml` 处理指令以防 XXE。`href`/`src` 只允许锚点、站内相对路径或 `http(s)`。

文件写入 `IconsDir`（默认 `./uploads/icons`，可由配置覆盖），文件名为 `icon-<32 位 hex>.svg`，返回 URL 为 `/uploads/icons/<文件名>`。静态上传路由只服务上传目录下的文件，因此自定义图标目录若在外部，需要自行挂载静态服务。

## RSS

`GET /api/rss` 公开，返回通过字符串拼接（而非 XML 编码器）构建的 RSS 2.0 XML。订阅覆盖固定七天窗口，包含：

- 最近更新的剧集，标题带当前集数，链接到 `/episode/<id>`；
- 最近发布的单集，链接方式相同。

站点名与描述来自站点内容，缺失时回退默认值。标题与描述会做 XML 转义。`GET /api/rss/api-usage` 仅超管可访问，返回 API 调用统计。

## 翻译

`POST /api/translate` 与 `POST /api/translate/batch` 限流为每分钟 200 次，且刻意不要求认证。翻译流程分层：

1. `targetLang` 为 `zh` 时原样返回 key，并保留原始 JSON 类型。
2. 硬编码词典处理已知 UI 键，如 `twoFactor.*` 与 `report.*`；这些字符串是固定的。
3. 否则交给机器翻译：
   - 内存缓存上限 5000 条，TTL 24 小时；
   - 配置了 API key 时优先使用 Azure Translator；
   - 没有 Azure 时先试 MyMemory，再并行试 Google 与 Bing；
   - 结果通过合理性检查后才缓存。

超过 450 字符的文本会被切分。单请求 20 秒超时返回 504；批量请求 60 秒超时，端点上串行处理，内部非 Azure 路径使用 6 个并发 worker。不支持的目标语言返回 400。机器翻译为空时序列化为 `null`。

## 备份导出与导入

`/backup` 受双重保护：`Protect(creator, admin, superadmin)` 之后再内联要求 `superadmin`。因此两个端点对其他人一律 403。

导出遍历固定 14 个集合的白名单：`episodes`、`users`、`categories`、`banners`、`ratings`、`follows`、`favorites`、`histories`、`notifications`、`reports`、`sitecontents`、`singleepisodes`、`creatorprofiles`、`announcements`。单个集合失败会跳过而不中断整体导出。`users` 会脱敏：移除 `password`、`lastLoginIp`、`lastLoginRegion` 与 `deviceInfo`。响应是名为 `backup_<YYYY-MM-DD>.json` 的 JSON 附件。

导入接受 `{data, overwrite}` 并应用多项独立限制：

- 序列化数据最多 50 MB，按 UTF-16 码元计算，匹配旧实现的 JavaScript 字符串长度。
- 只处理白名单集合，其他集合报告为 `skipped: not allowed`。
- 每个集合在插入前按固定字段白名单清洗，白名单之外的字段会被丢弃。
- 每个集合最多 10000 个文档，超出会被跳过并提示。
- `overwrite` 为真时，每个集合在事务内整体替换（先删全部再无序插入）；否则追加。
- 结果按集合报告插入数或错误/跳过字符串，并写入一条 `数据恢复` 审计记录。

HTTP 请求体限制对 `/api/backup/import` 单独放宽到 50 MB，且只对 JSON 请求体生效，不对表单编码生效。

## 分页

`internal/pagination` 提供追番、收藏夹、反馈等列表端点共用的契约：

- `page` 默认 1，必须为正；
- `limit` 默认 20，钳制到 `[1, 100]`；
- `Skip()` 为 `(page-1) * limit`；
- 空结果时 `TotalPages` 返回 0；
- 响应使用 `Result{list, page, limit, total, totalPages}`。

若干领域刻意偏离，未经兼容性决策不应统一：系列默认每页 50，剧集列表只有同时提供两个参数才分页，通知把负 limit 转为 1，部分管理列表没有上限钳制。

## 相关阅读

- [请求生命周期与中间件管线](../architecture/request-lifecycle.md)——请求体大小限制与静态路由位置。
- [内容目录 API](content-catalog.md)——上传调用点。
- [部署与运行时](../operations/deployment-and-runtime.md)——上传与图标目录配置。
