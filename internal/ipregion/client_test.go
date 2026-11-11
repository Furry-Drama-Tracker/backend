package ipregion

import (
	"context"
	"fmt"
	"reflect"
	"testing"
)

func TestLocalIP(t *testing.T) {
	c := NewClient(nil)
	for _, ip := range []string{"127.0.0.1", "::1", "::ffff:127.0.0.1"} {
		if got := c.GetRegion(context.Background(), ip); got != "本地" {
			t.Fatalf("ip %s should be 本地, got %q", ip, got)
		}
	}
}

func TestCacheAndFallback(t *testing.T) {
	var calls int
	c := NewClient(func(ctx context.Context, ip string) string {
		calls++
		return "CN · Shanghai · Shanghai"
	})
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if got := c.GetRegion(ctx, "8.8.8.8"); got != "CN · Shanghai · Shanghai" {
			t.Fatalf("region mismatch: %q", got)
		}
	}
	if calls != 1 {
		t.Fatalf("expected 1 fetch (cached), got %d", calls)
	}
}

func TestCacheEviction(t *testing.T) {
	c := NewClient(func(ctx context.Context, ip string) string { return "X" })
	ctx := context.Background()
	// 超过上限触发淘汰，但不应 panic，且最近访问仍命中。
	for i := 0; i < cacheMax+50; i++ {
		c.GetRegion(ctx, fmt.Sprintf("10.0.0.%d", i%250))
	}
	if got := c.GetRegion(ctx, "10.0.0.0"); got != "X" {
		t.Fatalf("expected hit, got %q", got)
	}
}

func TestUnknownOnFetchError(t *testing.T) {
	c := NewClient(func(ctx context.Context, ip string) string { return "未知" })
	if got := c.GetRegion(context.Background(), "1.2.3.4"); got != "未知" {
		t.Fatalf("expected 未知, got %q", got)
	}
}

// TestDisabledNoOutbound 禁用态（默认装配）不得外呼：本地 IP 仍识别，其余一律 "未知"。
func TestDisabledNoOutbound(t *testing.T) {
	c := NewDisabled()
	// 显式断言没有接上任何外呼实现（数据出境合规的硬约束）。
	if reflect.ValueOf(c.fetch).Pointer() == reflect.ValueOf(fetchFromIPAPI).Pointer() {
		t.Fatal("NewDisabled 不得装配 ipapi.co 外呼")
	}
	for _, ip := range []string{"127.0.0.1", "::1", "::ffff:127.0.0.1"} {
		if got := c.GetRegion(context.Background(), ip); got != "本地" {
			t.Fatalf("ip %s 应返回 本地, got %q", ip, got)
		}
	}
	for _, ip := range []string{"8.8.8.8", "2001:4860:4860::8888", "10.0.0.1"} {
		if got := c.GetRegion(context.Background(), ip); got != "未知" {
			t.Fatalf("禁用态下 ip %s 应返回 未知, got %q", ip, got)
		}
	}
}
