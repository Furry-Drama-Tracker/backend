package auth

// TOTP 基于时间的一次性口令，基于 pquerna/otp 的 hotp 实现，参数为固定契约
// （verifyTOTP / generateTOTPSecret / generateBackupCodes 同一套参数，勿改）：
//   - HMAC-SHA1、30 秒步长、6 位数字码
//   - 验证窗口 ±1（允许时钟偏移一个步长）
//   - secret 为 RFC 4648 标准 base64（20 字节，既有存储格式，勿改）
//   - pquerna hotp 期望 base32 secret：内部把字节重新编码为 base32（保字节不变）
//   - 备用码为 10 个 8 字节（64 位熵）hex 字符串（16 字符）

import (
	"crypto/rand"
	"encoding/base32"
	"encoding/base64"
	"strings"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/hotp"
)

const (
	// TimeStep 是 TOTP 时间步长（秒），固定 30s，勿改。
	TimeStep int64 = 30
	// TOTPWindow 是验证窗口（±1 步长）。
	TOTPWindow = 1
	// totpSecretLen 是 secret 随机字节数（20 字节 = 160 位）。
	totpSecretLen = 20
)

// GenerateTOTPSecret 生成 2FA secret：20 随机字节的标准 base64 字符串（固定格式）。
func GenerateTOTPSecret() string {
	buf := make([]byte, totpSecretLen)
	if _, err := rand.Read(buf); err != nil {
		buf = []byte(time.Now().Format("20060102150405.000000000"))
	}
	return base64.StdEncoding.EncodeToString(buf)
}

// GenerateSecret 是 GenerateTOTPSecret 的别名（模块接口约定名，行为相同）。
func GenerateSecret() string {
	return GenerateTOTPSecret()
}

// GenerateBackupCodes 生成 10 个 2FA 备用码，每个为 8 字节（64 位熵）的
// hex 字符串（16 字符，固定格式）。
func GenerateBackupCodes() []string {
	codes := make([]string, 10)
	for i := range codes {
		codes[i] = RandomHex(8)
	}
	return codes
}

// SecretBase32 把内部 base64 格式的 secret 转为 otpauth:// 规范要求的
// RFC 4648 Base32（无填充大写）——Google/Microsoft Authenticator 等工具的
// 二维码 secret 参数与"手动输入密钥"都只认这个格式。
func SecretBase32(secretBase64 string) (string, error) {
	secretBytes, err := base64.StdEncoding.DecodeString(secretBase64)
	if err != nil {
		return "", err
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(secretBytes), nil
}

// VerifyTOTP 校验 6 位 TOTP 验证码，使用当前时间（30s 步长，窗口 ±1）。
func VerifyTOTP(secret, token string) bool {
	return verifyTOTPAt(secret, token, time.Now())
}

// Verify 是 VerifyTOTP 的别名（模块接口约定名，行为相同）。
func Verify(secret, token string) bool {
	return VerifyTOTP(secret, token)
}

// VerifyTOTPAt 同 VerifyTOTP，但时间点可注入（便于测试与固定时间向量）。
func VerifyTOTPAt(secret, token string, at time.Time) bool {
	return verifyTOTPAt(secret, token, at)
}

// VerifyTOTPStep 同 VerifyTOTP，但额外返回命中的时间步（Unix 秒 / TimeStep）；
// 未命中返回 -1。调用方用该步做一次性消费——同一时间步不得被接受两次，
// 以实现 NIST SP 800-63B 要求的 OTP 重放防护。
func VerifyTOTPStep(secret, token string, at time.Time) (int64, bool) {
	return verifyTOTPStepAt(secret, token, at)
}

// verifyTOTPAt 的逐字段行为是固定契约，用 pquerna hotp 计算：
//   - token 非 6 位数字字符串 → false
//   - secret 按标准 base64 解码（解码失败 → false）
//   - 字节重新编码为 base32（保字节）供 pquerna 使用
//   - 窗口 [-1, +1]，步长 30s，HMAC-SHA1
func verifyTOTPAt(secret, token string, at time.Time) bool {
	_, ok := verifyTOTPStepAt(secret, token, at)
	return ok
}

// verifyTOTPStepAt 是 verifyTOTPAt 的带步长版本：命中时返回具体时间步。
// 窗口顺序与参数是固定契约（-1 → 0 → +1），勿改。
func verifyTOTPStepAt(secret, token string, at time.Time) (int64, bool) {
	if !isSixDigit(token) {
		return -1, false
	}
	secretBytes, err := base64.StdEncoding.DecodeString(secret)
	if err != nil {
		return -1, false
	}
	b32 := strings.TrimRight(base32.StdEncoding.EncodeToString(secretBytes), "=")
	opts := hotp.ValidateOpts{Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1}
	currentStep := at.Unix() / TimeStep
	for i := -TOTPWindow; i <= TOTPWindow; i++ {
		step := currentStep + int64(i)
		if ok, err := hotp.ValidateCustom(token, uint64(step), b32, opts); err == nil && ok {
			return step, true
		}
	}
	return -1, false
}

// isSixDigit 校验 token 恰好为 6 位十进制数字（等价 /^\d{6}$/）。
func isSixDigit(token string) bool {
	if len(token) != 6 {
		return false
	}
	for i := 0; i < len(token); i++ {
		c := token[i]
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
