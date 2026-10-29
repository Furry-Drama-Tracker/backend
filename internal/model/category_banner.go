package model

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// Category 分类文档（categories 集合）。
// 逐字段与既有集合数据形态一致（勿改 bson 标签）；字段默认值由仓储 Create 在写入前补齐。
type Category struct {
	ID        primitive.ObjectID `bson:"_id,omitempty" json:"_id"`
	Name      string             `bson:"name" json:"name"`
	NameEn    string             `bson:"nameEn" json:"nameEn"`
	NameJa    string             `bson:"nameJa" json:"nameJa"`
	Order     int                `bson:"order" json:"order"`
	CreatedAt time.Time          `bson:"createdAt" json:"createdAt"`
	// VersionKey 是文档版本键 __v（整文档覆盖保存时自增）。
	VersionKey int `bson:"__v,omitempty" json:"__v"`
}

// Banner 轮播图文档（banners 集合）。
// 逐字段与既有集合数据形态一致（勿改 bson 标签）；字段默认值由仓储 Create 在写入前补齐。
type Banner struct {
	ID         primitive.ObjectID `bson:"_id,omitempty" json:"_id"`
	Title      string             `bson:"title" json:"title"`
	TitleEn    string             `bson:"titleEn" json:"titleEn"`
	TitleJa    string             `bson:"titleJa" json:"titleJa"`
	Subtitle   string             `bson:"subtitle" json:"subtitle"`
	SubtitleEn string             `bson:"subtitleEn" json:"subtitleEn"`
	SubtitleJa string             `bson:"subtitleJa" json:"subtitleJa"`
	Image      string             `bson:"image" json:"image"`
	Link       string             `bson:"link" json:"link"`
	Order      int                `bson:"order" json:"order"`
	Active     bool               `bson:"active" json:"active"`
	CreatedAt  time.Time          `bson:"createdAt" json:"createdAt"`
	// VersionKey 是文档版本键 __v（整文档覆盖保存时自增）。
	VersionKey int `bson:"__v,omitempty" json:"__v"`
}
