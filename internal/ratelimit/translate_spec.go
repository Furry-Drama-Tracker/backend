package ratelimit

import "time"

// TranslateSpec 翻译接口限流器 200/60s（窗口 60s、上限 200、
// message 'Too many translation requests'，对应 POST / 与 POST /batch）。
//
// 挂载路径声明为 /translate：middleware.RateLimit 的端点型匹配为「/api/<Mount> 精确 +
// 子路径前缀」，单个挂载即可覆盖 /api/translate、/api/translate/ 与 /api/translate/batch
// （同一 limiter 对全部子路由生效，固定语义）。
// 注意：每次 h.RL(Spec) 调用会新建一个独立内存计数（与 register_routes.go 现有
// 多路径限流器的既有模式一致）。
var TranslateSpec = Spec{
	Name:    "translate",
	Mounts:  []string{"/translate"},
	Window:  time.Minute,
	Max:     200,
	Message: "Too many translation requests",
}
