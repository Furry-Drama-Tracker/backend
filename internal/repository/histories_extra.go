package repository

import (
	"context"
	"errors"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/xiedada05/furry-drama-be-neo/internal/model"
)

// 本文件为 /api/histories 域的数据访问方法，
// 挂在 *HistoryRepo 与 *EpisodeRepo 上。方法名带 History 前缀，避免与
// episodes/follows 等其它域的跨集合方法（Episodes* / Follows* 前缀）冲突。

// historyDoc 是 histories 集合文档（含文档版本键 __v，响应需输出 __v）。
// 内联嵌入 model.History 复用字段。
type historyDoc struct {
	model.History `bson:",inline"`
	Version       int `bson:"__v"`
}

// HistoryRow 是单条历史记录 + 文档版本键 __v（供 handler 组装响应）。
type HistoryRow struct {
	History model.History
	Version int
}

// HistoryCheckRow 是 GET /check 的返回数据。时间用指针以保留 null
// （lastWatched 缺省输出 null，消费方依赖此格式）；
// watchedEpisodes 由 handler 归一化为空数组。
type HistoryCheckRow struct {
	WatchedEpisodes          []int      `bson:"watchedEpisodes"`
	LastWatchedEpisodeNumber *int       `bson:"lastWatchedEpisodeNumber"`
	LastWatched              *time.Time `bson:"lastWatched"`
}

// HistoryEpisodeView 是历史响应中展开 episodeId 得到的剧集摘要（选定字段：
// title titleEn titleJa coverImage totalEpisodes currentEpisodes status
// category tags views averageRating；默认值形态与集合文档一致）。
type HistoryEpisodeView struct {
	ID              primitive.ObjectID `bson:"_id" json:"_id"`
	Title           string             `bson:"title" json:"title"`
	TitleEn         string             `bson:"titleEn" json:"titleEn"`
	TitleJa         string             `bson:"titleJa" json:"titleJa"`
	CoverImage      string             `bson:"coverImage" json:"coverImage"`
	TotalEpisodes   *int               `bson:"totalEpisodes" json:"totalEpisodes"`
	CurrentEpisodes int                `bson:"currentEpisodes" json:"currentEpisodes"`
	Status          string             `bson:"status" json:"status"`
	Category        []string           `bson:"category" json:"category"`
	Tags            []string           `bson:"tags" json:"tags"`
	Views           int64              `bson:"views" json:"views"`
	AverageRating   float64            `bson:"averageRating" json:"averageRating"`
}

// normalize 补齐集合文档默认值：status 缺省 ongoing，category/tags 缺省 []。
func (v *HistoryEpisodeView) normalize() {
	if v.Status == "" {
		v.Status = "ongoing"
	}
	if v.Category == nil {
		v.Category = []string{}
	}
	if v.Tags == nil {
		v.Tags = []string{}
	}
}

// HistoryUpsertRecord 记录/更新一条观看历史（POST /record 使用）：
//   - 不存在 → 插入 {watchedEpisodes:[epNum], lastWatchedEpisodeNumber:epNum, lastWatched:now}；
//   - 已存在 → epNum 不在 watchedEpisodes 则追加，更新 lastWatchedEpisodeNumber /
//     lastWatched，__v 自增（定点更新语义）。
//
// 返回保存后的历史文档与 __v 值。
func (r *HistoryRepo) HistoryUpsertRecord(ctx context.Context, userID, episodeID primitive.ObjectID, epNum int, now time.Time) (*model.History, int, error) {
	ctx, cancel := context.WithTimeout(ctx, contextTimeout)
	defer cancel()
	now = now.UTC().Truncate(time.Millisecond) // 毫秒精度 + UTC Z 时区，与既有写入格式一致

	doc := &historyDoc{}
	err := r.coll.FindOne(ctx, bson.M{"userId": userID, "episodeId": episodeID}).Decode(doc)
	if err == nil {
		found := false
		for _, n := range doc.WatchedEpisodes {
			if n == epNum {
				found = true
				break
			}
		}
		if !found {
			doc.WatchedEpisodes = append(doc.WatchedEpisodes, epNum)
		}
		doc.LastWatchedEpisodeNumber = &epNum
		doc.LastWatched = now
		doc.Version++
		if _, err := r.coll.UpdateOne(ctx, bson.M{"_id": doc.ID}, bson.M{"$set": bson.M{
			"watchedEpisodes":          doc.WatchedEpisodes,
			"lastWatchedEpisodeNumber": epNum,
			"lastWatched":              now,
			"__v":                      doc.Version,
		}}); err != nil {
			return nil, 0, err
		}
		h := doc.History
		return &h, doc.Version, nil
	}
	if !errors.Is(err, mongo.ErrNoDocuments) {
		return nil, 0, err
	}

	h := &model.History{
		UserID:                   userID,
		EpisodeID:                episodeID,
		WatchedEpisodes:          []int{epNum},
		LastWatchedEpisodeNumber: &epNum,
		LastWatched:              now,
		CreatedAt:                now,
	}
	res, err := r.coll.InsertOne(ctx, historyDoc{History: *h, Version: 0})
	if err != nil {
		return nil, 0, err
	}
	h.ID = res.InsertedID.(primitive.ObjectID)
	return h, 0, nil
}

