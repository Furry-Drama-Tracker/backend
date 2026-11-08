# API 参考（手写可读版）

本文件是**全部 `/api` 端点的纯文本参考**，与 `/api/docs`（swagger，机器可查）互补：这里可直接 grep、复制、离线读。所有端点只在 `/api` 下注册（v1 镜像已移除）。

> **与 swagger 的关系**：`docs/swagger.json` 由 swaggo 注解生成，覆盖可能滞后（如 `/themes`、`/icons` 域尚未入库）。**本文以路由源码为准**（`internal/server/register_routes.go` + 各 handler 的 `Register()`）。改动端点时请同步更新本文与 swagger 注解。另：本文旧版记载的 `/users/background-upload`、`/users/background-prefs` 已下线，由 `/api/wallpapers/personal` 与 `PUT /api/themes/background-prefs` 接管。

## 认证与前置

- **CSRF**：所有非 GET 请求需带 `X-XSRF-TOKEN` header，且与 `XSRF-TOKEN` cookie 值相等（先 `GET /api/csrf-token` 获取两者）。CSRF 三态失败 → 403。
- **鉴权**：登录后 access token 在 httpOnly cookie `accessToken` 中自动携带；也可用 `Authorization: Bearer <token>`。
- **角色层级**：`user < creator < admin < superadmin`。`Protect(角色...)` 不满足 → 403。部分超管变更路由附加 `RequireEmailChanged`：默认超管邮箱（`admin@furry09.com`）未改邮箱前发非 GET 一律 403（刚初始化的超管可看不可改）。
- **dev-token 绕过**（仅开发）：header `x-dev-token` 等于 `DEV_API_TOKEN` 时绕过 altcha/邮箱/设备验证；`SKIP_RATE_LIMIT=1` 跳过登录限流。
- **全局限流**：`/api/*` 统一 300/min（`/api/translate` 与 `/api/auth/captcha` 豁免全局、走各自端点限流）；端点限流按字面挂载路径匹配。超限 429。

## 其他前置端点

| 方法 | 路径 | 鉴权 | 限流 | 请求要点 | 响应要点 |
|---|---|---|---|---|---|
| GET | `/csrf-token` | 公开（豁免 CSRF/限流） | — | — | 200；设置 `XSRF-TOKEN` cookie |
| GET | `/health` | 公开 | 全局限流内 | — | 200 `{status:"ok",timestamp,db:"connected"}`；DB 不通 503 |
| GET/HEAD | `/uploads/*filepath` | 公开 | 不在 `/api` 组（无限流/统计） | — | 仅 `.jpg/.jpeg/.png/.gif/.webp/.svg` 六种扩展名，其余 404；`Cache-Control: immutable` 7 天 + 沙箱 CSP |

## Cookie

| 名称 | httpOnly | Path | 有效期 | SameSite | 说明 |
|---|---|---|---|---|---|
| `accessToken` | 是 | `/` | 15min | lax | access JWT |
| `refreshToken` | 是 | `/api/auth` | 7d | lax | refresh JWT（轮换） |
| `token` | 是 | `/` | 30d | lax | 兼容旧客户端 |
| `XSRF-TOKEN` | 否 | `/` | 24h | strict | CSRF 双拷贝 |

## 认证端点 `/api/auth`

| 方法 | 路径 | 鉴权 | 限流 | 请求要点 | 响应要点 |
|---|---|---|---|---|---|
| GET | `/auth/captcha` | 公开 | captcha 60/min | — | 200 `{parameters:{algorithm,cost,expiresAt,keyLength,keyPrefix,nonce,salt},signature}`（altcha 挑战） |
| GET | `/auth/check-accountId?accountId=` | 公开 | 20/min | query accountId | 200 `{available:bool}` |
| POST | `/auth/register` | 公开 | 3/h | `{accountId,username,email,password,deviceInfo?,altcha}`；accountId 3-20 `[a-zA-Z0-9_]`，密码≥8含字母数字 | 200 `{message,email,needVerification:true}`；400 各类校验文案 |
| POST | `/auth/login` | 公开 | 5/15min | `{email,password,deviceInfo?,altcha}` | 200 用户对象 或 `{need2FA,email,twoFactorChallenge}`；403 `{needVerification}` / `{needDeviceVerify,deviceInfo}` |
| POST | `/auth/logout` | 登录 | — | — | 200 `{message:"退出成功"}` |
| POST | `/auth/refresh` | refresh cookie | 5/15min | 需 `refreshToken` cookie | 200 用户对象（轮换新 cookie）；401/409 |
| GET | `/auth/me` | 登录 | — | — | 200 `{_id,accountId,username,email,isEmailVerified,role,avatar,emailNotificationPrefs,backgroundPrefs,personalWallpapers}` |
| GET | `/auth/sse-ticket` | 登录 | — | — | 200 `{ticket}`（30s JWT，SSE 用） |
| POST | `/auth/verify-device` | 公开 | 5/15min | `{token}`（device-verify JWT） | 200 `{verified:true,loginCode}`；400 各类 |
| POST | `/auth/confirm-device-login` | 公开 | 5/15min | `{loginCode}`（6 位） | 200 用户对象 或 `{need2FA,email,twoFactorChallenge}`；400 |
| POST | `/auth/login-2fa` | 公开 | 5/15min | `{email,twoFactorToken,twoFactorChallenge,deviceInfo?}` | 200 用户对象；400/403/423 |
| PUT | `/auth/change-password` | 登录 | 3/h | `{currentPassword,newPassword}` | 200 `{message:"密码修改成功"}`（吊销全部会话+清cookie） |
| POST | `/auth/forgot-password` | 公开 | 3/h | `{email,altcha}` | 200 统一文案（不泄露账号存在性） |
| POST | `/auth/reset-password` | 公开 | 3/h | `{token,newPassword}` | 200 `{message:"密码重置成功"}`；token 一次性（UsedToken） |
| PUT | `/auth/change-email` | 超管 | 3/h | `{newEmail,password}` | 200 `{message,email,isEmailVerified:false,forceEmailChange:false}` |
| PUT | `/auth/email-notification-prefs` | 登录 | — | 7 布尔键（episodeUpdate/newDeviceLogin/feedbackReply/friendLinkStatus/friendLinkApply/announcement/reviewResult） | 200 `{message,emailNotificationPrefs}` |
| POST | `/auth/verify-email` | 公开 | 5/h | `{code,email?}`（6 位验证码） | 200 `{message:"邮箱验证成功"}`；码一次性、5 次尝试上限 |
| POST | `/auth/resend-verification` | 登录 | 5/h | — | 200 `{message}`（未配置 SMTP 时提示） |
| POST | `/auth/resend-verification-by-email` | 公开 | 5/h | `{email,altcha}` | 200 统一文案 |
| POST | `/auth/request-email-change` | 登录 | 3/h | `{password,newEmail,altcha}` | 200 `{message:"验证邮件已发送到新邮箱"}`；锁定感知（423） |
| POST | `/auth/verify-email-change` | 公开 | — | `{token}`（email-change JWT） | 200 `{message:"邮箱修改成功，请重新登录",email}`；一次性 |
| POST | `/auth/request-deletion` | 登录 | — | — | 200 `{message,deletionRequestedAt,deleteAt}`（7 天宽限） |
| POST | `/auth/cancel-deletion` | 登录 | — | — | 200 `{message:"注销申请已取消"}` |
| GET | `/auth/deletion-status` | 登录 | — | — | 200 `{requested:false}` 或 `{requested:true,deletionRequestedAt,deleteAt}` |

