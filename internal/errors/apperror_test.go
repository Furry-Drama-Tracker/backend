package errors

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func render(t *testing.T, isDev bool, ae *AppError) (int, map[string]any) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/x", func(c *gin.Context) {
		abortAppError(c, ae, isDev)
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/x", nil))
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %q: %v", w.Body.String(), err)
	}
	return w.Code, body
}

// TestAbortAppErrorCodedBody 4xx/5xx 响应体统一附带 error（错误码）与 ref（参考号）；
// 200 不经过本渲染器（由 handler 直接写），此处仅验证 4xx/5xx 形状。
func TestAbortAppErrorCodedBody(t *testing.T) {
	for _, st := range []int{400, 401, 403, 404, 419, 429, 500, 502, 504} {
		code, body := render(t, true, &AppError{Message: "msg", Status: st, Code: "TST0001"})
		if code != st {
			t.Fatalf("status = %d, want %d", code, st)
		}
		if body["error"] != "TST0001" {
			t.Fatalf("st=%d error = %v", st, body["error"])
		}
		if body["status"] != float64(st) {
			t.Fatalf("st=%d body.status = %v", st, body["status"])
		}
		ref, ok := body["ref"].(float64)
		if !ok || ref < 0 || ref >= 1_000_000_000 {
			t.Fatalf("st=%d ref = %v", st, body["ref"])
		}
		if _, ok := body["messageKey"]; ok {
			t.Fatalf("st=%d 不应出现空 messageKey", st)
		}
	}
}

// TestAbortAppErrorRefIsInteger ref 必须是 JSON 整数（前端契约：补零 9 位后三位一组）。
func TestAbortAppErrorRefIsInteger(t *testing.T) {
	_, body := render(t, true, &AppError{Message: "m", Status: 400, Code: "TST0001"})
	if _, ok := body["ref"].(float64); !ok {
		t.Fatalf("ref 应为数值, got %T", body["ref"])
	}
}

// TestAbortAppErrorProdPlainMessage 生产环境 message 恒为纯文案（无编号后缀），
// 编号只经 error/ref JSON 字段传递；开发环境行为一致。
func TestAbortAppErrorProdPlainMessage(t *testing.T) {
	ae := &AppError{Message: "该邮箱已注册", Status: 400, Code: "USR0007"}
	_, body := render(t, false, ae)
	if body["message"] != "该邮箱已注册" {
		t.Fatalf("prod message = %v, 不应内嵌编号", body["message"])
	}
	if body["error"] != "USR0007" {
		t.Fatalf("prod error = %v", body["error"])
	}
	_, body = render(t, true, ae)
	if body["message"] != "该邮箱已注册" {
		t.Fatalf("dev message = %v", body["message"])
	}
}

// TestAbortAppErrorFallbackCode 未编码错误回退 SYS0001；
// 生产 5xx message 隐藏内部文案（纯文案，无后缀）。
func TestAbortAppErrorFallbackCode(t *testing.T) {
	_, body := render(t, true, &AppError{Message: "inner", Status: 500})
	if body["error"] != fallbackCode {
		t.Fatalf("error = %v, want %s", body["error"], fallbackCode)
	}
	if body["message"] != "inner" {
		t.Fatalf("dev 5xx message = %v", body["message"])
	}
	_, body = render(t, false, &AppError{Message: "inner", Status: 500})
	if body["message"] != "服务器内部错误" {
		t.Fatalf("prod 5xx message = %v", body["message"])
	}
}

// TestAbortAppErrorStatusFallbackCodes 400/404 未编码时分别回退 SYS0002/SYS0003。
func TestAbortAppErrorStatusFallbackCodes(t *testing.T) {
	for _, tc := range []struct {
		status int
		code   string
	}{
		{http.StatusBadRequest, "SYS0002"},
		{http.StatusNotFound, "SYS0003"},
		{http.StatusInternalServerError, fallbackCode},
	} {
		_, body := render(t, true, &AppError{Message: "m", Status: tc.status})
		if body["error"] != tc.code {
			t.Fatalf("status=%d error=%v want %s", tc.status, body["error"], tc.code)
		}
	}
}

// TestUploadErrorCodedEnvelope 上传错误也走统一 4xx 信封，历史 Code 映射为 UPL 码。
func TestUploadErrorCodedEnvelope(t *testing.T) {
	for _, tc := range []struct {
		legacy string
		code   string
	}{
		{"LIMIT_FILE_SIZE", "UPL0001"},
		{"LIMIT_FILE_TYPE", "UPL0002"},
		{"BAD_MAGIC", "UPL0003"},
		{"OTHER", "UPL0004"},
	} {
		gin.SetMode(gin.TestMode)
		r := gin.New()
		r.GET("/x", func(c *gin.Context) {
			AbortWithAppError(c, &UploadError{Code: tc.legacy, Message: "msg"}, true)
		})
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", "/x", nil))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s status=%d want 400", tc.legacy, w.Code)
		}
		var body map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("%s decode %q: %v", tc.legacy, w.Body.String(), err)
		}
		if body["error"] != tc.code || body["status"] != float64(http.StatusBadRequest) {
			t.Fatalf("%s body=%v want error=%s status=400", tc.legacy, body, tc.code)
		}
		if _, ok := body["ref"].(float64); !ok {
			t.Fatalf("%s ref 缺失: %v", tc.legacy, body)
		}
		if body["message"] != "文件上传错误: msg" {
			t.Fatalf("%s message=%v", tc.legacy, body["message"])
		}
	}
}

