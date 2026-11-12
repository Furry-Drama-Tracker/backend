package middleware

import (
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	ulule "github.com/ulule/limiter/v3"
	memory "github.com/ulule/limiter/v3/drivers/store/memory"

	"github.com/Furry-Drama-Tracker/backend/internal/model"
	"github.com/Furry-Drama-Tracker/backend/internal/ratelimit"
)

// RateLimitOpts 是 RateLimit 的挂载与开关参数。
type RateLimitOpts struct {
	// IsDev 等价于 NODE_ENV !== 'production'。
	IsDev bool
	// SkipRateLimit 对应 SKIP_RATE_LIMIT=1。
	SkipRateLimit bool
	// Log 是可选的日志器：429 命中时输出 `event=ratelimited spec=<名字>`，
	// 便于在 journalctl 里直接看出是哪个限流器打的（原本只能看到 429 状态码）。
	Log *slog.Logger
}

// specLimiter 封装 ulule/limiter（每个命名限流器一个独立 limiter + 内存 store）。
type specLimiter struct {
	spec ratelimit.Spec
	lim  *ulule.Limiter
}

// RateLimit 构造一个命名限流中间件（挂载语义为固定契约），
// 计数由 github.com/ulule/limiter 提供：
//
//   - spec.Mounts 为空 → 全局型：匹配 /api 与全部 /api/ 前缀（含 /api/health），
//     并跳过 /api/translate 与 /api/auth/captcha 路径（全局型固定 skip 规则）；
//   - spec.Mounts 非空 → 端点型：只匹配 /api/<Mount> 精确与子路径。
//
// auth（AuthSpec）在非生产且 SKIP_RATE_LIMIT=1 时整体跳过（该 spec 固定 skip 规则）。
// requestEmailChange 同时按来源 IP 与目标邮箱小写值限流，避免用不同邮箱刷验证邮件。
//
// 429 响应体 {"message": <spec.Message>}，并设置 draft-6 标准限流头：
// RateLimit-Policy / -Limit / -Remaining / -Reset，以及 Retry-After。
func RateLimit(spec ratelimit.Spec, opts RateLimitOpts) gin.HandlerFunc {
	sl := &specLimiter{
		spec: spec,
		lim: ulule.New(memory.NewStore(), ulule.Rate{
			Period: spec.Window,
			Limit:  int64(spec.Max),
		}),
	}
	global := len(spec.Mounts) == 0
	windowSeconds := int64(spec.Window.Seconds())
	skipAuth := opts.IsDev && opts.SkipRateLimit

	type prefixPair struct{ exact, sub string }
	var mounts []prefixPair
	for _, m := range spec.Mounts {
		p := "/api" + m
		mounts = append(mounts, prefixPair{exact: p, sub: p + "/"})
	}

	return func(c *gin.Context) {
		path := c.Request.URL.Path
		if global {
			if !(path == "/api" || strings.HasPrefix(path, "/api/")) {
				c.Next()
				return
			}
			// globalLimiter.skip：跳过翻译与验证码路径。
			if strings.HasPrefix(path, "/api/translate") || strings.HasPrefix(path, "/api/auth/captcha") {
				c.Next()
				return
			}
		} else {
			matched := false
			for _, m := range mounts {
				if path == m.exact || strings.HasPrefix(path, m.sub) {
					matched = true
					break
				}
			}
			if !matched {
				c.Next()
				return
			}
		}

		if skipAuth && spec.Name == ratelimit.AuthName {
			c.Next()
			return
		}

		// 客户端 IP 解析统一走引擎级 c.ClientIP()（标头清单/信任开关/受信代理
		// 来源校验由 server/app.go 按 [server] 配置装配）。
		ip := c.ClientIP()
		key := spec.Name + ":" + ratelimit.IPKey(ip)
		switch spec.Name {
		case ratelimit.RequestEmailChangeName:
			// 同时按来源 IP 与目标邮箱限流，避免攻击者用不同邮箱刷验证邮件。
			key = spec.Name + ":" + ratelimit.IPKey(ip) + ":" + strings.ToLower(newEmailFromBody(c))
		case ratelimit.AuthName, ratelimit.TwoFactorName, ratelimit.PasswordResetName,
			ratelimit.EmailVerifyName:
			// 凭证/邮件类限流：key 绑定目标邮箱（请求体 email 小写）。
			// 来源 IP 可经伪造代理标头轮换（CWE-348），key 中保留 IP 分量会让
			// 这些 spec 在 IP 轮换下退化为无限流——按邮箱绑定后，对同一账号的
			// 猜测/邮件请求无论换多少来源 IP 都共享同一计数桶。
			// 请求体无 email 的挂载（reset-password / change-password / 2fa/* /
			// confirm-device-login 等无邮箱体端点）回退到原 IP key（与旧行为一致）。
			if email := emailFromBody(c); email != "" {
				key = spec.Name + ":" + email
			}
		}

		ctx, err := sl.lim.Get(c.Request.Context(), key)
		if err != nil {
			// 存储异常不阻断请求（降级放行，固定语义）。
			c.Next()
			return
		}

		// 标准限流响应头（draft-6 格式，固定契约，勿增删改）。
		c.Header("RateLimit-Policy", fmt.Sprintf("%d;w=%d", spec.Max, windowSeconds))
		c.Header("RateLimit-Limit", strconv.FormatInt(ctx.Limit, 10))
		c.Header("RateLimit-Remaining", strconv.FormatInt(ctx.Remaining, 10))
		c.Header("RateLimit-Reset", strconv.FormatInt(ctx.Reset, 10))

		if ctx.Reached {
			retryAfter := ctx.Reset - time.Now().Unix()
			if retryAfter < 0 {
				retryAfter = 0
			}
			c.Header("Retry-After", strconv.FormatInt(retryAfter, 10))
			// 关键排障信息：哪个限流器、哪条路径、哪个 IP。
			// 不加这行时，日志里只有 "status=429"，无法区分是登录、refresh
			// 还是全局限流命中（排查"掉登录"时踩过这个坑）。
			if opts.Log != nil {
				opts.Log.Warn("auth event",
					"event", model.AuthEventRateLimited,
					"spec", spec.Name,
					"path", path,
					"ip", ip,
					"retry_after_s", retryAfter,
				)
			}
			c.AbortWithStatusJSON(429, gin.H{"message": spec.Message})
			return
		}
		c.Next()
	}
}

// newEmailFromBody 读取请求体中 newEmail 字段（缺省回退 'unknown'）。
func newEmailFromBody(c *gin.Context) string {
	body := GetBody(c)
	if body == nil {
		return "unknown"
	}
	if v, ok := body["newEmail"].(string); ok && v != "" {
		return v
	}
	return "unknown"
}

// emailFromBody 读取请求体中 email 字段（小写、去空白；缺省返回空串）。
// 供凭证/邮件类限流把 key 绑定到目标账号，避免伪造代理标头轮换 IP 绕过。
func emailFromBody(c *gin.Context) string {
	body := GetBody(c)
	if body == nil {
		return ""
	}
	if v, ok := body["email"].(string); ok {
		return strings.ToLower(strings.TrimSpace(v))
	}
	return ""
}
