package auth

import (
	"strings"
	"testing"
)

// TestHashBackupCodeFormat 验证哈希备份码的存储格式为 "bc:" + 64 位 hex，
// 且不包含明文（哈希不可逆）。
func TestHashBackupCodeFormat(t *testing.T) {
	code := "0123456789abcdef"
	got := HashBackupCode(code)
	if !strings.HasPrefix(got, BackupCodeHashPrefix) {
		t.Fatalf("missing %q prefix: %q", BackupCodeHashPrefix, got)
	}
	if len(got) != len(BackupCodeHashPrefix)+64 {
		t.Fatalf("unexpected hash length: %d (%q)", len(got), got)
	}
	if strings.Contains(got, code) {
		t.Fatalf("hash must not embed plaintext code: %q", got)
	}
	if HashBackupCode(code) != got {
		t.Fatal("hash must be deterministic")
	}
	if HashBackupCode("fedcba9876543210") == got {
		t.Fatal("different codes must hash differently")
	}
}

// TestHashBackupCodes 验证批量哈希保持顺序与长度。
func TestHashBackupCodes(t *testing.T) {
	in := []string{"aa", "bb", "cc"}
	out := HashBackupCodes(in)
	if len(out) != len(in) {
		t.Fatalf("length mismatch: got %d want %d", len(out), len(in))
	}
	for i := range in {
		if out[i] != HashBackupCode(in[i]) {
			t.Fatalf("element %d mismatch", i)
		}
	}
	if HashBackupCodes(nil) != nil {
		t.Fatal("nil input should stay nil")
	}
}

// TestConsumeBackupCodeHashed 验证命中哈希备份码后被移除，其余保持哈希格式。
func TestConsumeBackupCodeHashed(t *testing.T) {
	stored := HashBackupCodes([]string{"c1", "c2", "c3"})
	remaining, ok := ConsumeBackupCode(stored, "c2", nodeVectorKeyHex)
	if !ok {
		t.Fatal("expected hit")
	}
	if len(remaining) != 2 {
		t.Fatalf("remaining length: got %d want 2", len(remaining))
	}
	for _, v := range remaining {
		if !IsHashedBackupCode(v) {
			t.Fatalf("remaining entry must stay hashed: %q", v)
		}
		if v == HashBackupCode("c2") {
			t.Fatal("consumed code must be removed")
		}
	}
	if _, ok := ConsumeBackupCode(remaining, "c2", nodeVectorKeyHex); ok {
		t.Fatal("consumed code must no longer match")
	}
}

// TestConsumeBackupCodeLegacyUpgrade 验证旧格式（AES 密文）可匹配，
// 且匹配后整个数组就地升级为哈希格式。
func TestConsumeBackupCodeLegacyUpgrade(t *testing.T) {
	stored := EncryptArray([]string{"l1", "l2", "l3"}, nodeVectorKeyHex)
	for _, v := range stored {
		if !strings.HasPrefix(v, "enc:") {
			t.Fatalf("legacy fixture should be encrypted: %q", v)
		}
	}
	remaining, ok := ConsumeBackupCode(stored, "l2", nodeVectorKeyHex)
	if !ok {
		t.Fatal("legacy code should match")
	}
	if len(remaining) != 2 {
		t.Fatalf("remaining length: got %d want 2", len(remaining))
	}
	for _, v := range remaining {
		if !IsHashedBackupCode(v) {
			t.Fatalf("legacy entries must be upgraded to hash on hit: %q", v)
		}
	}
	// 升级后的数组仍能继续校验剩余明文码。
	if _, ok := ConsumeBackupCode(remaining, "l1", nodeVectorKeyHex); !ok {
		t.Fatal("upgraded array must still verify remaining plaintext codes")
	}
}

// TestConsumeBackupCodeMiss 验证未命中时返回原数组与 false，且不修改内容。
func TestConsumeBackupCodeMiss(t *testing.T) {
	stored := HashBackupCodes([]string{"m1", "m2"})
	before := append([]string(nil), stored...)
	remaining, ok := ConsumeBackupCode(stored, "nope", nodeVectorKeyHex)
	if ok {
		t.Fatal("unexpected hit")
	}
	if len(remaining) != len(before) {
		t.Fatalf("array length changed on miss: %d", len(remaining))
	}
	for i := range before {
		if remaining[i] != before[i] {
			t.Fatalf("array mutated on miss at %d", i)
		}
	}
}

// TestConsumeBackupCodeEmpty 验证空输入不命中。
func TestConsumeBackupCodeEmpty(t *testing.T) {
	if _, ok := ConsumeBackupCode(nil, "x", nodeVectorKeyHex); ok {
		t.Fatal("nil stored should not match")
	}
	stored := HashBackupCodes([]string{"e1"})
	if _, ok := ConsumeBackupCode(stored, "", nodeVectorKeyHex); ok {
		t.Fatal("empty token should not match")
	}
	// 空字符串条目应被跳过而不是 panic。
	if _, ok := ConsumeBackupCode([]string{"", "e1"}, "e1", nodeVectorKeyHex); !ok {
		t.Fatal("empty entry should be skipped, not break matching")
	}
}

// TestBackupCodeEqual 验证常量时间比对的语义。
func TestBackupCodeEqual(t *testing.T) {
	if !BackupCodeEqual("abc", "abc") {
		t.Fatal("equal strings should match")
	}
	if BackupCodeEqual("abc", "abd") {
		t.Fatal("different strings should not match")
	}
	if BackupCodeEqual("abc", "abcd") {
		t.Fatal("different lengths should not match")
	}
}
