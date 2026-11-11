// Package indexes 在启动时为所有集合幂等建立索引。
//
// 幂等策略：先按索引名（沿用字段+方向的自动命名约定，如 email_1、userId_1_isActive_1）
// 判断是否已存在，存在则跳过；
// 创建时若报"already exists"（同名但键/选项冲突）同样视为已建立，不阻断启动。
package indexes

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// indexSpec 描述一个待建索引。
type indexSpec struct {
	coll       string
	name       string
	keys       bson.D
	unique     bool
	sparse     bool
	isTTL      bool
	ttlSeconds int32
}

// options 由 spec 构建建索引选项。
func (s indexSpec) options() *options.IndexOptions {
	opts := options.Index()
	if s.unique {
		opts.SetUnique(true)
	}
	if s.sparse {
		opts.SetSparse(true)
	}
	if s.isTTL {
		opts.SetExpireAfterSeconds(s.ttlSeconds)
	}
	return opts
}

// Ensure 幂等地为全部集合建立索引。
//
// 清单（键与 TTL 秒数）：
//
//	users:            email 唯一, accountId 唯一
//	usersessions:     userId+isActive, isActive+lastActiveAt, refreshTokenHash 唯一 sparse
//	usedtokens:       tokenHash 唯一, expiresAt TTL 0s
//	histories:        lastWatched TTL 365d, userId+lastWatched
//	notifications:    userId+isRead, createdAt TTL 90d
//	ratings:          userId+episodeId 唯一, episodeId
//	follows:          userId+episodeId 唯一
//	favorites:        userId+episodeId 唯一
//	auditlogs:        createdAt, adminId, userId
//	apiusages:        endpoint+date 唯一
//	sitecontents:     key 唯一
//	folders:          userId+type
func Ensure(ctx context.Context, db *mongo.Database) error {
	specs := []indexSpec{
		{coll: "users", name: "email_1", keys: bson.D{{Key: "email", Value: 1}}, unique: true},
		{coll: "users", name: "accountId_1", keys: bson.D{{Key: "accountId", Value: 1}}, unique: true},

		{coll: "usersessions", name: "userId_1_isActive_1", keys: bson.D{{Key: "userId", Value: 1}, {Key: "isActive", Value: 1}}},
		{coll: "usersessions", name: "isActive_1_lastActiveAt_-1", keys: bson.D{{Key: "isActive", Value: 1}, {Key: "lastActiveAt", Value: -1}}},
		{coll: "usersessions", name: "refreshTokenHash_1", keys: bson.D{{Key: "refreshTokenHash", Value: 1}}, unique: true, sparse: true},
		{coll: "usersessions", name: "tokenHash_1", keys: bson.D{{Key: "tokenHash", Value: 1}}, unique: true, sparse: true},

		{coll: "usedtokens", name: "tokenHash_1", keys: bson.D{{Key: "tokenHash", Value: 1}}, unique: true},
		{coll: "usedtokens", name: "expiresAt_1", keys: bson.D{{Key: "expiresAt", Value: 1}}, isTTL: true, ttlSeconds: 0},

		{coll: "histories", name: "lastWatched_1", keys: bson.D{{Key: "lastWatched", Value: 1}}, isTTL: true, ttlSeconds: 365 * 24 * 60 * 60},
		{coll: "histories", name: "userId_1_lastWatched_-1", keys: bson.D{{Key: "userId", Value: 1}, {Key: "lastWatched", Value: -1}}},

		{coll: "notifications", name: "userId_1_isRead_1", keys: bson.D{{Key: "userId", Value: 1}, {Key: "isRead", Value: 1}}},
		{coll: "notifications", name: "createdAt_1", keys: bson.D{{Key: "createdAt", Value: 1}}, isTTL: true, ttlSeconds: 90 * 24 * 60 * 60},

		{coll: "ratings", name: "userId_1_episodeId_1", keys: bson.D{{Key: "userId", Value: 1}, {Key: "episodeId", Value: 1}}, unique: true},
		{coll: "ratings", name: "episodeId_1", keys: bson.D{{Key: "episodeId", Value: 1}}},

		{coll: "follows", name: "userId_1_episodeId_1", keys: bson.D{{Key: "userId", Value: 1}, {Key: "episodeId", Value: 1}}, unique: true},
		{coll: "favorites", name: "userId_1_episodeId_1", keys: bson.D{{Key: "userId", Value: 1}, {Key: "episodeId", Value: 1}}, unique: true},

		{coll: "auditlogs", name: "createdAt_-1", keys: bson.D{{Key: "createdAt", Value: -1}}},
		{coll: "auditlogs", name: "adminId_1", keys: bson.D{{Key: "adminId", Value: 1}}},
		{coll: "auditlogs", name: "userId_1", keys: bson.D{{Key: "userId", Value: 1}}},

		// authevents 是认证排障事件（会话吊销/refresh 异常/限流），按 userId+时间查，
		// 并靠 at 的 TTL 索引自动过期（30 天），避免无界增长。
		{coll: "authevents", name: "userId_1_at_-1", keys: bson.D{{Key: "userId", Value: 1}, {Key: "at", Value: -1}}},
		{coll: "authevents", name: "email_1_at_-1", keys: bson.D{{Key: "email", Value: 1}, {Key: "at", Value: -1}}},
		{coll: "authevents", name: "at_1", keys: bson.D{{Key: "at", Value: 1}}, isTTL: true, ttlSeconds: 30 * 24 * 60 * 60},

		{coll: "apiusages", name: "endpoint_1_date_1", keys: bson.D{{Key: "endpoint", Value: 1}, {Key: "date", Value: 1}}, unique: true},
		{coll: "sitecontents", name: "key_1", keys: bson.D{{Key: "key", Value: 1}}, unique: true},
		{coll: "folders", name: "userId_1_type_1", keys: bson.D{{Key: "userId", Value: 1}, {Key: "type", Value: 1}}},
		{coll: "savedfolders", name: "userId_1", keys: bson.D{{Key: "userId", Value: 1}}},

		// 内容域高频查询索引（公开列表/搜索/审核/创作中心）。
		{coll: "episodes", name: "reviewStatus_1_updatedAt_-1", keys: bson.D{{Key: "reviewStatus", Value: 1}, {Key: "updatedAt", Value: -1}}},
		{coll: "episodes", name: "reviewStatus_1_views_-1", keys: bson.D{{Key: "reviewStatus", Value: 1}, {Key: "views", Value: -1}}},
		{coll: "episodes", name: "reviewStatus_1_createdAt_-1", keys: bson.D{{Key: "reviewStatus", Value: 1}, {Key: "createdAt", Value: -1}}},
		{coll: "episodes", name: "createdBy_1", keys: bson.D{{Key: "createdBy", Value: 1}}},
		{coll: "episodes", name: "allowedEditors_1", keys: bson.D{{Key: "allowedEditors", Value: 1}}},
		{coll: "episodes", name: "customAuthors_1", keys: bson.D{{Key: "customAuthors", Value: 1}}},
		{coll: "episodes", name: "tags_1", keys: bson.D{{Key: "tags", Value: 1}}},
		{coll: "episodes", name: "category_1", keys: bson.D{{Key: "category", Value: 1}}},
		{coll: "episodes", name: "premiereDate_1", keys: bson.D{{Key: "premiereDate", Value: 1}}},

		{coll: "series", name: "createdBy_1", keys: bson.D{{Key: "createdBy", Value: 1}}},
		{coll: "series", name: "createdAt_-1", keys: bson.D{{Key: "createdAt", Value: -1}}},

		{coll: "themes", name: "ownerId_1", keys: bson.D{{Key: "ownerId", Value: 1}}},
		{coll: "themes", name: "isSystem_1_enabled_1", keys: bson.D{{Key: "isSystem", Value: 1}, {Key: "enabled", Value: 1}}},
		{coll: "themes", name: "status_1", keys: bson.D{{Key: "status", Value: 1}}},
		{coll: "themes", name: "isDefault_1", keys: bson.D{{Key: "isDefault", Value: 1}}},

		{coll: "notifications", name: "userId_1_createdAt_-1", keys: bson.D{{Key: "userId", Value: 1}, {Key: "createdAt", Value: -1}}},
		{coll: "reports", name: "reporterId_1_status_1", keys: bson.D{{Key: "reporterId", Value: 1}, {Key: "status", Value: 1}}},
		{coll: "reports", name: "status_1_createdAt_-1", keys: bson.D{{Key: "status", Value: 1}, {Key: "createdAt", Value: -1}}},
	}

	var errs []error
	for _, spec := range specs {
		if err := ensureIndex(ctx, db.Collection(spec.coll), spec); err != nil {
			// 单个索引失败不阻断其余索引创建（常见于历史重复数据）。
			errs = append(errs, fmt.Errorf("indexes.Ensure: %s.%s: %w", spec.coll, spec.name, err))
		}
	}
	return errors.Join(errs...)
}

// ensureIndex 幂等建索引：按名字查重，存在跳过；创建冲突（同名不同键/选项）容错。
func ensureIndex(ctx context.Context, coll *mongo.Collection, spec indexSpec) error {
	if exists, err := indexByName(ctx, coll, spec.name); err != nil {
		return err
	} else if exists {
		return nil
	}
	_, err := coll.Indexes().CreateOne(ctx, mongo.IndexModel{Keys: spec.keys, Options: spec.options()})
	if err != nil {
		// 并发创建或同名但键/选项冲突：视为已建立（already exists 容错）
		if strings.Contains(err.Error(), "already exists") {
			return nil
		}
		return err
	}
	return nil
}

// indexByName 判断集合是否已存在指定名字的索引（含 _id 之外的自动/手工索引）。
func indexByName(ctx context.Context, coll *mongo.Collection, name string) (bool, error) {
	cur, err := coll.Indexes().List(ctx)
	if err != nil {
		return false, err
	}
	defer cur.Close(ctx)
	for cur.Next(ctx) {
		var idx struct {
			Name string `bson:"name"`
		}
		if err := cur.Decode(&idx); err != nil {
			return false, err
		}
		if idx.Name == name {
			return true, nil
		}
	}
	return false, cur.Err()
}
