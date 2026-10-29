package handler

import (
	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/gin-gonic/gin"

	"github.com/xiedada05/furry-drama-be-neo/internal/config"
	"github.com/xiedada05/furry-drama-be-neo/internal/middleware"
	"github.com/xiedada05/furry-drama-be-neo/internal/model"
	"github.com/xiedada05/furry-drama-be-neo/internal/ratelimit"
	"github.com/xiedada05/furry-drama-be-neo/internal/repository"
)

// 本文件实现 /api/banners 子域。

// Banners 是 /api/banners 域 handler 容器。
type Banners struct {
	Repos  *repository.Repos
	Config *config.Config
	AuthMW *middleware.Auth
	RL     func(ratelimit.Spec) gin.HandlerFunc
}

// NewBanners 构造轮播图 handler 容器。
func NewBanners(repos *repository.Repos, cfg *config.Config, amw *middleware.Auth, rl func(ratelimit.Spec) gin.HandlerFunc) *Banners {
	return &Banners{Repos: repos, Config: cfg, AuthMW: amw, RL: rl}
}

// Register 挂载 /api/banners 全部端点（不含 /api 前缀）。
// GET / 公开；GET /all、POST/PUT/DELETE 为 adminOnlyProtect（admin/superadmin）。
// GET/POST 同时注册 "" 与 "/"：gin 默认对尾斜杠缺失发起 307 重定向，而接口契约
// 要求带与不带尾斜杠都直接匹配（无重定向），需消除该差异。
func (h *Banners) Register(g *gin.RouterGroup) {
	adminOnly := []string{middleware.RoleAdmin, middleware.RoleSuperAdmin}
	g.GET("", h.List)
	g.GET("/", h.List)
	g.GET("/all", h.AuthMW.Protect(adminOnly...), h.ListAll)
	g.POST("", h.AuthMW.Protect(adminOnly...), h.Create)
	g.POST("/", h.AuthMW.Protect(adminOnly...), h.Create)
	g.PUT("/:id", h.AuthMW.Protect(adminOnly...), h.Update)
	g.DELETE("/:id", h.AuthMW.Protect(adminOnly...), h.Delete)
}

// List GET /api/banners（公开，仅 active，按 order 升序、createdAt 倒序）。
// @Summary 轮播图列表（激活中）
// @Tags 轮播图
// @Success 200 {array} map[string]any "轮播图数组"
// @Router /banners [get]
func (h *Banners) List(c *gin.Context) {
	banners, err := h.Repos.Banners.FindActiveSorted(c.Request.Context())
	if err != nil {
		serverError(c)
		return
	}
	list := make([]gin.H, 0, len(banners))
	for i := range banners {
		list = append(list, bannerJSON(&banners[i]))
	}
	c.JSON(200, list)
}

// ListAll GET /api/banners/all（adminOnlyProtect，全部）。
// @Summary 轮播图列表（全部）
// @Tags 轮播图
// @Security bearerAuth
// @Success 200 {array} map[string]any "轮播图数组"
// @Router /banners/all [get]
func (h *Banners) ListAll(c *gin.Context) {
	banners, err := h.Repos.Banners.FindAllSorted(c.Request.Context())
	if err != nil {
		serverError(c)
		return
	}
	list := make([]gin.H, 0, len(banners))
	for i := range banners {
		list = append(list, bannerJSON(&banners[i]))
	}
	c.JSON(200, list)
}

// Create POST /api/banners（adminOnlyProtect）。
// @Summary 创建轮播图
// @Tags 轮播图
// @Security bearerAuth
// @Accept json
// @Param body body object true "title/titleEn/titleJa/subtitle/subtitleEn/subtitleJa/image/link/order/active"
// @Success 200 {object} map[string]any "轮播图对象"
// @Router /banners [post]
func (h *Banners) Create(c *gin.Context) {
	body := cmsReadBody(c)
	if !bannerLinkValid(body) {
		c.JSON(400, gin.H{"message": "链接格式不合法，仅支持 http/https 协议"})
		return
	}
	// title/image 必填（固定契约）：缺失或为空 → 500 Server error（catch-all）。
	if asString(body["title"]) == "" || asString(body["image"]) == "" {
		serverError(c)
		return
	}
	active := true
	if v, ok := body["active"]; ok {
		active = cmsCastBool(v)
	}
	banner := &model.Banner{
		Title:      asString(body["title"]),
		TitleEn:    orDefaultString(body["titleEn"], ""),
		TitleJa:    orDefaultString(body["titleJa"], ""),
		Subtitle:   orDefaultString(body["subtitle"], ""),
		SubtitleEn: orDefaultString(body["subtitleEn"], ""),
		SubtitleJa: orDefaultString(body["subtitleJa"], ""),
		Image:      asString(body["image"]),
		Link:       orDefaultString(body["link"], ""),
		Order:      toInt(body["order"]),
		Active:     active,
	}
	if err := h.Repos.Banners.Create(c.Request.Context(), banner); err != nil {
		serverError(c)
		return
	}
	c.JSON(200, bannerJSON(banner))
}

