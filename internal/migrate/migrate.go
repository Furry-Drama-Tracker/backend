// Package migrate 实现启动时执行的一次性数据迁移（accountId/role/creatorProfile 回填）。
//
// 每个迁移由 settings 集合的标记（value: {done:true}）控制，幂等可重入：
//   - accountId_migration_v1：为缺少 accountId 的旧用户按规则回填
//   - role_migration_v1：adminAccess 布尔字段 → role 字符串角色
//   - creatorProfile_migration_v1：CreatorProfile.adminId 字段重命名为 creatorId，
//     并为 creator/admin/superadmin 角色补建创作者主页
package migrate

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/Furry-Drama-Tracker/backend/internal/model"
	"github.com/Furry-Drama-Tracker/backend/internal/repository"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// 迁移标记键（settings 集合）。
const (
	keyAccountIDMigration      = "accountId_migration_v1"
	keyRoleMigration           = "role_migration_v1"
	keyCreatorProfileMigration = "creatorProfile_migration_v1"
	keyUIDBackfillMigration    = "uid_backfill_v1"
	keyLoginNameLowerMigration = "loginname_lower_backfill_v1"
)

// nonWordRegex 对应 JS 的 /[^\w]/g（\w = [A-Za-z0-9_]）。
var nonWordRegex = regexp.MustCompile(`[^\w]`)

// generateAccountID 由 username 生成 accountId（与既有 accountId 数据形态兼容）：
// username.replace(/[^\w]/g, '_').toLowerCase()。
func generateAccountID(username string) string {
	return strings.ToLower(nonWordRegex.ReplaceAllString(username, "_"))
}

// Run 执行全部启动迁移（幂等）。任一步失败即返回错误并停止后续迁移，
// 由调用方（cmd/server 启动流程）决定是否中断进程。
func Run(ctx context.Context, db *mongo.Database, repos *repository.Repos) error {
	if err := migrateAccountID(ctx, repos); err != nil {
		return fmt.Errorf("migrate accountId: %w", err)
	}
	if err := migrateRole(ctx, db, repos); err != nil {
		return fmt.Errorf("migrate role: %w", err)
	}
	if err := migrateCreatorProfile(ctx, db, repos); err != nil {
		return fmt.Errorf("migrate creatorProfile: %w", err)
	}
	if err := migrateUIDBackfill(ctx, db, repos); err != nil {
		return fmt.Errorf("migrate uid backfill: %w", err)
	}
	if err := migrateLoginNameLowerBackfill(ctx, db, repos); err != nil {
		return fmt.Errorf("migrate loginname lower backfill: %w", err)
	}
	return nil
}

// migrateLoginNameLowerBackfill 为存量用户回填 accountIdLower（折叠小写）。
// 背景：大小写策略为 GitHub 式——存储保留原样，登录/唯一性按折叠判定
// （accountIdLower_1 唯一索引）。存量 accountId 含大写时保留原样不改写；
// 仅当两个只差大小写的变体（"Alice" 与 "alice"）折叠后冲突时，按 _id 序
// 给后出现的变体追加 _N 后缀改名（先出现者保留原样）。
func migrateLoginNameLowerBackfill(ctx context.Context, db *mongo.Database, repos *repository.Repos) error {
	done, err := repos.Settings.GetFlag(ctx, keyLoginNameLowerMigration)
	if err != nil {
		return err
	}
	if done {
		return nil
	}

	type nameUser struct {
		ID        primitive.ObjectID `bson:"_id"`
		AccountID string             `bson:"accountId"`
	}
	var users []nameUser
	cur, err := db.Collection("users").Find(ctx, bson.M{"accountId": bson.M{"$exists": true}},
		options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}).
			SetProjection(bson.M{"_id": 1, "accountId": 1}))
	if err != nil {
		return err
	}
	if err := cur.All(ctx, &users); err != nil {
		cur.Close(ctx)
		return err
	}
	cur.Close(ctx)

	seenLower := make(map[string]bool, len(users))
	renamed, backfilled := 0, 0
	for _, u := range users {
		lower := strings.ToLower(u.AccountID)
		if !seenLower[lower] {
			// 无冲突：只回填折叠字段，accountId 原样保留。
			seenLower[lower] = true
			if _, err := db.Collection("users").UpdateOne(ctx, bson.M{"_id": u.ID},
				bson.M{"$set": bson.M{"accountIdLower": lower}}); err != nil {
				return err
			}
			backfilled++
			continue
		}
		// 折叠冲突（大小写变体）：给后出现者追加 _N 改名（保留先出现者原样）。
		newName := ""
		for i := 1; ; i++ {
			candidate := fmt.Sprintf("%s_%d", lower, i)
			exists, gerr := repos.Users.AccountIDExistsExcluding(ctx, candidate, u.ID)
			if gerr != nil {
				return gerr
			}
			if !exists {
				newName = candidate
				break
			}
		}
		if err := repos.Users.UpdateAccountID(ctx, u.ID, newName); err != nil {
			return err
		}
		seenLower[strings.ToLower(newName)] = true
		renamed++
	}
	if renamed > 0 || backfilled > 0 {
		slog.Info("已为存量用户回填用户名折叠字段", "backfilled", backfilled, "renamedConflicts", renamed)
	}
	return repos.Settings.SetFlag(ctx, keyLoginNameLowerMigration, bson.M{"done": true})
}