## 用户会话 `/api/user-sessions`

| 方法 | 路径 | 鉴权 | 请求要点 | 响应要点 |
|---|---|---|---|---|
| POST | `/user-sessions/create` | 登录 | `{screenWidth,screenHeight,language}` | 200 `{sessionId}`（按 access token hash upsert） |
| GET | `/user-sessions/my` | 登录 | — | 200 会话数组（含 `isCurrent`） |
| PUT | `/user-sessions/:id/name` | 登录 | `{deviceName}`（≤50，本人） | 200 `{message}` |
| DELETE | `/user-sessions/:id` | 登录 | 不能下线当前设备 | 200 `{message}` |
| DELETE | `/user-sessions/my/all` | 登录 | — | 200 `{message:"已下线其他所有设备"}` |
| POST | `/user-sessions/heartbeat` | 登录 | — | 200 `{ok:true}` |
| GET | `/user-sessions/all` | 超管 | limit 200 | 200 会话数组（含 username/userRole） |
| DELETE | `/user-sessions/admin/:id` | 超管 | 附加 RequireEmailChanged | 200 |
| DELETE | `/user-sessions/admin/user/:userId/all` | 超管 | 附加 RequireEmailChanged | 200 |

## 2FA `/api/2fa`（全部走 twoFactor 限流 5/15min）

| 方法 | 路径 | 鉴权 | 请求要点 | 响应要点 |
|---|---|---|---|---|
| POST | `/2fa/enable` | 登录 | — | 200 `{secret,backupCodes,otpauthUrl}`（secret 为 base64，AES 加密存储） |
| POST | `/2fa/verify-enable` | 登录 | `{token}`（TOTP） | 200 `{message:"2FA enabled successfully"}` |
| POST | `/2fa/disable` | 登录 | `{token}`（TOTP 或备份码） | 200 `{message:"2FA disabled successfully"}` |
| POST | `/2fa/verify` | 登录 | `{token}` | 200 `{verified:true}` |

## 用户 `/api/users`

| 方法 | 路径 | 鉴权 | 限流 | 请求要点 | 响应要点 |
|---|---|---|---|---|---|
| POST | `/users/avatar` | 登录 | upload 20/h | multipart `avatar`（≤2MB，图片） | 200 `{url:"/uploads/avatar-..."}` |
| PUT | `/users/profile` | 登录 | — | `{username}`（≤20） | 200 用户对象 |
| GET | `/users/export-my-data?format=json\|csv` | 登录 | 3/h | 下载文件（json 或 csv 含 BOM） | 文件下载 |

> 原 `/users/background-upload`、`/users/background-prefs` 已下线：个人壁纸移至 `/api/wallpapers/personal`，背景偏好移至 `PUT /api/themes/background-prefs`。

## 剧集目录 `/api/episodes`

公开列表/搜索只含 `reviewStatus=approved`（或字段缺失的遗留文档）。详情用 `OptionalAuth`：未审核剧集对非创作者/授权编辑/自定义作者/管理员返回 404，有权查看者响应带 `X-Robots-Tag: noindex, nofollow`。

