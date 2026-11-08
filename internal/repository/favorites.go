package repository

import (
	"context"
	"errors"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/Furry-Drama-Tracker/backend/internal/model"
)

// 本文件给 *FavoriteRepo 补齐收藏域（/api/favorites）专用查询方法。
// 方法名统一 Favorite 前缀，避免与其它域 agent 添加的方法重名。

// FavoriteInsert 插入收藏记录；userId+episodeId 唯一键冲突返回 errDuplicateKey
// （E11000 → 409 'Already favorited'，消费方依赖该错误信息）。
func (r *FavoriteRepo) FavoriteInsert(ctx context.Context, f *model.Favorite) error {
	ctx, cancel := context.WithTimeout(ctx, contextTimeout)
	defer cancel()
	_, err := r.coll.InsertOne(ctx, f)
	if mongo.IsDuplicateKeyError(err) {
		return errDuplicateKey
	}
	return err
}

// FavoriteDeleteOne 删除用户对某剧集的收藏。不存在也返回成功（deleteOne 幂等语义）。
func (r *FavoriteRepo) FavoriteDeleteOne(ctx context.Context, userID, episodeID any) error {
	ctx, cancel := context.WithTimeout(ctx, contextTimeout)
	defer cancel()
	_, err := r.coll.DeleteOne(ctx, bson.M{"userId": userID, "episodeId": episodeID})
	return err
}

// FavoriteCount 按筛选条件统计收藏数（countDocuments(filter)）。
func (r *FavoriteRepo) FavoriteCount(ctx context.Context, filter bson.M) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, contextTimeout)
	defer cancel()
	return r.coll.CountDocuments(ctx, filter)
}

// FavoriteListPaged 分页查询收藏，按 updatedAt 倒序
// （GET /list 默认分支：sort({updatedAt:-1}).skip().limit()）。
func (r *FavoriteRepo) FavoriteListPaged(ctx context.Context, filter bson.M, page, limit int) ([]model.Favorite, error) {
	ctx, cancel := context.WithTimeout(ctx, contextTimeout)
	defer cancel()
	cur, err := r.coll.Find(ctx, filter,
		options.Find().
			SetSort(bson.D{{Key: "updatedAt", Value: -1}}).
			SetSkip(int64((page-1)*limit)).
			SetLimit(int64(limit)))
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	var list []model.Favorite
	if err := cur.All(ctx, &list); err != nil {
		return nil, err
	}
	return list, nil
}

// FavoriteListLimited 取前 limit 条收藏、不排序（name/rating/lastWatched 分支：
// find(filter).limit(maxItems)，由调用方内存排序后再切片）。
func (r *FavoriteRepo) FavoriteListLimited(ctx context.Context, filter bson.M, limit int64) ([]model.Favorite, error) {
	ctx, cancel := context.WithTimeout(ctx, contextTimeout)
	defer cancel()
	cur, err := r.coll.Find(ctx, filter, options.Find().SetLimit(limit))
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	var list []model.Favorite
	if err := cur.All(ctx, &list); err != nil {
		return nil, err
	}
	return list, nil
}

// FavoriteFindByUserEpisode 查用户对某剧集的收藏（findOne({userId, episodeId})）；
// 不存在返回 ErrNotFound。
func (r *FavoriteRepo) FavoriteFindByUserEpisode(ctx context.Context, userID, episodeID any) (*model.Favorite, error) {
	ctx, cancel := context.WithTimeout(ctx, contextTimeout)
	defer cancel()
	f := &model.Favorite{}
	err := r.coll.FindOne(ctx, bson.M{"userId": userID, "episodeId": episodeID}).Decode(f)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, ErrNotFound
	}
	return f, err
}
