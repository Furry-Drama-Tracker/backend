package service

import (
	"context"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Furry-Drama-Tracker/backend/internal/auth"
	"github.com/Furry-Drama-Tracker/backend/internal/code"
	"github.com/Furry-Drama-Tracker/backend/internal/errors"
	"github.com/Furry-Drama-Tracker/backend/internal/model"
	"github.com/Furry-Drama-Tracker/backend/internal/repository"
)

// Login2FA POST /api/auth/login-2fa：第二因素校验后完成登录（流程为固定契约）。
// method 决定第二因素类型："totp"（默认，TOTP 码或备份码）/"emailOtp"（邮箱验证码）。
func (s *AuthService) Login2FA(c *gin.Context, identifier, challenge, method, token string, deviceInfo *auth.DeviceInfoPayload) (*LoginResult, error) {
	if challenge == "" {
		return nil, errors.New(400, "缺少2FA挑战令牌，请重新登录")
	}
	challengeClaims, err := s.Signer.Verify(challenge)
	if err != nil {
		return nil, errors.New(400, "2FA挑战令牌已过期或无效，请重新登录")
	}
	if challengeClaims.Purpose != "2fa-challenge" {
		return nil, errors.New(400, "无效的2FA挑战令牌")
	}

	id := strings.ToLower(strings.TrimSpace(identifier))
	user, err := s.Repos.Users.FindByLoginIdentifierWith2FA(c.Request.Context(), id)
	if err != nil || !user.TwoFactorEnabled {
		return nil, errors.New(400, "该账号未启用两步验证")
	}
	if user.ID.Hex() != challengeClaims.ID {
		return nil, errors.New(400, "验证失败")
	}
	if repository.IsLocked(user) {
		return nil, errors.New(423, "账号已被锁定，请30分钟后再试")
	}
	if !user.IsEmailVerified && !s.SkipVerification(user) && !s.devTokenActive(c.GetHeader("x-dev-token")) {
		return nil, errors.New(403, "请先验证邮箱后再登录")
	}

	totpOK := false
	backupHit := false
	var totpStep int64
	var remaining []string

	switch method {
	case "emailOtp":
		// 邮箱 OTP 第二因素：一次性消费 + 尝试上限。
		if !s.consumeEmailOTP(user.ID.Hex(), token) {
			return nil, s.rejectTwoFactor(c.Request.Context(), user)
		}
	default:
		// TOTP / 备份码（默认路径，保持既有语义）。
		keyHex := auth.FieldKey(s.Config.JWT.EncryptionKey, s.Config.JWT.Secret)
		secret := auth.DecryptField(user.TwoFactorSecret, keyHex)
		// 备份码匹配 + 消费：命中时返回已升级为哈希格式的剩余数组。
		remaining, backupHit = auth.ConsumeBackupCode(user.TwoFactorBackupCodes, token, keyHex)
		if secret != "" {
			totpStep, totpOK = auth.VerifyTOTPStep(secret, token, time.Now())
		}
		if !totpOK && !backupHit {
			return nil, s.rejectTwoFactor(c.Request.Context(), user)
		}
		// TOTP 一次性消费：同一时间步只能被接受一次（防重放）。
		if totpOK {
			accepted, err := s.Repos.Users.MarkTOTPStepUsed(c.Request.Context(), user.ID, totpStep)
			if err != nil {
				return nil, errors.New(500, "服务器错误")
			}
			if !accepted {
				// 同一时间步重复提交：可能是重放，也可能是用户重复点击。
				uid := user.ID
				s.Rec.Record(model.AuthEvent{
					Event:  model.AuthEventTwoFAReplay,
					UserID: &uid,
					Email:  user.Email,
					Detail: "totp step already consumed",
				})
				return nil, s.rejectTwoFactor(c.Request.Context(), user)
			}
			// 同步内存值（MarkTOTPStepUsed 已原子落库；登录态字段改走定点更新）。
			user.TwoFactorLastStep = totpStep
		}
	}
	_ = s.Repos.Users.ResetLoginAttempts(c.Request.Context(), user.ID)

	// 备份码命中 → 已消费，写回（同时把旧格式就地升级为哈希）。
	if backupHit {
		user.TwoFactorBackupCodes = remaining
	}

	ua := c.Request.UserAgent()
	parsed := auth.ParseUserAgent(ua)
	ip := s.ClientIP(c)
	di := auth.BuildDeviceInfo(deviceInfo, parsed, ua, c.GetHeader("Accept-Language"))
	now := time.Now()
	user.DeviceInfo = di
	user.LastLoginAt = &now
	user.LastLoginIP = ip
	region := "未知"
	if s.IPRegion != nil {
		region = s.IPRegion.GetRegion(c.Request.Context(), ip)
	}
	user.LastLoginRegion = region
	// 登录态字段定点更新（勿整文档替换）：user 虽由 FindByLoginIdentifierWith2FA 全字段加载，
	// 但整体回写仍会把请求加载后并发落库的密码轮换/锁定计数回滚（复活旧凭据）。
	// 备份码消费单独定点落库（其内存同步已在上方完成）。
	_ = s.Repos.Users.UpdateDeviceInfoAndLogin(c.Request.Context(), user.ID, di, now, ip, region)
	if backupHit {
		_ = s.Repos.Users.UpdateTwoFactorBackupCodes(c.Request.Context(), user.ID, remaining)
	}

	if err := s.IssueSession(c, user, di, ip, "LOGIN_2FA"); err != nil {
		return nil, errors.New(500, "服务器错误")
	}
	// 未识别设备 → 通知邮件（受偏好控制）；已识别设备不重复提醒。
	if s.Mail != nil && !s.isKnownDevice(c.Request.Context(), c, user.ID) {
		go s.sendNewDeviceNotification(user, parsed, ip, region)
	}
	return &LoginResult{User: user}, nil
}

