package repository

import (
	"context"
	"errors"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/Furry-Drama-Tracker/backend/internal/model"
)

// 本文件为 reports 域的数据访问方法，
// 挂到已有 *ReportRepo 类型上。方法统一以 Reports 前缀命名，避免与其它域 agent 重名。

// ReportsDoc 是举报文档视图（含 __v，响应需包含文档版本键）。
// model.Report 未定义 __v 字段，故在此内联补充。
type ReportsDoc struct {
	model.Report `bson:",inline"`
	VersionKey   int `bson:"__v" json:"__v"`
}

// ReportsCreate 插入举报；补 _id/createdAt/updatedAt/__v:0
// 默认值（时间戳与版本键形态与既有集合文档一致）。
func (r *ReportRepo) ReportsCreate(ctx context.Context, rep *model.Report) (*ReportsDoc, error) {
	ctx, cancel := context.WithTimeout(ctx, contextTimeout)
	defer cancel()
	if rep.ID.IsZero() {
		rep.ID = primitive.NewObjectID()
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	if rep.CreatedAt.IsZero() {
		rep.CreatedAt = now
	}
	if rep.UpdatedAt.IsZero() {
		rep.UpdatedAt = now
	}
	doc := &ReportsDoc{Report: *rep, VersionKey: 0}
	_, err := r.coll.InsertOne(ctx, doc)
	return doc, err
}

// ReportsFindPending 查某用户对某目标的待处理举报
// （findOne({reporterId, targetType, targetId, status:'pending'})）；
// 不存在返回 ErrNotFound。
func (r *ReportRepo) ReportsFindPending(ctx context.Context, reporterID any, targetType string, targetID any) (*ReportsDoc, error) {
	ctx, cancel := context.WithTimeout(ctx, contextTimeout)
	defer cancel()
	doc := &ReportsDoc{}
	err := r.coll.FindOne(ctx, bson.M{
		"reporterId": reporterID, "targetType": targetType,
		"targetId": targetID, "status": "pending",
	}).Decode(doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, ErrNotFound
	}
	return doc, err
}

// ReportsCount 统计举报数（countDocuments(filter)）。
func (r *ReportRepo) ReportsCount(ctx context.Context, filter bson.M) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, contextTimeout)
	defer cancel()
	return r.coll.CountDocuments(ctx, filter)
}

// ReportsList 分页查询举报（filter + createdAt 倒序 + skip/limit）。
func (r *ReportRepo) ReportsList(ctx context.Context, filter bson.M, skip, limit int64) ([]ReportsDoc, error) {
	ctx, cancel := context.WithTimeout(ctx, contextTimeout)
	defer cancel()
	opts := options.Find().SetSort(bson.D{{Key: "createdAt", Value: -1}})
	// skip 恒设置（含负值）：由 mongo 拒绝 → error → 500（page<1 时 skip 为负，既有报错路径）。
	opts.SetSkip(skip)
	if limit > 0 {
		opts.SetLimit(limit)
	}
	cur, err := r.coll.Find(ctx, filter, opts)
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	var list []ReportsDoc
	if err := cur.All(ctx, &list); err != nil {
		return nil, err
	}
	return list, nil
}

// ReportsResolve 处理举报并返回更新后的文档
// （$set status/resolveNote/resolvedBy）。不存在返回 ErrNotFound。
func (r *ReportRepo) ReportsResolve(ctx context.Context, id any, status, note string, resolvedBy any) (*ReportsDoc, error) {
	ctx, cancel := context.WithTimeout(ctx, contextTimeout)
	defer cancel()
	doc := &ReportsDoc{}
	err := r.coll.FindOneAndUpdate(ctx, bson.M{"_id": ToObjectID(id)},
		bson.M{"$set": bson.M{"status": status, "resolveNote": note, "resolvedBy": resolvedBy}},
		options.FindOneAndUpdate().SetReturnDocument(options.After)).Decode(doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, ErrNotFound
	}
	return doc, err
}

// ReportsFindReporterRefs 批量取举报者视图（_id + accountId + username，
// 展开 reporterId 用）；返回 hex → 视图。查询挂在
// *UserRepo 上（目标集合为 users）。
func (r *UserRepo) ReportsFindReporterRefs(ctx context.Context, ids []primitive.ObjectID) (map[string]ReportsReporterRef, error) {
	out := map[string]ReportsReporterRef{}
	if len(ids) == 0 {
		return out, nil
	}
	var rows []struct {
		ID        primitive.ObjectID `bson:"_id"`
		AccountID string             `bson:"accountId"`
		Username  string             `bson:"username"`
	}
	ctx2, cancel := context.WithTimeout(ctx, contextTimeout)
	defer cancel()
	cur, err := r.coll.Find(ctx2,
		bson.M{"_id": bson.M{"$in": ids}},
		options.Find().SetProjection(bson.M{"accountId": 1, "username": 1}))
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx2)
	if err := cur.All(ctx2, &rows); err != nil {
		return nil, err
	}
	for _, row := range rows {
		out[row.ID.Hex()] = ReportsReporterRef{ID: row.ID, AccountID: row.AccountID, Username: row.Username}
	}
	return out, nil
}

// ReportsReporterRef 是举报列表中展开 reporterId 得到的举报者视图。
type ReportsReporterRef struct {
	ID        primitive.ObjectID `bson:"_id" json:"_id"`
	AccountID string             `bson:"accountId" json:"accountId"`
	Username  string             `bson:"username" json:"username"`
}