| 方法 | 路径 | 鉴权 | 请求要点 | 响应要点 |
|---|---|---|---|---|
| GET | `/episodes` | 公开 | query：category/tag/status/year（含 `earlier` 桶）/minRating/search/sort（latest\|views\|premiere\|rating）/order；**同时给 page+limit 才分页**，否则默认上限 100 | 200 `{episodes,total}`（可选分页字段）；LRU 缓存 5min |
| GET | `/episodes/search?q=&limit=` | 公开 | q 必填 | 最多 50 条按观看量排序；仅 `_id`/标题/封面/分类/平均分 |
| GET | `/episodes/search-suggestions?q=` | 公开 | q 必填 | 标题与标签 |
| GET | `/episodes/popular-tags` | 公开 | — | 标签聚合 Top20 |
| GET | `/episodes/:id` | 可选登录 | — | 详情 + 内嵌单集数组；`episodeLabel` 已解析（空回退「第N集」），`episodeLabelCustom` 保留原始值 |
| GET | `/episodes/:id/user-status` | 登录 | — | 200 `{isFollowing,followedAtEpisodes,watchedEpisodes,score,isFavorite}` |
| PUT | `/episodes/:id/view` | 公开 | — | 观看+1；需 approved；同 IP 10min 冷却（注册的 ViewSpec 限流因路径含 `:id` 实际不触发） |
| PUT | `/episodes/single/:id/view` | 公开 | — | 单集观看+1（同上冷却） |
| POST | `/episodes` | creator+ | 多语言标题/简介/封面（`/uploads/` 或 http(s)）/状态/platformLinks/qqGroupLink | 200 剧集；创作者→pending，管理员→直接 approved |
| POST | `/episodes/upload` | creator+（upload 20/h） | multipart `image`（≤5MB） | 200 `{url:"/uploads/cover-..."}` |
| POST | `/episodes/:id/episodes` | creator+ | 单集字段（`episodeNumber` 为排序/计数依据；`episodeLabel` 仅展示） | 创建 available/preview 事件单集会给追番用户写 `new_episode` 通知 |
| PUT | `/episodes/:id` | creator+ | 同创建 | 编辑前快照写入 `episodeversions`（上限 50 条）；创作者改 approved 剧集→进 `pendingChanges`，改 pending/rejected→直接写回并回 pending |
| POST | `/episodes/:id/resubmit` | creator+ | — | 仅 rejected 有效，重置审核字段为 pending |
| PUT | `/episodes/single/:id` | creator+ | 单集字段 | 归属校验，越权 403 |
| DELETE | `/episodes/single/:id` | creator+ | — | 删除单集（归属校验） |
| DELETE | `/episodes/:id` | creator+ | — | **非物理删除**：以原因 `deleted` 移入剧集回收站 |

## 系列 `/api/series`

| 方法 | 路径 | 鉴权 | 请求要点 | 响应要点 |
|---|---|---|---|---|
| GET | `/series` | 公开 | page/limit（默认 50/页，钳 100——非共享默认 20） | 列表；剧集 ID 填充为列表投影（标题/封面/计数/状态/评分） |
| GET | `/series/:id` | 公开 | — | 详情投影额外含简介/分类/标签/观看量 |
| POST | `/series` | creator+ | `{name,episodes[],description?}` | 200 |
| PUT | `/series/:id` | creator+ | 同上 | 归属校验：创作者只能改自己创建的，他人 403 |
| DELETE | `/series/:id` | creator+ | — | 只删系列文档，引用的剧集不受影响 |

## 追番 `/api/follows` 与收藏 `/api/favorites`（全部登录）

| 方法 | 路径 | 请求要点 | 响应要点 |
|---|---|---|---|
| POST | `/follows/add` | `{episodeId,folderId?}`；剧集须已通过（未审核 403）；folderId 须属于自己否则 400 | 重复 400 `Already following`；记录 `followedAtEpisodes` |
| POST | `/follows/remove` | `{episodeId}` | 幂等 |
| GET | `/follows/list` | page/limit/folderId/sort | 共享分页；按 name/rating 排序时内存排序（最多 `page*limit+100` 条） |
| GET | `/follows/check/:episodeId` | — | `{isFollowing}`；无追番时省略 `followedAtEpisodes` |
| POST | `/favorites/add` | `{episodeId,folderId?}`（同上校验） | 重复 400 `Already favorited` |
| POST | `/favorites/remove` | `{episodeId}` | 幂等 |
| GET | `/favorites/list` | page/limit/folderId/sort | 分页 |
| GET | `/favorites/counts` | — | 各收藏夹收藏计数 |
| GET | `/favorites/check/:episodeId` | — | `{isFavorite}` |

## 收藏夹 `/api/folders` 与保存夹 `/api/saved-folders`（除分享视图外全部登录）

收藏夹有类型 `follow`/`favorite`；`__unclassified__` 为虚拟桶（分享它会暴露 `folderId=null` 的条目）。

| 方法 | 路径 | 请求要点 | 响应要点 |
|---|---|---|---|
| GET | `/folders` | query `type` 可选 | 我的收藏夹列表 |
| POST | `/folders` | `{name,type}` | 创建 |
| PUT | `/folders/reorder` | `{folderIds[]}` | 重排 |
| POST | `/folders/share-unclassified` | — | 为未分类桶生成可分享表示 |
| PUT | `/folders/:id` | `{name?,description?}` | 重命名/改描述 |
| DELETE | `/folders/:id` | — | 删除 |
| POST | `/folders/:id/items` | `{episodeId}` | 添加条目 |
| DELETE | `/folders/:id/items/:episodeId` | — | 移除条目 |
| POST | `/folders/:id/share` | — | 惰性生成随机 `shareToken` |
| DELETE | `/folders/:id/share` | — | 撤销分享（清空 token） |
| GET | `/folders/shared/:shareToken` | **公开** | 收藏夹+填充剧集；过滤 pending/rejected 剧集 |
| GET | `/saved-folders` | — | 我收藏的他人收藏夹列表 |
| POST | `/saved-folders` | `{shareToken,creatorName}` | 收藏他人分享的收藏夹（书签，不含条目） |
| DELETE | `/saved-folders/:id` | — | 取消收藏 |

## 观看历史 `/api/histories` 与评分 `/api/ratings`（全部登录）

| 方法 | 路径 | 请求要点 | 响应要点 |
|---|---|---|---|
| POST | `/histories/record` | `{episodeId,episodeNumber,...}` | 按用户+剧集 upsert，合并 `watchedEpisodes`；**缺失标识 404、格式非法 500（有意契约）** |
| GET | `/histories/continue-watching` | — | 最近 10 条（关联剧集，不存在输出 `null`） |
| GET | `/histories/list` | page/limit | 分页 |
| GET | `/histories/check/:episodeId` | — | 是否看过 |
| DELETE | `/histories/:episodeId` | — | 删单条 |
| DELETE | `/histories`（`/` 同） | — | 清空全部历史（双注册固定契约）；集合有 365 天 TTL |
| POST | `/ratings/` | `{episodeId,score}` 1-5；目标须 approved（未审核 403） | upsert 后重算剧集平均分（一位小数）与人数；输入转换模仿 JS `Number()` 语义（`"3abc"`→3） |
| DELETE | `/ratings/:episodeId` | — | 移除评分并重算 |
| GET | `/ratings/check/:episodeId` | — | `{score}` |

