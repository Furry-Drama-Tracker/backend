package handler

import (
	"net/url"

	"github.com/gin-gonic/gin"

	"github.com/Furry-Drama-Tracker/backend/internal/auth"
	"github.com/Furry-Drama-Tracker/backend/internal/middleware"
)

// twoFactorKeyHex 派生 2FA 字段加密密钥（ENCRYPTION_KEY 或 JWT_SECRET）。
func (h *Auth) twoFactorKeyHex() string {
	return auth.FieldKey(h.Config.JWT.EncryptionKey, h.Config.JWT.Secret)
}

// Enable2FA POST /api/2fa/enable（protect）：生成 TOTP secret + 备份码并加密存储。
// 已启用时直接拒绝——否则一次带有效会话的请求就能把 2FA 静默关掉（twoFactorEnabled=false）。
// 角色门控：普通用户禁开（2FA 是创作者及以上才开放的能力）；创作者可选。
// @Summary 开启 2FA（生成密钥）
// @Tags 2FA
// @Security bearerAuth
// @Produce json
// @Success 200 {object} map[string]any "secret/backupCodes/otpauthUrl"
// @Router /2fa/enable [post]
func (h *Auth) Enable2FA(c *gin.Context) {
	user, _ := middleware.GetUser(c)
	if user.Role == middleware.RoleUser {
		c.JSON(403, gin.H{"message": "普通用户不可开启两步验证"})
		return
	}
	u, err := h.Svc.Repos.Users.FindByIDWithAllSecrets(c.Request.Context(), user.ID)
	if err != nil {
		c.JSON(500, gin.H{"message": "服务器错误"})
		return
	}
	// 已启用：不允许用 enable 覆盖既有 secret（那等价于无验证关闭 2FA）。
	// 需要重置请先走 /2fa/disable（要求 TOTP 或备份码）。
	if u.TwoFactorEnabled {
		c.JSON(400, gin.H{"message": "2FA already enabled; disable it before re-enrolling"})
		return
	}
	secret := auth.GenerateSecret()
	backup := auth.GenerateBackupCodes()
	keyHex := h.twoFactorKeyHex()
	// secret 需要可逆（要复算 TOTP），备份码只需单向验证 → 只存哈希。
	if err := h.Svc.Repos.Users.SetTwoFactorSetup(c.Request.Context(), user.ID,
		auth.EncryptField(secret, keyHex), auth.HashBackupCodes(backup), false); err != nil {
		c.JSON(500, gin.H{"message": "服务器错误"})
		return
	}
	h.Svc.Audit(c.Request.Context(), u, "2FA_SETUP_INITIATED", "auth", "User initiated 2FA setup", h.clientIP(c))
	otpauthURL := "otpauth://totp/FurryDrama:" + url.QueryEscape(u.AccountID) + "?secret=" + url.QueryEscape(secret) + "&issuer=FurryDrama"
	c.JSON(200, gin.H{"secret": secret, "backupCodes": backup, "otpauthUrl": otpauthURL})
}

// VerifyEnable2FA POST /api/2fa/verify-enable（protect）：TOTP 校验后启用 2FA。
// @Summary 确认开启 2FA
// @Tags 2FA
// @Security bearerAuth
// @Accept json
// @Param body body object true "TOTP 码"
// @Success 200 {object} map[string]string
// @Router /2fa/verify-enable [post]
func (h *Auth) VerifyEnable2FA(c *gin.Context) {
	var req struct {
		Token string `json:"token"`
	}
	_ = c.ShouldBindJSON(&req)
	user, _ := middleware.GetUser(c)
	if user.Role == middleware.RoleUser {
		c.JSON(403, gin.H{"message": "普通用户不可开启两步验证"})
		return
	}
	u, err := h.Svc.Repos.Users.FindByIDWithAllSecrets(c.Request.Context(), user.ID)
	if err != nil || u.TwoFactorSecret == "" {
		c.JSON(400, gin.H{"message": "2FA not set up"})
		return
	}
	secret := auth.DecryptField(u.TwoFactorSecret, h.twoFactorKeyHex())
	if !auth.VerifyTOTP(secret, req.Token) {
		c.JSON(400, gin.H{"message": "Invalid verification code"})
		return
	}
	if err := h.Svc.Repos.Users.EnableTwoFactor(c.Request.Context(), user.ID); err != nil {
		c.JSON(500, gin.H{"message": "服务器错误"})
		return
	}
	h.Svc.Audit(c.Request.Context(), u, "2FA_ENABLED", "auth", "User enabled 2FA", h.clientIP(c))
	c.JSON(200, gin.H{"message": "2FA enabled successfully"})
}

