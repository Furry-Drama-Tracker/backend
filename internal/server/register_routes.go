package server

import (
	"fmt"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Furry-Drama-Tracker/backend/internal/authlog"
	"github.com/Furry-Drama-Tracker/backend/internal/email"
	"github.com/Furry-Drama-Tracker/backend/internal/handler"
	"github.com/Furry-Drama-Tracker/backend/internal/ipregion"
	"github.com/Furry-Drama-Tracker/backend/internal/middleware"
	"github.com/Furry-Drama-Tracker/backend/internal/ratelimit"
	"github.com/Furry-Drama-Tracker/backend/internal/router"
	"github.com/Furry-Drama-Tracker/backend/internal/service"
)

// RegisterRoutes 挂载第一段业务路由（认证 + 用户域）到受限流的 /api 路由组下。
// 每个端点只在 /api 下注册一次；per-endpoint 限流由 middleware.RateLimit 施加。
// rec 是认证诊断事件记录器（nil 时静默）。
func RegisterRoutes(d Deps, api *gin.RouterGroup, opts middleware.RateLimitOpts, rec *authlog.Recorder) {
	mail := email.NewClient(d.Config, d.Repos.SiteContents)
	mail.StartCleanup(10 * time.Minute)
	// IP 地域解析默认禁用（外呼 ipapi.co，数据出境合规；见 docs/decisions.md ADR-017）。
	// 关闭时 LastLoginRegion 保持 service 侧的 "未知" 兜底，不影响登录/注册流程；
	// 未来接本地离线库时只需替换这里的构造函数（ipregion.NewClient(<本地查询>)）。
	ipr := ipregion.NewDisabled()
	if d.Config.Security.IPRegionEnabled {
		ipr = ipregion.NewClient(nil)
	}
	svc := service.NewAuthService(d.Config, d.Repos, d.Signer, mail, ipr)
	svc.Rec = rec
	amw := middleware.NewAuth(d.Repos, d.Signer)
	amw.Rec = rec
	h := handler.NewAuth(svc, amw, d.Config)
	// 同一 spec 在同一个 App 内复用同一个 limiter，使挂载到多个路由的 spec
	// （例如 2FA）共享计数器；不同 App/测试实例之间互不影响。
	limiters := map[string]gin.HandlerFunc{}
	rl := func(spec ratelimit.Spec) gin.HandlerFunc {
		key := spec.Name + "|" + spec.Window.String() + "|" + fmt.Sprint(spec.Max)
		if h, ok := limiters[key]; ok {
			return h
		}
		h := middleware.RateLimit(spec, opts)
		limiters[key] = h
		return h
	}

	router.Mount(api, "/auth", func(g *gin.RouterGroup) {
		g.GET("/captcha", rl(ratelimit.CaptchaSpec), h.Captcha)
		g.GET("/check-accountId", rl(ratelimit.CheckAccountIDSpec), h.CheckAccountID)
		g.POST("/register", rl(ratelimit.RegisterSpec), h.Register)
		g.POST("/login", rl(ratelimit.AuthSpec), h.Login)
		g.POST("/logout", amw.Protect(), h.Logout)
		g.POST("/refresh", rl(ratelimit.RefreshSpec), h.Refresh)
		g.GET("/me", amw.Protect(), h.Me)
		g.GET("/sse-ticket", amw.Protect(), h.SSETicket)
		g.POST("/verify-device", rl(ratelimit.TwoFactorSpec), h.VerifyDevice)
		g.POST("/confirm-device-login", rl(ratelimit.TwoFactorSpec), h.ConfirmDeviceLogin)
		g.POST("/login-2fa", rl(ratelimit.TwoFactorSpec), h.Login2FA)
		g.PUT("/change-password", rl(ratelimit.PasswordResetSpec), amw.Protect(), h.ChangePassword)
		g.POST("/forgot-password", rl(ratelimit.PasswordResetSpec), h.ForgotPassword)
		g.POST("/reset-password", rl(ratelimit.PasswordResetSpec), h.ResetPassword)
		g.PUT("/change-email", amw.Protect("superadmin"), amw.RequireEmailChanged(), rl(ratelimit.ChangeEmailSpec), h.ChangeEmail)
		g.PUT("/email-notification-prefs", amw.Protect(), h.EmailNotificationPrefs)
		g.POST("/verify-email", rl(ratelimit.EmailVerifySpec), h.VerifyEmail)
		g.POST("/resend-verification", amw.Protect(), rl(ratelimit.EmailVerifySpec), h.ResendVerification)
		g.POST("/resend-verification-by-email", rl(ratelimit.EmailVerifySpec), h.ResendVerificationByEmail)
		g.POST("/request-email-change", amw.Protect(), rl(ratelimit.RequestEmailChangeSpec), h.RequestEmailChange)
		g.POST("/verify-email-change", h.VerifyEmailChange)
		g.POST("/request-deletion", amw.Protect(), h.RequestDeletion)
		g.POST("/cancel-deletion", amw.Protect(), h.CancelDeletion)
		g.GET("/deletion-status", amw.Protect(), h.DeletionStatus)
	})

	router.Mount(api, "/user-sessions", func(g *gin.RouterGroup) {
		reqEmail := amw.RequireEmailChanged()
		g.POST("/create", amw.Protect(), h.CreateUserSession)
		g.GET("/my", amw.Protect(), h.MyUserSessions)
		g.PUT("/:id/name", amw.Protect(), h.RenameUserSession)
		g.DELETE("/:id", amw.Protect(), h.DeleteUserSession)
		g.DELETE("/my/all", amw.Protect(), h.DeleteAllOtherSessions)
		g.POST("/heartbeat", amw.Protect(), h.Heartbeat)
		g.GET("/all", amw.Protect("superadmin"), h.AllUserSessions)
		g.DELETE("/admin/:id", amw.Protect("superadmin"), reqEmail, h.AdminDeleteSession)
		g.DELETE("/admin/user/:userId/all", amw.Protect("superadmin"), reqEmail, h.AdminDeleteUserSessions)
	})

	router.Mount(api, "/2fa", func(g *gin.RouterGroup) {
		g.POST("/enable", amw.Protect(), rl(ratelimit.TwoFactorSpec), h.Enable2FA)
		g.POST("/verify-enable", amw.Protect(), rl(ratelimit.TwoFactorSpec), h.VerifyEnable2FA)
		g.POST("/disable", amw.Protect(), rl(ratelimit.TwoFactorSpec), h.Disable2FA)
		g.POST("/verify", amw.Protect(), rl(ratelimit.TwoFactorSpec), h.Verify2FA)
	})

	router.Mount(api, "/users", func(g *gin.RouterGroup) {
		g.POST("/avatar", amw.Protect(), rl(ratelimit.UploadSpec), h.Avatar)
		g.PUT("/profile", amw.Protect(), h.Profile)
		g.GET("/export-my-data", amw.Protect(), rl(ratelimit.ExportSpec), h.ExportMyData)
	})

	// ---- 内容域（挂载前缀为固定路由表，勿改）----
	ep := handler.NewEpisodes(d.Repos, d.Config, amw, rl, mail)
	router.Mount(api, "/episodes", ep.Register)

	se := handler.NewSeries(d.Repos, d.Config, amw, rl)
	router.Mount(api, "/series", se.Register)

	fw := handler.NewFollows(d.Repos, d.Config, amw, rl)
	router.Mount(api, "/follows", fw.Register)

	fv := handler.NewFavorites(d.Repos, d.Config, amw, rl)
	router.Mount(api, "/favorites", fv.Register)

	fo := handler.NewFolders(d.Repos, d.Config, amw, rl)
	router.Mount(api, "/folders", fo.Register)

	sf := handler.NewSavedFolders(d.Repos, d.Config, amw, rl)
	router.Mount(api, "/saved-folders", sf.Register)

	hi := handler.NewHistories(d.Repos, d.Config, amw, rl)
	router.Mount(api, "/histories", hi.Register)

	ra := handler.NewRatings(d.Repos, d.Config, amw, rl)
	router.Mount(api, "/ratings", ra.Register)

	// ---- CMS 域（公告/壁纸/友链，挂载前缀为固定路由表，勿改）----
	an := handler.NewAnnouncements(d.Repos, d.Config, amw, rl, mail)
	router.Mount(api, "/announcements", an.Register)

	wp := handler.NewWallpapers(d.Repos, d.Config, amw, rl)
	router.Mount(api, "/wallpapers", wp.Register)

	fl := handler.NewFriendLinks(d.Repos, d.Config, amw, rl, mail, svc.VerifyAltcha)
	router.Mount(api, "/friend-links", fl.Register)

	// ---- 主题 / 图标管理域 ----
	th := handler.NewThemes(d.Repos, d.Config, amw, rl)
	router.Mount(api, "/themes", th.Register)

	ic := handler.NewIcons(d.Repos, d.Config, amw, rl)
	router.Mount(api, "/icons", ic.Register)

	// ---- 管理后台 + 审计日志域 ----
	ad := handler.NewAdmin(d.Repos, d.Config, amw, rl, mail)
	router.Mount(api, "/admin", ad.Register)

	al := handler.NewAuditLogs(d.Repos, d.Config, amw, rl)
	router.Mount(api, "/audit-logs", al.Register)

	// ---- 通知域 ----
	nt := handler.NewNotifications(d.Repos, d.Config, amw, rl, d.DB)
	router.Mount(api, "/notifications", nt.Register)

	// ---- 统计 / 举报 / 审核域（挂载前缀为固定路由表，勿改）----
	st := handler.NewStats(d.Repos, d.Config, amw, rl)
	router.Mount(api, "/stats", st.Register)

	rp := handler.NewReports(d.Repos, d.Config, amw, rl)
	router.Mount(api, "/reports", rp.Register)

	rv := handler.NewReview(d.Repos, d.Config, amw, rl, mail)
	router.Mount(api, "/review", rv.Register)

	// ---- 剧集回收站域（删除/审核拒绝的剧集移入此处，仅 admin/superadmin）----
	et := handler.NewEpisodeTrash(d.Repos, amw)
	router.Mount(api, "/episode-trash", et.Register)

	// ---- 分类 / 轮播图 / 后台自动任务 / 版本历史域 ----
	cat := handler.NewCategories(d.Repos, d.Config, amw, rl)
	router.Mount(api, "/categories", cat.Register)

	bn := handler.NewBanners(d.Repos, d.Config, amw, rl)
	router.Mount(api, "/banners", bn.Register)

	as := handler.NewAutoStatus(d.Repos, d.Config, amw, rl)
	router.Mount(api, "/auto-status", as.Register)

	vs := handler.NewVersions(d.Repos, d.Config, amw, rl)
	router.Mount(api, "/versions", vs.Register)

	// ---- RSS 订阅 / 翻译 / 数据备份域（挂载前缀为固定路由表，勿改）----
	rs := handler.NewRSS(d.Repos, d.Config, amw, rl)
	router.Mount(api, "/rss", rs.Register)

	tl := handler.NewTranslate(d.Repos, d.Config, amw, rl)
	router.Mount(api, "/translate", tl.Register)

	bk := handler.NewBackup(d.Repos, d.Config, amw, rl, d.DB)
	router.Mount(api, "/backup", bk.Register)

	// ---- 创作者中心 / 创作者主页 / 站点内容 / 反馈 / 动态流域
	// （挂载前缀为固定路由表，勿改）----
	cr := handler.NewCreator(d.Repos, d.Config, amw, rl)
	router.Mount(api, "/creator", cr.Register)

	cp := handler.NewCreatorProfiles(d.Repos, d.Config, amw, rl)
	router.Mount(api, "/creator-profile", cp.Register)

	sc := handler.NewSiteContents(d.Repos, d.Config, amw, rl, mail)
	router.Mount(api, "/site-content", sc.Register)

	fb := handler.NewFeedback(d.Repos, d.Config, amw, rl, mail)
	router.Mount(api, "/feedback", fb.Register)

	ac := handler.NewActivity(d.Repos, d.Config, amw, rl)
	router.Mount(api, "/activity", ac.Register)

	// 前台公开站点规模数据（用户数/剧集数/在线人数），只读聚合，无鉴权
	ss := handler.NewSiteStats(d.Repos)
	router.Mount(api, "/site-stats", ss.Register)
}