## 创作中心 `/api/creator` 与创作者主页 `/api/creator-profile`

| 方法 | 路径 | 鉴权 | 请求要点 | 响应要点 |
|---|---|---|---|---|
| GET | `/creator/my-episodes` | creator+ | page/limit | 我作为创作者/授权编辑/自定义作者参与的剧集 |
| GET | `/creator/editable` | creator+ | page/limit | 可编辑的已审核剧集 |
| GET | `/creator-profile/my-profile` | creator+ | — | 首次访问按用户名自动创建默认主页 |
| PUT | `/creator-profile/my-profile` | creator+ | 主页字段；简介截断 500 字符；头像须 `/uploads/` 或 http(s) | **从不改线上字段**：写 `pendingChanges` + `reviewStatus=pending` |
| GET | `/creator-profile/public/:id` | 公开 | — | 仅已通过字段+该创作者已审核剧集（剥离待审核块） |
| GET | `/creator-profile/by-creator/:creatorId` | 公开 | — | 同上 |

## 审核 `/api/review` 与回收站 `/api/episode-trash`

路由先 `Protect(creator+)` 再内联收窄为 **admin/superadmin**；写路由附加 `RequireEmailChanged`。

| 方法 | 路径 | 请求要点 | 响应要点 |
|---|---|---|---|
| GET | `/review/pending` | page/limit | 待审核剧集 + 已审核剧集的待审核修改 |
| GET | `/review/all` | page/limit | 全部剧集 |
| PUT | `/review/approve/:id` | — | 有 pendingChanges→应用并清空；新提交→原子置 approved；集数增加会广播 `new_episode` 通知+更新邮件（按剧集/集数/事件去重 1h） |
| PUT | `/review/reject/:id` | 备注可选 | 已审核剧集：丢弃 pendingChanges 线上不变；新提交：移入回收站（原因 `rejected`） |
| PUT | `/review/assign-editor/:episodeId` | `{editorId}` | 分配剧集编辑者 |
| PUT | `/review/remove-editor/:episodeId` | — | 移除编辑者 |
| GET | `/episode-trash` | page/limit | 回收站列表 |
| GET | `/episode-trash/:id/versions` | — | 回收站剧集的版本快照日志 |
| PUT | `/episode-trash/:id/restore` | — | 用原 `_id` 恢复到 `episodes`（版本历史保持有效；删副本失败回滚） |
| DELETE | `/episode-trash/:id` | — | **彻底删除**：级联清理单集/版本/追番/收藏/历史/评分/通知 |

## 自动任务 `/api/auto-status`（superadmin + RequireEmailChanged）

手动维护触发器（旧系统为后台任务，现无调度器调用），均返回被修改的文档数。

| 方法 | 路径 | 请求要点 | 响应要点 |
|---|---|---|---|
| POST | `/auto-status/auto-complete` | — | 扫描连载中剧集：`currentEpisodes>0` 且 ≥ `totalEpisodes` 时置 `status=completed` |
| POST | `/auto-status/check-premieres` | — | 发布到期单集：`isUpcoming=false`、`releaseDate=premiereDate` |

## 版本历史 `/api/versions`（creator+）

| 方法 | 路径 | 请求要点 | 响应要点 |
|---|---|---|---|
| GET | `/versions/:episodeId` | page/limit（默认 1/20，无钳制） | 版本列表；**limit=0 时 totalPages 序列化为 null（怪癖）** |
| GET | `/versions/:episodeId/:version` | — | 单版本快照 |
| GET | `/versions/:episodeId/diff/:v1/:v2` | — | 差异按固定 DTO 字段顺序（客户端依赖该顺序） |
| POST | `/versions/:episodeId/rollback/:version` | — | 回滚到指定版本 |

## 管理后台 `/api/admin`

后台无独立登录：管理员走公开 `POST /api/auth/login`（Altcha/锁定/邮箱/设备/2FA 全生效），再 `GET /admin/verify` 校验。写路由均附加 `RequireEmailChanged`。

| 方法 | 路径 | 鉴权 | 请求要点 | 响应要点 |
|---|---|---|---|---|
| GET | `/admin/verify` | creator+ | — | 后台权限校验+管理员信息 |
| GET | `/admin/pending-counts` | creator+ | — | 待办汇总（剧集/举报/反馈/友链/主题） |
| POST | `/admin/logout` | creator+ | — | 同前台登出 |
| POST | `/admin/verify-password` | creator+（3/h） | `{password}` | 校验密码 |
| GET | `/admin/creators` | admin+ | — | 创作者列表 |
| GET | `/admin/list` | 超管 | page/limit | 管理/创作者账户列表 |
| POST | `/admin/register` | 超管 | `{role:admin\|creator,...}`；共享密码强度规则 | 创建账户（邮箱视为已验证；creator 自动建主页、发邮件） |
| DELETE | `/admin/:id` | 超管 | — | 删管理/创作者账户（best-effort 级联）；拒绝删自己/最后超管 |
| GET | `/admin/users` | 超管 | page/limit/search | 用户列表 |
| DELETE | `/admin/users/:id` | 超管 | — | 删用户（**严格级联**：追番/历史/通知/收藏/收藏夹/反馈/会话/推送/举报/评分+重算剧集评分；任一步失败 500） |
| PUT | `/admin/role/:id` | 超管 | `{role:user\|creator\|admin\|superadmin}` | 拒绝改自己/降级最后超管；转 creator 自动建主页 |
| PUT | `/admin/user-admin-access/:id` | 超管 | — | user↔admin 切换（拒操作 superadmin/creator） |
| GET | `/admin/creator-profiles` | 超管 | — | 创作者主页列表 |
| GET | `/admin/creator-profiles/:id` | 超管 | — | 主页详情 |
| PUT | `/admin/creator-profiles/:id` | 超管 | 主页字段 | 直接编辑=终态（清 pendingChanges + approved） |
| PUT | `/admin/creator-profiles/:id/approve` | 超管 | — | 应用 pendingChanges + `profile_review` 通知 |
| PUT | `/admin/creator-profiles/:id/reject` | 超管 | 备注可选 | 驳回，保留 pendingChanges 供修改重提 |