// Send2FAEmailOTP POST /api/auth/2fa/email-otp/send：凭 2FA 挑战令牌向账号邮箱
// 发送第二因素验证码（未验证邮箱的账号不提供该方法）。
func (s *AuthService) Send2FAEmailOTP(ctx context.Context, challenge, devToken string) (string, error) {
	if challenge == "" {
		return "", errors.New(400, "缺少2FA挑战令牌，请重新登录")
	}
	claims, err := s.Signer.Verify(challenge)
	if err != nil || claims.Purpose != "2fa-challenge" {
		return "", errors.New(400, "2FA挑战令牌已过期或无效，请重新登录")
	}
	user, err := s.Repos.Users.FindByID(ctx, claims.ID)
	if err != nil {
		return "", errors.New(400, "用户不存在")
	}
	if !user.TwoFactorEnabled {
		return "", errors.New(400, "该账号未启用两步验证")
	}
	if user.Email == "" || !user.IsEmailVerified {
		return "", errors.New(400, "该账号未绑定已验证的邮箱")
	}
	otpCode := s.EmailOTPCodes.Mint(code.Entry{
		UserID:    user.ID.Hex(),
		Email:     user.Email,
		ExpiresAt: time.Now().Add(CodeTTL),
		Attempts:  0,
	})
	if s.Mail != nil {
		go s.Mail.SendVerificationCodeEmail(context.Background(), user.Email, otpCode, "login")
	}
	return user.Email, nil
}

// consumeEmailOTP 校验并一次性消费邮件 OTP（key=code，与验证码存储共享命名空间
// 但独立 store 实例）。
func (s *AuthService) consumeEmailOTP(userID, otpCode string) bool {
	otpCode = strings.TrimSpace(otpCode)
	if otpCode == "" {
		return false
	}
	entry, ok := s.EmailOTPCodes.Get(otpCode)
	if !ok || time.Now().After(entry.ExpiresAt) {
		if ok {
			s.EmailOTPCodes.Delete(otpCode)
		}
		return false
	}
	entry.Attempts++
	if entry.Attempts > 5 {
		s.EmailOTPCodes.Delete(otpCode)
		return false
	}
	if entry.UserID != userID {
		// 码属于别的账号：按校验失败处理，不消费、不留痕（防跨账号枚举）。
		return false
	}
	s.EmailOTPCodes.Set(otpCode, entry)
	s.EmailOTPCodes.Delete(otpCode)
	return true
}

// rejectTwoFactor 处理 2FA 校验失败：累计失败次数，达到阈值时返回 423 锁定，
// 否则返回 400 验证码无效（失败计数不清零，重放同样计入）。
func (s *AuthService) rejectTwoFactor(ctx context.Context, user *model.User) error {
	_ = s.Repos.Users.IncLoginAttempts(ctx, user.ID)
	nextAttempts := user.LoginAttempts + 1
	if repository.IsLocked(user) || nextAttempts >= s.Config.Security.LoginMaxAttempts {
		return errors.New(423, "账号已被锁定，请30分钟后再试")
	}
	return errors.New(400, "验证码无效")
}
