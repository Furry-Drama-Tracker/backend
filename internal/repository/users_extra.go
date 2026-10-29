package repository

import (
	"context"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// 本文件为收藏夹域列表端点把 userId 展开为用户名所需的
// 跨域批量查用户名方法，挂到已有 UserRepo 类型上。

// FindUsernamesByIDs 批量查用户名，返回 id→username 映射。
// 关联语义与既有集合查询一致（展开 userId 只取 username）：
// 只取 _id + username，不存在的用户不出现在映射中（调用方回退空串）。
func (r *UserRepo) FindUsernamesByIDs(ctx context.Context, ids []primitive.ObjectID) (map[primitive.ObjectID]string, error) {
	ctx, cancel := context.WithTimeout(ctx, contextTimeout)
	defer cancel()
	out := make(map[primitive.ObjectID]string, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	cur, err := r.coll.Find(ctx, bson.M{"_id": bson.M{"$in": ids}},
		options.Find().SetProjection(bson.M{"username": 1}))
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	for cur.Next(ctx) {
		var u struct {
			ID       primitive.ObjectID `bson:"_id"`
			Username string             `bson:"username"`
		}
		if err := cur.Decode(&u); err != nil {
			return nil, err
		}
		out[u.ID] = u.Username
	}
	if err := cur.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
