package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/Furry-Drama-Tracker/backend/internal/auth"
)

// ---- OAuth（QQ 预留）----

// QQOAuthStatus GET /api/auth/qq/status（公开）：返回 QQ 登录是否可用。
// @Summary QQ 登录可用状态
// @Tags 认证
// @Success 200 {object} map[string]bool "enabled"
// @Router /auth/qq/status [get]
func (h *Auth) QQOAuthStatus(c *gin.Context) {
	c.JSON(200, gin.H{"enabled": h.Svc.QQ.Enabled()})
}

// QQOAuthStart GET /api/auth/qq/start（公开）：返回授权跳转地址。
// @Summary 开始 QQ 授权（预留）
// @Tags 认证
// @Success 200 {object} map[string]string "authUrl"
// @Failure 501 {object} map[string]string
// @Router /auth/qq/start [get]
func (h *Auth) QQOAuthStart(c *gin.Context) {
	if !h.Svc.QQ.Enabled() {
		c.JSON(http.StatusNotImplemented, gin.H{"message": "QQ 登录尚未开放"})
		return
	}
	url, err := h.Svc.QQ.GetAuthURL(c.Request.Context(), auth.RandomHex(16))
	if err != nil {
		c.JSON(500, gin.H{"message": "服务器错误"})
		return
	}
	c.JSON(200, gin.H{"authUrl": url})
}

// QQOAuthCallback POST /api/auth/qq/callback {code}：授权码换身份并登录/注册。
// @Summary QQ 授权回调（预留）
// @Tags 认证
// @Accept json
// @Failure 501 {object} map[string]string
// @Router /auth/qq/callback [post]
func (h *Auth) QQOAuthCallback(c *gin.Context) {
	if !h.Svc.QQ.Enabled() {
		c.JSON(http.StatusNotImplemented, gin.H{"message": "QQ 登录尚未开放"})
		return
	}
	c.JSON(http.StatusNotImplemented, gin.H{"message": "QQ 登录尚未开放"})
}
