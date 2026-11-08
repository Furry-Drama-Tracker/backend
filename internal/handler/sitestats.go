package handler

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/bson"

	"github.com/Furry-Drama-Tracker/backend/internal/repository"
)

// SiteStats 前台公开站点规模数据（用户数 / 剧集数 / 在线人数）。
//
// 仅暴露聚合计数，不含任何用户标识或业务明细；带 15 秒进程内缓存，
// 避免首页每次加载都打库。所有查询均为只读。
type SiteStats struct {
	Repos *repository.Repos

	mu       sync.Mutex
	cachedAt time.Time
	cached   gin.H
}

const (
	siteStatsTTL         = 15 * time.Second
	siteStatsOnlineWindow = 10 * time.Minute
)

// NewSiteStats 构造公开站点统计 handler。
func NewSiteStats(repos *repository.Repos) *SiteStats {
	return &SiteStats{Repos: repos}
}

// Register 注册路由：GET /api/site-stats
func (h *SiteStats) Register(g *gin.RouterGroup) {
	g.GET("", h.Get)
	g.GET("/", h.Get)
}

// Get 返回公开统计。
// @Summary 站点公开统计（用户数/剧集数/在线人数）
// @Tags 统计
// @Success 200 {object} map[string]any "users/episodes/online"
// @Router /site-stats [get]
func (h *SiteStats) Get(c *gin.Context) {
	h.mu.Lock()
	if h.cached != nil && time.Since(h.cachedAt) < siteStatsTTL {
		data := h.cached
		h.mu.Unlock()
		c.Header("Cache-Control", "public, max-age=15")
		c.JSON(http.StatusOK, data)
		return
	}
	h.mu.Unlock()

	ctx := c.Request.Context()

	episodes, err := h.Repos.Episodes.CountDocuments(ctx, bson.M{"reviewStatus": "approved"})
	if err != nil {
		serverError(c)
		return
	}
	users, err := h.Repos.Users.StatsUserCount(ctx, bson.M{})
	if err != nil {
		serverError(c)
		return
	}
	online, err := h.Repos.Sessions.CountActiveUsersSince(ctx, time.Now().Add(-siteStatsOnlineWindow))
	if err != nil {
		// 在线人数为锦上添花的数据，失败时降级为 0，不影响用户数/剧集数展示
		online = 0
	}

	data := gin.H{
		"users":    users,
		"episodes": episodes,
		"online":   online,
	}

	h.mu.Lock()
	h.cached = data
	h.cachedAt = time.Now()
	h.mu.Unlock()

	c.Header("Cache-Control", "public, max-age=15")
	c.JSON(http.StatusOK, data)
}
