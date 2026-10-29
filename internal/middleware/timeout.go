package middleware

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"
)

// requestTimeout 是请求级慢客户端防护超时，固定 30s，勿调。
const requestTimeout = 30 * time.Second

// Timeout 给每个请求注入 30s 的 context deadline（慢客户端防护超时，勿调）。
//
// handler/service 应消费 c.Request.Context()，超时后数据库等阻塞操作会返回
// context deadline exceeded，从而中止慢请求。注意：本实现不主动掐断已开始的
// 写响应，仅在 handler 协作下生效（等价于设置 deadline，上限固定 30s）。
func Timeout() gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), requestTimeout)
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}
