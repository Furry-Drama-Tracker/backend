package auth

import (
	"strings"

	"github.com/mssola/useragent"

	"github.com/Furry-Drama-Tracker/backend/internal/model"
)

// ParsedUA 是设备信息解析结果。
type ParsedUA struct {
	Browser        string
	BrowserVersion string
	OS             string
	OSVersion      string
	DeviceType     string
	DeviceModel    string
}

// ParseUserAgent 解析 UA 字符串（基于 github.com/mssola/useragent，替代旧手写正则表）。
//
// 口径说明（2026-11 换库，ADR-024）：
//   - 设备类型：iPad / Mobile 二分，其余 desktop；
//   - OS 名归一到既有口径（Mac OS X→macOS、iPhone OS→iOS）；
//   - 旧版手写正则的特例（如 Safari 26→macOS 版本映射）不再保留——客户端上报的
//     deviceInfo 优先级更高（BuildDeviceInfo），UA 解析仅作兜底，口径差异可接受；
//   - DeviceModel 由 mssola 的 Model() 提取（移动端括号段机型），桌面端为空。
func ParseUserAgent(ua string) ParsedUA {
	r := ParsedUA{DeviceType: "desktop"}
	if ua == "" {
		return r
	}
	p := useragent.New(ua)
	switch {
	case p.Platform() == "iPad" || strings.Contains(ua, "iPad"):
		r.DeviceType = "tablet"
	case p.Mobile():
		r.DeviceType = "mobile"
	}
	r.Browser, r.BrowserVersion = p.Browser()
	info := p.OSInfo()
	r.OS = normalizeOSName(info.Name)
	if v := info.Version; v != "" {
		r.OSVersion = v
	}
	if r.DeviceType != "desktop" {
		r.DeviceModel = trimModel(p.Model())
	}
	return r
}

// normalizeOSName 把 mssola 的 OS 名归一到既有统计口径。
func normalizeOSName(name string) string {
	switch strings.TrimSpace(name) {
	case "Mac OS X":
		return "macOS"
	case "iPhone OS", "iPhone OSX":
		return "iOS"
	case "":
		return ""
	default:
		return strings.TrimSpace(name)
	}
}

// trimModel 机型截断到 50 rune（与旧解析一致），空串原样返回。
func trimModel(s string) string {
	s = strings.TrimSpace(s)
	if len([]rune(s)) > 50 {
		s = string([]rune(s)[:50])
	}
	return s
}

// BuildDeviceInfo 合并客户端上报 deviceInfo、UA 解析结果与 accept-language
// （合并规则固定，勿随意改）。
func BuildDeviceInfo(deviceInfo *DeviceInfoPayload, parsed ParsedUA, ua, acceptLang string) model.DeviceInfo {
	di := model.DeviceInfo{}
	if deviceInfo != nil {
		di.Browser = deviceInfo.Browser
		di.BrowserVersion = deviceInfo.BrowserVersion
		di.OS = deviceInfo.OS
		di.OSVersion = deviceInfo.OSVersion
		di.DeviceType = deviceInfo.DeviceType
		di.DeviceModel = deviceInfo.DeviceModel
		di.ScreenWidth = deviceInfo.ScreenWidth
		di.ScreenHeight = deviceInfo.ScreenHeight
		di.Language = deviceInfo.Language
		di.Carrier = deviceInfo.Carrier
	}
	if di.Browser == "" {
		di.Browser = parsed.Browser
	}
	if di.BrowserVersion == "" {
		di.BrowserVersion = parsed.BrowserVersion
	}
	if di.OS == "" {
		di.OS = parsed.OS
	}
	if di.OSVersion == "" {
		di.OSVersion = parsed.OSVersion
	}
	if di.DeviceType == "" {
		di.DeviceType = parsed.DeviceType
	}
	if di.DeviceModel == "" {
		di.DeviceModel = parsed.DeviceModel
	}
	if di.Language == "" && acceptLang != "" {
		di.Language = acceptLang
	}
	di.UserAgent = ua
	return di
}

// DeviceInfoPayload 是客户端上报的设备信息（handler 解析 JSON body 用）。
type DeviceInfoPayload struct {
	Browser        string `json:"browser"`
	BrowserVersion string `json:"browserVersion"`
	OS             string `json:"os"`
	OSVersion      string `json:"osVersion"`
	DeviceType     string `json:"deviceType"`
	DeviceModel    string `json:"deviceModel"`
	ScreenWidth    int    `json:"screenWidth"`
	ScreenHeight   int    `json:"screenHeight"`
	Language       string `json:"language"`
	Carrier        string `json:"carrier"`
}
