package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"strings"
)

// 2FA 备份码的存储格式与匹配规则。
//
// 设计要点：
//   - 备份码是 8 字节（64 位熵）随机 hex，等效于口令但不能被"读取"使用，
//     因此只需要单向证明"用户手里的码等于库里的码"——用快哈希即可（不需 bcrypt：
//     这里是高熵随机值，暴力空间 2^64 不可穷举，而 10 个码 × cost12 bcrypt
//     会让单次登录多出数秒延迟）。
//   - 存储格式 "bc:<sha256 hex>"。哈希本身已不可逆，无需再做 AES 加密。
//   - 旧数据是 AES 密文（"enc:<iv>:<ct>"），读取时兼容：解密后比对，命中即
//     就地升级为哈希（ConsumeBackupCode 会把整个数组统一重写为哈希格式）。
//
// 注意：bcrypt 的 72 字节截断对 16 字符码无影响，但成本过高，故不采用。

// BackupCodeHashPrefix 是哈希备份码的存储前缀（格式契约）。
const BackupCodeHashPrefix = "bc:"

// HashBackupCode 计算备份码的存储值（"bc:" + sha256 hex）。
func HashBackupCode(code string) string {
	sum := sha256.Sum256([]byte(code))
	return BackupCodeHashPrefix + hex.EncodeToString(sum[:])
}

// HashBackupCodes 批量计算存储值（新签发的备份码直接用此结果落库）。
func HashBackupCodes(codes []string) []string {
	if codes == nil {
		return nil
	}
	out := make([]string, len(codes))
	for i, c := range codes {
		out[i] = HashBackupCode(c)
	}
	return out
}

// IsHashedBackupCode 判断存储项是否已是哈希格式。
func IsHashedBackupCode(stored string) bool {
	return strings.HasPrefix(stored, BackupCodeHashPrefix)
}

// BackupCodeEqual 常量时间比对两个备份码字符串。
func BackupCodeEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// ConsumeBackupCode 在（可能混合格式的）备份码数组中匹配 code：
//   - 命中：返回消费后的新数组（**已统一升级为哈希格式**，命中的条目被移除）
//     与 true；
//   - 未命中：返回原数组与 false（调用方不应写回）。
//
// 兼容三种存储形态：哈希（bc:）、AES 密文（enc:，旧数据）、明文（历史兜底）。
// keyHex 仅在处理 enc: 条目时使用，派生规则见 FieldKey。
func ConsumeBackupCode(stored []string, code, keyHex string) ([]string, bool) {
	if code == "" || len(stored) == 0 {
		return stored, false
	}
	matched := -1
	target := HashBackupCode(code)
	for i, raw := range stored {
		if raw == "" {
			continue
		}
		var plain string
		if IsHashedBackupCode(raw) {
			plain = raw
		} else {
			// 旧数据（AES 密文）或明文：解密是幂等的（非 enc: 原样返回），
			// 统一走 DecryptField 后再比较。
			legacy := DecryptField(raw, keyHex)
			if legacy == "" {
				continue
			}
			plain = HashBackupCode(legacy)
		}
		if BackupCodeEqual(plain, target) {
			matched = i
			break
		}
	}
	if matched < 0 {
		return stored, false
	}
	out := make([]string, 0, len(stored)-1)
	for i, raw := range stored {
		if i == matched || raw == "" {
			continue
		}
		if IsHashedBackupCode(raw) {
			out = append(out, raw)
			continue
		}
		if legacy := DecryptField(raw, keyHex); legacy != "" {
			out = append(out, HashBackupCode(legacy))
		}
	}
	return out, true
}
