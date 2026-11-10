package middleware

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Furry-Drama-Tracker/backend/internal/ratelimit"
)

// do 执行一次请求（供本包测试通用）。
func do(r *gin.Engine, method, path string, body string, ct string) *httptest.ResponseRecorder {
	var bodyReader io.Reader
	if body != "" {
		bodyReader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, bodyReader)
	if ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func newTestLimitEngine(spec ratelimit.Spec, opts RateLimitOpts, paths ...string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	// 镜像生产默认标头清单（config defaults()；gin.New() 自带的是 XFF > X-Real-IP）。
	r.RemoteIPHeaders = []string{"CF-Connecting-IP", "X-Real-IP", "X-Forwarded-For"}
	limiter := RateLimit(spec, opts)
	for _, p := range paths {
		r.GET(p, limiter, func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })
	}
	return r
}

// TestRateLimitTriggers429 超过上限 → 429 + message + 限流头。
func TestRateLimitTriggers429(t *testing.T) {
	spec := ratelimit.Spec{Name: "test", Window: time.Minute, Max: 2, Message: "too many"}
	r := newTestLimitEngine(spec, RateLimitOpts{IsDev: true}, "/api/probe")
	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/probe", nil))
		if w.Code != 200 {
			t.Fatalf("请求 %d 应通过: %d", i+1, w.Code)
		}
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/probe", nil))
	if w.Code != 429 {
		t.Fatalf("第 3 次应 429, got %d", w.Code)
	}
	var body map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["message"] != "too many" {
		t.Fatalf("429 message 不符: %v", body)
	}
	if w.Header().Get("RateLimit-Limit") != "2" {
		t.Fatalf("RateLimit-Limit 应为 2: %s", w.Header().Get("RateLimit-Limit"))
	}
}

// TestRateLimitPerEndpointMount 端点型限流只覆盖 /api/<mount>，不匹配其他前缀。
func TestRateLimitPerEndpointMount(t *testing.T) {
	spec := ratelimit.Spec{Name: "login", Mounts: []string{"/auth/login"}, Window: time.Minute, Max: 1, Message: "x"}
	r := newTestLimitEngine(spec, RateLimitOpts{IsDev: true}, "/api/auth/login", "/api/v1/auth/login")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/auth/login", nil))
	if w.Code != 200 {
		t.Fatalf("第一次应通过: %d", w.Code)
	}
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/auth/login", nil))
	if w.Code != 429 {
		t.Fatalf("同路径第二次应 429: %d", w.Code)
	}
	// 非 /api 前缀不命中端点限流，即使路由存在也应放行。
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/auth/login", nil))
	if w.Code != 200 {
		t.Fatalf("非 /api 前缀不应触发端点限流: %d", w.Code)
	}
}

// TestRateLimitAuthSkip dev + SKIP_RATE_LIMIT 时 auth 限流跳过。
func TestRateLimitAuthSkip(t *testing.T) {
	spec := ratelimit.AuthSpec
	r := newTestLimitEngine(spec, RateLimitOpts{IsDev: true, SkipRateLimit: true}, "/api/auth/login")
	for i := 0; i < 6; i++ {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/auth/login", nil))
		if w.Code != 200 {
			t.Fatalf("跳过时应全通过, 第 %d 次: %d", i+1, w.Code)
		}
	}
}

