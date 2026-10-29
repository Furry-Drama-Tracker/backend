package repository

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"

	"github.com/xiedada05/furry-drama-be-neo/internal/model"
)

// 本文件为 auto-status 域在既有 repo 类型上新增的
// 独占方法。EpisodeRepo.Save（整文档覆盖保存）已在 episode.go 定义，复用；
// SingleEpisode 无 Save 方法，故在此补充。

// AutoStatusFindOngoing 查询全部 ongoing 状态剧集（供 POST /auto-complete 使用）。
func (r *EpisodeRepo) AutoStatusFindOngoing(ctx context.Context) ([]model.Episode, error) {
	ctx, cancel := context.WithTimeout(ctx, contextTimeout)
	defer cancel()
	cur, err := r.coll.Find(ctx, bson.M{"status": "ongoing"})
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	var list []model.Episode
	if err := cur.All(ctx, &list); err != nil {
		return nil, err
	}
	return list, nil
}

// AutoStatusFindDuePremieres 查询已到首播时间的预告单集
// （isUpcoming=true 且 premiereDate <= now）。
func (r *SingleEpisodeRepo) AutoStatusFindDuePremieres(ctx context.Context, now time.Time) ([]model.SingleEpisode, error) {
	ctx, cancel := context.WithTimeout(ctx, contextTimeout)
	defer cancel()
	cur, err := r.coll.Find(ctx, bson.M{
		"isUpcoming":   true,
		"premiereDate": bson.M{"$lte": now},
	})
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	var list []model.SingleEpisode
	if err := cur.All(ctx, &list); err != nil {
		return nil, err
	}
	return list, nil
}

// AutoStatusSave 整文档覆盖保存单集（__v 自增）。
func (r *SingleEpisodeRepo) AutoStatusSave(ctx context.Context, s *model.SingleEpisode) error {
	ctx, cancel := context.WithTimeout(ctx, contextTimeout)
	defer cancel()
	s.VersionKey++
	_, err := r.coll.ReplaceOne(ctx, bson.M{"_id": s.ID}, s)
	return err
}
