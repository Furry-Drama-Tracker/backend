package repository

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/Furry-Drama-Tracker/backend/internal/model"
)

// AuthEventRepo 认证诊断事件仓储（authevents 集合，at 建 TTL 索引自动过期）。
//
// 只增不改：事件是排障证据，不提供删除/更新方法（TTL 负责生命周期）。
type AuthEventRepo struct {
	coll *mongo.Collection
}

// NewAuthEventRepo 构造仓储。
func NewAuthEventRepo(coll *mongo.Collection) *AuthEventRepo {
	return &AuthEventRepo{coll: coll}
}

func (r *AuthEventRepo) newCtx(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, contextTimeout)
}

// Insert 写入一条事件。
func (r *AuthEventRepo) Insert(ctx context.Context, ev *model.AuthEvent) error {
	ctx, cancel := r.newCtx(ctx)
	defer cancel()
	_, err := r.coll.InsertOne(ctx, ev)
	return err
}

// FindByUserID 按用户查事件（since 之后，按时间倒序）。
func (r *AuthEventRepo) FindByUserID(ctx context.Context, userID any, since time.Time, limit int64) ([]model.AuthEvent, error) {
	return r.find(ctx, bson.M{"userId": ToObjectID(userID), "at": bson.M{"$gte": since}}, limit)
}

// FindByEmail 按邮箱查事件（用户报障通常只给邮箱，无需先查 _id）。
func (r *AuthEventRepo) FindByEmail(ctx context.Context, email string, since time.Time, limit int64) ([]model.AuthEvent, error) {
	return r.find(ctx, bson.M{"email": email, "at": bson.M{"$gte": since}}, limit)
}

// Recent 返回 since 之后的最近事件（全局，用于看是否大面积发生）。
func (r *AuthEventRepo) Recent(ctx context.Context, since time.Time, limit int64) ([]model.AuthEvent, error) {
	return r.find(ctx, bson.M{"at": bson.M{"$gte": since}}, limit)
}

func (r *AuthEventRepo) find(ctx context.Context, filter bson.M, limit int64) ([]model.AuthEvent, error) {
	ctx, cancel := r.newCtx(ctx)
	defer cancel()
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	cur, err := r.coll.Find(ctx, filter, options.Find().
		SetSort(bson.D{{Key: "at", Value: -1}}).
		SetLimit(limit))
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	var out []model.AuthEvent
	if err := cur.All(ctx, &out); err != nil {
		return nil, err
	}
	return out, nil
}
