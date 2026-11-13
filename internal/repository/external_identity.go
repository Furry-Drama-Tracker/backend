package repository

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"

	"github.com/Furry-Drama-Tracker/backend/internal/model"
)

// ExternalIdentityRepo 外部身份（QQ 预留）仓储。
type ExternalIdentityRepo struct {
	coll *mongo.Collection
}

// NewExternalIdentityRepo 构造外部身份仓储。
func NewExternalIdentityRepo(coll *mongo.Collection) *ExternalIdentityRepo {
	return &ExternalIdentityRepo{coll: coll}
}

// FindByProviderOpenID 按 (provider, openid) 查找绑定记录。
func (r *ExternalIdentityRepo) FindByProviderOpenID(ctx context.Context, provider, openid string) (*model.ExternalIdentity, error) {
	ctx, cancel := context.WithTimeout(ctx, contextTimeout)
	defer cancel()
	var doc model.ExternalIdentity
	err := r.coll.FindOne(ctx, bson.M{"provider": provider, "openid": openid}).Decode(&doc)
	if err == mongo.ErrNoDocuments {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &doc, nil
}

// FindByUser 查找某用户的全部外部身份绑定。
func (r *ExternalIdentityRepo) FindByUser(ctx context.Context, userID any) ([]model.ExternalIdentity, error) {
	ctx, cancel := context.WithTimeout(ctx, contextTimeout)
	defer cancel()
	cur, err := r.coll.Find(ctx, bson.M{"userId": ToObjectID(userID)})
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	var list []model.ExternalIdentity
	if err := cur.All(ctx, &list); err != nil {
		return nil, err
	}
	return list, nil
}

// Create 插入绑定记录（(provider, openid) 唯一索引兜底并发重复绑定）。
func (r *ExternalIdentityRepo) Create(ctx context.Context, e *model.ExternalIdentity) error {
	ctx, cancel := context.WithTimeout(ctx, contextTimeout)
	defer cancel()
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now().UTC().Truncate(time.Millisecond)
	}
	res, err := r.coll.InsertOne(ctx, e)
	if mongo.IsDuplicateKeyError(err) {
		return errDuplicateKey
	}
	if err != nil {
		return err
	}
	if oid, ok := res.InsertedID.(primitive.ObjectID); ok {
		e.ID = oid
	}
	return nil
}

// DeleteByUserAndProvider 解绑某用户的某个 provider 身份。
func (r *ExternalIdentityRepo) DeleteByUserAndProvider(ctx context.Context, userID any, provider string) error {
	ctx, cancel := context.WithTimeout(ctx, contextTimeout)
	defer cancel()
	_, err := r.coll.DeleteOne(ctx, bson.M{"userId": ToObjectID(userID), "provider": provider})
	return err
}

// DeleteByUser 删除某用户的全部外部身份绑定（注销清理用）。
func (r *ExternalIdentityRepo) DeleteByUser(ctx context.Context, userID any) error {
	ctx, cancel := context.WithTimeout(ctx, contextTimeout)
	defer cancel()
	_, err := r.coll.DeleteMany(ctx, bson.M{"userId": ToObjectID(userID)})
	return err
}