## 审计日志 `/api/audit-logs`

| 方法 | 路径 | 鉴权 | 请求要点 | 响应要点 |
|---|---|---|---|---|
| GET | `/audit-logs` | 超管 | page/limit/action/admin/user（转义后不区分大小写正则过滤） | 响应键严格为 `logs/total/page/totalPages`（无 limit 键） |

注意：审计只在登录会话签发成功与备份导入时写入，日常管理变更不产生条目——不是完整操作轨迹。

## 举报 `/api/reports` 与反馈 `/api/feedback`

| 方法 | 路径 | 鉴权 | 限流 | 请求要点 | 响应要点 |
|---|---|---|---|---|---|
| POST | `/reports` | 登录 | 10/h | `{targetType:episode\|creator,targetId,reason:inappropriate\|copyright\|spam\|misleading\|other,description?≤2000}` | 同一举报者对同一目标已有待处理 → 400 `Already reported` |
| GET | `/reports/list` | admin+ | — | status/page/limit | 举报列表 |
| PUT | `/reports/resolve/:id` | admin+ | — | `{status:resolved\|dismissed,resolveNote?}` | 保存处理人+备注；向举报者写 `report_result` 通知 |
| POST | `/feedback` | 登录 | 5/h | `{type?:suggestion\|bug\|complaint\|other,content≤2000}` | 200 |
| GET | `/feedback/my` | 登录 | — | page/limit | 我的反馈 |
| GET | `/feedback` | admin+ | — | status/page/limit（**limit 无钳制**） | 全部反馈 |
| PUT | `/feedback/:id` | admin+ | — | `{status?:pending\|read\|replied,reply?≤1000}` | 非空回复 → `feedback_reply` 通知（50 字符预览）+ 异步邮件 |

## 分类 `/api/categories` 与轮播图 `/api/banners`

两域 GET/POST 均双注册 `""` 与 `"/"`（避免 Gin 尾斜杠重定向，固定契约）。

| 方法 | 路径 | 鉴权 | 请求要点 | 响应要点 |
|---|---|---|---|---|
| GET | `/categories` | 公开 | — | 按 order+创建时间排序 |
| POST | `/categories` | admin+ | `{name,order?}` | 重名拒绝；**缺 name 落 500（既有设计）** |
| PUT | `/categories/:id` | admin+ | 同上 | 更新 |
| DELETE | `/categories/:id` | admin+ | — | 删除 |
| GET | `/banners` | 公开 | — | 仅 active，order 升序+创建时间降序 |
| GET | `/banners/all` | admin+ | — | 全部 |
| POST | `/banners` | admin+ | `{title,image,link?,order?}`；link 仅 http(s) | 缺 title/image 落 500（既有设计） |
| PUT | `/banners/:id` | admin+ | 同上 | 更新 |
| DELETE | `/banners/:id` | admin+ | — | 删除 |

## 统计 `/api/stats` 与站点统计 `/api/site-stats`

Stats 域带 `middleware.Cache`（TTL 5min、上限 200 条，按完整 URI 缓存）。

| 方法 | 路径 | 鉴权 | 请求要点 | 响应要点 |
|---|---|---|---|---|
| GET | `/stats/overview?period=7d\|30d` | admin+ | — | 聚合计数+高分/最多观看/最多追番榜单（缓存） |
| GET | `/stats/calendar?year=&month=` | 公开 | — | 发布日历；`episodeLabel` 非空用之否则「第N集」（缓存） |
| GET | `/stats/recommendations/:episodeId` | 公开 | — | 相关推荐（标签/分类/浏览量混合打分，缓存） |
| GET | `/stats/recommendations/collaborative` | 登录 | — | 协同过滤推荐（**不缓存**） |
| GET | `/stats/recommendations/personalized` | 登录 | — | 个性化推荐（**不缓存**） |
| GET | `/stats/activity-heatmap` | admin+ | — | 一年活跃度热力图（缓存） |
| GET | `/stats/episode-lifecycle` | admin+ | — | 周浏览量曲线（由总观看量推导，**近似值**） |
| GET | `/stats/realtime` | admin+ | — | 在线（5min 活跃会话）/今日访问/今日新用户/新剧集（**不缓存**） |
| GET | `/site-stats` | 公开 | — | `{users,episodes,online}`；15s 进程内缓存 + `Cache-Control: public, max-age=15`；在线查询失败降级 0 |

## 通知 `/api/notifications`

除 `/stream` 与 `/vapid-public-key` 外全部登录。通知类型：`new_episode / review_result / report_result / profile_review / friend_link_apply / feedback_reply / announcement`。

