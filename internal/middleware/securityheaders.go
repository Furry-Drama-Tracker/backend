package middleware

import (
	"github.com/gin-gonic/gin"
)

// 安全响应头集合（固定契约，勿随意增删改）。
//
// 配置项 crossOriginEmbedderPolicy / crossOriginOpenerPolicy /
// crossOriginResourcePolicy 均为 false（不设置对应头）；其余默认项全部设置。
//
// Strict-Transport-Security：恒定输出 max-age=31536000;
// includeSubDomains，不区分生产/开发（固定行为）。
const (
	cspHeader = "default-src 'self';script-src 'self' 'unsafe-inline' blob:;style-src 'self' 'unsafe-inline';img-src 'self' data: blob: https:;font-src 'self';connect-src 'self' https://api.mymemory.translated.net https://translate.googleapis.com https://api.cognitive.microsofttranslator.com https://ipapi.co;frame-src 'self' https://player.bilibili.com https://www.youtube.com https://embed.nicovideo.jp;object-src 'none';upgrade-insecure-requests;base-uri 'self';form-action 'self';frame-ancestors 'self';script-src-attr 'none'"
)

// SecurityHeaders 设置安全响应头。必须尽早注册（在会写头的中间件之前），
// 固定放置位置：先于 CORS / body 解析。
func SecurityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Content-Security-Policy", cspHeader)
		c.Header("Origin-Agent-Cluster", "?1")
		c.Header("Referrer-Policy", "no-referrer")
		c.Header("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=()")
		c.Header("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-DNS-Prefetch-Control", "off")
		c.Header("X-Download-Options", "noopen")
		c.Header("X-Frame-Options", "SAMEORIGIN")
		c.Header("X-Permitted-Cross-Domain-Policies", "none")
		c.Header("X-XSS-Protection", "0")
		c.Next()
	}
}
