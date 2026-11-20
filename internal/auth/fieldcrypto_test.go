package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"strings"
	"testing"
)

// 黄金向量：同算法（key=sha256(secret) raw，AES-256-CBC）
// 用 secret="my-secret-key" 加密 "hello世界123" 得到的固定校验向量，勿改。
// 用于验证与既有加密数据（enc: 前缀，只读兼容）解密兼容。
const (
	nodeVectorSecret = "my-secret-key"
	nodeVectorCipher = "enc:6dd9c98664e83b7e8bdb6235155c98df:724fd420c02a6d0a45a6d9d1c189af20"
	goldenPlain      = "hello世界123"
	// key = sha256("my-secret-key") 的 hex（固定校验值）。
	nodeVectorKeyHex = "1311f8fc80a7ea28d78dd7723f09c44c1754cd35160ca8e7133ae3d7f636a19a"
)

// TestFieldKeyDerivation 验证 key 派生（优先 ENCRYPTION_KEY，缺省回退 JWT_SECRET，勿改）。
func TestFieldKeyDerivation(t *testing.T) {
	if got := FieldKey("", nodeVectorSecret); got != nodeVectorKeyHex {
		t.Fatalf("FieldKey derivation mismatch: got %s want %s", got, nodeVectorKeyHex)
	}
	// ENCRYPTION_KEY 优先。
	if got := FieldKey("enc-first", nodeVectorSecret); got == nodeVectorKeyHex {
		t.Fatalf("FieldKey should prefer encryptionKey over jwtSecret")
	}
}

// TestDecryptNodeVector 验证能解密固定黄金向量（enc: 存量数据只读兼容）。
func TestDecryptNodeVector(t *testing.T) {
	got, err := DecryptField(nodeVectorCipher, nodeVectorKeyHex)
	if err != nil {
		t.Fatalf("decrypt golden vector error: %v", err)
	}
	if got != goldenPlain {
		t.Fatalf("decrypt golden vector mismatch: got %q want %q", got, goldenPlain)
	}
}

// TestEncryptRoundtripGCM 验证新格式加密→解密往返一致，且输出为 enc2: 前缀。
func TestEncryptRoundtripGCM(t *testing.T) {
	for _, plain := range []string{"hello", "中文内容 with 空格 and §ymbols", "very long string padded beyond block size 0123456789abcdef"} {
		enc, err := EncryptField(plain, nodeVectorKeyHex)
		if err != nil {
			t.Fatalf("EncryptField(%q): %v", plain, err)
		}
		if !strings.HasPrefix(enc, "enc2:") {
			t.Fatalf("ciphertext should have enc2: prefix, got %q", enc[:12])
		}
		got, err := DecryptField(enc, nodeVectorKeyHex)
		if err != nil {
			t.Fatalf("DecryptField(%q): %v", plain, err)
		}
		if got != plain {
			t.Fatalf("roundtrip mismatch for %q: got %q", plain, got)
		}
	}
}

// TestEncryptFieldIdempotent 验证空值/已加密串原样返回、无前缀明文透传。
func TestEncryptFieldIdempotent(t *testing.T) {
	if got, err := EncryptField("", nodeVectorKeyHex); got != "" || err != nil {
		t.Fatalf("empty string should pass through: got %q err %v", got, err)
	}
	if got, err := EncryptField("enc:abc", nodeVectorKeyHex); got != "enc:abc" || err != nil {
		t.Fatalf("already-encrypted string should pass through: got %q err %v", got, err)
	}
	if got, err := EncryptField("enc2:abc", nodeVectorKeyHex); got != "enc2:abc" || err != nil {
		t.Fatalf("enc2 string should pass through: got %q err %v", got, err)
	}
	if got, err := DecryptField("plain", nodeVectorKeyHex); got != "plain" || err != nil {
		t.Fatalf("non-enc string should pass through: got %q err %v", got, err)
	}
}

// TestEncryptFailClosed 验证加密 fail-closed：key 非法时返回错误与空串，绝不透传明文。
func TestEncryptFailClosed(t *testing.T) {
	got, err := EncryptField("secret-plaintext", "bad-key")
	if err == nil {
		t.Fatal("EncryptField with bad key should fail")
	}
	if got != "" {
		t.Fatalf("EncryptField must not leak plaintext on error, got %q", got)
	}
}

// TestDecryptFailClosed 验证解密 fail-closed：篡改密文/错误 key/畸形载荷均返回错误。
func TestDecryptFailClosed(t *testing.T) {
	enc, err := EncryptField("hello", nodeVectorKeyHex)
	if err != nil {
		t.Fatalf("EncryptField: %v", err)
	}
	// 篡改密文最后一个字节（GCM 认证标签应拒绝）。
	tampered := enc[:len(enc)-2] + "00"
	if _, err := DecryptField(tampered, nodeVectorKeyHex); err == nil {
		t.Fatal("tampered ciphertext should fail GCM open")
	}
	// 错误 key。
	wrongKey := strings.Repeat("ab", 32)
	if _, err := DecryptField(enc, wrongKey); err == nil {
		t.Fatal("wrong key should fail")
	}
	// 畸形载荷。
	for _, bad := range []string{"enc2:", "enc2:zz:00", "enc:aa", "enc:aa:zz"} {
		if _, err := DecryptField(bad, nodeVectorKeyHex); err == nil {
			t.Fatalf("malformed payload %q should fail", bad)
		}
	}
	// key 非法。
	if _, err := DecryptField(enc, "bad-key"); err == nil {
		t.Fatal("bad key hex should fail")
	}
}

// TestEncryptUniqueness 验证随机 nonce：同一明文两次加密产生不同密文。
func TestEncryptUniqueness(t *testing.T) {
	a, _ := EncryptField("same", nodeVectorKeyHex)
	b, _ := EncryptField("same", nodeVectorKeyHex)
	if a == b {
		t.Fatal("two encryptions of the same plaintext should differ (random nonce)")
	}
}

// TestDecryptLegacyPlaintext 验证历史明文数据兜底：无前缀原样透传。
func TestDecryptLegacyPlaintext(t *testing.T) {
	got, err := DecryptField("raw-totp-secret", nodeVectorKeyHex)
	if err != nil || got != "raw-totp-secret" {
		t.Fatalf("legacy plaintext passthrough: got %q err %v", got, err)
	}
}

// 辅助：用旧版 CBC 算法现场构造 "enc:" 密文（模拟未迁移的存量数据），
// 与黄金向量互补——黄金向量是固定值，这里验证任意明文的 CBC 解密兼容。
func TestDecryptLegacyCBCRoundtrip(t *testing.T) {
	keyRaw, _ := hex.DecodeString(nodeVectorKeyHex)
	block, err := aes.NewCipher(keyRaw)
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	iv := make([]byte, aes.BlockSize)
	if _, err := rand.Read(iv); err != nil {
		t.Fatalf("rand: %v", err)
	}
	padded := []byte("legacy backup code")
	padLen := aes.BlockSize - len(padded)%aes.BlockSize
	pad := make([]byte, padLen)
	for i := range pad {
		pad[i] = byte(padLen)
	}
	padded = append(padded, pad...)
	ct := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(ct, padded)
	legacy := "enc:" + hex.EncodeToString(iv) + ":" + hex.EncodeToString(ct)

	got, err := DecryptField(legacy, nodeVectorKeyHex)
	if err != nil {
		t.Fatalf("legacy CBC decrypt: %v", err)
	}
	if got != "legacy backup code" {
		t.Fatalf("legacy CBC decrypt mismatch: got %q", got)
	}
}
