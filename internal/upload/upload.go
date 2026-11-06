// Package upload 实现图片上传能力：
// 磁盘存储、扩展名 + MIME 过滤、魔数校验、大小限制。
//
// 错误契约（配合 internal/errors 的 AbortWithAppError / UploadError 渲染）：
//   - 类型不符 → UploadError{Code:"LIMIT_FILE_TYPE"}
//   - 魔数不符 → UploadError{Code:"BAD_MAGIC"}（文件已删除）
//   - 超出大小 → UploadError{Code:"LIMIT_FILE_SIZE"}
//   - 无文件   → ErrNoFile（调用方应回 400 {"message":"请选择要上传的图片"}，
//     固定契约）
package upload

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	apperrors "github.com/xiedada05/furry-drama-be-neo/internal/errors"
)

// DefaultUploadsDir 与 config.ServerConfig.UploadsDir 的默认值一致（./uploads）。
const DefaultUploadsDir = "./uploads"

// Dir 是当前上传存储目录（SaveImage 的落盘位置），默认 ./uploads。
// 启动时用 SetDir 覆盖为 cfg.Server.UploadsDir。
var Dir = DefaultUploadsDir

// SetDir 覆盖上传存储目录（空串忽略）。应传 cfg.Server.UploadsDir。
func SetDir(dir string) {
	if dir != "" {
		Dir = dir
	}
}

// ErrNoFile 表示请求中没有指定字段的文件（对应路由层
// 400 {"message":"请选择要上传的图片"} 校验，不属于上传错误码契约）。
var ErrNoFile = errors.New("请选择要上传的图片")

// 扩展名与 MIME 过滤正则：
//   - ext：必须完整匹配 .jpg/.jpeg/.png/.gif/.webp（避免 .notjpg 等绕过）
//   - mime：/^image\/(jpeg|png|gif|webp)$/
var (
	allowedExtRe  = regexp.MustCompile(`^\.(jpe?g|png|gif|webp)$`)
	allowedMimeRe = regexp.MustCompile(`^image/(jpeg|png|gif|webp)$`)
)

// magicPrefixes 是前 12 字节 base64 后的魔数前缀（固定集合，勿改）。
var magicPrefixes = []string{"/9j/", "iVBOR", "R0lGOD", "UklGR"}

// 错误文案（固定契约，勿本地化/勿改）。
const (
	typeErrMsg  = "仅支持图片文件 (jpg, jpeg, png, gif, webp)"
	magicErrMsg = "文件内容与类型不匹配，仅支持图片文件"
	sizeErrMsg  = "File too large" // LIMIT_FILE_SIZE 的固定 message
)

// uploadBodySlack 是 multipart 体限制相对 maxBytes 的余量（multipart 边界、
// 表单头与其它字段的开销），配合落盘后的 file.Size 精确判断实现
// "文件内容 ≤ maxBytes" 语义（大小限制只统计文件内容，不含开销，固定语义）。
const uploadBodySlack = 1 << 20

