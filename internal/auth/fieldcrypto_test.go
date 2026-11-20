package auth

import (
	"strings"
	"testing"
)

// key 派生自 secret="my-secret-key"（固定校验值，勿改）。
const (
	nodeVectorSecret = "my-secret-key"
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

// TestEncryptRoundtripGCM 验证加密→解密往返一致，且输出为 enc2: 前缀。
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
	if got, err := EncryptField("enc2:abc", nodeVectorKeyHex); got != "enc2:abc" || err != nil {
		t.Fatalf("enc2 string should pass through: got %q err %v", got, err)
	}
	// enc: 前缀不再是「已加密」标记（存量已由迁移重加密），按普通明文加密。
	got, err := EncryptField("enc:abc", nodeVectorKeyHex)
	if err != nil || !strings.HasPrefix(got, "enc2:") {
		t.Fatalf("enc:-prefixed input should be treated as plaintext: got %q err %v", got, err)
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
	for _, bad := range []string{"enc2:", "enc2:zz:00"} {
		if _, err := DecryptField(bad, nodeVectorKeyHex); err == nil {
			t.Fatalf("malformed payload %q should fail", bad)
		}
	}
	// key 非法。
	if _, err := DecryptField(enc, "bad-key"); err == nil {
		t.Fatal("bad key hex should fail")
	}
}

// TestDecryptLegacyCBCRejected 验证 enc:（迁移前 CBC 格式）不再受支持：
// 解密遇到该前缀必须报错（迁移 fieldcrypto_gcm_v1 未覆盖即 fail-closed 暴露）。
func TestDecryptLegacyCBCRejected(t *testing.T) {
	if _, err := DecryptField("enc:6dd9c98664e83b7e8bdb6235155c98df:724fd420c02a6d0a45a6d9d1c189af20", nodeVectorKeyHex); err == nil {
		t.Fatal("legacy enc: format should be rejected")
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
