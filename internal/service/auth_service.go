// Package service 承载认证与用户域的领域逻辑（登录分支流、refresh 轮换、
// 设备登录码链、2FA、导出）。handler 层负责 HTTP 解析与响应组装。
package service

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"strings"
	"time"

	altchalib "github.com/altcha-org/altcha-lib-go/v2"
	"github.com/gin-gonic/gin"

	"github.com/Furry-Drama-Tracker/backend/internal/auth"
	"github.com/Furry-Drama-Tracker/backend/internal/authlog"
	"github.com/Furry-Drama-Tracker/backend/internal/code"
	"github.com/Furry-Drama-Tracker/backend/internal/config"
	"github.com/Furry-Drama-Tracker/backend/internal/email"
	"github.com/Furry-Drama-Tracker/backend/internal/ipregion"
	"github.com/Furry-Drama-Tracker/backend/internal/model"
	"github.com/Furry-Drama-Tracker/backend/internal/repository"
)

// DefaultSuperAdminEmail 是超管默认邮箱（requireEmailChanged 判定）。
const DefaultSuperAdminEmail = "admin@furry09.com"

// DeletionGraceDays 是注销宽限期（对齐 7 天）。
const DeletionGraceDays = 7

// CodeTTL 是验证码有效期（对齐 10 分钟）。
const CodeTTL = 10 * time.Minute

// AuthService 是认证域服务，聚合认证所需的全部依赖。
type AuthService struct {
	Repos    *repository.Repos
	Signer   *auth.Signer
	Mail     *email.Client
	IPRegion *ipregion.Client
	Config   *config.Config

	EmailVerifyCodes *code.Store
	DeviceLoginCodes *code.Store

	// Rec 是认证诊断事件记录器（可选，nil 时静默）。
	Rec *authlog.Recorder
}

// NewAuthService 构造认证服务并初始化验证码内存存储。
func NewAuthService(cfg *config.Config, repos *repository.Repos, signer *auth.Signer, mail *email.Client, ipr *ipregion.Client) *AuthService {
	return &AuthService{
		Repos:            repos,
		Signer:           signer,
		Mail:             mail,
		IPRegion:         ipr,
		Config:           cfg,
		EmailVerifyCodes: code.NewStore(CodeTTL),
		DeviceLoginCodes: code.NewStore(CodeTTL),
	}
}

// AltchaHMACSecret 派生 altcha HMAC 签名密钥字符串：优先配置，
// 缺省 sha256("altcha-"+JWT_SECRET) 的 hex（固定派生规则，勿改）。
func (s *AuthService) AltchaHMACSecret() string {
	if k := s.Config.JWT.AltchaHMACKey; k != "" {
		return k
	}
	sum := sha256.Sum256([]byte("altcha-" + s.Config.JWT.Secret))
	return hex.EncodeToString(sum[:])
}

// VerifyAltcha 校验 altcha payload（官方 altcha-lib-go）；DEV_API_TOKEN 配置且
// x-dev-token 匹配时直接通过（固定行为）。
func (s *AuthService) VerifyAltcha(payload, devToken string) bool {
	if s.Config.JWT.DevAPIToken != "" && devToken == s.Config.JWT.DevAPIToken {
		return true
	}
	if payload == "" {
		return false
	}
	raw, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return false
	}
	var p altchalib.Payload
	if err := json.Unmarshal(raw, &p); err != nil {
		return false
	}
	res, err := altchalib.VerifySolution(altchalib.VerifySolutionOptions{
		Challenge:           p.Challenge,
		Solution:            p.Solution,
		DeriveKey:           altchalib.DeriveKeySHA(),
		HMACSignatureSecret: s.AltchaHMACSecret(),
	})
	return err == nil && res.Verified
}

// CreateCaptcha 生成 altcha 挑战（官方库；SHA-256, cost 10000, keyPrefix "00", 5min 过期）。
// 挑战参数为固定契约，勿改（前端求解器按同参数求解）。
func (s *AuthService) CreateCaptcha() (altchalib.Challenge, error) {
	expires := time.Now().Add(5 * time.Minute)
	return altchalib.CreateChallenge(altchalib.CreateChallengeOptions{
		Algorithm:           "SHA-256",
		DeriveKey:           altchalib.DeriveKeySHA(),
		Cost:                10000,
		KeyLength:           32,
		KeyPrefix:           "00",
		HMACSignatureSecret: s.AltchaHMACSecret(),
		ExpiresAt:           &expires,
	})
}

// SkipVerification 判断是否跳过邮箱/设备验证：仅非生产且邮箱在 DEMO_EMAILS 中
// （固定行为）。
func (s *AuthService) SkipVerification(u *model.User) bool {
	if s.Config.IsDev && containsFoldSlice(s.Config.JWT.DemoEmails, strings.ToLower(u.Email)) {
		return true
	}
	return false
}

