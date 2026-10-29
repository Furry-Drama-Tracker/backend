package ratelimit

import "time"

// ViewSpec 观看计数限流器 10/60s（窗口 60s、上限 10，
// 对应 PUT /:id/view 与 PUT /single/:id/view）。
//
// 注意：本实现的 RateLimit 挂载语义为「/api/<Mount> 精确 + 子路径前缀」，
// 动态路径 /episodes/:id/view 无法用前缀精确表达，故本 Mounts 列表按任务约定
// 声明为 /episodes/view 与 /episodes/single/:id/view；实际动态路由不会被命中，
// 限流不生效（已知怪癖，勿"修复"挂载语义）。窗口/上限/文案为固定值，勿改。
var ViewSpec = Spec{
	Name:    "view",
	Mounts:  []string{"/episodes/view", "/episodes/single/:id/view"},
	Window:  time.Minute,
	Max:     10,
	Message: "操作过于频繁，请稍后再试",
}
