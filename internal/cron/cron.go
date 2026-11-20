// Package cron 实现后台定时任务。
//
// 调度基于 github.com/robfig/cron/v3（替代旧版 time.Timer/Ticker 手写对齐循环）：
// 全部任务按墙钟时刻触发（cron spec），重启不漂移、对齐确定。
// 传入的 ctx 取消时停止调度器并等待在跑任务收尾。
package cron

import (
	"context"
	"log/slog"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/Furry-Drama-Tracker/backend/internal/model"
	"github.com/Furry-Drama-Tracker/backend/internal/repository"
	"github.com/Furry-Drama-Tracker/backend/internal/service"
)

const (
	// sessionTTL 会话 30 天无活跃即清理。
	sessionTTL = 30 * 24 * time.Hour
	// readNotificationTTL 已读通知 30 天后清理。
	readNotificationTTL = 30 * 24 * time.Hour
)

// StartSessions 启动认证/用户域相关的后台定时任务（本阶段只含会话与已读通知清理）。
//
// 调度计划（墙钟对齐，本地时区）：
//   - 会话清理：每小时整点（'0 * * * *'）把 lastActiveAt 超过 30 天的 active
//     会话批量置为 inactive + logoutAt（SessionRepo.MarkInactiveOlderThan）。
//   - 已读通知清理：每天 03:00（'0 3 * * *'）删除 isRead=true 且 createdAt
//     超过 30 天的通知（NotificationRepo.DeleteReadOlderThan）。
//   - 注销宽限期账号物理删除：每 6 小时整点（'0 6-18/6 * * *'，即 6/12/18 点）删除
//     deletionRequestedAt 超过 7 天宽限期的用户并清理关联数据。旧实现是"启动后延迟
//     6 小时再每 6 小时"，换 robfig/cron 后对齐墙钟——只会在固定时刻触发，
//     升级/重启瞬间不会批量删除（ADR-024）。
//
// TODO（依赖 Episode 等后续段仓储，M2 之后再启用，见 AGENTS.md）：
//   - 自动完结：连载剧 currentEpisodes >= totalEpisodes 时置 status='completed' 并向
//     追番用户发送站内通知 / Web Push / 邮件（每小时执行）。
//
// 调用方应在服务优雅关停时 cancel ctx；本函数立即返回，不阻塞。
func StartSessions(ctx context.Context, repos *repository.Repos) {
	c := newCron(ctx, repos)
	c.Start()
	go func() {
		<-ctx.Done()
		// Stop 等待在跑任务结束后才返回（返回值是完成通知 ctx，此处仅等待收尾）。
		<-c.Stop().Done()
	}()
}

// newCron 构造调度器并注册全部任务（单测校验条目数/spec 用）。
func newCron(ctx context.Context, repos *repository.Repos) *cron.Cron {
	// Recover：单次任务 panic 只记日志，不终止调度器。
	c := cron.New(cron.WithChain(cron.Recover(cron.DefaultLogger)))
	mustAdd(c, "0 * * * *", func() {
		cleanupExpiredSessions(ctx, repos)
	})
	mustAdd(c, "0 3 * * *", func() {
		cleanupReadNotifications(ctx, repos)
	})
	mustAdd(c, "0 6-18/6 * * *", func() {
		cleanupDeletionRequests(ctx, repos)
	})
	return c
}

// mustAdd 注册任务；spec 是编译期常量，出错说明代码写错了，直接 panic 暴露。
func mustAdd(c *cron.Cron, spec string, fn func()) {
	if _, err := c.AddFunc(spec, fn); err != nil {
		panic("cron: invalid spec " + spec + ": " + err.Error())
	}
}

// cleanupExpiredSessions 会话清理任务体。
func cleanupExpiredSessions(ctx context.Context, repos *repository.Repos) {
	cutoff := time.Now().Add(-sessionTTL)
	n, err := repos.Sessions.MarkInactiveOlderThan(ctx, cutoff, model.RevokeExpired)
	if err != nil {
		slog.Error("[Cron] Session cleanup error", "err", err)
		return
	}
	if n > 0 {
		slog.Info("[Cron] Cleaned expired sessions", "count", n)
	}
}

// cleanupDeletionRequests 删除注销申请已过 7 天宽限期的用户及其个人数据。
func cleanupDeletionRequests(ctx context.Context, repos *repository.Repos) {
	cutoff := time.Now().Add(-service.DeletionGraceDays * 24 * time.Hour)
	users, err := repos.Users.FindDeletionRequestedBefore(ctx, cutoff)
	if err != nil {
		slog.Error("[Cron] Deletion cleanup query error", "err", err)
		return
	}
	for _, u := range users {
		if err := service.PurgeUserData(ctx, repos, u.ID); err != nil {
			slog.Error("[Cron] Purge user error", "userId", u.ID.Hex(), "err", err)
			continue
		}
		slog.Info("[Cron] Purged deleted user", "userId", u.ID.Hex())
	}
}

// cleanupReadNotifications 已读通知清理任务体。
func cleanupReadNotifications(ctx context.Context, repos *repository.Repos) {
	cutoff := time.Now().Add(-readNotificationTTL)
	n, err := repos.Notifications.DeleteReadOlderThan(ctx, cutoff)
	if err != nil {
		slog.Error("[Cron] Notification cleanup error", "err", err)
		return
	}
	if n > 0 {
		slog.Info("[Cron] Cleaned old read notifications", "count", n)
	}
}
