package service

import (
	"context"
	"html"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Furry-Drama-Tracker/backend/internal/auth"
	"github.com/Furry-Drama-Tracker/backend/internal/code"
	"github.com/Furry-Drama-Tracker/backend/internal/email"
	"github.com/Furry-Drama-Tracker/backend/internal/errors"
	"github.com/Furry-Drama-Tracker/backend/internal/model"
	"github.com/Furry-Drama-Tracker/backend/internal/repository"
)

// LoginInput 是登录请求解析结果。
// Identifier 是登录标识：用户名（accountId）或邮箱（含 @ 即按邮箱查找）。
// Email 字段保留兼容（前端旧版本仍发 email）。
type LoginInput struct {
	Identifier string
	Email      string
	Password   string
	DeviceInfo *auth.DeviceInfoPayload
	Altcha     string
	Ua         string
	DevToken   string
	AcceptLang string
	IP         string
}

// LoginResult 表达登录各分支的响应数据。
type LoginResult struct {
	Email              string
	NeedVerification   bool
	Need2FA            bool
	TwoFactorChallenge string
	// TwoFactorMethods 是登录响应中告知前端可用的第二因素（totp/emailOtp）。
	TwoFactorMethods []string
	User             *model.User
}

// Login 登录分支流程。分支顺序是固定契约，必须严格保持，勿重排。
// 需要 *gin.Context（登录成功时设置 access/refresh cookie）。
func (s *AuthService) Login(c *gin.Context, in LoginInput) (*LoginResult, error) {
	ctx := c.Request.Context()
	identifier := strings.ToLower(strings.TrimSpace(in.Identifier))
	if identifier == "" {
		// 兼容旧客户端：回退到 email 字段。
		identifier = strings.ToLower(strings.TrimSpace(in.Email))
	}

	if !s.VerifyAltcha(in.Altcha, in.DevToken) {
		return nil, errors.New(400, "验证码错误或已过期")
	}

	user, err := s.Repos.Users.FindByLoginIdentifierWithAuth(ctx, identifier)
	if err != nil {
		// 不区分"账号不存在"和"密码错误"，降低账号枚举面。
		return nil, errors.New(400, "用户名或密码错误")
	}
	if repository.IsLocked(user) {
		return nil, errors.New(400, "用户名或密码错误")
	}

	// 注销宽限已过 → 物理删除全部关联数据后仍返回统一错误（不泄露）。
	if user.DeletionRequestedAt != nil {
		deleteAt := user.DeletionRequestedAt.Add(DeletionGraceDays * 24 * time.Hour)
		if time.Now().After(deleteAt) {
			if err := s.purgeUser(ctx, user.ID); err == nil {
				return nil, errors.New(400, "用户名或密码错误")
			}
		}
	}

	if !auth.Compare(user.Password, in.Password) {
		_ = s.Repos.Users.IncLoginAttempts(ctx, user.ID)
		return nil, errors.New(400, "用户名或密码错误")
	}
	_ = s.Repos.Users.ResetLoginAttempts(ctx, user.ID)

	// 邮箱未验证：发验证码（除非 skipVerification 或 dev-token 绕过）。
	if !user.IsEmailVerified && !s.SkipVerification(user) && !s.devTokenActive(in.DevToken) {
		verifyCode := s.EmailVerifyCodes.Mint(code.Entry{
			UserID:    user.ID.Hex(),
			Email:     user.Email,
			ExpiresAt: time.Now().Add(CodeTTL),
			Attempts:  0,
		})
		if s.Mail != nil {
			go s.Mail.SendVerificationCodeEmail(context.Background(), user.Email, verifyCode, "login")
		}
		return &LoginResult{Email: user.Email, NeedVerification: true}, nil
	}

	// skipVerification 且未验证 → 自动置 verified（对齐 L345-348）。
	// 定点 $set：此处 user 来自 FindByLoginIdentifierWithAuth（不含 2FA 密文），整文档替换
	// 会把 twoFactorSecret/twoFactorBackupCodes 清成零值。
	if s.SkipVerification(user) && !user.IsEmailVerified {
		user.IsEmailVerified = true
		_ = s.Repos.Users.UpdateVerified(ctx, user.ID)
	}

	// 2FA 已开启 → 返回 2FA 挑战（JWT 5m purpose=2fa-challenge），对齐 L408-419。
	if user.TwoFactorEnabled {
		challenge, err := s.Signer.Sign(user.ID.Hex(), "2fa-challenge", 5*time.Minute, nil)
		if err != nil {
			return nil, errors.New(500, "服务器错误")
		}
		return &LoginResult{
			Email:              user.Email,
			Need2FA:            true,
			TwoFactorChallenge: challenge,
			TwoFactorMethods:   s.availableTwoFactorMethods(user),
		}, nil
	}

	// 登录成功：更新设备信息 + 签发会话 + 审计 + 新设备提醒。
	ua := c.Request.UserAgent()
	parsed := auth.ParseUserAgent(ua)
	di := auth.BuildDeviceInfo(in.DeviceInfo, parsed, ua, in.AcceptLang)
	ip := s.ClientIP(c)
	region := "未知"
	if s.IPRegion != nil {
		region = s.IPRegion.GetRegion(ctx, ip)
	}
	now := time.Now()
	user.DeviceInfo = di
	user.LastLoginAt = &now
	user.LastLoginIP = ip
	user.LastLoginRegion = region
	// 登录态字段定点更新（勿整文档替换）：并发密码轮换落库后，
	// 本请求早前加载的旧结构若整体回写会把新哈希回滚（复活旧密码）。
	_ = s.Repos.Users.UpdateDeviceInfoAndLogin(ctx, user.ID, di, now, ip, region)

	if err := s.IssueSession(c, user, di, ip, "LOGIN"); err != nil {
		return nil, errors.New(500, "服务器错误")
	}
	// 未识别设备 → 发通知邮件（受 emailNotificationPrefs.newDeviceLogin 控制）。
	// 已识别设备不再重复提醒。通知是"事后提醒"，不再拦截登录。
	if s.Mail != nil && !s.isKnownDevice(ctx, c, user.ID) {
		go s.sendNewDeviceNotification(user, parsed, ip, region)
	}
	return &LoginResult{User: user}, nil
}

