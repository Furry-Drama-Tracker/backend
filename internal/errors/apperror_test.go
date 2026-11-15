package errors

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

// TestAbortAppErrorProdEmbedsCodeRef 生产环境 message 末尾内嵌 [CODE·ref]；
// 开发环境 message 保持原文（字段仍附带）。
func TestAbortAppErrorProdEmbedsCodeRef(t *testing.T) {
	ae := &AppError{Message: "该邮箱已注册", Status: 400, Code: "USR0007"}
	_, body := render(t, false, ae)
	want := "该邮箱已注册 [USR0007·"
	msg, _ := body["message"].(string)
	if len(msg) < len(want) || msg[:len(want)] != want {
		t.Fatalf("prod message = %q, want 前缀 %q", msg, want)
	}
	if body["error"] != "USR0007" {
		t.Fatalf("prod error = %v", body["error"])
	}
	_, body = render(t, true, ae)
	if body["message"] != "该邮箱已注册" {
		t.Fatalf("dev message = %v, 不应内嵌", body["message"])
	}
}

// TestAbortAppErrorFallbackCode 未编码错误回退 SYS0001；
// 生产 5xx message 隐藏内部文案。
func TestAbortAppErrorFallbackCode(t *testing.T) {
	_, body := render(t, true, &AppError{Message: "inner", Status: 500})
	if body["error"] != fallbackCode {
		t.Fatalf("error = %v, want %s", body["error"], fallbackCode)
	}
	if body["message"] != "inner" {
		t.Fatalf("dev 5xx message = %v", body["message"])
	}
	_, body = render(t, false, &AppError{Message: "inner", Status: 500})
	if body["message"] != "服务器内部错误 [SYS0001·" && msgOf(body)[:len("服务器内部错误 [SYS0001·")] != "服务器内部错误 [SYS0001·" {
		t.Fatalf("prod 5xx message = %v", body["message"])
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

func msgOf(body map[string]any) string {
	s, _ := body["message"].(string)
	return s
}
