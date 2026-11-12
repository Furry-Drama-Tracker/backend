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

// DeviceVerifyResult 是 verify-device 的响应。
type DeviceVerifyResult struct {
	Verified  bool
	LoginCode string
}

// VerifyDevice POST /api/auth/verify-device：校验邮箱内的一次性验证链接，
// 生成设备登录码返回给前端（流程为固定契约）。
func (s *AuthService) VerifyDevice(ctx context.Context, token string) (*DeviceVerifyResult, error) {
	if token == "" {
		return nil, errors.New(400, "缺少验证令牌")
	}
	tokenHash := auth.HashToken(token)
	if used, err := s.Repos.UsedTokens.IsUsed(ctx, tokenHash); err == nil && used {
		return nil, errors.New(400, "该验证链接已被使用")
	}
	claims, err := s.Signer.Verify(token)
	if err != nil {
		if err == auth.ErrTokenExpired {
			return nil, errors.New(400, "验证链接已过期，请重新登录")
		}
		return nil, errors.New(400, "验证失败")
	}
	if claims.Purpose != "device-verify" {
		return nil, errors.New(400, "无效的验证令牌")
	}
	user, err := s.Repos.Users.FindByID(ctx, claims.ID)
	if err != nil {
		return nil, errors.New(400, "用户不存在")
	}
	_ = s.Repos.UsedTokens.MarkUsed(ctx, tokenHash, "device-verify", 30*time.Minute)

	loginCode := s.DeviceLoginCodes.Mint(code.Entry{
		UserID:    user.ID.Hex(),
		ExpiresAt: time.Now().Add(CodeTTL),
		Need2FA:   user.TwoFactorEnabled,
		Attempts:  0,
	})
	return &DeviceVerifyResult{Verified: true, LoginCode: loginCode}, nil
}

// ConfirmDeviceLogin POST /api/auth/confirm-device-login：用户输入 6 位码完成登录
// 或进入 2FA 流程（分支顺序为固定契约，勿重排）。
func (s *AuthService) ConfirmDeviceLogin(c *gin.Context, loginCode string) (*LoginResult, error) {
	if loginCode == "" {
		return nil, errors.New(400, "请输入验证码")
	}
	entry, ok := s.DeviceLoginCodes.Get(loginCode)
	if !ok || time.Now().After(entry.ExpiresAt) {
		if ok {
			s.DeviceLoginCodes.Delete(loginCode)
		}
		// miss 记账（ADR-019）：guess/喷洒不命中时此前零状态零痕迹，尝试上限
		// 只数命中、防不住枚举；把 miss 留痕到 authevents（按 IP 节流落库），
		// 使喷洒式猜测在排障链路可见。
		s.Rec.Record(model.AuthEvent{
			Event:  model.AuthEventDeviceCodeMiss,
			IP:     s.ClientIP(c),
			UA:     c.Request.UserAgent(),
			Path:   c.Request.URL.Path,
			Detail: "login code miss",
		})
		return nil, errors.New(400, "验证码无效或已过期，请重新验证")
	}
	entry.Attempts++
	if entry.Attempts > 5 {
		s.DeviceLoginCodes.Delete(loginCode)
		return nil, errors.New(400, "尝试次数过多，验证码已作废，请重新验证")
	}
	// 更新计数（保留条目）
	s.DeviceLoginCodes.Set(loginCode, entry)

	user, err := s.Repos.Users.FindByIDWithAuth(c.Request.Context(), entry.UserID)
	if err != nil {
		s.DeviceLoginCodes.Delete(loginCode)
		return nil, errors.New(400, "用户不存在")
	}

	if entry.Need2FA {
		challenge, err := s.Signer.Sign(user.ID.Hex(), "2fa-challenge", 5*time.Minute, map[string]string{
			"deviceLoginCode": loginCode,
		})
		if err != nil {
			return nil, errors.New(500, "服务器错误")
		}
		return &LoginResult{Email: user.Email, Need2FA: true, TwoFactorChallenge: challenge}, nil
	}

	s.DeviceLoginCodes.Delete(loginCode)

	// 完成登录。
	ua := c.Request.UserAgent()
	parsed := auth.ParseUserAgent(ua)
	ip := s.ClientIP(c)
	di := auth.BuildDeviceInfo(nil, parsed, ua, c.GetHeader("Accept-Language"))
	now := time.Now()
	user.DeviceInfo = di
	user.LastLoginAt = &now
	user.LastLoginIP = ip
	region := "未知"
	if s.IPRegion != nil {
		region = s.IPRegion.GetRegion(c.Request.Context(), ip)
	}
	user.LastLoginRegion = region
	// 登录态字段定点更新（勿整文档替换）：本请求早前加载的旧结构若整体回写，
	// 会回滚并发落库的密码轮换（复活旧凭据）。
	_ = s.Repos.Users.UpdateDeviceInfoAndLogin(c.Request.Context(), user.ID, di, now, ip, region)

	if err := s.IssueSession(c, user, di, ip, "LOGIN"); err != nil {
		return nil, errors.New(500, "服务器错误")
	}
	if s.Mail != nil {
		go s.sendNewDeviceNotification(user, parsed, ip, region)
	}
	return &LoginResult{User: user}, nil
}

// Login2FA POST /api/auth/login-2fa：TOTP 或备份码校验后完成登录（流程为固定契约）。
func (s *AuthService) Login2FA(c *gin.Context, emailAddr, challenge, token string, deviceInfo *auth.DeviceInfoPayload) (*LoginResult, error) {
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

	user, err := s.Repos.Users.FindByEmailWith2FA(c.Request.Context(), strings.ToLower(strings.TrimSpace(emailAddr)))
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

	keyHex := auth.FieldKey(s.Config.JWT.EncryptionKey, s.Config.JWT.Secret)
	secret := auth.DecryptField(user.TwoFactorSecret, keyHex)
	// 备份码匹配 + 消费：命中时返回已升级为哈希格式的剩余数组。
	remaining, backupHit := auth.ConsumeBackupCode(user.TwoFactorBackupCodes, token, keyHex)

	var totpStep int64
	totpOK := false
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
	// 登录态字段定点更新（勿整文档替换）：user 虽由 FindByEmailWith2FA 全字段加载，
	// 但整体回写仍会把请求加载后并发落库的密码轮换/锁定计数回滚（复活旧凭据）。
	// 备份码消费单独定点落库（其内存同步已在上方完成）。
	_ = s.Repos.Users.UpdateDeviceInfoAndLogin(c.Request.Context(), user.ID, di, now, ip, region)
	if backupHit {
		_ = s.Repos.Users.UpdateTwoFactorBackupCodes(c.Request.Context(), user.ID, remaining)
	}

	if err := s.IssueSession(c, user, di, ip, "LOGIN_2FA"); err != nil {
		return nil, errors.New(500, "服务器错误")
	}
	// 来自设备验证流程 → 删除一次性登录码。
	if challengeClaims.DeviceLoginCode != "" {
		s.DeviceLoginCodes.Delete(challengeClaims.DeviceLoginCode)
	}
	if s.Mail != nil {
		go s.sendNewDeviceNotification(user, parsed, ip, region)
	}
	return &LoginResult{User: user}, nil
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
