package repository

import (
	"context"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/xiedada05/furry-drama-be-neo/internal/model"
)

// 本文件为 series 域跨域访问 episodes 集合的查询方法，挂到已有
// EpisodeRepo 类型上。方法名带 Series 后缀，避免与 episodes/folders 域并行
// 新增的方法重名（如 FindFolderItemsByIDs）。series.go 本身不改动。

// FindByIDsForSeries 按 ID 批量查询系列引用的剧集（系列列表/详情端点的字段投影）。
// 返回详情端点的字段超集；列表端点由 handler 构造精简视图。
// 不存在的 ID 直接跳过（已删除剧集在数组中不留位，既有契约）。
func (r *EpisodeRepo) FindByIDsForSeries(ctx context.Context, ids []primitive.ObjectID) ([]model.SeriesEpisodeDetail, error) {
	ctx, cancel := context.WithTimeout(ctx, contextTimeout)
	defer cancel()
	if len(ids) == 0 {
		return []model.SeriesEpisodeDetail{}, nil
	}
	projection := bson.M{
		"title": 1, "coverImage": 1, "currentEpisodes": 1, "totalEpisodes": 1,
		"status": 1, "averageRating": 1, "description": 1, "category": 1,
		"tags": 1, "views": 1,
	}
	// 公开系列接口只能展示已审核剧集，避免创作者把 pending/rejected 剧集
	// 加入系列后绕过审核。
	filter := bson.M{
		"_id": bson.M{"$in": ids},
		"$or": bson.A{
			bson.M{"reviewStatus": "approved"},
			bson.M{"reviewStatus": bson.M{"$exists": false}},
		},
	}
	cur, err := r.coll.Find(ctx, filter,
		options.Find().SetProjection(projection))
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	list := make([]model.SeriesEpisodeDetail, 0)
	if err := cur.All(ctx, &list); err != nil {
		return nil, err
	}
	// 与集合文档数组默认形态一致：缺失字段补为 []，JSON 输出 [] 而非 null。
	for i := range list {
		if list[i].Category == nil {
			list[i].Category = []string{}
		}
		if list[i].Tags == nil {
			list[i].Tags = []string{}
		}
	}
	return list, nil
}
