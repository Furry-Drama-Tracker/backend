package repository

import (
	"context"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// 本文件为 episodes 域列表/详情端点把 createdBy/allowedEditors/customAuthors
// 批量展开为用户摘要（_id + accountId + username）所需的
// 跨集合批量查用户方法，挂到已有 *UserRepo 类型上。

// EpisodesUserRef 是用户引用视图（_id + accountId + username）。
type EpisodesUserRef struct {
	ID        primitive.ObjectID `bson:"_id" json:"_id"`
	AccountID string             `bson:"accountId" json:"accountId"`
	Username  string             `bson:"username" json:"username"`
}

// EpisodesFindUserRefsByIDs 批量查用户引用，返回 hex → EpisodesUserRef 的映射。
// 关联语义与既有集合查询一致：只取 _id + accountId + username；
// 不存在的用户不出现在映射中（调用方渲染 null）。
func (r *UserRepo) EpisodesFindUserRefsByIDs(ctx context.Context, ids []primitive.ObjectID) (map[string]EpisodesUserRef, error) {
	ctx, cancel := context.WithTimeout(ctx, contextTimeout)
	defer cancel()
	out := make(map[string]EpisodesUserRef, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	cur, err := r.coll.Find(ctx, bson.M{"_id": bson.M{"$in": ids}},
		options.Find().SetProjection(bson.M{"accountId": 1, "username": 1}))
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	for cur.Next(ctx) {
		var u EpisodesUserRef
		if err := cur.Decode(&u); err != nil {
			return nil, err
		}
		out[u.ID.Hex()] = u
	}
	if err := cur.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// EpisodesMailTarget 是邮件通知收件人信息（sendBatchNotificationEmails 的 join 视图）。
type EpisodesMailTarget struct {
	ID                primitive.ObjectID `bson:"_id"`
	Email             string             `bson:"email"`
	IsEmailVerified   bool               `bson:"isEmailVerified"`
	EpisodeUpdatePref *bool              `bson:"episodeUpdate"`
}

// EpisodesFindMailTargetsByIDs 批量查邮件收件人（email + isEmailVerified +
// emailNotificationPrefs.episodeUpdate；select email/isEmailVerified/
// emailNotificationPrefs）：偏好键缺失（nil）视为未关闭（允许发送），仅显式 false 才跳过。
func (r *UserRepo) EpisodesFindMailTargetsByIDs(ctx context.Context, ids []primitive.ObjectID) (map[string]EpisodesMailTarget, error) {
	ctx, cancel := context.WithTimeout(ctx, contextTimeout)
	defer cancel()
	out := make(map[string]EpisodesMailTarget, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	var rows []struct {
		ID                     primitive.ObjectID `bson:"_id"`
		Email                  string             `bson:"email"`
		IsEmailVerified        bool               `bson:"isEmailVerified"`
		EmailNotificationPrefs struct {
			EpisodeUpdate *bool `bson:"episodeUpdate"`
		} `bson:"emailNotificationPrefs"`
	}
	cur, err := r.coll.Find(ctx, bson.M{"_id": bson.M{"$in": ids}},
		options.Find().SetProjection(bson.M{"email": 1, "isEmailVerified": 1, "emailNotificationPrefs.episodeUpdate": 1}))
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	if err := cur.All(ctx, &rows); err != nil {
		return nil, err
	}
	for _, row := range rows {
		out[row.ID.Hex()] = EpisodesMailTarget{
			ID:                row.ID,
			Email:             row.Email,
			IsEmailVerified:   row.IsEmailVerified,
			EpisodeUpdatePref: row.EmailNotificationPrefs.EpisodeUpdate,
		}
	}
	return out, nil
}