// TestHandlerUploadErrorCodedEnvelope 全局 Handler 捕获 UploadError 时同样带码。
func TestHandlerUploadErrorCodedEnvelope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(Handler(func() bool { return true }))
	r.GET("/x", func(c *gin.Context) {
		_ = c.Error(&UploadError{Code: "BAD_MAGIC", Message: "bad"})
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/x", nil))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400 body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %q: %v", w.Body.String(), err)
	}
	if body["error"] != "UPL0003" || body["status"] != float64(http.StatusBadRequest) {
		t.Fatalf("body=%v", body)
	}
}

// TestAbortAppErrorKeepsExtraFields messageKey / Extra 附加字段保留；
// Extra 不允许覆盖 message/messageKey/error/ref。
func TestAbortAppErrorKeepsExtraFields(t *testing.T) {
	ae := NewExtraC("TST0002", 400, "msg", map[string]any{
		"emailTaken": true, "message": "hack", "error": "hack", "ref": -1,
	})
	ae.MessageKey = "auth.emailTaken"
	_, body := render(t, true, ae)
	if body["emailTaken"] != true {
		t.Fatalf("emailTaken = %v", body["emailTaken"])
	}
	if body["messageKey"] != "auth.emailTaken" {
		t.Fatalf("messageKey = %v", body["messageKey"])
	}
	if body["message"] != "msg" || body["error"] != "TST0002" {
		t.Fatalf("保留字段被 Extra 覆盖: %v", body)
	}
}

// TestAbortAppErrorStatusZero 状态 0 回退 500。
func TestAbortAppErrorStatusZero(t *testing.T) {
	code, body := render(t, true, &AppError{Message: "m", Code: "TST0003"})
	if code != http.StatusInternalServerError {
		t.Fatalf("status = %d", code)
	}
	if body["error"] != "TST0003" {
		t.Fatalf("error = %v", body["error"])
	}
}

// TestNewRefIDRange 参考号取值 0-999999999（000000000-999999999）。
func TestNewRefIDRange(t *testing.T) {
	for i := 0; i < 10000; i++ {
		if n := NewRefID(); n < 0 || n >= 1_000_000_000 {
			t.Fatalf("ref out of range: %d", n)
		}
	}
}

// TestCallerSiteSkip 钉死 skip 语义：1 = CallerSite 的直接调用方。
// 历史上 Abort500C 用 skip=1 导致 site 恒指 apperror.go 自身（无定位能力），
// 此处连同 wrapper 场景一起防回归。
func TestCallerSiteSkip(t *testing.T) {
	if site := CallerSite(1); !strings.Contains(site, "apperror_test.go:") {
		t.Fatalf("CallerSite(1) = %q, 应指向本测试文件", site)
	}
}

// callerSiteViaHelper 模拟 serverError 式共享助手：助手内取 skip=2 = 助手的调用方。
func callerSiteViaHelper() string { return CallerSite(2) }

func TestCallerSiteSeesThroughWrapper(t *testing.T) {
	if site := callerSiteViaHelper(); !strings.Contains(site, "apperror_test.go:") {
		t.Fatalf("helper 转递 site = %q, 应看到测试调用行", site)
	}
}

// TestAbort500SiteC5xxLog 显式 site 的 5xx 出口：日志带 code/ref/path/site/cause；
// Abort500C（skip 修正后）自动抓取自身调用方而非 apperror.go 内部行。
func TestAbort500SiteC5xxLog(t *testing.T) {
	var buf bytes.Buffer
	prev := logger
	SetLogger(slog.New(slog.NewTextHandler(&buf, nil)))
	defer func() { logger = prev }()

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/api/episodes/x/episodes", nil)
	Abort500SiteC(c, "internal/handler/episodes.go:1026", "EPS0037", "Server error",
		errors.New("boom"), false)
	for _, want := range []string{
		"msg=5xx", "code=EPS0037", "path=/api/episodes/x/episodes",
		"site=internal/handler/episodes.go:1026", "cause=boom", "ref=",
	} {
		if !strings.Contains(buf.String(), want) {
			t.Fatalf("5xx 日志缺 %q:\n%s", want, buf.String())
		}
	}

	buf.Reset()
	c2, _ := gin.CreateTestContext(httptest.NewRecorder())
	c2.Request = httptest.NewRequest("GET", "/y", nil)
	Abort500C(c2, "TST0500", "m", errors.New("x"), false)
	if !strings.Contains(buf.String(), "site=internal/errors/apperror_test.go:") {
		t.Fatalf("Abort500C site 未指向调用方:\n%s", buf.String())
	}
}
