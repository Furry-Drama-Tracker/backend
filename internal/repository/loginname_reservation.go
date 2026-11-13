package repository

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// LoginNameReservation 是改名的旧用户名保留记录（loginnamereservations 集合）：
// 改名后旧名进入保留期（reservedUntil 内任何人不可注册），到期由 TTL 索引自动删除。
type LoginNameReservation struct {
	LoginName     string    `bson:"loginName" json:"loginName"`
	UserID        string    `bson:"userId" json:"userId"`
	ReservedUntil time.Time `bson:"reservedUntil" json:"reservedUntil"`
	CreatedAt     time.Time `bson:"createdAt" json:"createdAt"`
}

// LoginNameReservationRepo 旧用户名保留仓储。
type LoginNameReservationRepo struct {
	coll *mongo.Collection
}

// NewLoginNameReservationRepo 构造保留仓储。
func NewLoginNameReservationRepo(coll *mongo.Collection) *LoginNameReservationRepo {
	return &LoginNameReservationRepo{coll: coll}
}

// Reserve 写入一条旧名保留记录；同名记录已存在时刷新到期时间（upsert）。
// loginName 由调用方传折叠小写（防止大小写变体绕过保留期）。
func (r *LoginNameReservationRepo) Reserve(ctx context.Context, loginName, userID string, reservedUntil time.Time) error {
	ctx, cancel := context.WithTimeout(ctx, contextTimeout)
	defer cancel()
	now := time.Now().UTC().Truncate(time.Millisecond)
	_, err := r.coll.UpdateOne(ctx,
		bson.M{"loginName": loginName},
		bson.M{"$set": bson.M{
			"userId":        userID,
			"reservedUntil": reservedUntil,
			"createdAt":     now,
		}},
		options.Update().SetUpsert(true),
	)
	return err
}

// ExistsActive 判断某名字当前是否处于保留期（TTL 索引会自动清掉过期记录，
// 但删除有约 60s 延迟，这里仍按 reservedUntil 过滤兜底）。
// excludeUserID 非 nil（string 形式的 users._id hex）时豁免该用户自己的记录——
// 大小写微调（Alice→ALICE）的新名折叠形式等于自己刚保留的旧名，不应挡住自己。
func (r *LoginNameReservationRepo) ExistsActive(ctx context.Context, loginName string, now time.Time, excludeUserID any) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, contextTimeout)
	defer cancel()
	filter := bson.M{
		"loginName":     loginName,
		"reservedUntil": bson.M{"$gt": now},
	}
	if excludeUserID != nil {
		filter["userId"] = bson.M{"$ne": excludeUserID}
	}
	err := r.coll.FindOne(ctx, filter, options.FindOne().SetProjection(bson.M{"_id": 1})).Err()
	if err == mongo.ErrNoDocuments {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}
