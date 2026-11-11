// Package authlog 记录认证链路的诊断事件。
//
// 两个出口，各有用途：
//   - 结构化日志（stderr / journalctl）：`event=session_revoked reason=rotated ...`，
//     现场可以直接 `journalctl -u <unit> | grep 'event='` 过滤；
//   - authevents 集合：异步落库（30 天 TTL），日志轮转/重启后仍可回溯，
//     由 `cmd/diag` 按用户/邮箱汇总成时间线。
//
// 只记录失败与吊销类事件（正常登录成功不记），量级很小。
package authlog

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/Furry-Drama-Tracker/backend/internal/model"
	"github.com/Furry-Drama-Tracker/backend/internal/repository"
)

// persistTimeout 是落库超时（与请求上下文解耦：响应已返回后仍要写完）。
const persistTimeout = 3 * time.Second

// persistThrottle 是"同一来源同类事件"的落库节流窗口。
//
// 动机：客户端在令牌被吊销后常会并发发起多个请求（一次页面刷新就是十几个），
// 每个都会命中 session_revoked。日志保留全量（journalctl 便宜），但落库按
// event+userId/ip 每窗口只写一条，避免被卡住的客户端把 authevents 写爆。
const persistThrottle = 30 * time.Second

// Recorder 是认证事件记录器。
type Recorder struct {
	log  *slog.Logger
	repo *repository.AuthEventRepo
	// last 记录各节流 key 上次落库时间（sync.Map 零值可用）。
	last sync.Map
}

// New 构造记录器。log/repo 任一为 nil 时只走另一个出口（便于测试与最小部署）。
func New(log *slog.Logger, repo *repository.AuthEventRepo) *Recorder {
	return &Recorder{log: log, repo: repo}
}

// Record 记录一条事件。nil receiver 安全，调用方无需判空。
// 落库是 fire-and-forget（失败只记一行 warn），不阻塞也不影响主响应。
func (r *Recorder) Record(ev model.AuthEvent) {
	if r == nil {
		return
	}
	if ev.At.IsZero() {
		ev.At = time.Now()
	}
	if r.log != nil {
		attrs := make([]any, 0, 16)
		attrs = append(attrs, "event", ev.Event)
		if ev.UserID != nil {
			attrs = append(attrs, "userId", ev.UserID.Hex())
		}
		if ev.Email != "" {
			attrs = append(attrs, "email", ev.Email)
		}
		attrs = append(attrs, "ip", ev.IP)
		if ev.Path != "" {
			attrs = append(attrs, "path", ev.Path)
		}
		if ev.Reason != "" {
			attrs = append(attrs, "reason", ev.Reason)
		}
		if ev.Detail != "" {
			attrs = append(attrs, "detail", ev.Detail)
		}
		if ev.UA != "" {
			attrs = append(attrs, "ua", ev.UA)
		}
		r.log.Warn("auth event", attrs...)
	}
	if r.repo == nil {
		return
	}
	if !r.allowPersist(ev) {
		return
	}
	evCopy := ev
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), persistTimeout)
		defer cancel()
		if err := r.repo.Insert(ctx, &evCopy); err != nil && r.log != nil {
			r.log.Warn("auth event persist failed", "event", evCopy.Event, "err", err)
		}
	}()
}

// allowPersist 按 event+来源 做落库节流（见 persistThrottle）。
func (r *Recorder) allowPersist(ev model.AuthEvent) bool {
	key := ev.Event + "|"
	switch {
	case ev.UserID != nil:
		key += "u:" + ev.UserID.Hex()
	case ev.Email != "":
		key += "e:" + ev.Email
	default:
		key += "ip:" + ev.IP
	}
	now := time.Now()
	if v, ok := r.last.Load(key); ok {
		if t, ok := v.(time.Time); ok && now.Sub(t) < persistThrottle {
			return false
		}
	}
	r.last.Store(key, now)
	return true
}
