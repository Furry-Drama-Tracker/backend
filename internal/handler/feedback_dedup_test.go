package handler

import (
	"testing"
	"time"
)

// TestContentDedupFirst 验证 contentDedup.First 的窗口语义：
// 首次 true → 窗口内重复 false（不刷新时间戳）→ 过期后重新 true。
func TestContentDedupFirst(t *testing.T) {
	d := newContentDedup(20 * time.Millisecond)
	if !d.First("k1") {
		t.Fatal("first call should pass")
	}
	if d.First("k1") {
		t.Fatal("duplicate within window should be rejected")
	}
	if !d.First("k2") {
		t.Fatal("different key should pass")
	}
	time.Sleep(25 * time.Millisecond)
	if !d.First("k1") {
		t.Fatal("key should pass again after window expiry")
	}
}