// ForceEmailChange 判断超管是否未改默认邮箱（响应中的 forceEmailChange 字段）。
func (s *AuthService) ForceEmailChange(u *model.User) bool {
	return u.Role == "superadmin" && u.Email == DefaultSuperAdminEmail
}

// SessionUserJSON 组装登录/refresh 响应的用户对象（字段集为固定契约，勿增删）。
func (s *AuthService) SessionUserJSON(u *model.User) gin.H {
	return gin.H{
		"_id":                u.ID.Hex(),
		"accountId":          u.AccountID,
		"username":           u.Username,
		"email":              u.Email,
		"isEmailVerified":    u.IsEmailVerified,
		"role":               u.Role,
		"forceEmailChange":   s.ForceEmailChange(u),
		"backgroundPrefs":    u.BackgroundPrefs,
		"personalWallpapers": nonNilWallpapers(u.PersonalWallpapers),
	}
}

// MeJSON 组装 /me 响应（字段集为固定契约，勿增删）。
func (s *AuthService) MeJSON(u *model.User) gin.H {
	return gin.H{
		"_id":                    u.ID.Hex(),
		"accountId":              u.AccountID,
		"username":               u.Username,
		"email":                  u.Email,
		"isEmailVerified":        u.IsEmailVerified,
		"role":                   u.Role,
		"avatar":                 u.Avatar,
		"emailNotificationPrefs": u.EmailNotificationPrefs,
		"backgroundPrefs":        u.BackgroundPrefs,
		"personalWallpapers":     nonNilWallpapers(u.PersonalWallpapers),
	}
}

// nonNilWallpapers 把 nil 墙纸数组归一化为空数组（输出恒为数组，固定契约）。
func nonNilWallpapers(w []model.Wallpaper) []model.Wallpaper {
	if w == nil {
		return []model.Wallpaper{}
	}
	return w
}

// IssueSession 签发双 token、创建会话、设置 cookie 并写审计日志（登录成功路径）。
func (s *AuthService) IssueSession(c *gin.Context, u *model.User, deviceInfo model.DeviceInfo, ip string, action string) error {
	accessToken, err := s.Signer.Sign(u.ID.Hex(), "access", s.Config.JWT.AccessTTL, map[string]string{"jti": auth.RandomHex(24)})
	if err != nil {
		return err
	}
	// refresh 带随机 jti（固定规则，勿改——jti 用于会话轮换标识），保证每次轮换生成唯一令牌。
	refreshToken, err := s.Signer.Sign(u.ID.Hex(), "refresh", s.Config.JWT.RefreshTTL, map[string]string{"jti": auth.RandomHex(24)})
	if err != nil {
		return err
	}
	session := &model.UserSession{
		UserID:           u.ID,
		TokenHash:        auth.HashToken(accessToken),
		RefreshTokenHash: auth.HashToken(refreshToken),
		DeviceInfo:       deviceInfo,
		IP:               ip,
		IsActive:         true,
		LoginAt:          time.Now(),
		LastActiveAt:     time.Now(),
	}
	if err := s.Repos.Sessions.Create(c.Request.Context(), session); err != nil {
		return err
	}
	auth.SetAuthCookies(c, accessToken, refreshToken, !s.Config.IsDev)
	// 审计（失败静默）。
	go s.Audit(c.Request.Context(), u, action, "auth", "User "+strings.ToLower(action)+" success", ip)
	return nil
}

// Audit 写审计日志（失败静默，对齐 logManual 的 catch(()=>{})）。
func (s *AuthService) Audit(ctx context.Context, u *model.User, action, target, details, ip string) {
	userID := u.ID
	userName := u.Username
	if userName == "" {
		userName = u.AccountID
	}
	_ = s.Repos.AuditLogs.Create(ctx, &model.AuditLog{
		UserID:   &userID,
		UserName: userName,
		Action:   action,
		Target:   target,
		Details:  details,
		IP:       ip,
	})
}

// ClientIP 返回客户端 IP。解析规则由引擎级代理标头配置统一决定
// （server/app.go 按 [server] 配置装配：标头清单、信任开关、受信代理来源校验），
// 本层不再手写解析。
func (s *AuthService) ClientIP(c *gin.Context) string {
	return c.ClientIP()
}

// EmailRegex 是邮箱格式校验（对齐 register L169）。
var EmailRegex = regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`)

// AccountIDRegex 是 accountId 校验（对齐 register L172）。
var AccountIDRegex = regexp.MustCompile(`^[a-zA-Z0-9_]+$`)

func containsFoldSlice(list []string, v string) bool {
	for _, x := range list {
		if strings.EqualFold(x, v) {
			return true
		}
	}
	return false
}
