package router

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestMountRegistersOnlyAPI 锁定单前缀挂载：v1 镜像不得重新出现。
func TestMountRegistersOnlyAPI(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	api := r.Group("/api")
	Mount(api, "/ping", func(g *gin.RouterGroup) {
		g.GET("", func(c *gin.Context) { c.Status(http.StatusOK) })
	})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/ping", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("/api/ping 状态码=%d, want 200", w.Code)
	}
	if got := w.Header().Get("Deprecation"); got != "" {
		t.Fatalf("/api/ping 不应带 Deprecation 头: %q", got)
	}

	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/ping", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("/api/v1/ping 状态码=%d, want 404", w.Code)
	}
}