// isKnownDevice 以服务端签发的设备令牌（fdt-device cookie ↔ 会话 deviceTokenHash）
// 判定已知设备。User-Agent 客户端完全可控、可枚举重放（CWE-290），仅作展示，
// 不再作为信任依据。旧会话无 deviceTokenHash → 不匹配（fail-closed）。
func (s *AuthService) isKnownDevice(ctx context.Context, c *gin.Context, userID interface{}) bool {
	deviceToken := auth.GetDeviceToken(c)
	if deviceToken == "" {
		return false
	}
	tokenHash := auth.HashToken(deviceToken)
	knownSessions, err := s.Repos.Sessions.FindActiveByUser(ctx, userID)
	if err != nil {
		return false
	}
	for _, sess := range knownSessions {
		if sess.DeviceTokenHash != "" && sess.DeviceTokenHash == tokenHash {
			return true
		}
	}
	return false
}

// availableTwoFactorMethods 返回用户可用的第二因素方法列表。
// totp 恒有（TOTP 密钥在 2FA 开启时必已注册）；emailOtp 仅邮箱已验证时可用。
func (s *AuthService) availableTwoFactorMethods(u *model.User) []string {
	methods := []string{"totp"}
	if u.IsEmailVerified && u.Email != "" {
		methods = append(methods, "emailOtp")
	}
	return methods
}

func (s *AuthService) devTokenActive(devToken string) bool {
	return s.Config.JWT.DevAPIToken != "" && devToken == s.Config.JWT.DevAPIToken
}

// purgeUser 注销宽限已过时的物理删除（行为固定）。
func (s *AuthService) purgeUser(ctx context.Context, userID interface{}) error {
	return PurgeUserData(ctx, s.Repos, userID)
}

// sendNewDeviceNotification 新设备登录提醒邮件（受 emailNotificationPrefs.newDeviceLogin 控制）。
func (s *AuthService) sendNewDeviceNotification(u *model.User, parsed auth.ParsedUA, ip, region string) {
	if !u.EmailNotificationPrefs.NewDeviceLogin || !u.IsEmailVerified {
		return
	}
	_ = s.Mail.SendNotificationEmail(context.Background(), u.Email, "新设备登录提醒 - 兽剧聚合平台",
		`<h2 style="margin:0 0 16px;color:#1e293b;font-size:22px;font-weight:700;">新设备登录提醒</h2>`+
			`<p style="margin:0 0 16px;color:#475569;font-size:14px;">您的账号检测到一次新的设备登录：</p>`+
			email.EmailInfoBox(
				`<p style="margin:4px 0;"><strong>浏览器：</strong>`+html.EscapeString(orUnknown(parsed.Browser))+" "+html.EscapeString(parsed.BrowserVersion)+`</p>`+
					`<p style="margin:4px 0;"><strong>操作系统：</strong>`+html.EscapeString(orUnknown(parsed.OS))+" "+html.EscapeString(parsed.OSVersion)+`</p>`+
					`<p style="margin:4px 0;"><strong>IP地址：</strong>`+html.EscapeString(ip)+`</p>`+
					`<p style="margin:4px 0;"><strong>地域：</strong>`+html.EscapeString(orUnknown(region))+`</p>`, "info"),
		"检测到新设备登录")
}

func orUnknown(s string) string {
	if s == "" {
		return "未知"
	}
	return s
}
