package model

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// Theme 主题文档（themes 集合）。
//
// 主题是壁纸外观包（不含按钮调色：调色由左下角强调色面板承担）。
// SVG 图标包功能已于 2026-11 移除（前端图标改用 Font Awesome）；
// 存量文档中残留的 icons 子文档不再读取、不再写入。
//
// 主题分两类：
//   - 系统主题（isSystem=true）：由管理员创建，或个人主题审核通过后升级而来，
//     全站用户可见可选；enabled 控制是否对用户开放。
//   - 个人主题（isSystem=false）：登录用户自制，默认仅在该用户多端同步可见；
//     通过 submit 提交后台审核，approve 后变为系统主题。
//
// Status 仅对个人主题有意义：draft（草稿）/ pending（待审核）/
// approved（已通过，此时 isSystem=true）/ rejected（已驳回）。
type Theme struct {
	ID          primitive.ObjectID `bson:"_id,omitempty" json:"_id"`
	Name        string             `bson:"name" json:"name"`
	Description string             `bson:"description" json:"description"`
	// WallpaperURL 是主题壁纸地址（空串表示不含壁纸）。
	WallpaperURL string `bson:"wallpaperUrl" json:"wallpaperUrl"`
	// WallpaperThumb 是壁纸缩略图地址（卡片预览用，可空，回退 WallpaperURL）。
	WallpaperThumb string `bson:"wallpaperThumb" json:"wallpaperThumb"`
	// AccentColor 是主题可选强调色（#rrggbb；空串表示不改变用户主题色，
	// 应用主题时「有则应用、无则保持用户当前主题色」）。
	AccentColor string `bson:"accentColor" json:"accentColor"`
	IsSystem    bool   `bson:"isSystem" json:"isSystem"`
	// OwnerID 是个人主题的所属用户；系统主题为 nil。
	OwnerID *primitive.ObjectID `bson:"ownerId" json:"ownerId"`
	Status  string              `bson:"status" json:"status"`
	// ReviewNote 是管理员审核备注（驳回原因等）。
	ReviewNote string `bson:"reviewNote" json:"reviewNote"`
	// IsDefault 表示该主题是否站点默认主题（全站唯一，未登录/未选择用户生效）。
	IsDefault bool `bson:"isDefault" json:"isDefault"`
	Enabled   bool `bson:"enabled" json:"enabled"`
	// UsageCount 是使用该主题的用户数（定时/事件更新，仅展示参考）。
	UsageCount int64     `bson:"usageCount" json:"usageCount"`
	CreatedAt  time.Time `bson:"createdAt" json:"createdAt"`
	UpdatedAt  time.Time `bson:"updatedAt" json:"updatedAt"`
	// VersionKey 是文档版本键 __v。
	VersionKey int `bson:"__v,omitempty" json:"__v"`
}

// 主题状态常量。
const (
	ThemeStatusDraft    = "draft"
	ThemeStatusPending  = "pending"
	ThemeStatusApproved = "approved"
	ThemeStatusRejected = "rejected"
)

// 主题内容类型常量（由 WallpaperURL 推导）。
const (
	// ThemeTypeWallpaper 仅壁纸。
	ThemeTypeWallpaper = "wallpaper"
	// ThemeTypeLegacy 旧版配色主题（无壁纸，仅历史数据）。
	ThemeTypeLegacy = "legacy"
)

// ThemeType 返回主题内容类型（wallpaper / legacy）。
func (t *Theme) ThemeType() string {
	if t.WallpaperURL != "" {
		return ThemeTypeWallpaper
	}
	return ThemeTypeLegacy
}
