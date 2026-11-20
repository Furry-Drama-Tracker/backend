package auth

import "testing"

// TestParseUserAgentDesktop 验证桌面 Chrome/Windows 解析。
func TestParseUserAgentDesktop(t *testing.T) {
	ua := "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36"
	p := ParseUserAgent(ua)
	if p.DeviceType != "desktop" {
		t.Errorf("DeviceType = %q, want desktop", p.DeviceType)
	}
	if p.Browser != "Chrome" {
		t.Errorf("Browser = %q, want Chrome", p.Browser)
	}
	if p.BrowserVersion == "" {
		t.Error("BrowserVersion should not be empty")
	}
	if p.OS != "Windows" {
		t.Errorf("OS = %q, want Windows", p.OS)
	}
}

// TestParseUserAgentIPhone 验证 iPhone Safari：mobile + iOS + 机型。
func TestParseUserAgentIPhone(t *testing.T) {
	ua := "Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Mobile/15E148 Safari/604.1"
	p := ParseUserAgent(ua)
	if p.DeviceType != "mobile" {
		t.Errorf("DeviceType = %q, want mobile", p.DeviceType)
	}
	if p.OS != "iOS" {
		t.Errorf("OS = %q, want iOS", p.OS)
	}
	if p.DeviceModel == "" {
		t.Error("DeviceModel should not be empty for iPhone")
	}
}

// TestParseUserAgentAndroid 验证 Android Chrome：mobile + Android + 机型。
func TestParseUserAgentAndroid(t *testing.T) {
	ua := "Mozilla/5.0 (Linux; Android 13; SM-G991B) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Mobile Safari/537.36"
	p := ParseUserAgent(ua)
	if p.DeviceType != "mobile" {
		t.Errorf("DeviceType = %q, want mobile", p.DeviceType)
	}
	if p.OS != "Android" {
		t.Errorf("OS = %q, want Android", p.OS)
	}
	if p.DeviceModel != "SM-G991B" {
		t.Errorf("DeviceModel = %q, want SM-G991B", p.DeviceModel)
	}
}

// TestParseUserAgentIPad 验证 iPad → tablet。
func TestParseUserAgentIPad(t *testing.T) {
	ua := "Mozilla/5.0 (iPad; CPU OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Mobile/15E148 Safari/604.1"
	p := ParseUserAgent(ua)
	if p.DeviceType != "tablet" {
		t.Errorf("DeviceType = %q, want tablet", p.DeviceType)
	}
}

// TestParseUserAgentEmpty 验证空 UA 返回 desktop 缺省值。
func TestParseUserAgentEmpty(t *testing.T) {
	p := ParseUserAgent("")
	if p.DeviceType != "desktop" || p.Browser != "" || p.OS != "" {
		t.Errorf("empty UA should yield desktop defaults, got %+v", p)
	}
}

// TestBuildDeviceInfoMerge 验证合并策略（契约核心）：客户端上报优先，
// UA 解析仅补空位，UserAgent 恒为原始串。
func TestBuildDeviceInfoMerge(t *testing.T) {
	parsed := ParsedUA{
		Browser: "UA-Browser", BrowserVersion: "1",
		OS: "UA-OS", OSVersion: "2",
		DeviceType: "mobile", DeviceModel: "UA-Model",
	}
	payload := &DeviceInfoPayload{
		Browser: "Client-Browser", OS: "Client-OS",
		ScreenWidth: 390, ScreenHeight: 844, Language: "zh-CN",
	}
	di := BuildDeviceInfo(payload, parsed, "ua-string", "en")
	if di.Browser != "Client-Browser" || di.OS != "Client-OS" {
		t.Errorf("client-reported fields must win: got browser=%q os=%q", di.Browser, di.OS)
	}
	if di.BrowserVersion != "1" || di.OSVersion != "2" || di.DeviceType != "mobile" {
		t.Errorf("UA fills blanks: got version=%q osVersion=%q type=%q", di.BrowserVersion, di.OSVersion, di.DeviceType)
	}
	if di.ScreenWidth != 390 || di.Language != "zh-CN" {
		t.Errorf("payload extras lost: %v", di)
	}
	if di.UserAgent != "ua-string" {
		t.Errorf("UserAgent must be the raw string, got %q", di.UserAgent)
	}
	// 无 payload：全部来自 UA。
	di2 := BuildDeviceInfo(nil, parsed, "ua-string", "")
	if di2.Browser != "UA-Browser" || di2.DeviceModel != "UA-Model" || di2.Language != "" {
		t.Errorf("nil payload should fall through to UA: %+v", di2)
	}
}
