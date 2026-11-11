// Package server 装配 Gin 引擎：中间件管线顺序、静态资源、业务路由、优雅关停。
//
// 中间件顺序是固定契约，勿重排；新增中间件需确认放置位置：
//
//	recovery → gzip → securityheaders → cors → 30s 超时 → slowlogger →
//	bodyparse(1mb/50mb) → sanitizeInput → sanitizeHeaders →
//	GET /api/csrf-token → CSRF 校验(非 GET) → apiTracker → requestLogger →
//	[受限流路由组 /api] globalLimiter(300/min) → /api/health →
//	业务路由（M2）→ 全局错误处理
//
// 关键装配说明：
//   - gin 的 r.Use 对所有请求生效（含 csrf-token），故 csrf-token 单独注册在
//     CSRF 校验之前；CSRF 中间件对 GET 放行，行为一致。
//   - globalLimiter 挂载到 /api 路由组（不含 csrf-token，天然豁免）。
//   - per-endpoint 限流在 M2 业务路由上施加（RateLimit(store, spec, opts)）。
package server

import (
	"log/slog"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
	"go.mongodb.org/mongo-driver/mongo"

	_ "github.com/Furry-Drama-Tracker/backend/docs" // swag init 生成的 OpenAPI spec（init 注册）
	"github.com/Furry-Drama-Tracker/backend/internal/auth"
	"github.com/Furry-Drama-Tracker/backend/internal/authlog"
	"github.com/Furry-Drama-Tracker/backend/internal/config"
	"github.com/Furry-Drama-Tracker/backend/internal/errors"
	"github.com/Furry-Drama-Tracker/backend/internal/handler"
	"github.com/Furry-Drama-Tracker/backend/internal/logging"
	"github.com/Furry-Drama-Tracker/backend/internal/middleware"
	"github.com/Furry-Drama-Tracker/backend/internal/ratelimit"
	"github.com/Furry-Drama-Tracker/backend/internal/repository"
	"github.com/Furry-Drama-Tracker/backend/internal/upload"
)

// Deps 是装配 Gin 引擎所需的全部依赖。
type Deps struct {
	Config *config.Config
	DB     *mongo.Database
	Repos  *repository.Repos
	Signer *auth.Signer
	// Log 可选：不传则按 IsDev 新建（测试可注入捕获器）。
	Log *slog.Logger
}

