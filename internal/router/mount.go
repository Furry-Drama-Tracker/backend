// Package router 提供领域路由的挂载辅助。
package router

import (
	"github.com/gin-gonic/gin"
)

// Mount 把 register 注册的 handler 挂载到 <parent>/<mountPath>。
// 典型用法：parent 为 /api 路由组（继承全局中间件与限流），
// mountPath 形如 "/auth"（首部含斜杠）。
func Mount(parent *gin.RouterGroup, mountPath string, register func(g *gin.RouterGroup)) {
	register(parent.Group(mountPath))
}
