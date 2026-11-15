// Package errors 提供应用错误类型与全局错误处理中间件。
//
// 全局错误响应规则：
//   - CORS 拒绝 → 403 {"message":"CORS policy denied"}
//   - 文件上传错误（大小/类型超限等）→ 400 {"message":"文件上传错误: <msg>"}
//   - 其它 → err.Status || 500；生产环境 5xx 不泄露内部 message（"服务器内部错误"）
//   - 开发环境响应体附带 stack
//   - 4xx/5xx 响应体统一附带 error（错误码 XXXYYYY，模块前缀+文件内顺序号，
//     契约见 backend/AGENTS.md「错误码与参考号」）与 ref（0-999999999 随机参考号，
//     JSON 整数；前端按三位一段空格分组展示）。
//     生产环境 message 末尾追加 " [CODE·ref]"，非技术用户可直接复制报告；
//     开发环境 message 保持原文。
//   - 5xx 渲染时额外打一行 ERROR 日志（code/ref/cause/site/path），
//     site 为错误构造点 file:line（runtime.Caller 自动抓取）。
package errors

import (
	crand "crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"net/http"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// ContextRefID 是 gin.Context 中本请求参考号（int64，0-999999999）的键。
// middleware.RequestRef 写入，本包渲染器读取；键定义在此以避免 middleware → errors 反向依赖环。
const ContextRefID = "refID"

// refMax 是参考号上界（半开区间），取值空间 0..999999999 即 000000000-999999999。
const refMax = 1_000_000_000

// fallbackCode 是未编码 4xx/5xx 的兜底错误码。
const fallbackCode = "SYS0001"

// 包级日志器：server.NewApp 启动时注入一次；未注入回退 slog.Default()。
var logger *slog.Logger

// SetLogger 注入包级日志器（应用启动时调用一次；nil 忽略）。
func SetLogger(l *slog.Logger) {
	if l != nil {
		logger = l
	}
}

// NewRefID 生成随机参考号：0-999999999 的十进制整数（crypto/rand）。
func NewRefID() int64 {
	n, err := crand.Int(crand.Reader, big.NewInt(refMax))
	if err != nil {
		// 兜底：crypto/rand 实际不可能失败，仅防御性处理。
		return time.Now().UnixNano() % refMax
	}
	return n.Int64()
}

// AppError 是领域层抛出的可操作错误：携带 HTTP 状态码与可选的 messageKey（前端 i18n 键）。
type AppError struct {
	Message    string
	Status     int
	MessageKey string
	// Code 是错误码 XXXYYYY（见 AGENTS.md），响应体 error 字段；空则渲染时回退 SYS0001。
	Code string
	// Cause 保留底层错误（DB/网络等），用于日志，不直接暴露给客户端。
	Cause error
	// IsOperational 标识"预期内的操作错误"；非操作错误（如 panic、DB 断连）在生产隐藏详情。
	IsOperational bool
	// Extra 附加到响应体的标志字段（如登录"账号不存在"的 accountNotFound、
	// 注册"邮箱已占用"的 emailTaken），供前端做下一步引导（跳注册/跳登录）。
	Extra map[string]any
	// site 是错误构造点 "file:line"（构造器自动抓取），仅用于 5xx 日志定位，不进响应体。
	site string
}

// Error 实现 error 接口。
func (e *AppError) Error() string {
	if e.MessageKey != "" {
		return fmt.Sprintf("[%d] %s (key=%s)", e.Status, e.Message, e.MessageKey)
	}
	return fmt.Sprintf("[%d] %s", e.Status, e.Message)
}

// Unwrap 支持 errors.Is / errors.As 透传 Cause。
func (e *AppError) Unwrap() error { return e.Cause }

// callerSite 抓取第 skip 层调用点 "file:line"（路径裁剪到 backend/ 之后）。
func callerSite(skip int) string {
	_, file, line, ok := runtime.Caller(skip)
	if !ok {
		return ""
	}
	if i := strings.LastIndex(file, "/backend/"); i >= 0 {
		file = file[i+len("/backend/"):]
	} else {
		file = filepath.Base(file)
	}
	return fmt.Sprintf("%s:%d", file, line)
}

// New 构造一个操作错误（无错误码；渲染时 error 字段回退 SYS0001）。
func New(status int, message string) *AppError {
	return &AppError{Message: message, Status: status, IsOperational: true}
}

// NewKey 构造带 messageKey 的操作错误（鉴权中间件使用）。
func NewKey(status int, message, messageKey string) *AppError {
	return &AppError{Message: message, Status: status, MessageKey: messageKey, IsOperational: true}
}

// NewExtra 构造带附加标志字段的操作错误（响应体 message 之外携带布尔标志，
// 供前端识别分支并做引导跳转，如 accountNotFound / emailTaken）。
func NewExtra(status int, message string, extra map[string]any) *AppError {
	return &AppError{Message: message, Status: status, Extra: extra, IsOperational: true}
}

// Wrap 包裹底层错误为一个操作错误。
func Wrap(status int, message string, cause error) *AppError {
	return &AppError{Message: message, Status: status, Cause: cause, IsOperational: true}
}

// ---- 带错误码的构造器家族（契约见 AGENTS.md「错误码与参考号」）----

// NewC 构造带错误码的操作错误，site 自动抓取调用点。
func NewC(code string, status int, message string) *AppError {
	return &AppError{Message: message, Status: status, Code: code, IsOperational: true, site: callerSite(2)}
}

// NewKeyC 构造带错误码与 messageKey 的操作错误。
func NewKeyC(code string, status int, message, messageKey string) *AppError {
	return &AppError{Message: message, Status: status, MessageKey: messageKey, Code: code, IsOperational: true, site: callerSite(2)}
}

// NewExtraC 构造带错误码与附加标志字段的操作错误。
func NewExtraC(code string, status int, message string, extra map[string]any) *AppError {
	return &AppError{Message: message, Status: status, Extra: extra, Code: code, IsOperational: true, site: callerSite(2)}
}

// WrapC 包裹底层错误为带错误码的操作错误。
func WrapC(code string, status int, message string, cause error) *AppError {
	return &AppError{Message: message, Status: status, Cause: cause, Code: code, IsOperational: true, site: callerSite(2)}
}

// IsNotFound 判断错误是否为"资源不存在"语义（404）。
func IsNotFound(err error) bool {
	var ae *AppError
	return errors.As(err, &ae) && ae.Status == http.StatusNotFound
}

// UploadError 是文件上传错误（LIMIT_FILE_SIZE / LIMIT_FILE_TYPE 等），
// 由上传中间件抛给全局处理器。
type UploadError struct {
	Code    string // "LIMIT_FILE_SIZE" | "LIMIT_FILE_TYPE" | ...
	Message string
}

// Error 实现 error 接口。
func (e *UploadError) Error() string { return e.Message }

// IsUploadError 判断错误是否为上传错误。
func IsUploadError(err error) bool {
	var ue *UploadError
	return errors.As(err, &ue)
}

// AbortWithAppError 把 AppError 写入响应并中止请求链。
// 若错误不是 *AppError，回退为 500（生产隐藏 message）。
func AbortWithAppError(c *gin.Context, err error, isDev bool) {
	var ae *AppError
	if errors.As(err, &ae) {
		abortAppError(c, ae, isDev)
		return
	}
	var ue *UploadError
	if errors.As(err, &ue) {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": "文件上传错误: " + ue.Message})
		return
	}
	abortAppError(c, &AppError{Message: err.Error(), Status: http.StatusInternalServerError}, isDev)
}

// Abort500C 渲染 5xx 并中止请求链（handler 直写 5xx 的标准出口）：
// 生产隐藏 message、附带 error/ref、打一行 ERROR 日志（含 site=调用点）。
func Abort500C(c *gin.Context, code, message string, cause error, isDev bool) {
	abortAppError(c, &AppError{
		Message: message, Status: http.StatusInternalServerError, Code: code,
		Cause: cause, IsOperational: true, site: callerSite(1),
	}, isDev)
}

// AbortErrC 同 Abort500C，但状态码显式（502/504 等 5xx 变体使用）。
func AbortErrC(c *gin.Context, status int, code, message string, cause error, isDev bool) {
	abortAppError(c, &AppError{
		Message: message, Status: status, Code: code,
		Cause: cause, IsOperational: true, site: callerSite(1),
	}, isDev)
}

// AbortCoded 渲染 4xx 并中止请求链（handler/中间件直写 4xx 的标准出口）：
// 附带 error/ref；生产 message 末尾追加 " [CODE·ref]"。
func AbortCoded(c *gin.Context, status int, code, message string, isDev bool) {
	abortAppError(c, &AppError{Message: message, Status: status, Code: code, IsOperational: true, site: callerSite(1)}, isDev)
}

// AbortKeyed 同 AbortCoded，另带 messageKey（鉴权等 i18n 场景）。
func AbortKeyed(c *gin.Context, status int, code, message, messageKey string, isDev bool) {
	abortAppError(c, &AppError{Message: message, Status: status, MessageKey: messageKey, Code: code, IsOperational: true, site: callerSite(1)}, isDev)
}

// refFromContext 读取请求参考号；无中间件（单测等）时现场生成。
func refFromContext(c *gin.Context) int64 {
	if v, ok := c.Get(ContextRefID); ok {
		if n, ok := v.(int64); ok {
			return n
		}
	}
	return NewRefID()
}

// abortAppError 写 AppError 响应，遵循包注释声明的统一消息规则。
func abortAppError(c *gin.Context, ae *AppError, isDev bool) {
	status := ae.Status
	if status == 0 {
		status = http.StatusInternalServerError
	}
	msg := ae.Message
	// 生产环境：5xx 隐藏内部 message，只回"服务器内部错误"。
	if status >= 500 && !isDev {
		msg = "服务器内部错误"
	} else if msg == "" {
		msg = "服务器错误"
	}
	body := gin.H{"message": msg}
	if ae.MessageKey != "" {
		body["messageKey"] = ae.MessageKey
	}
	for k, v := range ae.Extra {
		if k == "message" || k == "messageKey" || k == "error" || k == "ref" {
			continue // 保留字段不允许被 Extra 覆盖
		}
		body[k] = v
	}
	// 4xx/5xx 统一附带错误码与参考号；生产 message 末尾内嵌，供非技术用户直接复制报告。
	if status >= 400 {
		code := ae.Code
		if code == "" {
			code = fallbackCode
		}
		ref := refFromContext(c)
		body["error"] = code
		body["ref"] = ref
		if !isDev {
			body["message"] = fmt.Sprintf("%s [%s·%d]", msg, code, ref)
		}
		if status >= 500 {
			log5xx(c, ae, code, ref)
		}
	}
	if isDev && status >= 500 && ae.Cause != nil {
		body["stack"] = ae.Cause.Error()
	}
	c.AbortWithStatusJSON(status, body)
}

// log5xx 为 5xx 打一行 ERROR 日志（code/ref/cause/site/path）。
func log5xx(c *gin.Context, ae *AppError, code string, ref int64) {
	log := logger
	if log == nil {
		log = slog.Default()
	}
	attrs := []any{
		"code", code,
		"ref", ref,
		"path", c.Request.URL.Path,
	}
	if ae.site != "" {
		attrs = append(attrs, "site", ae.site)
	}
	if ae.Cause != nil {
		attrs = append(attrs, "cause", ae.Cause.Error())
	}
	log.Log(c.Request.Context(), slog.LevelError, "5xx", attrs...)
}

// Handler 是全局错误处理中间件，必须最后注册（gin 会捕获后续 handler 的 panic），
// 统一兜底渲染为 {"message": ...} JSON 响应。
func Handler(isDev func() bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()
		// gin 约定：中间件若捕获到 panic 会写入 c.Errors，此处统一转成 AppError 形态。
		if len(c.Errors) > 0 {
			err := c.Errors.Last().Err
			if isUploadError(err) {
				c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": "文件上传错误: " + err.Error()})
				return
			}
			dev := isDev()
			msg := "服务器错误"
			if !dev {
				msg = "服务器内部错误"
			}
			abortAppError(c, &AppError{Message: msg, Status: http.StatusInternalServerError, Code: "SYS0501", site: "apperror.go:Handler"}, dev)
		}
	}
}

func isUploadError(err error) bool {
	var ue *UploadError
	return errors.As(err, &ue)
}
