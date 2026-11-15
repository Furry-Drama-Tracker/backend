package middleware

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/Furry-Drama-Tracker/backend/internal/errors"
)

// TestRequestRef 每请求生成参考号：写 context、X-Ref-ID 响应头、取值 0-999999999。
func TestRequestRef(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(RequestRef())
	seen := map[int64]bool{}
	r.GET("/x", func(c *gin.Context) {
		ref := refFromCtx(c)
		if ref < 0 || ref >= 1_000_000_000 {
			t.Fatalf("ref out of range: %d", ref)
		}
		seen[ref] = true
		c.Status(200)
	})
	for i := 0; i < 100; i++ {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", "/x", nil))
		if w.Code != 200 {
			t.Fatalf("status = %d", w.Code)
		}
		// 从响应 recorder 读 X-Ref-ID（c.GetHeader 读的是请求头）。
		if got := w.Header().Get("X-Ref-ID"); got == "" {
			t.Fatal("缺少 X-Ref-ID 响应头")
		}
	}
	if len(seen) < 90 {
		t.Fatalf("参考号随机性不足：100 次仅 %d 个不同值", len(seen))
	}
}

// TestRefFromCtxMissing 无 RequestRef 中间件（部分单测引擎）时返回 0。
func TestRefFromCtxMissing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	if refFromCtx(c) != 0 {
		t.Fatal("缺失中间件应返回 0")
	}
	if _, ok := c.Get(errors.ContextRefID); ok {
		t.Fatal("不应写入 context")
	}
}
