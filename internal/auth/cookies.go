package auth

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// Cookie 名称（固定契约，勿改——改动会使已签发的会话 cookie 失效）。
const (
	CookieAccessToken  = "accessToken"
	CookieRefreshToken = "refreshToken"
	CookieLegacyToken  = "token"
	CookieCSRF         = "XSRF-TOKEN"
	// CSRFMaxAgeHours 是 CSRF cookie 有效期（默认 24h）。
	CSRFMaxAgeHours = 24
)

// access/refresh 有效期（固定契约：access 15m，refresh 7d，勿改）。
const (
	AccessTTL  = 15 * time.Minute
	RefreshTTL = 7 * 24 * time.Hour
)

// sameSite 帮助：生产用 strict，开发用 lax（清除 cookie 时，固定行为）。
func sameSite(isProd bool) http.SameSite {
	if isProd {
		return http.SameSiteStrictMode
	}
	return http.SameSiteLaxMode
}

// rawSetCookie 直接写 Set-Cookie 响应头（完全控制属性）。
func rawSetCookie(c *gin.Context, name, value string, maxAge time.Duration, path string, httpOnly, secure bool, sameSite http.SameSite) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     path,
		MaxAge:   int(maxAge.Seconds()),
		HttpOnly: httpOnly,
		Secure:   secure,
		SameSite: sameSite,
	})
}

// SetCSRFCookie 签发 CSRF cookie：非 httpOnly（前端 JS 需读取放回头部）、
// sameSite strict、24h、path=/（属性固定，勿改）。
func SetCSRFCookie(c *gin.Context, isProd bool, maxAgeHours int) string {
	if maxAgeHours <= 0 {
		maxAgeHours = CSRFMaxAgeHours
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		// 极低概率随机源失败：回退时间戳派生，保证不 panic。
		return hex.EncodeToString([]byte(time.Now().Format("20060102150405.000000000")))
	}
	token := hex.EncodeToString(buf)
	rawSetCookie(c, CookieCSRF, token, time.Duration(maxAgeHours)*time.Hour, "/", false, isProd, http.SameSiteStrictMode)
	return token
}

// GetCSRFToken 读取请求携带的 CSRF cookie 值。
func GetCSRFToken(c *gin.Context) string {
	if v, err := c.Cookie(CookieCSRF); err == nil {
		return v
	}
	return ""
}

// GetHeaderCSRFToken 读取 X-XSRF-TOKEN 请求头。
func GetHeaderCSRFToken(c *gin.Context) string {
	return c.GetHeader("X-XSRF-TOKEN")
}

// SetAuthCookies 设置 access + refresh cookie。
// access: httpOnly, secure=prod, sameSite=lax, maxAge 15m, path=/
// refresh: httpOnly, secure=prod, sameSite=lax, maxAge 7d, path=/api/auth
func SetAuthCookies(c *gin.Context, accessToken, refreshToken string, isProd bool) {
	rawSetCookie(c, CookieAccessToken, accessToken, AccessTTL, "/", true, isProd, http.SameSiteLaxMode)
	rawSetCookie(c, CookieRefreshToken, refreshToken, RefreshTTL, "/api/auth", true, isProd, http.SameSiteLaxMode)
}

// ClearAuthCookies 清除 accessToken(path=/)、refreshToken(path=/api/auth)、
// 兼容旧 token(path=/)。sameSite：生产 strict，否则 lax（清除语义固定）。
func ClearAuthCookies(c *gin.Context, isProd bool) {
	ss := sameSite(isProd)
	clearCookie(c, CookieAccessToken, "/", ss)
	clearCookie(c, CookieRefreshToken, "/api/auth", ss)
	clearCookie(c, CookieLegacyToken, "/", ss)
}

func clearCookie(c *gin.Context, name, path string, ss http.SameSite) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     name,
		Path:     path,
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: ss,
	})
}

// GetAccessToken 从请求提取 access token：Authorization Bearer → accessToken cookie → 旧 token cookie
//（提取顺序为固定契约，勿重排）。
func GetAccessToken(c *gin.Context) string {
	if h := c.GetHeader("Authorization"); len(h) > 7 && h[:7] == "Bearer " {
		return h[7:]
	}
	if v, err := c.Cookie(CookieAccessToken); err == nil && v != "" {
		return v
	}
	if v, err := c.Cookie(CookieLegacyToken); err == nil && v != "" {
		return v
	}
	return ""
}

// GetRefreshToken 读取 refreshToken cookie。
func GetRefreshToken(c *gin.Context) string {
	if v, err := c.Cookie(CookieRefreshToken); err == nil && v != "" {
		return v
	}
	return ""
}