// HistoryCountByUser 统计用户历史总数（GET /list 的 total）。
func (r *HistoryRepo) HistoryCountByUser(ctx context.Context, userID primitive.ObjectID) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, contextTimeout)
	defer cancel()
	return r.coll.CountDocuments(ctx, bson.M{"userId": userID})
}

// HistoryFindContinueWatching 返回用户最近观看的至多 limit 条历史
// （sort({lastWatched:-1})；/continue-watching 传 limit=10）。
func (r *HistoryRepo) HistoryFindContinueWatching(ctx context.Context, userID primitive.ObjectID, limit int) ([]HistoryRow, error) {
	return r.historyFindRows(ctx, userID, 0, int64(limit))
}

// HistoryFindPage 分页查询用户历史（sort({lastWatched:-1}) + skip/limit）。
//   - skip 恒设置：负值由 mongo 拒绝 → error → handler 500（page<1 时
//     skip=(page-1)*limit 为负，既有报错路径，勿改）；
//   - limit==0 表示不限条数；limit<0 由 mongo 拒绝 → 500。
func (r *HistoryRepo) HistoryFindPage(ctx context.Context, userID primitive.ObjectID, skip, limit int64) ([]HistoryRow, error) {
	return r.historyFindRows(ctx, userID, skip, limit)
}

// historyFindRows 按 lastWatched 倒序查询历史行。
func (r *HistoryRepo) historyFindRows(ctx context.Context, userID primitive.ObjectID, skip, limit int64) ([]HistoryRow, error) {
	ctx, cancel := context.WithTimeout(ctx, contextTimeout)
	defer cancel()
	opts := options.Find().
		SetSort(bson.D{{Key: "lastWatched", Value: -1}}).
		SetSkip(skip)
	if limit != 0 {
		opts.SetLimit(limit)
	}
	cur, err := r.coll.Find(ctx, bson.M{"userId": userID}, opts)
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	rows := make([]HistoryRow, 0)
	for cur.Next(ctx) {
		var d historyDoc
		if err := cur.Decode(&d); err != nil {
			return nil, err
		}
		rows = append(rows, HistoryRow{History: d.History, Version: d.Version})
	}
	return rows, cur.Err()
}

// HistoryFindCheck 查用户对某剧集的观看历史（GET /check/:episodeId，
// findOne({userId, episodeId})）；不存在返回 ErrNotFound。
func (r *HistoryRepo) HistoryFindCheck(ctx context.Context, userID, episodeID primitive.ObjectID) (*HistoryCheckRow, error) {
	ctx, cancel := context.WithTimeout(ctx, contextTimeout)
	defer cancel()
	row := &HistoryCheckRow{}
	err := r.coll.FindOne(ctx, bson.M{"userId": userID, "episodeId": episodeID},
		options.FindOne().SetProjection(bson.M{"watchedEpisodes": 1, "lastWatchedEpisodeNumber": 1, "lastWatched": 1})).Decode(row)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, ErrNotFound
	}
	return row, err
}

// HistoryDeleteOneByUserEpisode 删除用户对某剧集的观看历史（DELETE /:episodeId，
// deleteOne({userId, episodeId})）。
func (r *HistoryRepo) HistoryDeleteOneByUserEpisode(ctx context.Context, userID, episodeID primitive.ObjectID) error {
	ctx, cancel := context.WithTimeout(ctx, contextTimeout)
	defer cancel()
	_, err := r.coll.DeleteOne(ctx, bson.M{"userId": userID, "episodeId": episodeID})
	return err
}

// HistoryFindEpisodePopulate 批量拉取历史展开 episodeId 所需的剧集摘要
// （key 为 episode _id；不存在的剧集不在 map 中 → 响应 episodeId: null，
// 已删除剧集输出 null 是既有契约，消费方依赖此格式）。
func (r *EpisodeRepo) HistoryFindEpisodePopulate(ctx context.Context, ids []primitive.ObjectID) (map[primitive.ObjectID]*HistoryEpisodeView, error) {
	ctx, cancel := context.WithTimeout(ctx, contextTimeout)
	defer cancel()
	out := make(map[primitive.ObjectID]*HistoryEpisodeView, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	projection := bson.M{
		"title": 1, "titleEn": 1, "titleJa": 1, "coverImage": 1,
		"totalEpisodes": 1, "currentEpisodes": 1, "status": 1,
		"category": 1, "tags": 1, "views": 1, "averageRating": 1,
	}
	cur, err := r.coll.Find(ctx, bson.M{"_id": bson.M{"$in": ids}},
		options.Find().SetProjection(projection))
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	for cur.Next(ctx) {
		var v HistoryEpisodeView
		if err := cur.Decode(&v); err != nil {
			return nil, err
		}
		v.normalize()
		out[v.ID] = &v
	}
	return out, cur.Err()
}