| 方法 | 路径 | 请求要点 | 响应要点 |
|---|---|---|---|
| GET | `/notifications/stream?ticket=` | **公开**（票据认证）：query `ticket` 为 `GET /auth/sse-ticket` 签发的 30s JWT | `text/event-stream`；`connected`/`:heartbeat`(30s)/`notification`(10s 轮询，DB 为唯一事实源) 事件；30min 超时；上限：IP≤10 / 全局≤500 / 用户≤5（第 6 条踢最旧） |
| GET | `/notifications/list` | page/limit（≤100；负 limit→1，零→默认 20） | 分页通知 |
| GET | `/notifications/unread-count` | — | 未读数 |
| PUT | `/notifications/read-all` | — | 全部已读 |
| PUT | `/notifications/read-episode/:episodeId` | — | 某剧集未读全部已读 |
| PUT | `/notifications/read/:id` | — | 单条已读（限定本人） |
| DELETE | `/notifications/clear-read` | — | 删除全部已读 |
| DELETE | `/notifications/:id` | — | 删除单条（限定本人） |
| POST | `/notifications/subscribe-reminder` | `{episodeId}` | 若无追番关系则创建（复用追番机制） |
| GET | `/notifications/vapid-public-key` | 公开 | `{publicKey}`；公私钥未配齐时 `null` |
| POST | `/notifications/push/subscribe` | `{subscription:{endpoint,keys:{p256dh,auth}}}` | 按 userId+endpoint upsert |
| POST | `/notifications/push/unsubscribe` | `{endpoint}` | 幂等删除 |

> Web Push 订阅是真实文档，但各域 `sendPushToUser` 均为空实现——**推送永远不会投递**，只有站内通知与邮件在生效。

## 公告 `/api/announcements`（超管 + 全路由 RequireEmailChanged）

| 方法 | 路径 | 请求要点 | 响应要点 |
|---|---|---|---|
| GET | `/announcements/active?channel=` | 公开；channel=popup\|banner | 生效中的公告 |
| GET | `/announcements/:id` | 公开 | 详情 |
| GET | `/announcements` | — | 管理列表 |
| POST | `/announcements` | `{title,content,type:info\|warning\|maintenance\|update,link?,titleEn?,contentEn?,showPopup,showBanner,sendNotification,sendEmail,dismissible,active,pinned,publishAt?,expireAt?}` | link 为空或 http(s)，否则 400；type 非法落 500；active 且 publishAt 不在未来→立即广播（通知每批 1000 + 邮件每批 50） |
| PUT | `/announcements/:id` | 可更新字段 | active/sendNotification/sendEmail 由 false→true 时重置已发送标志并重新广播；删除公告连带删引用它的通知 |
| DELETE | `/announcements/:id` | — | 删除 |
| POST | `/announcements/:id/send-email` | — | 手动触发（需 sendEmail 已启用）；持久化 emailSent/At/Count |
| POST | `/announcements/:id/send-notification` | — | 手动触发（需 sendNotification 已启用） |

## 站点内容 `/api/site-content`

`sitecontents` 之上的键值存储；默认键 `privacy/terms/about/settings`（List 惰性创建）。

| 方法 | 路径 | 鉴权 | 请求要点 | 响应要点 |
|---|---|---|---|---|
| GET | `/site-content` | 超管 | — | 全部键（缺失时按需建默认） |
| GET | `/site-content/:key` | 公开（**key=email 时仅超管**，响应 no-store） | — | 读旧数据 HTML 转义损坏时返回反转义结果（只读修复） |
| PUT | `/site-content/:key` | 超管 | `{content}`；`email` 键直接拒绝；about/settings 须合法 JSON | 更新 |
| POST | `/site-content/upload` | 超管（upload 20/h） | multipart `image`（≤5MB） | `{url:"/uploads/site-..."}` |
| POST | `/site-content/test-email` | 超管 | `{host,port?,user,pass,to,fromName?}`（port 默认 465） | 用请求体凭据发测试邮件 |
| GET | `/site-content/pwa-manifest` | 公开 | — | 由 `settings` 内容 JSON 推导 Web App Manifest |

## 壁纸 `/api/wallpapers`

系统壁纸（`systemwallpapers` 集合）与个人壁纸（内嵌用户文档，≤20 张）是两个互不相关的存储。

| 方法 | 路径 | 鉴权 | 请求要点 | 响应要点 |
|---|---|---|---|---|
| GET | `/wallpapers/system` | 公开 | — | 启用的系统壁纸 |
| GET | `/wallpapers/system/all` | 超管 | — | 全部 |
| POST | `/wallpapers/system` | 超管 | multipart `image`（≤8MB）+ `name`/`enabled`（仅字面 `"false"` 为关）/`sortOrder`（JS parseInt 语义） | 上传；删除时同时删磁盘文件 |
| PUT | `/wallpapers/system/:id` | 超管 | — | 更新 |
| DELETE | `/wallpapers/system/:id` | 超管 | — | 删除（含文件） |
| GET | `/wallpapers/personal` | 登录 | — | 我的个人壁纸 |
| POST | `/wallpapers/personal` | 登录（upload 20/h） | multipart `image`（≤8MB）+ `name` | 超 20 张：删除刚写文件并 400 |
| DELETE | `/wallpapers/personal` | 登录 | `{url}` | 仅允许 `/uploads/wallpaper-` 前缀且未被引用 |

## 主题 `/api/themes`（swagger 未收录，以源码为准）

主题=壁纸和/或图标映射的外观包；`ThemeType()` 推导 `full/wallpaper/icons/legacy`。个人主题默认 `draft`，可提审 → 通过转系统主题 / 拒绝保持个人带备注；站点默认主题至多一个且可为空。

