package auth

import (
	"strings"

	ua "github.com/mileusna/useragent"

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

// ParseUserAgent 解析 UA 字符串（基于 github.com/mileusna/useragent，ADR-025）。
//
// 口径说明（2026-11，ADR-024 先换 mssola 后改 mileusna——mileusna 的 OS 常量
// 与既有统计口径一致（macOS/iOS/Android/Windows），且 Device 字段直接给出
// Android 机型 / iPhone / iPad，无需归一化映射）：
//   - 设备类型：Tablet / Mobile 二分，其余 desktop；
//   - 客户端上报的 deviceInfo 优先级更高（BuildDeviceInfo），UA 解析仅作兜底。
func ParseUserAgent(uaStr string) ParsedUA {
	r := ParsedUA{DeviceType: "desktop"}
	if uaStr == "" {
		return r
	}
	p := ua.Parse(uaStr)
	switch {
	case p.Tablet:
		r.DeviceType = "tablet"
	case p.Mobile:
		r.DeviceType = "mobile"
	}
	r.Browser = p.Name
	r.BrowserVersion = p.Version
	r.OS = p.OS
	r.OSVersion = p.OSVersion
	if r.DeviceType != "desktop" {
		r.DeviceModel = trimModel(p.Device)
	}
	return r
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
