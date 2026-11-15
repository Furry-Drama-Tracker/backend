package middleware

import (
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/Furry-Drama-Tracker/backend/internal/errors"
)

// RequestRef 为每个请求生成参考号（0-999999999 随机十进制整数，crypto/rand）：
//   - c.Set(errors.ContextRefID, ref)：渲染器（errors 包）与请求日志读取；
//   - 响应头 X-Ref-ID 携带十进制整数串（运维/curl 关联用；前端契约是对
//     JSON 整数字段 ref 补零到 9 位后按三位一段空格分组展示）。
//
// 必须注册在全局中间件链最前，保证后续所有中间件与错误渲染器可用。
func RequestRef() gin.HandlerFunc {
	return func(c *gin.Context) {
		ref := errors.NewRefID()
		c.Set(errors.ContextRefID, ref)
		c.Header("X-Ref-ID", strconv.FormatInt(ref, 10))
		c.Next()
	}
}

// refFromCtx 读取请求参考号；RequestRef 缺失（单测引擎）时返回 0。
func refFromCtx(c *gin.Context) int64 {
	if v, ok := c.Get(errors.ContextRefID); ok {
		if n, ok := v.(int64); ok {
			return n
		}
	}
	return 0
}

// codedBody 在中间件固定契约响应字段上补 status/error（错误码）/ref（参考号）。
// message 文本保持不变（auth.Protect 等响应契约为固定值，勿内嵌出错编号后缀）；
// 编号展示由前端从 JSON 字段拼装。
func codedBody(c *gin.Context, status int, code string, fields gin.H) gin.H {
	fields["status"] = status
	fields["error"] = code
	fields["ref"] = refFromCtx(c)
	return fields
}
