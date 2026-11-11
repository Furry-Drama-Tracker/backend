// cmd/diag 是认证排障工具：给定用户（邮箱或 _id），输出会话与认证事件时间线，
// 并给出"掉登录"的初步结论。
//
// 用法：
//
//	go run ./cmd/diag -config=dev.ini -user=someone@example.com
//	go run ./cmd/diag -user=665f1c...（24 位 ObjectID）
//	go run ./cmd/diag -recent -hours=6        # 看全局最近事件（判断是否大面积）
//
// 只读：不修改任何数据。
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/Furry-Drama-Tracker/backend/internal/config"
	"github.com/Furry-Drama-Tracker/backend/internal/model"
	"github.com/Furry-Drama-Tracker/backend/internal/repository"
)

func main() {
	var (
		cfgPath = flag.String("config", "", "配置文件路径（默认按 OS）")
		userArg = flag.String("user", "", "用户邮箱或 24 位 ObjectID")
		days    = flag.Int("days", 7, "回溯天数（会话+事件）")
		hours   = flag.Int("hours", 6, "全局最近事件的时间窗（小时，仅 -recent）")
		limit   = flag.Int64("limit", 50, "每个区块最多显示条数")
		recent  = flag.Bool("recent", false, "只列全局最近事件（不指定用户）")
	)
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("配置加载失败: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	db, err := repository.Connect(ctx, cfg.Database.URI, cfg.Database.Name, 5)
	if err != nil {
		log.Fatalf("MongoDB 连接失败: %v", err)
	}
	repos := repository.NewRepos(db, cfg.Security.LoginMaxAttempts, cfg.Security.LoginLockMinutes)

	if *recent {
		dumpRecent(ctx, repos, time.Duration(*hours)*time.Hour, *limit)
		return
	}
	if strings.TrimSpace(*userArg) == "" {
		fmt.Fprintln(os.Stderr, "需要 -user=<邮箱或 ObjectID>，或用 -recent 看全局最近事件")
		os.Exit(2)
	}
	dumpUser(ctx, repos, strings.TrimSpace(*userArg), time.Duration(*days)*24*time.Hour, *limit)
}

// dumpRecent 打印全局最近认证事件（用于判断是否大面积故障）。
func dumpRecent(ctx context.Context, repos *repository.Repos, since time.Duration, limit int64) {
	events, err := repos.AuthEvents.Recent(ctx, time.Now().Add(-since), limit)
	if err != nil {
		log.Fatalf("查询事件失败: %v", err)
	}
	fmt.Printf("=== 最近 %s 的认证事件（共 %d 条）===\n", humanDur(since), len(events))
	if len(events) == 0 {
		fmt.Println("（无）")
		return
	}
	byEvent := map[string]int{}
	for _, ev := range events {
		byEvent[ev.Event]++
	}
	fmt.Println("按类型统计：")
	for k, v := range byEvent {
		fmt.Printf("  %-16s %d\n", k, v)
	}
	fmt.Println()
	printEvents(events, limit)
}

// dumpUser 打印单个用户的会话与事件时间线 + 结论文本。
func dumpUser(ctx context.Context, repos *repository.Repos, arg string, since time.Duration, limit int64) {
	u, err := findUser(ctx, repos, arg)
	if err != nil {
		log.Fatalf("找不到用户 %q: %v", arg, err)
	}
	fmt.Printf("用户: %s (%s)  id=%s  role=%s\n", u.Username, u.Email, u.ID.Hex(), u.Role)
	if u.LastLoginAt != nil {
		fmt.Printf("最近登录: %s  ip=%s  region=%s\n",
			u.LastLoginAt.Local().Format("2006-01-02 15:04:05"), u.LastLoginIP, u.LastLoginRegion)
	}
	fmt.Println()

	sessions, err := repos.Sessions.FindByUserSince(ctx, u.ID, time.Now().Add(-since), limit)
	if err != nil {
		log.Fatalf("查询会话失败: %v", err)
	}
	fmt.Printf("=== 会话（最近 %s，共 %d 条）===\n", humanDur(since), len(sessions))
	if len(sessions) == 0 {
		fmt.Println("（无）")
	}
	for _, s := range sessions {
		status := "active"
		if !s.IsActive {
			status = "revoked"
			if s.RevokeReason != "" {
				status += "(" + s.RevokeReason + ")"
			}
		}
		when := s.LoginAt.Local().Format("2006-01-02 15:04:05")
		line := fmt.Sprintf("%s  %-28s ip=%-15s ua=%s", when, status, s.IP, truncate(s.DeviceInfo.UserAgent, 60))
		if s.LogoutAt != nil {
			line += "  end=" + s.LogoutAt.Local().Format("15:04:05")
		}
		fmt.Println(line)
	}
	fmt.Println()

	events, err := repos.AuthEvents.FindByUserID(ctx, u.ID, time.Now().Add(-since), limit)
	if err != nil {
		log.Fatalf("查询事件失败: %v", err)
	}
	fmt.Printf("=== 认证事件（最近 %s，共 %d 条）===\n", humanDur(since), len(events))
	if len(events) == 0 {
		fmt.Println("（无）")
	}
	printEvents(events, limit)
	fmt.Println()

	fmt.Println("=== 结论 ===")
	fmt.Println(explain(sessions, events))
}

// findUser 支持邮箱与 ObjectID 两种定位方式。
func findUser(ctx context.Context, repos *repository.Repos, arg string) (*model.User, error) {
	if oid, err := primitive.ObjectIDFromHex(arg); err == nil {
		return repos.Users.FindByID(ctx, oid)
	}
	return repos.Users.FindByEmail(ctx, strings.ToLower(arg))
}

// explain 把"最后一条被吊销的会话 + 最近事件"翻译成可执行的结论。
func explain(sessions []model.UserSession, events []model.AuthEvent) string {
	for _, s := range sessions {
		if s.IsActive {
			continue
		}
		if s.RevokeReason == "" {
			continue
		}
		at := s.LoginAt.Local().Format("2006-01-02 15:04:05")
		if s.LogoutAt != nil {
			at = s.LogoutAt.Local().Format("2006-01-02 15:04:05")
		}
		return fmt.Sprintf("会话在 %s 被服务端吊销，原因 = %s。\n%s", at, s.RevokeReason, reasonHint(s.RevokeReason))
	}
	if len(events) > 0 {
		ev := events[0]
		return fmt.Sprintf("最近一条认证事件：%s（%s，%s）。\n%s",
			ev.Event, ev.At.Local().Format("2006-01-02 15:04:05"), ev.Detail, eventHint(ev.Event))
	}
	return "没有发现服务端吊销记录或认证事件 —— 更可能是客户端侧原因：\n" +
		"  · refresh cookie 未随请求发出（浏览器清理/无痕模式/跨站 SameSite 限制）\n" +
		"  · 前端把 429/403 当成登出处理\n" +
		"  · 用户记错时间（用「最近登录」时间对照）"
}

func reasonHint(reason string) string {
	switch reason {
	case model.RevokeLogout:
		return "  → 用户自己点了退出登录（或前端调用了 logout）。属正常行为，无需处理。"
	case model.RevokeRotated:
		return "  → refresh 正常轮换留下的记录，不是掉登录的原因。"
	case model.RevokePasswordChanged, model.RevokePasswordReset:
		return "  → 改密码/重置密码会吊销全部会话。若用户没改过密码，视为被盗信号，建议查 auditlogs 的 CHANGE_PASSWORD/PASSWORD_RESET。"
	case model.RevokeEmailChanged:
		return "  → 改绑邮箱会吊销全部会话，且新邮箱需要重新验证。"
	case model.RevokeRefreshReuse:
		return "  → refresh token 重用检测命中，全部会话被吊销。常见成因：弱网/后台挂起后迟到的旧 token 超过宽限期、同一 token 被多端复制使用、或真被盗用。\n" +
			"    宽限期当前为 2 分钟（见 service.ConcurrentRefreshGrace）。"
	case model.RevokeUserRevoked, model.RevokeUserRevokedAll:
		return "  → 用户在「账号安全-设备管理」里主动下线了该设备。"
	case model.RevokeAdmin:
		return "  → 管理员强制下线（后台用户会话管理）。"
	case model.RevokeExpired:
		return "  → 超过 30 天无活跃，被定时任务清理。"
	default:
		return "  → 原因未知（旧数据未记录 revokeReason，或第三方直接改库）。"
	}
}

func eventHint(event string) string {
	switch event {
	case model.AuthEventSessionRevoked:
		return "  → 有请求带着已失效的 access token 进来了：说明该用户前端仍持有旧 cookie。结合上面会话区块看是哪个原因吊销的。"
	case model.AuthEventRefreshGrace:
		return "  → 并发刷新宽限命中（409），服务端**没有**吊销会话，属正常抖动，前端会自动重试。"
	case model.AuthEventRefreshReuse:
		return "  → refresh token 重用，全部会话被吊销 —— 这就是掉登录的直接原因。"
	case model.AuthEventRefreshInvalid:
		return "  → refresh 请求本身不可用（cookie 缺失/过期/非法）。若大量出现，重点查 cookie 域/路径与 HTTPS/反代配置。"
	case model.AuthEventTwoFAReplay:
		return "  → 同一 TOTP 时间步被重复提交（可能重复点击，也可能是在重放验证码）。"
	case model.AuthEventRateLimited:
		return "  → 触发限流。看 Reason/详情里的限流器名：refresh/两个 2FA 桶已拆开，若仍是 refresh 被限流说明并发异常。"
	default:
		return ""
	}
}

func printEvents(events []model.AuthEvent, limit int64) {
	for i, ev := range events {
		if int64(i) >= limit {
			fmt.Printf("...（已截断，用 -limit 调整）\n")
			return
		}
		var b strings.Builder
		b.WriteString(ev.At.Local().Format("2006-01-02 15:04:05"))
		b.WriteString("  ")
		b.WriteString(ev.Event)
		if ev.Reason != "" {
			b.WriteString(" reason=" + ev.Reason)
		}
		if ev.Detail != "" {
			b.WriteString(" detail=" + ev.Detail)
		}
		if ev.IP != "" {
			b.WriteString(" ip=" + ev.IP)
		}
		if ev.Path != "" {
			b.WriteString(" path=" + ev.Path)
		}
		fmt.Println(b.String())
	}
}

func humanDur(d time.Duration) string {
	if d >= 24*time.Hour && d%(24*time.Hour) == 0 {
		return fmt.Sprintf("%d 天", int(d/(24*time.Hour)))
	}
	return fmt.Sprintf("%d 小时", int(d/time.Hour))
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