| 方法 | 路径 | 鉴权 | 请求要点 | 响应要点 |
|---|---|---|---|---|
| GET | `/themes/active` | 公开 | — | 启用的系统主题 |
| GET | `/themes/list` | 公开 | — | 系统主题+本人主题 |
| GET | `/themes/my` | 登录 | — | 我的主题 |
| GET | `/themes/my/selection` | 登录 | — | 双槽选择（壁纸槽/图标槽独立）+ 未设置时回退站点默认 + 遗留单 `themeId` 迁移 |
| PUT | `/themes/selection` | 登录 | `{wallpaperThemeId,iconsThemeId}` 或遗留 `{themeId}` | 修改壁纸槽会同步用户背景偏好 |
| PUT | `/themes/background-prefs` | 登录 | `{enabled?,opacity(0-100),blur(0-20),image?}` | 更新背景偏好（原 `/users/background-prefs`） |
| POST | `/themes/` | 登录 | `{name,description?,wallpaperUrl?,wallpaperThumb?,icons?,accentColor?,isSystem?(仅超管生效)}` | 创建，默认 draft；资源须 `/uploads/` 或 http(s)；图标键须匹配组件键正则、≤64 个；`misc.logo` 不可覆盖（仅注册带尾斜杠形式） |
| POST | `/themes/upload-wallpaper` | 登录（upload 20/h） | multipart `image`（≤8MB） | `{url}` |
| POST | `/themes/upload-icon` | 登录（upload 20/h） | multipart `file`（SVG ≤1MB） | `{url}` |
| PUT | `/themes/:id` | 登录（归属） | 同创建 | 更新 |
| DELETE | `/themes/:id` | 登录（归属） | — | 删除（仅清理 `theme-wallpaper-` 前缀且未被引用的文件） |
| POST | `/themes/:id/submit` | 登录（归属） | — | draft → pending 提审 |
| GET | `/themes/all` | 超管 | — | 全部主题 |
| POST | `/themes/:id/review` | 超管 | `{action:approve\|reject,note?}` | approve→转系统主题并启用；reject→保持个人+备注 |
| POST | `/themes/:id/default` | 超管 | `{default:bool}` | 设/取消站点默认（须为启用的系统主题） |
| PUT | `/themes/:id/admin` | 超管 | 主题字段 | 管理更新（个人转系统视为已通过） |

## SVG 图标 `/api/icons`（swagger 未收录，以源码为准）

| 方法 | 路径 | 鉴权 | 请求要点 | 响应要点 |
|---|---|---|---|---|
| GET | `/icons/list` | 公开 | — | 启用图标 + 组件键→URL 的扁平 `mappings` |
| GET | `/icons/all` | 超管 | — | 全部 |
| POST | `/icons/upload` | 超管（upload 20/h） | multipart `files`（多）或 `file`（单）；单次≤50 个，SVG ≤1MB/个 | 单文件失败进 `failed` 数组不整批失败 |
| PUT | `/icons/:id` | 超管 | 名称/分类/描述/映射/启用（长度与键校验） | 更新 |
| DELETE | `/icons/:id` | 超管 | — | 删除文档+文件 |

## 友情链接 `/api/friend-links`

| 方法 | 路径 | 鉴权 | 限流 | 请求要点 | 响应要点 |
|---|---|---|---|---|---|
| GET | `/friend-links` | 公开 | — | — | 已激活友链 |
| POST | `/friend-links/apply` | 可选登录 | 5/h | `{name,url,logo?,description?,altcha}`（url/logo 仅 http(s)；altcha 必需） | 创建 `status=pending`；向全部超管发通知+邮件 |
| GET | `/friend-links/my-applications` | 登录 | — | — | 我的申请记录 |
| GET | `/friend-links/all` | 超管 | — | — | 全部 |
| POST | `/friend-links` | 超管 | — | 友链字段 | 直接创建/审批（无单独 approve 端点） |
| PUT | `/friend-links/:id` | 超管 | — | 友链字段 | 更新 |
| DELETE | `/friend-links/:id` | 超管 | — | — | 删除 |

## 动态流 `/api/activity`

没有 `activity` 集合——动态是实时聚合。

| 方法 | 路径 | 鉴权 | 请求要点 | 响应要点 |
|---|---|---|---|---|
| GET | `/activity` | 登录 | page/limit（内存分页） | 30 天窗口内：追番剧集的新单集/最近更新/高分评分（≥4，≤50 条） |
| GET | `/activity/public` | 公开 | — | 最近 30 个新单集 + 平均分≥4 前 10 + 最近更新 10，**固定最多 20 条** |

## RSS `/api/rss` 与翻译 `/api/translate`

| 方法 | 路径 | 鉴权 | 限流 | 请求要点 | 响应要点 |
|---|---|---|---|---|---|
| GET | `/rss` | 公开 | — | — | RSS 2.0 XML（固定 7 天窗口；单集标题按 episodeLabel 解析，无则「第N集」） |
| GET | `/rss/api-usage?days=` | 超管 | — | — | API 调用统计 |
| POST | `/translate` | 公开（有意免鉴权） | 200/min | `{key,targetLang}` | `zh` 原样返回 key；硬编码词典优先；机器翻译（Azure→MyMemory→Google/Bing，缓存 5000 条 24h）；20s 超时 504；不支持语言 400 |
| POST | `/translate/batch` | 公开 | 200/min | `{texts[],targetLang}` | 串行处理，60s 超时；翻译为空序列化为 `null` |

## 备份 `/api/backup`（双重校验：Protect(creator+) 后内联要求超管）

| 方法 | 路径 | 请求要点 | 响应要点 |
|---|---|---|---|
| GET | `/backup/export` | — | 14 集合白名单 JSON 附件 `backup_<日期>.json`；`users` 脱敏（去 password/lastLoginIp/lastLoginRegion/deviceInfo）；单集合失败跳过不中断 |
| POST | `/backup/import` | `{data,overwrite?}`；序列化数据 ≤50MB（UTF-16 码元）；HTTP 请求体限制单独放宽到 50MB（仅 JSON） | 白名单集合+字段清洗+每集合 ≤10000 文档；overwrite 时按集合事务内整体替换；结果写入一条「数据恢复」审计记录 |

## 上传与静态资源约定

所有图片上传走 `upload.SaveImage`，校验顺序为契约：multipart → 请求体粗限（maxBytes+1MB）→ 文件精确大小 → 锚定扩展名 + MIME → 落盘（`<prefix>-<32位hex><扩展名>`）→ 魔数校验（失败删文件）。SVG 走 `upload.SaveSVG` 独立路径（XML 结构化校验，拒绝 script/DOCTYPE/on* 等）。

