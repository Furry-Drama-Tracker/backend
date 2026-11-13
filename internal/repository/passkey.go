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

// PasskeyRepo WebAuthn 凭据仓储（passkeys 集合）。
type PasskeyRepo struct {
	coll *mongo.Collection
}

// NewPasskeyRepo 构造凭据仓储。
func NewPasskeyRepo(coll *mongo.Collection) *PasskeyRepo {
	return &PasskeyRepo{coll: coll}
}

// FindByCredentialID 按凭据 ID（base64url）查找。
func (r *PasskeyRepo) FindByCredentialID(ctx context.Context, credentialID string) (*model.Passkey, error) {
	ctx, cancel := context.WithTimeout(ctx, contextTimeout)
	defer cancel()
	var pk model.Passkey
	err := r.coll.FindOne(ctx, bson.M{"credentialId": credentialID}).Decode(&pk)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &pk, nil
}

// FindByUser 列出用户全部凭据（按创建时间倒序）。
func (r *PasskeyRepo) FindByUser(ctx context.Context, userID any) ([]model.Passkey, error) {
	ctx, cancel := context.WithTimeout(ctx, contextTimeout)
	defer cancel()
	cur, err := r.coll.Find(ctx, bson.M{"userId": ToObjectID(userID)},
		options.Find().SetSort(bson.M{"createdAt": -1}))
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	var list []model.Passkey
	if err := cur.All(ctx, &list); err != nil {
		return nil, err
	}
	return list, nil
}

// Create 插入凭据；credentialId 冲突返回 IsDuplicateKey。
func (r *PasskeyRepo) Create(ctx context.Context, pk *model.Passkey) error {
	ctx, cancel := context.WithTimeout(ctx, contextTimeout)
	defer cancel()
	now := time.Now().UTC().Truncate(time.Millisecond)
	if pk.CreatedAt.IsZero() {
		pk.CreatedAt = now
	}
	pk.LastUsedAt = now
	res, err := r.coll.InsertOne(ctx, pk)
	if mongo.IsDuplicateKeyError(err) {
		return errDuplicateKey
	}
	if err != nil {
		return err
	}
	if oid, ok := res.InsertedID.(primitive.ObjectID); ok {
		pk.ID = oid
	}
	return nil
}

// UpdateSignCount 更新签名计数与最近使用时间（登录仪式成功后调用）。
func (r *PasskeyRepo) UpdateSignCount(ctx context.Context, id any, signCount uint32) error {
	ctx, cancel := context.WithTimeout(ctx, contextTimeout)
	defer cancel()
	_, err := r.coll.UpdateOne(ctx, bson.M{"_id": ToObjectID(id)},
		bson.M{"$set": bson.M{"signCount": signCount, "lastUsedAt": time.Now().UTC()}})
	return err
}

// UpdateNameAndBackupState 更新凭据名与备份标志位。
func (r *PasskeyRepo) UpdateNameAndBackupState(ctx context.Context, id any, name string, uve, backupState bool) error {
	ctx, cancel := context.WithTimeout(ctx, contextTimeout)
	defer cancel()
	_, err := r.coll.UpdateOne(ctx, bson.M{"_id": ToObjectID(id)},
		bson.M{"$set": bson.M{"name": name, "backupEligible": uve, "backupState": backupState}})
	return err
}

// DeleteByID 删除凭据（仅限属主，filter 带 userId）。
func (r *PasskeyRepo) DeleteByID(ctx context.Context, id, userID any) error {
	ctx, cancel := context.WithTimeout(ctx, contextTimeout)
	defer cancel()
	_, err := r.coll.DeleteOne(ctx, bson.M{"_id": ToObjectID(id), "userId": ToObjectID(userID)})
	return err
}

// DeleteByUser 删除用户全部凭据（注销清理用）。
func (r *PasskeyRepo) DeleteByUser(ctx context.Context, userID any) error {
	ctx, cancel := context.WithTimeout(ctx, contextTimeout)
	defer cancel()
	_, err := r.coll.DeleteMany(ctx, bson.M{"userId": ToObjectID(userID)})
	return err
}

// CountByUser 统计用户凭据数（注销前的绑定检查）。
func (r *PasskeyRepo) CountByUser(ctx context.Context, userID any) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, contextTimeout)
	defer cancel()
	return r.coll.CountDocuments(ctx, bson.M{"userId": ToObjectID(userID)})
}
