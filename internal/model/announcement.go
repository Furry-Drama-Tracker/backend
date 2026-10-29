package model

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// Announcement 公告文档（announcements 集合）。
// 逐字段与既有集合数据形态一致（勿改 bson 标签）；字段默认值由仓储 Create 在写入前补齐。
type Announcement struct {
	ID               primitive.ObjectID  `bson:"_id,omitempty" json:"_id"`
	Title            string              `bson:"title" json:"title"`
	TitleEn          string              `bson:"titleEn" json:"titleEn"`
	Content          string              `bson:"content" json:"content"`
	ContentEn        string              `bson:"contentEn" json:"contentEn"`
	Type             string              `bson:"type" json:"type"`
	ShowPopup        bool                `bson:"showPopup" json:"showPopup"`
	ShowBanner       bool                `bson:"showBanner" json:"showBanner"`
	SendNotification bool                `bson:"sendNotification" json:"sendNotification"`
	SendEmail        bool                `bson:"sendEmail" json:"sendEmail"`
	Dismissible      bool                `bson:"dismissible" json:"dismissible"`
	Active           bool                `bson:"active" json:"active"`
	Pinned           bool                `bson:"pinned" json:"pinned"`
	PublishAt        time.Time           `bson:"publishAt" json:"publishAt"`
	ExpireAt         *time.Time          `bson:"expireAt" json:"expireAt"`
	NotificationSent bool                `bson:"notificationSent" json:"notificationSent"`
	EmailSent        bool                `bson:"emailSent" json:"emailSent"`
	EmailSentAt      *time.Time          `bson:"emailSentAt" json:"emailSentAt"`
	EmailSentCount   int                 `bson:"emailSentCount" json:"emailSentCount"`
	Link             string              `bson:"link" json:"link"`
	CreatedBy        *primitive.ObjectID `bson:"createdBy" json:"createdBy"`
	CreatedAt        time.Time           `bson:"createdAt" json:"createdAt"`
	UpdatedAt        time.Time           `bson:"updatedAt" json:"updatedAt"`
	// VersionKey 是文档版本键 __v（整文档覆盖保存时自增；定点更新不变）。
	VersionKey int `bson:"__v,omitempty" json:"__v"`
}
