package repository

import (
	"context"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/Furry-Drama-Tracker/backend/internal/model"
)

// 本文件为收藏夹域跨域访问 episodes 集合的方法，挂到已有
// EpisodeRepo 类型上。方法名 Folder 前缀，避免与其它域新增方法冲突。

// FolderSharedProjection 是分享收藏夹剧集条目的投影字段，
// 与分享接口选中的字段一致：title titleEn coverImage currentEpisodes
// totalEpisodes averageRating ratingCount status reviewStatus。
var FolderSharedProjection = bson.M{
	"title": 1, "titleEn": 1, "coverImage": 1, "currentEpisodes": 1,
	"totalEpisodes": 1, "averageRating": 1, "ratingCount": 1,
	"status": 1, "reviewStatus": 1,
}

// FindFolderItemsByIDs 按 episodeId 批量取分享收藏夹展示字段（分享收藏夹端点使用）。
// 只返回投影字段；不存在的 id 直接跳过（与既有集合关联查询语义一致：
// 已删除的剧集不产生条目）。
func (r *EpisodeRepo) FindFolderItemsByIDs(ctx context.Context, ids []primitive.ObjectID) ([]model.FolderItem, error) {
	ctx, cancel := context.WithTimeout(ctx, contextTimeout)
	defer cancel()
	if len(ids) == 0 {
		return []model.FolderItem{}, nil
	}
	cur, err := r.coll.Find(ctx, bson.M{"_id": bson.M{"$in": ids}},
		options.Find().SetProjection(FolderSharedProjection))
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	items := make([]model.FolderItem, 0)
	if err := cur.All(ctx, &items); err != nil {
		return nil, err
	}
	return items, nil
}
