package service

import (
	"strings"
	"testing"
)

// TestValidatePassword service 侧密码规则：≥8 码元 + ≤72 字节（bcrypt 上限）+ 字母 + 数字。
// security.ValidatePassword 是同一规则的 UTF-16 码元镜像（xss_test.go 已覆盖），此处覆盖生产实现。
func TestValidatePassword(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", "密码长度至少8位"},
		{"short1", "密码长度至少8位"},
		{"12345678", "密码必须包含至少一个字母"},
		{"abcdefgh", "密码必须包含至少一个数字"},
		{"pass1234", ""},
		// 72 字节硬上限按字节计：中文等多字节字符远早于 72 个"字"触发。
		{strings.Repeat("a1", 36), ""},
		{strings.Repeat("a1", 37), "密码长度不能超过72个字节（汉字/表情符号占多个字节）"},
		{strings.Repeat("字", 24) + "a1", "密码长度不能超过72个字节（汉字/表情符号占多个字节）"},
	}
	for _, c := range cases {
		if got := ValidatePassword(c.in); got != c.want {
			t.Fatalf("ValidatePassword(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
