package code

import (
	"testing"
	"time"
)

func TestGenerateCodeSixDigits(t *testing.T) {
	for i := 0; i < 200; i++ {
		c := GenerateCode()
		if len(c) != 6 {
			t.Fatalf("code length %d != 6: %q", len(c), c)
		}
		for _, r := range c {
			if r < '0' || r > '9' {
				t.Fatalf("non-digit in code %q", c)
			}
		}
	}
}

func TestStoreSetGetDelete(t *testing.T) {
	s := NewStore(time.Hour)
	defer s.Stop()
	s.Set("123456", Entry{UserID: "u1", Email: "a@b.com"})
	e, ok := s.Get("123456")
	if !ok || e.UserID != "u1" || e.Email != "a@b.com" {
		t.Fatalf("get mismatch: %+v ok=%v", e, ok)
	}
	s.Delete("123456")
	if _, ok := s.Get("123456"); ok {
		t.Fatal("should be deleted")
	}
}

func TestStoreTTLExpiry(t *testing.T) {
	// 用极短 TTL 验证过期（Get 不主动判过期——固定契约，由调用方判）。
	s := NewStore(time.Millisecond)
	defer s.Stop()
	s.Set("111111", Entry{UserID: "u1"})
	time.Sleep(5 * time.Millisecond)
	// Get 仍返回条目（过期判定由调用方在 verify 时检查 expiresAt）。
	if _, ok := s.Get("111111"); !ok {
		t.Fatal("Get should still return before cleanup")
	}
}

// TestSetFreshRejectsLiveCollision 同一 code 的未过期条目不得被 SetFresh 覆盖
// （跨用户静默覆盖会让攻击者已知的 code 解析到别的账号，ADR-019）。
func TestSetFreshRejectsLiveCollision(t *testing.T) {
	s := NewStore(time.Hour)
	defer s.Stop()
	if !s.SetFresh("123456", Entry{UserID: "u1"}) {
		t.Fatal("first SetFresh should succeed")
	}
	if s.SetFresh("123456", Entry{UserID: "u2"}) {
		t.Fatal("SetFresh must reject live code")
	}
	e, ok := s.Get("123456")
	if !ok || e.UserID != "u1" {
		t.Fatalf("owner must remain u1: %+v", e)
	}
}

// TestSetFreshAllowsExpiredReuse 过期条目可以被 SetFresh 覆盖。
func TestSetFreshAllowsExpiredReuse(t *testing.T) {
	s := NewStore(time.Millisecond)
	defer s.Stop()
	s.SetFresh("123456", Entry{UserID: "u1"})
	time.Sleep(5 * time.Millisecond)
	if !s.SetFresh("123456", Entry{UserID: "u2"}) {
		t.Fatal("expired entry should be replaceable")
	}
	if e, _ := s.Get("123456"); e.UserID != "u2" {
		t.Fatalf("owner should be u2 after expiry: %+v", e)
	}
}

// TestMintNoLiveCollision Mint 签发的码与既有未过期条目不冲突，
// 且原属主的条目不被改动（对照旧行为：Set 会静默覆盖）。
func TestMintNoLiveCollision(t *testing.T) {
	s := NewStore(time.Hour)
	defer s.Stop()
	s.Set("654321", Entry{UserID: "victim"})
	// 反复 Mint，条目数只增不减、victim 条目永不丢失。
	for i := 0; i < 500; i++ {
		s.Mint(Entry{UserID: "minter"})
	}
	if e, ok := s.Get("654321"); !ok || e.UserID != "victim" {
		t.Fatalf("victim entry lost: %+v ok=%v", e, ok)
	}
	s.mu.Lock()
	n := len(s.items)
	s.mu.Unlock()
	if n < 501 {
		t.Fatalf("minted entries missing: %d", n)
	}
}
