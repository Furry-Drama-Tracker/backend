package model

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// Folder 收藏夹文档（folders 集合）。
//
// 注意：收藏夹文档本身不含 items 子文档——剧集条目存放在 Follow/Favorite
// 集合中，通过 folderId 外键关联（见 Follow/Favorite 的
// folderId 字段）。本 struct 逐字段与既有集合数据形态一致（勿改 bson 标签）。
type Folder struct {
	ID          primitive.ObjectID `bson:"_id,omitempty" json:"_id"`
	UserID      primitive.ObjectID `bson:"userId" json:"userId"`
	Name        string             `bson:"name" json:"name"`
	Type        string             `bson:"type" json:"type"`
	Description string             `bson:"description" json:"description"`
	SortOrder   int                `bson:"sortOrder" json:"sortOrder"`
	// ShareToken 分享令牌；未分享时为 nil（JSON 输出 null，与既有数据缺省形态一致）。
	ShareToken *string   `bson:"shareToken" json:"shareToken"`
	CreatedAt  time.Time `bson:"createdAt" json:"createdAt"`
}

// FolderItem 分享收藏夹中剧集条目的展示字段。
// 只包含分享接口选中的字段（+_id）：title titleEn coverImage currentEpisodes
// totalEpisodes averageRating ratingCount status reviewStatus。
type FolderItem struct {
	ID              primitive.ObjectID `bson:"_id" json:"_id"`
	Title           string             `bson:"title" json:"title"`
	TitleEn         string             `bson:"titleEn" json:"titleEn"`
	CoverImage      string             `bson:"coverImage" json:"coverImage"`
	CurrentEpisodes int                `bson:"currentEpisodes" json:"currentEpisodes"`
	TotalEpisodes   *int               `bson:"totalEpisodes" json:"totalEpisodes"`
	AverageRating   float64            `bson:"averageRating" json:"averageRating"`
	RatingCount     int                `bson:"ratingCount" json:"ratingCount"`
	Status          string             `bson:"status" json:"status"`
	ReviewStatus    string             `bson:"reviewStatus" json:"reviewStatus"`
}

// SavedFolder 收藏的他人收藏夹（savedfolders 集合）。
// 逐字段与既有集合数据形态一致；{userId, shareToken} 建唯一索引。
type SavedFolder struct {
	ID          primitive.ObjectID `bson:"_id,omitempty" json:"_id"`
	UserID      primitive.ObjectID `bson:"userId" json:"userId"`
	ShareToken  string             `bson:"shareToken" json:"shareToken"`
	FolderName  string             `bson:"folderName" json:"folderName"`
	CreatorID   primitive.ObjectID `bson:"creatorId" json:"creatorId"`
	CreatorName string             `bson:"creatorName" json:"creatorName"`
	Description string             `bson:"description" json:"description"`
	FolderType  string             `bson:"folderType" json:"folderType"`
	CreatedAt   time.Time          `bson:"createdAt" json:"createdAt"`
}
