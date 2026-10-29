package middleware

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	ulule "github.com/ulule/limiter/v3"
	memory "github.com/ulule/limiter/v3/drivers/store/memory"

	"github.com/xiedada05/furry-drama-be-neo/internal/ratelimit"
)

// RateLimitOpts 是 RateLimit 的挂载与开关参数。
type RateLimitOpts struct {
	// TrustXFF 生产环境为 true：优先取 X-Forwarded-For 首值作为客户端 IP
	// （信任代理=1，取 XFF 链上第一个地址）。开发环境忽略该头。
	TrustXFF bool
	// IsDev 等价于 NODE_ENV !== 'production'。
	IsDev bool
	// SkipRateLimit 对应 SKIP_RATE_LIMIT=1。
	SkipRateLimit bool
}

// specLimiter 封装 ulule/limiter（每个命名限流器一个独立 limiter + 内存 store）。
type specLimiter struct {
	spec ratelimit.Spec
	lim  *ulule.Limiter
}

// RateLimit 构造一个命名限流中间件（挂载语义为固定契约），
// 计数由 github.com/ulule/limiter 提供：
//
//   - spec.Mounts 为空 → 全局型：匹配 /api 与全部 /api/ 前缀（含 /api/v1/、
//     /api/health），并跳过 /api/translate 与 /api/auth/captcha 路径
//     （全局型固定 skip 规则）；
//   - spec.Mounts 非空 → 端点型：只匹配 /api/<Mount> 精确与子路径，
//     【不】匹配 /api/v1/ 前缀（限流挂载不对称铁律，勿改）。
//
// auth（AuthSpec）在非生产且 SKIP_RATE_LIMIT=1 时整体跳过（该 spec 固定 skip 规则）。
// requestEmailChange 的 key 生成规则是固定契约，勿改（改动会使既有计数失效）：
// [object Object]:<newEmail 小写>（keyGenerator 把请求对象传给了 ipKeyGenerator
// 所致的历史怪癖）。
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

		ip := clientIP(c, opts.TrustXFF)
		key := spec.Name + ":" + ratelimit.IPKey(ip)
		if spec.Name == ratelimit.RequestEmailChangeName {
			key = spec.Name + ":[object Object]:" + strings.ToLower(newEmailFromBody(c))
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

// clientIP 解析客户端 IP：生产（trustXFF）依次优先 CF-Connecting-IP（Cloudflare
// 注入的单值头）、X-Real-IP（nginx proxy_set_header 注入的单值头）、
// X-Forwarded-For 首值，否则取 RemoteAddr 去端口。
//
// 只认 XFF 时，若反代仅设置 X-Real-IP（常见 nginx 配置）或 Cloudflare 直连回源，
// RemoteAddr 恒为代理 IP——所有用户共享同一限流桶，表现为"限流没分 IP、误伤全员"。
// 补充单值头兜底后三种部署（CF / nginx / 直连回源）均能取到真实客户端 IP。
func clientIP(c *gin.Context, trustXFF bool) string {
	if trustXFF {
		if cf := strings.TrimSpace(c.GetHeader("CF-Connecting-IP")); cf != "" {
			return cf
		}
		if xri := strings.TrimSpace(c.GetHeader("X-Real-IP")); xri != "" {
			return xri
		}
		if xff := c.GetHeader("X-Forwarded-For"); xff != "" {
			if first := ratelimit.NormalizeXFF(xff); first != "" {
				return first
			}
		}
	}
	return remoteIP(c.Request.RemoteAddr)
}

// remoteIP 去掉 "IP:port" 中的端口（兼容 "[v6]:port"）。
func remoteIP(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}
