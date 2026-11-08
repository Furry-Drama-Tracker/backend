package repository

import (
	"context"
	"errors"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"

	"github.com/Furry-Drama-Tracker/backend/internal/model"
)

// 本文件为 episodes 域跨集合访问 History/Rating 集合的方法，
// 挂到已有 *HistoryRepo / *RatingRepo 类型上。方法统一以 Episodes 前缀命名。

// EpisodesFindByUserEpisode 查用户对某剧集的观看历史（findOne({userId, episodeId})）；
// 不存在返回 ErrNotFound。
func (r *HistoryRepo) EpisodesFindByUserEpisode(ctx context.Context, userID, episodeID any) (*model.History, error) {
	ctx, cancel := context.WithTimeout(ctx, contextTimeout)
	defer cancel()
	h := &model.History{}
	err := r.coll.FindOne(ctx, bson.M{"userId": ToObjectID(userID), "episodeId": ToObjectID(episodeID)}).Decode(h)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, ErrNotFound
	}
	return h, err
}

// EpisodesDeleteManyByEpisode 删除某剧集全部观看历史（删除剧集时清理）。
func (r *HistoryRepo) EpisodesDeleteManyByEpisode(ctx context.Context, episodeID any) error {
	ctx, cancel := context.WithTimeout(ctx, contextTimeout)
	defer cancel()
	_, err := r.coll.DeleteMany(ctx, bson.M{"episodeId": ToObjectID(episodeID)})
	return err
}

// EpisodesFindByUserEpisode 查用户对某剧集的评分（findOne({userId, episodeId})）；
// 不存在返回 ErrNotFound。
func (r *RatingRepo) EpisodesFindByUserEpisode(ctx context.Context, userID, episodeID any) (*model.Rating, error) {
	ctx, cancel := context.WithTimeout(ctx, contextTimeout)
	defer cancel()
	rt := &model.Rating{}
	err := r.coll.FindOne(ctx, bson.M{"userId": ToObjectID(userID), "episodeId": ToObjectID(episodeID)}).Decode(rt)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, ErrNotFound
	}
	return rt, err
}

// EpisodesDeleteManyByEpisode 删除某剧集全部评分（删除剧集时清理）。
func (r *RatingRepo) EpisodesDeleteManyByEpisode(ctx context.Context, episodeID any) error {
	ctx, cancel := context.WithTimeout(ctx, contextTimeout)
	defer cancel()
	_, err := r.coll.DeleteMany(ctx, bson.M{"episodeId": ToObjectID(episodeID)})
	return err
}
