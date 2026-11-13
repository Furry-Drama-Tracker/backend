package service

import "testing"

// TestValidateLoginName 用户名校验规则：字符集/长度/黑名单子串/保留名/`.`与`..`。
func TestValidateLoginName(t *testing.T) {
	cases := []struct {
		name  string
		input string
		ok    bool
	}{
		{"普通合法", "alice", true},
		{"带下划线", "alice_01", true},
		{"带连字符", "alice-9", true},
		{"下划线开头", "_alice", true},
		{"最短3位", "abc", true},
		{"32位上限", "abcdefghijklmnopqrstuvwxyzabcdef", true}, // 32 chars
		{"33位超限", "abcdefghijklmnopqrstuvwxyzabcdefg", false},
		{"两位过短", "ab", false},
		{"混合大小写（存储层归一化为小写后可过）", "Alice", true},
		{"含点号", "alice.smith", false},
		{"单点", ".", false},
		{"两点", "..", false},
		{"admin子串", "myadmin", false},
		{"administrator子串", "administrator", false},
		{"root子串", "rooter", false},
		{"api子串", "apisfan", false},
		{"保留名me", "me", false},
		{"数字开头", "9alice", false},
		{"含中文", "小明", false},
		{"含空格", "ali ce", false},
	}
	for _, tc := range cases {
		msg := ValidateLoginName(tc.input, "")
		if tc.ok && msg != "" {
			t.Errorf("%s %q: want ok, got %q", tc.name, tc.input, msg)
		}
		if !tc.ok && msg == "" {
			t.Errorf("%s %q: want reject, got ok", tc.name, tc.input)
		}
	}
}

// TestValidateLoginNameConfiguredBlacklist 配置词表与内置词表合并生效。
func TestValidateLoginNameConfiguredBlacklist(t *testing.T) {
	if msg := ValidateLoginName("furry09", ""); msg != "" {
		t.Errorf("furry09 should pass with default blacklist, got %q", msg)
	}
	if msg := ValidateLoginName("furry09", "furry09"); msg == "" {
		t.Error("furry09 must be rejected with configured blacklist")
	}
}
