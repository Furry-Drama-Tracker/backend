package handler

import (
	"fmt"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Furry-Drama-Tracker/backend/internal/errors"
	"github.com/Furry-Drama-Tracker/backend/internal/middleware"
	"github.com/Furry-Drama-Tracker/backend/internal/service"
	"github.com/Furry-Drama-Tracker/backend/internal/upload"
)

// Avatar POST /api/users/avatar（protect + 2MB）。
// @Summary 上传头像
// @Tags 用户
// @Security bearerAuth
// @Accept multipart/form-data
// @Param avatar formData file true "图片（≤2MB）"
// @Success 200 {object} map[string]string "url"
// @Router /users/avatar [post]
func (h *Auth) Avatar(c *gin.Context) {
	url, err := upload.SaveImage(c, "avatar", "avatar", 2<<20)
	if err != nil {
		h.abortUploadError(c, err, 2)
		return
	}
	user, _ := middleware.GetUser(c)
	// 删除旧头像文件（忽略错误）。
	if user.Avatar != "" {
		_ = removeUploadedFile(user.Avatar)
	}
	if err := h.Svc.Repos.Users.UpdateAvatar(c.Request.Context(), user.ID, url); err != nil {
		c.JSON(500, gin.H{"message": "头像上传失败"})
		return
	}
	c.JSON(200, gin.H{"url": url})
}

// Profile PUT /api/users/profile（protect）。
// @Summary 更新昵称
// @Tags 用户
// @Security bearerAuth
// @Accept json
// @Param body body object true "username"
// @Success 200 {object} map[string]any
// @Router /users/profile [put]
func (h *Auth) Profile(c *gin.Context) {
	var req struct {
		Username string `json:"username"`
	}
	_ = c.ShouldBindJSON(&req)
	username := strings.TrimSpace(req.Username)
	if len([]rune(username)) > 20 {
		c.JSON(400, gin.H{"message": "昵称长度不能超过20个字符"})
		return
	}
	if username == "" {
		c.JSON(400, gin.H{"message": "没有需要更新的数据"})
		return
	}
	user, _ := middleware.GetUser(c)
	if err := h.Svc.Repos.Users.UpdateUsername(c.Request.Context(), user.ID, username); err != nil {
		c.JSON(500, gin.H{"message": "服务器错误"})
		return
	}
	u, err := h.Svc.Repos.Users.FindByID(c.Request.Context(), user.ID)
	if err != nil {
		c.JSON(500, gin.H{"message": "服务器错误"})
		return
	}
	c.JSON(200, gin.H{
		"_id":             u.ID.Hex(),
		"accountId":       u.AccountID,
		"username":        u.Username,
		"email":           u.Email,
		"isEmailVerified": u.IsEmailVerified,
		"role":            u.Role,
		"avatar":          u.Avatar,
	})
}

// LoginName PUT /api/users/login-name（protect）：用户名改名（密码再认证 +
// 校验 + 冷却）。成功后旧名进入保留期（默认 90 天，期间他人不可注册）。
// @Summary 修改登录用户名
// @Tags 用户
// @Security bearerAuth
// @Accept json
// @Param body body object true "newLoginName/currentPassword"
// @Success 200 {object} map[string]any "accountId/cooldownDays"
// @Router /users/login-name [put]
func (h *Auth) LoginName(c *gin.Context) {
	var req struct {
		NewLoginName    string `json:"newLoginName"`
		CurrentPassword string `json:"currentPassword"`
	}
	_ = c.ShouldBindJSON(&req)
	user, _ := middleware.GetUser(c)
	newName, cooldownDays, err := h.Svc.ChangeLoginName(c.Request.Context(), user, service.ChangeLoginNameInput{
		NewLoginName:    req.NewLoginName,
		CurrentPassword: req.CurrentPassword,
	})
	if err != nil {
		errors.AbortWithAppError(c, err, h.Config.IsDev)
		return
	}
	c.JSON(200, gin.H{
		"message":      "用户名修改成功",
		"accountId":    newName,
		"cooldownDays": cooldownDays,
	})
}

// CheckLoginName GET /api/users/check-login-name?name=（protect）：改名前预检。
// @Summary 检查用户名是否可用
// @Tags 用户
// @Security bearerAuth
// @Param name query string true "用户名"
// @Success 200 {object} map[string]any "available/reason"
// @Router /users/check-login-name [get]
func (h *Auth) CheckLoginName(c *gin.Context) {
	name := c.Query("name")
	if name == "" {
		c.JSON(400, gin.H{"message": "name is required"})
		return
	}
	available, reason := h.Svc.CheckLoginNameAvailable(c.Request.Context(), name)
	c.JSON(200, gin.H{"available": available, "reason": reason})
}

// ExportMyData GET /api/users/export-my-data（protect + exportLimiter 3/h）。
// @Summary 导出个人数据
// @Tags 用户
// @Security bearerAuth
// @Param format query string false "json|csv"
// @Success 200 {string} string "文件下载"
// @Router /users/export-my-data [get]
func (h *Auth) ExportMyData(c *gin.Context) {
	user, _ := middleware.GetUser(c)
	data, err := h.Svc.BuildExportData(c.Request.Context(), user)
	if err != nil {
		c.JSON(500, gin.H{"message": "导出失败"})
		return
	}
	dateStr := time.Now().Format("2006-01-02")
	format := c.Query("format")
	if format == "csv" {
		c.Header("Content-Type", "text/csv; charset=utf-8")
		c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=my_data_%s.csv", dateStr))
		c.String(200, "\uFEFF"+data.ExportCSV())
		return
	}
	c.Header("Content-Type", "application/json")
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=my_data_%s.json", dateStr))
	c.JSON(200, data)
}

// ---- helpers ----

// abortUploadError 渲染上传错误（错误文案是固定契约，勿改）。
func (h *Auth) abortUploadError(c *gin.Context, err error, maxMB int) {
	if err == upload.ErrNoFile {
		c.JSON(400, gin.H{"message": "请选择要上传的图片"})
		return
	}
	if ue, ok := err.(*errors.UploadError); ok {
		switch ue.Code {
		case "LIMIT_FILE_SIZE":
			c.JSON(400, gin.H{"message": fmt.Sprintf("文件大小不能超过%dMB", maxMB)})
		case "LIMIT_FILE_TYPE":
			c.JSON(400, gin.H{"message": "仅支持图片文件 (jpg, jpeg, png, gif, webp)"})
		case "BAD_MAGIC":
			c.JSON(400, gin.H{"message": "文件内容与类型不匹配，仅支持图片文件"})
		default:
			c.JSON(400, gin.H{"message": "文件上传错误: " + ue.Message})
		}
		return
	}
	c.JSON(500, gin.H{"message": "服务器错误"})
}

// removeUploadedFile 删除已上传文件（忽略错误）。只允许删除头像前缀文件，
// 防止数据库字段被污染后删除任意上传文件。
func removeUploadedFile(url string) error {
	if !strings.HasPrefix(url, "/uploads/avatar-") {
		return nil
	}
	path := strings.TrimPrefix(url, "/uploads/")
	return upload.RemoveFile(path)
}
