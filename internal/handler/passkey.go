package handler

import (
	"github.com/gin-gonic/gin"

	"github.com/Furry-Drama-Tracker/backend/internal/auth"
	"github.com/Furry-Drama-Tracker/backend/internal/errors"
	"github.com/Furry-Drama-Tracker/backend/internal/middleware"
)

// ---- passkey ----

// reauthPassword 对敏感操作做密码再认证（与 request-email-change 模式一致）。
// 返回 true 表示校验通过。
func (h *Auth) reauthPassword(c *gin.Context, currentPassword string) bool {
	if currentPassword == "" {
		c.JSON(400, gin.H{"message": "请输入当前密码以确认操作"})
		return false
	}
	user, _ := middleware.GetUser(c)
	u, err := h.Svc.Repos.Users.FindByIDWithAuth(c.Request.Context(), user.ID)
	if err != nil || !auth.Compare(u.Password, currentPassword) {
		c.JSON(400, gin.H{"message": "当前密码不正确"})
		return false
	}
	return true
}

// PasskeyRegisterOptions POST /api/passkeys/register/options（protect + 密码再认证）。
// query: name（设备名，可选）。响应为 WebAuthn creation options（{publicKey:{...}}）。
// @Summary 开始注册 Passkey
// @Tags Passkey
// @Security bearerAuth
// @Param currentPassword body string true "当前密码"
// @Success 200 {object} map[string]any "publicKey options + ceremonyKey"
// @Router /passkeys/register/options [post]
func (h *Auth) PasskeyRegisterOptions(c *gin.Context) {
	var req struct {
		CurrentPassword string `json:"currentPassword"`
	}
	_ = c.ShouldBindJSON(&req)
	if !h.reauthPassword(c, req.CurrentPassword) {
		return
	}
	user, _ := middleware.GetUser(c)
	creation, key, err := h.Svc.BeginPasskeyRegistration(c.Request.Context(), user)
	if err != nil {
		errors.AbortWithAppError(c, err, h.Config.IsDev)
		return
	}
	c.JSON(200, gin.H{"publicKey": creation.Response, "ceremonyKey": key})
}

// PasskeyRegisterVerify POST /api/passkeys/register/verify?ceremonyKey=&name=（protect）。
// body 为浏览器 navigator.credentials.create() 的 JSON 结果（原样透传给校验器）。
// @Summary 完成 Passkey 注册
// @Tags Passkey
// @Security bearerAuth
// @Param ceremonyKey query string true "仪式会话键"
// @Param name query string false "设备名"
// @Success 200 {object} map[string]any
// @Router /passkeys/register/verify [post]
func (h *Auth) PasskeyRegisterVerify(c *gin.Context) {
	user, _ := middleware.GetUser(c)
	pk, err := h.Svc.FinishPasskeyRegistration(c.Request.Context(), user,
		c.Query("ceremonyKey"), c.Query("name"), c.Request)
	if err != nil {
		errors.AbortWithAppError(c, err, h.Config.IsDev)
		return
	}
	c.JSON(200, gin.H{
		"message": "Passkey 添加成功",
		"passkey": gin.H{
			"_id":       pk.ID.Hex(),
			"name":      pk.Name,
			"createdAt": pk.CreatedAt,
		},
	})
}

// ListPasskeys GET /api/passkeys（protect）。
// @Summary 列出我的 Passkey
// @Tags Passkey
// @Security bearerAuth
// @Success 200 {array} map[string]any
// @Router /passkeys [get]
func (h *Auth) ListPasskeys(c *gin.Context) {
	user, _ := middleware.GetUser(c)
	list, err := h.Svc.ListPasskeys(c.Request.Context(), user.ID)
	if err != nil {
		c.JSON(500, gin.H{"message": "服务器错误"})
		return
	}
	out := make([]gin.H, 0, len(list))
	for i := range list {
		pk := &list[i]
		out = append(out, gin.H{
			"_id":        pk.ID.Hex(),
			"name":       pk.Name,
			"createdAt":  pk.CreatedAt,
			"lastUsedAt": pk.LastUsedAt,
		})
	}
	c.JSON(200, out)
}

// DeletePasskey DELETE /api/passkeys/:id（protect + 密码再认证）。
// @Summary 删除 Passkey
// @Tags Passkey
// @Security bearerAuth
// @Param id path string true "凭据 ID"
// @Param body body object true "currentPassword"
// @Success 200 {object} map[string]string
// @Router /passkeys/{id} [delete]
func (h *Auth) DeletePasskey(c *gin.Context) {
	var req struct {
		CurrentPassword string `json:"currentPassword"`
	}
	_ = c.ShouldBindJSON(&req)
	if !h.reauthPassword(c, req.CurrentPassword) {
		return
	}
	user, _ := middleware.GetUser(c)
	if err := h.Svc.DeletePasskey(c.Request.Context(), user, c.Param("id")); err != nil {
		errors.AbortWithAppError(c, err, h.Config.IsDev)
		return
	}
	c.JSON(200, gin.H{"message": "Passkey 已删除"})
}

// PasskeyLoginOptions POST /api/auth/passkey/options（公开）。
// body 可带 identifier（用户名或邮箱）：带则限定该账号的凭据，不带则可发现凭据
// （无用户名登录）。响应为 WebAuthn assertion options（{publicKey:{...}}）。
// @Summary 开始 Passkey 登录
// @Tags 认证
// @Accept json
// @Success 200 {object} map[string]any "publicKey options + ceremonyKey"
// @Router /auth/passkey/options [post]
func (h *Auth) PasskeyLoginOptions(c *gin.Context) {
	var req struct {
		Identifier string `json:"identifier"`
	}
	_ = c.ShouldBindJSON(&req)
	assertion, key, err := h.Svc.BeginPasskeyLogin(c.Request.Context(), req.Identifier)
	if err != nil {
		errors.AbortWithAppError(c, err, h.Config.IsDev)
		return
	}
	c.JSON(200, gin.H{"publicKey": assertion.Response, "ceremonyKey": key})
}

// PasskeyLoginVerify POST /api/auth/passkey/verify?ceremonyKey=（公开）。
// body 为浏览器 navigator.credentials.get() 的 JSON 结果。
// Passkey 单因素登录直接通过（即使账号开启 2FA 也不再挑战第二因素）。
// @Summary 完成 Passkey 登录
// @Tags 认证
// @Accept json
// @Param ceremonyKey query string true "仪式会话键"
// @Success 200 {object} map[string]any "用户对象"
// @Router /auth/passkey/verify [post]
func (h *Auth) PasskeyLoginVerify(c *gin.Context) {
	res, err := h.Svc.PasskeyLogin(c, c.Query("ceremonyKey"))
	if err != nil {
		errors.AbortWithAppError(c, err, h.Config.IsDev)
		return
	}
	switch {
	case res.User != nil:
		c.JSON(200, h.Svc.SessionUserJSON(res.User))
	case res.NeedVerification:
		c.JSON(403, gin.H{
			"message":          "请先验证邮箱后再登录，验证码已发送至您的邮箱",
			"needVerification": true,
			"email":            res.Email,
		})
	default:
		c.JSON(500, gin.H{"message": "服务器错误"})
	}
}