// uidBackfillUser 是 uid 回填所需的用户投影。
type uidBackfillUser struct {
	ID        primitive.ObjectID `bson:"_id"`
	UID       int64              `bson:"uid"`
	CreatedAt time.Time          `bson:"createdAt"`
}

// migrateUIDBackfill 为缺 uid 的用户按 createdAt 升序派发连续 UID（0..N）。
// 首个用户自然得 0（仅表示注册顺序，无角色语义）；已有 uid 的用户不动。
// 结束后把 counters.uid 计数器推进到已发最大号，保证后续注册发号不冲突。
func migrateUIDBackfill(ctx context.Context, db *mongo.Database, repos *repository.Repos) error {
	done, err := repos.Settings.GetFlag(ctx, keyUIDBackfillMigration)
	if err != nil {
		return err
	}
	if done {
		return nil
	}

	users := db.Collection("users")
	var pending []uidBackfillUser
	cur, err := users.Find(ctx,
		bson.M{"uid": bson.M{"$exists": false}},
		options.Find().SetSort(bson.D{{Key: "createdAt", Value: 1}, {Key: "_id", Value: 1}}).
			SetProjection(bson.M{"_id": 1, "uid": 1, "createdAt": 1}))
	if err != nil {
		return err
	}
	if err := cur.All(ctx, &pending); err != nil {
		cur.Close(ctx)
		return err
	}
	cur.Close(ctx)

	if len(pending) > 0 {
		// 起点取全库已存在 uid 的最大值（-1 使无 uid 时从 0 开始）。
		maxDoc := users.FindOne(ctx, bson.M{"uid": bson.M{"$exists": true}},
			options.FindOne().SetSort(bson.M{"uid": -1}).SetProjection(bson.M{"uid": 1}))
		var top struct {
			UID int64 `bson:"uid"`
		}
		maxUID := int64(-1)
		if err := maxDoc.Decode(&top); err == nil {
			maxUID = top.UID
		} else if !errors.Is(err, mongo.ErrNoDocuments) {
			return err
		}

		next := maxUID + 1
		for _, u := range pending {
			if _, err := users.UpdateOne(ctx, bson.M{"_id": u.ID, "uid": bson.M{"$exists": false}},
				bson.M{"$set": bson.M{"uid": next}}); err != nil {
				return err
			}
			next++
		}
		if err := repos.Counters.SetUIDCounter(ctx, next-1); err != nil {
			return err
		}
		slog.Info("已为存量用户派发 UID", "count", len(pending), "max", next-1)
	}
	return repos.Settings.SetFlag(ctx, keyUIDBackfillMigration, bson.M{"done": true})
}

// migrateAccountID 回填缺 accountId 的用户。
// 生成规则：baseId = username.replace(/[^\w]/g,'_').toLowerCase()；
// 若与其它用户（排除自身）冲突则追加 _N 后缀（N 从 1 递增）。
func migrateAccountID(ctx context.Context, repos *repository.Repos) error {
	done, err := repos.Settings.GetFlag(ctx, keyAccountIDMigration)
	if err != nil {
		return err
	}
	if done {
		return nil
	}

	users, err := repos.Users.FindMissingAccountID(ctx)
	if err != nil {
		return err
	}
	backfilled := 0
	for _, u := range users {
		base := generateAccountID(u.Username)
		accountID := base
		counter := 1
		for {
			exists, err := repos.Users.AccountIDExistsExcluding(ctx, accountID, u.ID)
			if err != nil {
				return err
			}
			if !exists {
				break
			}
			accountID = fmt.Sprintf("%s_%d", base, counter)
			counter++
		}
		if err := repos.Users.UpdateAccountID(ctx, u.ID, accountID); err != nil {
			return err
		}
		backfilled++
	}
	if backfilled > 0 {
		slog.Info("已为旧用户自动生成账号ID", "count", backfilled)
	}
	return repos.Settings.SetFlag(ctx, keyAccountIDMigration, bson.M{"done": true})
}