// Disable2FA POST /api/2fa/disable（protect）：TOTP/备份码校验后关闭 2FA。
// @Summary 关闭 2FA
// @Tags 2FA
// @Security bearerAuth
// @Accept json
// @Param body body object true "TOTP 或备份码"
// @Success 200 {object} map[string]string
// @Router /2fa/disable [post]
func (h *Auth) Disable2FA(c *gin.Context) {
	var req struct {
		Token string `json:"token"`
	}
	_ = c.ShouldBindJSON(&req)
	user, _ := middleware.GetUser(c)
	// 管理员/超管强制开启 2FA：不允许自行关闭（解锁走 useradmin CLI 的 clear-2fa，
	// CLI 会吊销全部会话并要求重新走强制设置流程）。
	if user.Role == middleware.RoleAdmin || user.Role == middleware.RoleSuperAdmin {
		c.JSON(403, gin.H{"message": "管理员账号强制开启两步验证，不可自行关闭"})
		return
	}
	u, err := h.Svc.Repos.Users.FindByIDWithAllSecrets(c.Request.Context(), user.ID)
	if err != nil || !u.TwoFactorEnabled {
		c.JSON(400, gin.H{"message": "2FA not enabled"})
		return
	}
	keyHex := h.twoFactorKeyHex()
	secret := auth.DecryptField(u.TwoFactorSecret, keyHex)
	// 配置完整性兜底（ADR-019）：密钥与备份码均已丢失（如历史版本对投影加载的
	// user 做整文档 Save 把 twoFactorSecret 清空）时，2FA 已不存在任何可验证
	// 凭据——继续要求验证码会把账户永久锁死（登录侧 Login2FA 对空 secret 同样
	// 不尝试校验）。此时允许持有效会话的账户所有者直接关闭 2FA 重新设置；
	// 正常账户（secret 或备份码非空）行为不变，仍必须通过 TOTP/备份码校验。
	if secret == "" && len(u.TwoFactorBackupCodes) == 0 {
		if err := h.Svc.Repos.Users.DisableTwoFactor(c.Request.Context(), user.ID); err != nil {
			c.JSON(500, gin.H{"message": "服务器错误"})
			return
		}
		h.Svc.Audit(c.Request.Context(), u, "2FA_DISABLED", "auth", "User disabled 2FA (no verifiable credential stored)", h.clientIP(c))
		c.JSON(200, gin.H{"message": "2FA disabled successfully"})
		return
	}
	// 备份码命中即视为消费（命中旧格式时一并升级为哈希）；随后 DisableTwoFactor
	// 会清空全部备份码，故无需另行写回。
	_, backupHit := auth.ConsumeBackupCode(u.TwoFactorBackupCodes, req.Token, keyHex)
	if !auth.VerifyTOTP(secret, req.Token) && !backupHit {
		c.JSON(400, gin.H{"message": "Invalid verification code"})
		return
	}
	if err := h.Svc.Repos.Users.DisableTwoFactor(c.Request.Context(), user.ID); err != nil {
		c.JSON(500, gin.H{"message": "服务器错误"})
		return
	}
	h.Svc.Audit(c.Request.Context(), u, "2FA_DISABLED", "auth", "User disabled 2FA", h.clientIP(c))
	c.JSON(200, gin.H{"message": "2FA disabled successfully"})
}

// Verify2FA POST /api/2fa/verify（protect）：校验 TOTP/备份码。
// @Summary 校验 2FA 码
// @Tags 2FA
// @Security bearerAuth
// @Accept json
// @Param body body object true "TOTP 或备份码"
// @Success 200 {object} map[string]bool "verified"
// @Router /2fa/verify [post]
func (h *Auth) Verify2FA(c *gin.Context) {
	var req struct {
		Token string `json:"token"`
	}
	_ = c.ShouldBindJSON(&req)
	user, _ := middleware.GetUser(c)
	u, err := h.Svc.Repos.Users.FindByIDWithAllSecrets(c.Request.Context(), user.ID)
	if err != nil || !u.TwoFactorEnabled {
		c.JSON(400, gin.H{"message": "2FA not enabled for this account"})
		return
	}
	keyHex := h.twoFactorKeyHex()
	secret := auth.DecryptField(u.TwoFactorSecret, keyHex)
	remaining, backupHit := auth.ConsumeBackupCode(u.TwoFactorBackupCodes, req.Token, keyHex)
	if !auth.VerifyTOTP(secret, req.Token) && !backupHit {
		c.JSON(400, gin.H{"message": "Invalid verification code"})
		return
	}
	// 备份码命中 → 消费该码（remaining 已是哈希格式，直接落库）。
	if backupHit {
		_ = h.Svc.Repos.Users.SetTwoFactorSetup(c.Request.Context(), user.ID,
			u.TwoFactorSecret, remaining, true)
	}
	c.JSON(200, gin.H{"verified": true})
}
