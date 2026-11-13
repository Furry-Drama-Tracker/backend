package handler

import (
	"github.com/gin-gonic/gin"

	"github.com/Furry-Drama-Tracker/backend/internal/auth"
	"github.com/Furry-Drama-Tracker/backend/internal/errors"
	"github.com/Furry-Drama-Tracker/backend/internal/middleware"
)

// Login2FA POST /api/auth/login-2fa：TOTP/备份码/邮件 OTP 登录。
// @Summary 2FA 登录
// @Tags 认证
// @Accept json
// @Produce json
// @Param body body object true "identifier/twoFactorToken/twoFactorChallenge/method"
// @Success 200 {object} map[string]any "用户对象"
// @Router /auth/login-2fa [post]
func (h *Auth) Login2FA(c *gin.Context) {
	var req struct {
		Identifier         string                  `json:"identifier"`
		Email              string                  `json:"email"`
		Method             string                  `json:"method"`
		TwoFactorToken     string                  `json:"twoFactorToken"`
		TwoFactorChallenge string                  `json:"twoFactorChallenge"`
		DeviceInfo         *auth.DeviceInfoPayload `json:"deviceInfo"`
	}
	_ = c.ShouldBindJSON(&req)
	identifier := req.Identifier
	if identifier == "" {
		identifier = req.Email
	}
	res, err := h.Svc.Login2FA(c, identifier, req.TwoFactorChallenge, req.Method, req.TwoFactorToken, req.DeviceInfo)
	if err != nil {
		errors.AbortWithAppError(c, err, h.Config.IsDev)
		return
	}
	if res.User != nil {
		c.JSON(200, h.Svc.SessionUserJSON(res.User))
		return
	}
	c.JSON(500, gin.H{"message": "服务器错误"})
}

// Send2FAEmailOTP POST /api/auth/2fa/email-otp/send：凭 2FA 挑战令牌发送邮箱
// 验证码（第二因素之一，仅已验证邮箱的账号可用）。
// @Summary 发送 2FA 邮箱验证码
// @Tags 认证
// @Accept json
// @Param body body object true "twoFactorChallenge"
// @Success 200 {object} map[string]any "email"
// @Router /auth/2fa/email-otp/send [post]
func (h *Auth) Send2FAEmailOTP(c *gin.Context) {
	var req struct {
		Challenge string `json:"twoFactorChallenge"`
	}
	_ = c.ShouldBindJSON(&req)
	email, err := h.Svc.Send2FAEmailOTP(c.Request.Context(), req.Challenge, h.devToken(c))
	if err != nil {
		errors.AbortWithAppError(c, err, h.Config.IsDev)
		return
	}
	c.JSON(200, gin.H{"message": "验证码已发送", "email": email})
}

// ---- password ----

// ChangePassword PUT /api/auth/change-password（protect）。
// @Summary 修改密码
// @Tags 认证
// @Security bearerAuth
// @Accept json
// @Produce json
// @Param body body object true "currentPassword/newPassword"
// @Success 200 {object} map[string]string
// @Router /auth/change-password [put]
func (h *Auth) ChangePassword(c *gin.Context) {
	var req struct {
		CurrentPassword string `json:"currentPassword"`
		NewPassword     string `json:"newPassword"`
	}
	_ = c.ShouldBindJSON(&req)
	user, _ := middleware.GetUser(c)
	if err := h.Svc.ChangePassword(c, user.ID, req.CurrentPassword, req.NewPassword); err != nil {
		errors.AbortWithAppError(c, err, h.Config.IsDev)
		return
	}
	c.JSON(200, gin.H{"message": "密码修改成功"})
}

// ForgotPassword POST /api/auth/forgot-password。
// @Summary 忘记密码
// @Tags 认证
// @Accept json
// @Produce json
// @Param body body object true "email/altcha"
// @Success 200 {object} map[string]string
// @Router /auth/forgot-password [post]
func (h *Auth) ForgotPassword(c *gin.Context) {
	var req struct {
		Email  string `json:"email"`
		Altcha string `json:"altcha"`
	}
	_ = c.ShouldBindJSON(&req)
	message, err := h.Svc.ForgotPassword(c.Request.Context(), req.Email, req.Altcha, h.devToken(c))
	if err != nil {
		errors.AbortWithAppError(c, err, h.Config.IsDev)
		return
	}
	c.JSON(200, gin.H{"message": message})
}

// ResetPassword POST /api/auth/reset-password。
// @Summary 重置密码
// @Tags 认证
// @Accept json
// @Produce json
// @Param body body object true "token/newPassword"
// @Success 200 {object} map[string]string
// @Router /auth/reset-password [post]
func (h *Auth) ResetPassword(c *gin.Context) {
	var req struct {
		Token       string `json:"token"`
		NewPassword string `json:"newPassword"`
	}
	_ = c.ShouldBindJSON(&req)
	if err := h.Svc.ResetPassword(c.Request.Context(), req.Token, req.NewPassword); err != nil {
		errors.AbortWithAppError(c, err, h.Config.IsDev)
		return
	}
	c.JSON(200, gin.H{"message": "密码重置成功"})
}