// migrateRole 迁移 adminAccess 布尔字段 → role 字符串角色。
// 对 adminAccess=true 且 role 为 user/空/缺失（排除 superadmin）的用户置 role='admin'，
// 随后清除所有用户的 adminAccess 字段（先逐文档判断更新 role，再 updateMany $unset）。
func migrateRole(ctx context.Context, db *mongo.Database, repos *repository.Repos) error {
	done, err := repos.Settings.GetFlag(ctx, keyRoleMigration)
	if err != nil {
		return err
	}
	if done {
		return nil
	}

	users := db.Collection("users")
	// adminAccess:true 且 role 非 superadmin、逐文档 (role==='user' || !role) 成立
	res, err := users.UpdateMany(ctx, bson.M{
		"adminAccess": true,
		"$or": []bson.M{
			{"role": "user"},
			{"role": bson.M{"$in": []any{nil, ""}}},
		},
	}, bson.M{"$set": bson.M{"role": "admin"}})
	if err != nil {
		return err
	}
	if res.ModifiedCount > 0 {
		slog.Info("已将 adminAccess 用户迁移为 admin 角色", "count", res.ModifiedCount)
	}

	// 清除所有用户的 adminAccess 字段
	if _, err := users.UpdateMany(ctx, bson.M{}, bson.M{"$unset": bson.M{"adminAccess": ""}}); err != nil {
		return err
	}
	return repos.Settings.SetFlag(ctx, keyRoleMigration, bson.M{"done": true})
}

// roleUser 创作者主页迁移需要的用户投影。
type roleUser struct {
	ID       primitive.ObjectID `bson:"_id"`
	Username string             `bson:"username"`
	Role     string             `bson:"role"`
}

// migrateCreatorProfile 迁移 CreatorProfile：
//  1. 删除旧的 adminId_1 唯一索引（$rename 前必须移除，避免 null 冲突）；
//  2. 把存在 adminId 字段的文档 $rename 为 creatorId；
//  3. 为 creator/admin/superadmin 角色但无 CreatorProfile 的用户补建初始主页（幂等）。
func migrateCreatorProfile(ctx context.Context, db *mongo.Database, repos *repository.Repos) error {
	done, err := repos.Settings.GetFlag(ctx, keyCreatorProfileMigration)
	if err != nil {
		return err
	}
	if done {
		return nil
	}

	profiles := db.Collection("creatorprofiles")

	// 旧索引可能不存在，忽略删除错误
	_, _ = profiles.Indexes().DropOne(ctx, "adminId_1")

	oldCount, err := profiles.CountDocuments(ctx, bson.M{"adminId": bson.M{"$exists": true}})
	if err != nil {
		return err
	}
	if oldCount > 0 {
		if _, err := profiles.UpdateMany(ctx,
			bson.M{"adminId": bson.M{"$exists": true}},
			bson.M{"$rename": bson.M{"adminId": "creatorId"}}); err != nil {
			return err
		}
		slog.Info("已将 CreatorProfile 的 adminId 字段迁移为 creatorId", "count", oldCount)
	}

	var users []roleUser
	cur, err := db.Collection("users").Find(ctx,
		bson.M{"role": bson.M{"$in": []string{"creator", "admin", "superadmin"}}},
		options.Find().SetProjection(bson.M{"_id": 1, "username": 1, "role": 1}))
	if err != nil {
		return err
	}
	if err := cur.All(ctx, &users); err != nil {
		cur.Close(ctx)
		return err
	}
	cur.Close(ctx)

	created := 0
	for _, u := range users {
		existing, err := repos.CreatorProfiles.FindByCreator(ctx, u.ID)
		if err != nil {
			if repository.IsNotFound(err) {
				existing = nil
			} else {
				return err
			}
		}
		if existing != nil {
			continue
		}

		displayName := u.Username
		if displayName == "" {
			displayName = "创作者"
		}
		bio := "这位创作者还没有填写个人简介。"
		if u.Role == "superadmin" {
			bio = "站点管理员，负责内容审核与平台运营。"
		}
		p := &model.CreatorProfile{
			CreatorID:   u.ID,
			DisplayName: displayName,
			Bio:         bio,
			SocialLinks: map[string]string{},
		}
		if err := repos.CreatorProfiles.Create(ctx, p); err != nil {
			// 并发/重复创建（duplicate key 11000）则跳过（幂等）
			if repository.IsDuplicateKey(err) {
				continue
			}
			return err
		}
		created++
	}
	if created > 0 {
		slog.Info("已为角色用户补建创作者主页", "count", created)
	}
	return repos.Settings.SetFlag(ctx, keyCreatorProfileMigration, bson.M{"done": true})
}