// TestRateLimitClientIPHeaders 生产（信任标头）下 c.ClientIP() 解析优先级：
// CF-Connecting-IP（ESA/CF 边缘覆盖写入，最可信）> X-Real-IP > X-Forwarded-For
// 首值 > RemoteAddr；不同头代表不同用户时各自独立计数（不共享限流桶）。
// 引擎未配 trusted_proxies（默认信任全部来源）时 XFF 首值=最左值，
// 与历史 NormalizeXFF 语义一致。
func TestRateLimitClientIPHeaders(t *testing.T) {
	spec := ratelimit.Spec{Name: "probe", Mounts: []string{"/probe"}, Window: time.Minute, Max: 1, Message: "x"}
	r := newTestLimitEngine(spec, RateLimitOpts{IsDev: false}, "/api/probe")

	get := func(headers map[string]string) int {
		req := httptest.NewRequest(http.MethodGet, "/api/probe", nil)
		req.RemoteAddr = "10.0.0.1:1234"
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w.Code
	}

	// CF 头：同 IP 第 2 次 → 429；换一个 CF IP 又能过（桶按 IP 分离）。
	if c := get(map[string]string{"CF-Connecting-IP": "1.1.1.1"}); c != 200 {
		t.Fatalf("CF IP 第一次应通过: %d", c)
	}
	if c := get(map[string]string{"CF-Connecting-IP": "1.1.1.1"}); c != 429 {
		t.Fatalf("CF IP 第二次应 429: %d", c)
	}
	if c := get(map[string]string{"CF-Connecting-IP": "2.2.2.2"}); c != 200 {
		t.Fatalf("不同 CF IP 应独立计数: %d", c)
	}
	// 三头并存时 CF 优先（ESA 部署下 X-Real-IP 可能被中间层写成本地/节点 IP）。
	if c := get(map[string]string{
		"CF-Connecting-IP": "3.3.3.3", "X-Real-IP": "9.9.9.9", "X-Forwarded-For": "8.8.8.8",
	}); c != 200 {
		t.Fatalf("CF 应优先: %d", c)
	}
	if c := get(map[string]string{"CF-Connecting-IP": "3.3.3.3"}); c != 429 {
		t.Fatalf("三头并存应按 CF IP 计数: %d", c)
	}
	// 无 CF 时 X-Real-IP 优先于 XFF。
	if c := get(map[string]string{"X-Real-IP": "4.4.4.4", "X-Forwarded-For": "1.1.1.1"}); c != 200 {
		t.Fatalf("X-Real-IP 应优先于 XFF: %d", c)
	}
	if c := get(map[string]string{"X-Real-IP": "4.4.4.4"}); c != 429 {
		t.Fatalf("应按 X-Real-IP 计数: %d", c)
	}
	// 仅 XFF 时取首值。
	if c := get(map[string]string{"X-Forwarded-For": "5.5.5.5, 6.6.6.6"}); c != 200 {
		t.Fatalf("XFF 首值应作为独立桶: %d", c)
	}
	if c := get(map[string]string{"X-Forwarded-For": "5.5.5.5, 6.6.6.6"}); c != 429 {
		t.Fatalf("同 XFF 首值第二次应 429: %d", c)
	}
	// 无任何头 → RemoteAddr（10.0.0.1 独立桶）。
	if c := get(nil); c != 200 {
		t.Fatalf("RemoteAddr 应作为独立桶: %d", c)
	}
	if c := get(nil); c != 429 {
		t.Fatalf("同 RemoteAddr 第二次应 429: %d", c)
	}
}

// TestRateLimitClientIPTrustedProxies 配置 trusted_proxies 后来源校验生效：
// 非受信来源伪造的代理标头被整体忽略（按 RemoteAddr 计数），受信来源的标头正常采信。
func TestRateLimitClientIPTrustedProxies(t *testing.T) {
	spec := ratelimit.Spec{Name: "probe", Mounts: []string{"/probe"}, Window: time.Minute, Max: 1, Message: "x"}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.RemoteIPHeaders = []string{"CF-Connecting-IP", "X-Real-IP", "X-Forwarded-For"}
	if err := r.SetTrustedProxies([]string{"127.0.0.1"}); err != nil {
		t.Fatalf("SetTrustedProxies: %v", err)
	}
	limiter := RateLimit(spec, RateLimitOpts{IsDev: false})
	r.GET("/api/probe", limiter, func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	get := func(remote string, headers map[string]string) int {
		req := httptest.NewRequest(http.MethodGet, "/api/probe", nil)
		req.RemoteAddr = remote
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w.Code
	}

	// 非受信来源(10.0.0.1)伪造 CF 头 → 被忽略，按 RemoteAddr 计数：
	// 第二次不带任何头也应命中同一桶（429）。
	if c := get("10.0.0.1:1111", map[string]string{"CF-Connecting-IP": "9.9.9.9"}); c != 200 {
		t.Fatalf("非受信来源第一次应通过: %d", c)
	}
	if c := get("10.0.0.1:1111", nil); c != 429 {
		t.Fatalf("伪造标头应被忽略并按 RemoteAddr 计数: %d", c)
	}
	// 受信来源(127.0.0.1，模拟本机反代)的标头正常采信。
	if c := get("127.0.0.1:2222", map[string]string{"CF-Connecting-IP": "9.9.9.9"}); c != 200 {
		t.Fatalf("受信来源带标头应通过: %d", c)
	}
	if c := get("127.0.0.1:2222", map[string]string{"CF-Connecting-IP": "9.9.9.9"}); c != 429 {
		t.Fatalf("受信来源同标头第二次应 429: %d", c)
	}
	// 受信来源、不同标头 IP → 独立桶。
	if c := get("127.0.0.1:2222", map[string]string{"CF-Connecting-IP": "8.8.8.8"}); c != 200 {
		t.Fatalf("受信来源不同标头 IP 应独立计数: %d", c)
	}
}