// NewApp 按管线顺序装配并返回 Gin 引擎。
func NewApp(d Deps) *gin.Engine {
	if d.Config.IsDev {
		gin.SetMode(gin.DebugMode)
	} else {
		gin.SetMode(gin.ReleaseMode)
	}
	r := gin.New()
	r.Use(gin.Recovery())
	log := d.Log
	if log == nil {
		log = logging.New(d.Config.IsDev)
	}
	// 认证诊断事件记录器：结构化写 stderr + 异步落 authevents（30 天 TTL）。
	rec := authlog.New(log, d.Repos.AuthEvents)

	// ---- 客户端 IP 识别（gin c.ClientIP，配置见 [server] trust_proxy_headers 等）----
	// 信任开关缺省沿用历史行为：生产信任、开发不信任。
	srv := d.Config.Server
	trust := !d.Config.IsDev
	if srv.TrustProxyHeaders != nil {
		trust = *srv.TrustProxyHeaders
	}
	if !trust {
		// 关闭后 c.ClientIP() 一律返回 RemoteAddr，代理标头被整体无视。
		r.ForwardedByClientIP = false
	}
	// trusted_proxies 缺省不配 = gin 默认信任全部来源（与历史手写解析一致）；
	// 配置后（如 ESA → 本机反代拓扑填 127.0.0.1,::1）非受信来源直连伪造的
	// 代理标头会被整体忽略，重复同名标头合并后从右往左回溯取非受信值。
	if srv.TrustedProxies != nil {
		if err := r.SetTrustedProxies(srv.TrustedProxies); err != nil {
			// validate() 已拦截非法格式，此处为防御性兜底：保留 gin 默认信任状态。
			log.Warn("trusted_proxies 解析失败，忽略该配置", "err", err)
		}
	}
	// 标头清单与优先级可配置（缺省 CF-Connecting-IP > X-Real-IP > X-Forwarded-For：
	// CF 头由 ESA/CDN 边缘覆盖写入最可信；X-Real-IP 可能被中间层写成本地/节点 IP，仅作兜底）。
	r.RemoteIPHeaders = srv.ProxyIPHeaders
	if srv.ProxyTrustedPlatform != "" {
		// 注意：TrustedPlatform 分支不校验来源与 IP 格式（非空即信），
		// 仅应在前置网关保证覆盖写入该头时启用。
		r.TrustedPlatform = srv.ProxyTrustedPlatform
	}

	// ---- 全局中间件（顺序为固定契约，勿重排）----
	r.Use(middleware.Gzip())
	r.Use(middleware.SecurityHeaders())
	r.Use(middleware.CORS(d.Config.Server.AllowOrigins))
	r.Use(middleware.Timeout())
	r.Use(middleware.SlowLogger(log, d.Config.IsDev))
	r.Use(middleware.BodyParse())
	r.Use(middleware.SanitizeInput())
	r.Use(middleware.SanitizeHeaders())

	// ---- csrf-token：注册在 CSRF 校验与限流之前（该端点天然豁免 CSRF）----
	r.GET("/api/csrf-token", handler.CSRF(d.Config))

	r.Use(middleware.CSRF())
	r.Use(middleware.APITracker(d.Repos.ApiUsage))
	r.Use(middleware.RequestLogger(log))

	// ---- 受限流路由组 /api（globalLimiter 300/min，含 health）----
	opts := middleware.RateLimitOpts{
		IsDev:         d.Config.IsDev,
		SkipRateLimit: d.Config.Security.RateLimitSkip,
		Log:           log,
	}
	api := r.Group("/api")
	api.Use(middleware.RateLimit(ratelimit.GlobalSpec, opts))
	api.GET("/health", handler.Health(d.DB))

	// ---- Swagger UI（仅非生产，固定放置）----
	if d.Config.IsDev {
		r.GET("/api/docs/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))
	}

	// ---- 业务路由挂载（M2 填充；挂 api 组下以继承 globalLimiter）----
	RegisterRoutes(d, api, opts, rec)

	// ---- /uploads 静态服务（图片/SVG 图标，公开可读）----
	// 不挂 /api 组：避免吃 globalLimiter（300/min）；apitracker 只统计 /api/、CSRF 对 GET
	// 放行，故静态请求不受限流/统计/CSRF 影响。文件名 "<prefix>-<16字节hex><ext>" 内容
	// 不可变 → 长缓存 + immutable（换图=换文件名=新 URL，天然失效）。noindex + inline +
	// nosniff（nosniff 由全局 securityheaders 提供）；禁止目录列举与路径穿越。
	r.GET("/uploads/*filepath", serveUploads)
	r.HEAD("/uploads/*filepath", serveUploads)

	// TODO(M4): swagger（仅非生产）/api/docs

	// ---- 全局错误处理（最后注册）----
	r.Use(errors.Handler(func() bool { return d.Config.IsDev }))

	return r
}

// serveUploads 提供 /uploads 静态文件服务（静态服务行为固定）：
//   - 7 天缓存（Cache-Control: public, max-age=604800, immutable）；
//   - X-Content-Type-Options: nosniff、X-Robots-Tag: noindex、Content-Disposition: inline；
//   - 目录与不存在文件返回 404，禁止目录列举；
//   - 路径穿越（..）直接拒绝。
func serveUploads(c *gin.Context) {
	name := strings.TrimPrefix(path.Clean("/"+c.Param("filepath")), "/")
	if name == "" || strings.Contains(name, "..") {
		c.Status(http.StatusNotFound)
		return
	}
	// 仅允许公开的图片类型，避免上传目录中的历史 HTML/JS 等文件被同源执行。
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".svg":
	default:
		c.Status(http.StatusNotFound)
		return
	}
	full := filepath.Join(upload.Dir, filepath.FromSlash(name))
	info, err := os.Stat(full)
	if err != nil || info.IsDir() {
		c.Status(http.StatusNotFound)
		return
	}
	c.Header("Cache-Control", "public, max-age=604800, immutable")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("X-Robots-Tag", "noindex")
	c.Header("Content-Disposition", "inline")
	// 上传资源单独使用 sandbox CSP：即使 SVG 被直接打开，也禁止脚本执行。
	c.Header("Content-Security-Policy", "default-src 'none'; img-src 'self' data:; style-src 'unsafe-inline'; sandbox")
	c.File(full)
}