// Update PUT /api/banners/:id（adminOnlyProtect）。
// @Summary 更新轮播图
// @Tags 轮播图
// @Security bearerAuth
// @Accept json
// @Param id path string true "轮播图 ID"
// @Param body body object true "可更新字段（全字段可选）"
// @Success 200 {object} map[string]any "轮播图对象"
// @Router /banners/{id} [put]
func (h *Banners) Update(c *gin.Context) {
	oid, err := primitive.ObjectIDFromHex(c.Param("id"))
	if err != nil {
		serverError(c)
		return
	}
	body := cmsReadBody(c)
	// 链接合法性检查在按 ID 查询之前（检查顺序是固定契约，勿调换）。
	if !bannerLinkValid(body) {
		c.JSON(400, gin.H{"message": "链接格式不合法，仅支持 http/https 协议"})
		return
	}
	banner, err := h.Repos.Banners.FindByID(c.Request.Context(), oid)
	if err != nil {
		if repository.IsNotFound(err) {
			c.JSON(404, gin.H{"message": "轮播图不存在"})
			return
		}
		serverError(c)
		return
	}
	// 各字段仅 !== undefined 时更新（固定契约，勿改成覆盖式更新）。
	if v, ok := body["title"]; ok {
		banner.Title = asString(v)
	}
	if v, ok := body["titleEn"]; ok {
		banner.TitleEn = asString(v)
	}
	if v, ok := body["titleJa"]; ok {
		banner.TitleJa = asString(v)
	}
	if v, ok := body["subtitle"]; ok {
		banner.Subtitle = asString(v)
	}
	if v, ok := body["subtitleEn"]; ok {
		banner.SubtitleEn = asString(v)
	}
	if v, ok := body["subtitleJa"]; ok {
		banner.SubtitleJa = asString(v)
	}
	if v, ok := body["image"]; ok {
		banner.Image = asString(v)
	}
	if v, ok := body["link"]; ok {
		banner.Link = asString(v)
	}
	if v, ok := body["order"]; ok {
		banner.Order = toInt(v)
	}
	if v, ok := body["active"]; ok {
		banner.Active = cmsCastBool(v)
	}
	if err := h.Repos.Banners.Save(c.Request.Context(), banner); err != nil {
		serverError(c)
		return
	}
	c.JSON(200, bannerJSON(banner))
}

// Delete DELETE /api/banners/:id（adminOnlyProtect）。
// @Summary 删除轮播图
// @Tags 轮播图
// @Security bearerAuth
// @Param id path string true "轮播图 ID"
// @Success 200 {object} map[string]string "message"
// @Router /banners/{id} [delete]
func (h *Banners) Delete(c *gin.Context) {
	oid, err := primitive.ObjectIDFromHex(c.Param("id"))
	if err != nil {
		serverError(c)
		return
	}
	if _, err := h.Repos.Banners.FindByID(c.Request.Context(), oid); err != nil {
		if repository.IsNotFound(err) {
			c.JSON(404, gin.H{"message": "轮播图不存在"})
			return
		}
		serverError(c)
		return
	}
	if err := h.Repos.Banners.DeleteByID(c.Request.Context(), oid); err != nil {
		serverError(c)
		return
	}
	c.JSON(200, gin.H{"message": "轮播图已删除"})
}

// bannerLinkValid 校验请求体 link 字段：
// 仅当 link 非空时才校验，且必须为 http/https 绝对 URL（固定契约）。
func bannerLinkValid(body map[string]any) bool {
	link, ok := body["link"]
	if !ok {
		return true
	}
	s := asString(link)
	if s == "" {
		return true
	}
	return cmsIsValidHTTPURL(s)
}

// bannerJSON 组装轮播图响应对象（与既有集合数据形态一致：_id hex、时间 RFC3339、含 __v）。
func bannerJSON(b *model.Banner) gin.H {
	return gin.H{
		"_id":        b.ID.Hex(),
		"title":      b.Title,
		"titleEn":    b.TitleEn,
		"titleJa":    b.TitleJa,
		"subtitle":   b.Subtitle,
		"subtitleEn": b.SubtitleEn,
		"subtitleJa": b.SubtitleJa,
		"image":      b.Image,
		"link":       b.Link,
		"order":      b.Order,
		"active":     b.Active,
		"createdAt":  b.CreatedAt,
		"__v":        b.VersionKey,
	}
}
