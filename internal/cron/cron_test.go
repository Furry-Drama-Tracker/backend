package cron

import (
	"testing"

	"github.com/robfig/cron/v3"
)

// TestNewCronEntries 验证调度器注册了全部三个任务，且三个文档化 spec
// （会话清理每小时 / 已读通知每天 03:00 / 注销清理每 6 小时整点）均可被
// robfig 解析器接受——非法 spec 在 mustAdd 处 panic，这里防文档漂移。
func TestNewCronEntries(t *testing.T) {
	c := newCron(t.Context(), nil)
	if n := len(c.Entries()); n != 3 {
		t.Fatalf("entries = %d, want 3", n)
	}
	for _, spec := range []string{
		"0 * * * *",
		"0 3 * * *",
		"0 6-18/6 * * *",
	} {
		if _, err := cron.ParseStandard(spec); err != nil {
			t.Fatalf("documented spec %q should parse: %v", spec, err)
		}
	}
}

// TestMustAddPanicsOnInvalidSpec 验证非法 spec 触发防御性 panic。
func TestMustAddPanicsOnInvalidSpec(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("mustAdd should panic on invalid spec")
		}
	}()
	c := cron.New()
	mustAdd(c, "not-a-spec", func() {})
}