// SaveImage 读取并保存一个上传图片，返回可访问 URL（"/uploads/<文件名>"）。
// 处理流程固定（勿调整顺序）：
//
//  1. 请求体超 maxBytes+1MB（粗限）或落盘后文件内容超 maxBytes（精限）
//     → UploadError{LIMIT_FILE_SIZE}。
//  2. 扩展名（上传文件名的小写扩展名命中 /jpeg|jpg|png|gif|webp/）且 MIME
//     （Content-Type 命中 ^image/(jpeg|png|gif|webp)$）不满足 → UploadError{LIMIT_FILE_TYPE}。
//  3. 落盘到 Dir，文件名为 "<prefix>-<16 字节 hex><原扩展名>"。
//  4. 前 12 字节 base64 魔数校验失败 → 删除文件并 UploadError{BAD_MAGIC}。
//
// 无文件（含非 multipart 请求）→ ErrNoFile。maxBytes<=0 时默认 5MB
// （默认值固定）。
func SaveImage(c *gin.Context, field, prefix string, maxBytes int64) (string, error) {
	if maxBytes <= 0 {
		maxBytes = 5 << 20
	}
	if !strings.HasPrefix(c.ContentType(), "multipart/form-data") {
		return "", ErrNoFile
	}

	// 粗限：整个 multipart 体（含开销）上限 maxBytes+1MB。
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes+uploadBodySlack)
	file, err := c.FormFile(field)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return "", &apperrors.UploadError{Code: "LIMIT_FILE_SIZE", Message: sizeErrMsg}
		}
		if errors.Is(err, http.ErrMissingFile) {
			return "", ErrNoFile
		}
		return "", fmt.Errorf("读取上传文件失败: %w", err)
	}

	// 精限：文件内容本身不能超过 maxBytes（只统计文件内容，固定语义）。
	if file.Size > maxBytes {
		return "", &apperrors.UploadError{Code: "LIMIT_FILE_SIZE", Message: sizeErrMsg}
	}

	// 扩展名 + MIME 过滤（先于魔数校验，固定顺序）。
	ext := strings.ToLower(path.Ext(file.Filename))
	if !allowedExtRe.MatchString(ext) || !allowedMimeRe.MatchString(file.Header.Get("Content-Type")) {
		return "", &apperrors.UploadError{Code: "LIMIT_FILE_TYPE", Message: typeErrMsg}
	}

	// 落盘：<prefix>-<16字节hex><原扩展名>（保留原始大小写）。
	filename := fmt.Sprintf("%s-%s%s", prefix, randHex(16), path.Ext(file.Filename))
	dstPath := path.Join(Dir, filename)
	if err := saveToDisk(file, dstPath); err != nil {
		return "", fmt.Errorf("保存上传文件失败: %w", err)
	}

	// 魔数校验：前 12 字节 base64 前缀匹配；失败删除已落盘文件。
	if !validateMagicBytes(dstPath) {
		_ = os.Remove(dstPath)
		return "", &apperrors.UploadError{Code: "BAD_MAGIC", Message: magicErrMsg}
	}

	return "/uploads/" + filename, nil
}

// saveToDisk 把 multipart 文件写入 dstPath。
func saveToDisk(file *multipart.FileHeader, dstPath string) error {
	src, err := file.Open()
	if err != nil {
		return err
	}
	defer src.Close()
	// O_EXCL：文件名随机，正常不会冲突；同时避免覆盖已有文件或跟随符号链接。
	dst, err := os.OpenFile(dstPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(dst, src)
	closeErr := dst.Close()
	if copyErr != nil {
		_ = os.Remove(dstPath)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(dstPath)
		return closeErr
	}
	// 兜底去执行位（Windows 为空操作）；失败则删除文件，避免留下未知权限的文件。
	if err := stripExecBits(dstPath); err != nil {
		_ = os.Remove(dstPath)
		return err
	}
	return nil
}

// validateMagicBytes 读取文件前 12 字节，base64 后检查魔数前缀
// （读失败视为不匹配，固定行为）。
func validateMagicBytes(filePath string) bool {
	f, err := os.Open(filePath)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, 12)
	_, _ = f.Read(buf) // 短文件时剩余字节为 0（零填充）
	header := base64.StdEncoding.EncodeToString(buf)
	for _, magic := range magicPrefixes {
		if strings.HasPrefix(header, magic) {
			return true
		}
	}
	return false
}

// randHex 生成 n 字节随机数的 hex 字符串。
func randHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		// 随机源极低概率失败：回退纳秒时间戳派生，保证不 panic。
		return hex.EncodeToString([]byte(fmt.Sprintf("%020d", time.Now().UnixNano())))
	}
	return hex.EncodeToString(buf)
}

// RemoveFile 删除 uploads 目录下的文件（path 为相对文件名，不含 /uploads/ 前缀）。
// 文件不存在返回 nil（删除旧文件时忽略不存在错误，固定行为）。
//
// 防御性校验：只接受 Dir 直属的单层文件名（SaveImage 生成 "<prefix>-<hex>.<ext>"，
// 永不包含分隔符或 ".."）。空串/"."/".." 或含路径分隔符/ ".." 的路径一律拒绝，
// 防止 DB/URL 被污染时越权删除（含误删 Dir 本身或其父目录）。
func RemoveFile(path string) error {
	if path == "" || path == "." || path == ".." ||
		filepath.Base(path) != path || strings.Contains(path, "..") {
		return errors.New("非法的上传文件路径")
	}
	full := filepath.Join(Dir, path)
	if _, err := os.Stat(full); os.IsNotExist(err) {
		return nil
	}
	return os.Remove(full)
}
