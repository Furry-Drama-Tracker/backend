package model

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// 会话吊销原因（usersessions.revokeReason 取值）。
//
// 用途：用户报"我莫名掉登录"时，直接查 usersessions 就能知道会话是**因为什么**
// 被置为 inactive 的，不必靠猜。取值用 snake_case 便于 grep / journalctl 过滤。
const (
	RevokeLogout          = "logout"           // 用户主动登出
	RevokeRotated         = "rotated"          // refresh 轮换（正常，不是吊销）
	RevokePasswordChanged = "password_changed" // 改密码
	RevokePasswordReset   = "password_reset"   // 忘记密码重置
	RevokeEmailChanged    = "email_changed"    // 改绑邮箱
	RevokeRefreshReuse    = "refresh_reuse"    // refresh 重用检测命中
	RevokeUserRevoked     = "user_revoked"     // 用户手动下线某个设备
	RevokeUserRevokedAll  = "user_revoked_all" // 用户"下线其他所有设备"
	RevokeAdmin           = "admin_revoked"    // 管理员强制下线
	RevokeExpired         = "expired"          // 超过 30 天无活跃，被 cron 清理
)

// 认证事件类型（authevents.event 取值）。
const (
	AuthEventSessionRevoked = "session_revoked" // access 令牌对应的会话已不可用（被吊销）
	AuthEventRefreshGrace   = "refresh_grace"   // 并发刷新宽限命中（409，不吊销）
	AuthEventRefreshReuse   = "refresh_reuse"   // refresh 重用（401，吊销全部会话）
	AuthEventRefreshInvalid = "refresh_invalid" // refresh 缺失/过期/非法（401）
	AuthEventTwoFAReplay    = "twofa_replay"    // TOTP 同一时间步被重复提交
	AuthEventRateLimited    = "ratelimited"     // 限流命中（429）
	// AuthEventDeviceCodeMiss 设备登录码提交未命中（confirm-device-login 400）。
	// miss 此前零痕迹：per-entry 尝试上限只数命中，喷洒式枚举完全不可见；
	// 留痕后（按 IP 节流落库）可在 authevents/日志中发现批量猜测。
	AuthEventDeviceCodeMiss = "device_code_miss"
)

// AuthEvent 是认证链路上的低概率诊断事件（authevents 集合，at 建 TTL 索引自动过期）。
//
// 设计：只记录"失败/吊销"类事件，正常登录成功不记（避免量级膨胀）。
// 与 auditlogs 的区别：auditlogs 记业务操作（谁改了谁），本集合只记
// "为什么这个人的会话不工作了"，专供排障。
type AuthEvent struct {
	ID     primitive.ObjectID  `bson:"_id,omitempty" json:"_id"`
	At     time.Time           `bson:"at" json:"at"`
	Event  string              `bson:"event" json:"event"`
	UserID *primitive.ObjectID `bson:"userId,omitempty" json:"userId,omitempty"`
	Email  string              `bson:"email,omitempty" json:"email,omitempty"`
	IP     string              `bson:"ip" json:"ip"`
	UA     string              `bson:"ua,omitempty" json:"ua,omitempty"`
	Path   string              `bson:"path,omitempty" json:"path,omitempty"`
	// Reason 是吊销原因或限流器名（session_revoked → Revoke*；ratelimited → spec 名）。
	Reason string `bson:"reason,omitempty" json:"reason,omitempty"`
	Detail string `bson:"detail,omitempty" json:"detail,omitempty"`
}