| 端点 | 表单字段 | 上限 | 文件名前缀 |
|---|---|---|---|
| POST `/users/avatar` | `avatar` | 2MB | `avatar-` |
| POST `/episodes/upload` | `image` | 5MB | `cover-` |
| POST `/site-content/upload` | `image` | 5MB | `site-` |
| POST `/wallpapers/personal` | `image` | 8MB | `wallpaper-` |
| POST `/wallpapers/system` | `image` | 8MB | `wallpaper-` |
| POST `/themes/upload-wallpaper` | `image` | 8MB | `theme-wallpaper-` |
| POST `/themes/upload-icon` | `file` | 1MB（SVG） | `icon-` |
| POST `/icons/upload` | `files`/`file` | 1MB（SVG）/个，单次≤50 | `icon-` |

上传错误码：`LIMIT_FILE_SIZE`/`LIMIT_FILE_TYPE`/`BAD_MAGIC`，渲染为 400 固定文案（部分旧 handler 对类型不符刻意 500，属既有契约）。`RemoveFile` 只接受上传目录下单层路径段（拒 `..`/分隔符）。

## 错误码（messageKey → 前端 i18n）

| messageKey | HTTP | 触发 | 前端行为 |
|---|---|---|---|
| `auth.noToken` | 401 | 无 access token | 跳登录 |
| `auth.invalidToken` | 401 | token 非法 / refresh 等 purpose 误用 | 跳登录 |
| `auth.userNotFound` | 401 | 用户已删除 | 跳登录 |
| `auth.forbidden` | 403 | 角色不符（含 RequireEmailChanged 拦截） | 提示无权限 |
| `auth.accessTokenExpired` | **419** | access 过期 | **自动调 /api/auth/refresh** |
| `auth.noRefreshToken` | 401 | refresh cookie 缺失 | 跳登录 |
| `auth.refreshTokenExpired` | 401 | refresh 过期 | 跳登录 |
| `auth.concurrentRefresh` | 409 | 并发刷新宽限（30s 内旧 token） | 静默重试 |
| `auth.refreshTokenReuse` | 401 | 重用检测（吊销全部会话） | 跳登录 |

其他错误：限流 429 `{message}`；登录分支 403（`needVerification`/`needDeviceVerify`，**不带 messageKey**，前端按字段分支）；普通校验错误 400 `{message}`（中文文案）；服务端错误 500 `{message:"服务器内部错误"}`（生产隐藏详情）。**对象 ID 格式非法通常表现为 500**（与旧 API 一致的有意怪癖）。

## 数据结构速查（核心 collection）

**users**：`accountId`(唯一) `username` `email`(唯一) `password`(bcrypt,不输出) `avatar` `deviceInfo{...}` `lastLoginAt/Ip/Region` `deletionRequestedAt` `isEmailVerified` `role[user|creator|admin|superadmin]` `passwordChangedAt` `twoFactorEnabled` `twoFactorSecret/BackupCodes`(AES加密) `emailNotificationPrefs{7布尔}` `backgroundPrefs{image,enabled,opacity,blur}` `personalWallpapers[]` `themeWallpaperId/themeIconsId`(双槽) `loginAttempts/lockUntil`(锁定) `createdAt`

**usersessions**：`userId` `tokenHash`(旧access) `refreshTokenHash`(轮换) `deviceInfo` `ip` `isActive` `loginAt` `lastActiveAt` `logoutAt`

**usedtokens**（一次性防重放）：`tokenHash`(唯一) `purpose[reset-password|email-change|device-verify]` `expiresAt`(TTL)

**episodes**：多语言标题/简介、`coverImage`、`currentEpisodes/totalEpisodes`、`status[ongoing|completed|upcoming]`、`categories[]/tags[]`、`platformLinks[]`、`premiereDate`、审核字段（`reviewStatus[pending|approved|rejected]` `reviewNote` `reviewedBy` `reviewedAt`）、`pendingChanges{...}`、创作者/编辑者引用

**singleepisodes**：`episodeId`(父) `episodeNumber`(整数,排序/计数依据) `episodeLabel`(展示用,空回退「第N集」) `episodeLabelCustom`(原始自定义值) `event` `isUpcoming` `releaseDate`

**episodeversions**（上限 50 条/剧集）：`episodeId` `version` `snapshot` `editedBy` `createdAt`

**episodetrash**：剧集副本 + `trashReason[deleted|rejected]` `trashNote` `trashBy` `trashAt`

**folders**：`userId` `name` `type[follow|favorite]` `description` `shareToken`(惰性) `order`
**savedfolders**：`userId` `shareToken` `creatorName`（他人收藏夹书签，无条目）

**follows/favorites**：`userId` `episodeId` `folderId` `followedAtEpisodes[]`（追番时集数快照）
**histories**：`userId` `episodeId` `watchedEpisodes[]` `lastWatched` `lastWatchedEpisodeNumber`（TTL 365d）
**ratings**：`userId` `episodeId` `score(1-5)`；聚合平均分（一位小数）与人数写回剧集

**notifications**（TTL 90d）：`userId` `episodeId?` `type`（见通知域） `message` `link` `metadata` `isRead` `createdAt`
**pushsubscriptions**：`userId` `endpoint` `keys{p256dh,auth}` `userAgent`（**无发送器，不会投递**）

**creatorprofiles**：`creatorId` 多语言简介/头像/`socialLinks`/`qqGroupLink` `reviewStatus` `pendingChanges`
**themes**：`name/description` `wallpaperUrl/wallpaperThumb` `icons{}`(≤64) `accentColor` `isSystem` `status[draft|pending|approved|rejected]` `enabled` `isDefault`
**auditlogs**：`userId/userName` `action` `target` `details` `ip` `userAgent` `createdAt`

（完整字段见 `internal/model/` 的 struct）
