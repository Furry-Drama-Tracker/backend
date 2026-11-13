package repository

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// CounterRepo 序列号发号器（counters 集合）。
type CounterRepo struct {
	coll *mongo.Collection
}

// NewCounterRepo 构造发号器仓储。
func NewCounterRepo(coll *mongo.Collection) *CounterRepo {
	return &CounterRepo{coll: coll}
}

// NextUID 原子发放下一个用户 UID：FindAndModify $inc 保证并发下不重号。
// 首个用户（seed 账号）在调用方显式指定 UID=0，不走本方法；
// 因此首次调用返回 1。
func (r *CounterRepo) NextUID(ctx context.Context) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, contextTimeout)
	defer cancel()
	var doc struct {
		Seq int64 `bson:"seq"`
	}
	err := r.coll.FindOneAndUpdate(ctx,
		bson.M{"_id": "uid"},
		bson.M{"$inc": bson.M{"seq": int64(1)}},
		options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.After),
	).Decode(&doc)
	if err != nil {
		return 0, fmt.Errorf("counter next uid: %w", err)
	}
	return doc.Seq, nil
}

// SetUIDCounter 把 uid 计数器至少推进到 maxSeen（迁移回填后同步计数器起点，
// 使后续发号不会与回填号段冲突）。若当前值更大则保持不变。
func (r *CounterRepo) SetUIDCounter(ctx context.Context, maxSeen int64) error {
	ctx, cancel := context.WithTimeout(ctx, contextTimeout)
	defer cancel()
	if maxSeen < 0 {
		maxSeen = 0
	}
	_, err := r.coll.UpdateOne(ctx,
		bson.M{"_id": "uid", "seq": bson.M{"$lt": maxSeen}},
		bson.M{"$set": bson.M{"seq": maxSeen}},
		options.Update().SetUpsert(true),
	)
	return err
}
