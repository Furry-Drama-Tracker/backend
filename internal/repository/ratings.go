package repository

import (
	"context"
	"errors"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// 本文件为 /api/ratings 域的数据访问方法，
// 挂在 *RatingRepo 与 *EpisodeRepo 上。方法名带 Rating 前缀，避免与其它域的
// 跨集合方法（Episodes* / Follows* 前缀）冲突。

// ratingScoreDoc 是评分文档的 score 视图（float64 保留非整数评分，兼容 BSON
// double 存储；响应回显原始 score）。
type ratingScoreDoc struct {
	Score float64 `bson:"score"`
}

// RatingUpsertScore 幂等写入用户对某剧集的评分（POST / 使用）：
// 按 {userId, episodeId} upsert {score}；
// 新建补 createdAt/__v:0，更新刷新 updatedAt（时间戳语义与既有集合一致）。
func (r *RatingRepo) RatingUpsertScore(ctx context.Context, userID, episodeID primitive.ObjectID, score float64, now time.Time) error {
	ctx, cancel := context.WithTimeout(ctx, contextTimeout)
	defer cancel()
	now = now.UTC().Truncate(time.Millisecond) // 毫秒精度 + UTC Z 时区，与既有写入格式一致
	_, err := r.coll.UpdateOne(ctx,
		bson.M{"userId": userID, "episodeId": episodeID},
		bson.M{
			"$set":         bson.M{"score": score, "updatedAt": now},
			"$setOnInsert": bson.M{"createdAt": now, "__v": 0},
		},
		options.Update().SetUpsert(true))
	return err
}

// RatingFindScore 查用户对某剧集的评分值（DELETE /:episodeId 的存在性检查与
// GET /check/:episodeId）；不存在返回 ErrNotFound。
func (r *RatingRepo) RatingFindScore(ctx context.Context, userID, episodeID primitive.ObjectID) (float64, error) {
	ctx, cancel := context.WithTimeout(ctx, contextTimeout)
	defer cancel()
	var d ratingScoreDoc
	err := r.coll.FindOne(ctx, bson.M{"userId": userID, "episodeId": episodeID},
		options.FindOne().SetProjection(bson.M{"score": 1})).Decode(&d)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return 0, ErrNotFound
	}
	return d.Score, err
}

// RatingDeleteByUserEpisode 删除用户对某剧集的评分（DELETE /:episodeId，
// deleteOne({userId, episodeId})）。
func (r *RatingRepo) RatingDeleteByUserEpisode(ctx context.Context, userID, episodeID primitive.ObjectID) error {
	ctx, cancel := context.WithTimeout(ctx, contextTimeout)
	defer cancel()
	_, err := r.coll.DeleteOne(ctx, bson.M{"userId": userID, "episodeId": episodeID})
	return err
}

// RatingAggregateStats 统计某剧集的平均分与评分人数
// （aggregate([{$match:{episodeId}},{$group:{_id:null,avg:{$avg:'$score'},
// count:{$sum:1}}}])；评分增删后调用）。无评分时 avg=0, count=0。
func (r *RatingRepo) RatingAggregateStats(ctx context.Context, episodeID primitive.ObjectID) (avg float64, count int64, err error) {
	ctx, cancel := context.WithTimeout(ctx, contextTimeout)
	defer cancel()
	pipeline := mongo.Pipeline{
		{{Key: "$match", Value: bson.M{"episodeId": episodeID}}},
		{{Key: "$group", Value: bson.M{"_id": nil, "avg": bson.M{"$avg": "$score"}, "count": bson.M{"$sum": 1}}}},
	}
	cur, err := r.coll.Aggregate(ctx, pipeline)
	if err != nil {
		return 0, 0, err
	}
	defer cur.Close(ctx)
	var out []struct {
		Avg   float64 `bson:"avg"`
		Count int64   `bson:"count"`
	}
	if err := cur.All(ctx, &out); err != nil {
		return 0, 0, err
	}
	if len(out) == 0 {
		return 0, 0, nil
	}
	return out[0].Avg, out[0].Count, nil
}

// RatingSetEpisodeStats 更新剧集的平均分/评分人数
// （定点 $set averageRating/ratingCount；定点更新不递增 __v）。
func (r *EpisodeRepo) RatingSetEpisodeStats(ctx context.Context, episodeID primitive.ObjectID, avg float64, count int64) error {
	ctx, cancel := context.WithTimeout(ctx, contextTimeout)
	defer cancel()
	_, err := r.coll.UpdateOne(ctx, bson.M{"_id": episodeID},
		bson.M{"$set": bson.M{"averageRating": avg, "ratingCount": count}})
	return err
}
