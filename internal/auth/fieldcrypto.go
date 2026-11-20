package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strings"
)

// fieldcrypto 实现字段级对称加密，格式契约（ADR-024）：
//   - 新数据 AES-256-GCM（AEAD，带认证标签）：输出 "enc2:<nonce hex>:<ct hex>"（nonce 12 随机字节）
//   - 存量数据 AES-256-CBC + PKCS7（只读兼容）：输出 "enc:<iv hex>:<ct hex>"，EncryptField 不再产出
//   - key 派生固定：sha256(ENCRYPTION_KEY 或 JWT_SECRET) 的 hex 表示（见 FieldKey，勿改——改了旧数据将无法解密）
//   - 加解密 fail-closed：任何失败返回 error，绝不静默透传密文/明文（旧版 EncryptField 在
//     错误路径上原样返回明文，属 fail-open 设计缺陷，已修正）
//   - 无前缀输入按明文透传（历史明文数据兜底）；空值原样返回；已带前缀的输入不重复加密
//
// 用途：twoFactorSecret / SiteContent email.pass。
// 旧版 EncryptArray/DecryptArray 已删除：备份码消费（backupcode.go）自行按条目调用 DecryptField。
// 存量 enc: 数据读取时自动走 CBC 解密；是否一次性重加密为 enc2 由后续迁移决定（不迁移不影响行为）。

// FieldKey 派生 32 字节 AES key：sha256(encryptionKey 或 jwtSecret) 的 hex 表示。
// key 派生固定为：优先 ENCRYPTION_KEY，缺省回退 JWT_SECRET，勿改。
func FieldKey(encryptionKey, jwtSecret string) string {
	src := jwtSecret
	if encryptionKey != "" {
		src = encryptionKey
	}
	sum := sha256.Sum256([]byte(src))
	return hex.EncodeToString(sum[:])
}

// EncryptField 用 AES-256-GCM 加密明文，输出 "enc2:<nonce hex>:<ct hex>"。
// 空值或已带 enc:/enc2: 前缀的输入原样返回（幂等）。失败返回 error（fail-closed）。
func EncryptField(text, keyHex string) (string, error) {
	if text == "" || strings.HasPrefix(text, "enc2:") || strings.HasPrefix(text, "enc:") {
		return text, nil
	}
	key, err := decodeKey(keyHex)
	if err != nil {
		return "", err
	}
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", errors.New("fieldcrypto: nonce generation failed")
	}
	ct := gcm.Seal(nil, nonce, []byte(text), nil)
	return "enc2:" + hex.EncodeToString(nonce) + ":" + hex.EncodeToString(ct), nil
}

// DecryptField 解密密文，按前缀分派：enc2: → AES-GCM；enc: → AES-CBC + PKCS7（存量只读）；
// 无前缀 → 明文透传（历史明文兜底）。任何解密失败（key 不符、密文损坏、格式非法）返回 error。
func DecryptField(text, keyHex string) (string, error) {
	switch {
	case !strings.HasPrefix(text, "enc2:") && !strings.HasPrefix(text, "enc:"):
		return text, nil
	case strings.HasPrefix(text, "enc2:"):
		key, err := decodeKey(keyHex)
		if err != nil {
			return "", err
		}
		gcm, err := newGCM(key)
		if err != nil {
			return "", err
		}
		nonce, ct, err := splitPayload(text[5:])
		if err != nil {
			return "", err
		}
		if len(nonce) != gcm.NonceSize() {
			return "", errors.New("fieldcrypto: malformed enc2 nonce")
		}
		plain, err := gcm.Open(nil, nonce, ct, nil)
		if err != nil {
			return "", errors.New("fieldcrypto: gcm open failed")
		}
		return string(plain), nil
	default: // "enc:" 存量 CBC（只读）
		key, err := decodeKey(keyHex)
		if err != nil {
			return "", err
		}
		block, err := aes.NewCipher(key)
		if err != nil {
			return "", err
		}
		iv, ct, err := splitPayload(text[4:])
		if err != nil {
			return "", err
		}
		if len(iv) != aes.BlockSize || len(ct) == 0 || len(ct)%aes.BlockSize != 0 {
			return "", errors.New("fieldcrypto: malformed enc payload")
		}
		plain := make([]byte, len(ct))
		cipher.NewCBCDecrypter(block, iv).CryptBlocks(plain, ct)
		unpadded, err := pkcs7Unpad(plain, aes.BlockSize)
		if err != nil {
			return "", err
		}
		return string(unpadded), nil
	}
}

// decodeKey 校验并把 hex key 解码为 32 字节。
func decodeKey(keyHex string) ([]byte, error) {
	key, err := hex.DecodeString(keyHex)
	if err != nil || len(key) != 32 {
		return nil, errors.New("fieldcrypto: key must be 32 bytes hex")
	}
	return key, nil
}

// newGCM 构造 AES-GCM AEAD。
func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return gcm, nil
}

// splitPayload 拆分 "<iv/nonce hex>:<ct hex>" 载荷。
func splitPayload(body string) ([]byte, []byte, error) {
	parts := strings.SplitN(body, ":", 2)
	if len(parts) != 2 {
		return nil, nil, errors.New("fieldcrypto: malformed payload")
	}
	a, err1 := hex.DecodeString(parts[0])
	b, err2 := hex.DecodeString(parts[1])
	if err1 != nil || err2 != nil {
		return nil, nil, errors.New("fieldcrypto: malformed payload hex")
	}
	return a, b, nil
}

// pkcs7Unpad 去除 PKCS#7 填充（仅存量 enc: CBC 解密使用）。非法返回错误。
func pkcs7Unpad(data []byte, blockSize int) ([]byte, error) {
	if len(data) == 0 || len(data)%blockSize != 0 {
		return nil, errors.New("invalid padding")
	}
	padLen := int(data[len(data)-1])
	if padLen == 0 || padLen > blockSize || padLen > len(data) {
		return nil, errors.New("invalid padding length")
	}
	for _, b := range data[len(data)-padLen:] {
		if int(b) != padLen {
			return nil, errors.New("invalid padding byte")
		}
	}
	return data[:len(data)-padLen], nil
}
