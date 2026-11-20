// Package migrate 实现启动时执行的一次性数据迁移（accountId/role/creatorProfile 回填）。
//
// 每个迁移由 settings 集合的标记（value: {done:true}）控制，幂等可重入：
//   - accountId_migration_v1：为缺少 accountId 的旧用户按规则回填
//   - role_migration_v1：adminAccess 布尔字段 → role 字符串角色
//   - creatorProfile_migration_v1：CreatorProfile.adminId 字段重命名为 creatorId，
//     并为 creator/admin/superadmin 角色补建创作者主页
//   - email_unique_sparse_v1：users.email_1 唯一索引 → unique+sparse（email 成为
//     可选字段），并清理存量 email 空串/null
//   - fieldcrypto_gcm_v1：存量 AES-CBC（enc:）字段重加密为 AES-GCM（enc2:），
//     完成后运行时不再支持 CBC（ADR-025）
package migrate

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/Furry-Drama-Tracker/backend/internal/auth"
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
	keyEmailUniqueSparse       = "email_unique_sparse_v1"
	keyFieldCryptoGCM          = "fieldcrypto_gcm_v1"
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
// keyHex 为字段加密 key（auth.FieldKey 派生），供 fieldcrypto_gcm_v1 重加密使用。
func Run(ctx context.Context, db *mongo.Database, repos *repository.Repos, keyHex string) error {
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
	if err := migrateEmailUniqueSparse(ctx, db, repos); err != nil {
		return fmt.Errorf("migrate email unique sparse: %w", err)
	}
	if err := migrateFieldCryptoGCM(ctx, db, repos, keyHex); err != nil {
		return fmt.Errorf("migrate fieldcrypto gcm: %w", err)
	}
	return nil
}

// migrateFieldCryptoGCM 把存量 AES-CBC（enc:）字段一次性重加密为 AES-GCM（enc2:）。
// 覆盖 users.twoFactorSecret 与 users.twoFactorBackupCodes 数组中的 enc: 条目。
// 解密失败（key 不符/密文损坏）返回错误中断启动——这些凭据运行时已不可解
// （fieldcrypto 不再支持 CBC），静默跳过只会把故障推迟到 2FA 使用时刻。
// 幂等：enc2:/bc:/无前缀条目原样保留；崩溃后重跑只处理剩余 enc: 文档。
func migrateFieldCryptoGCM(ctx context.Context, db *mongo.Database, repos *repository.Repos, keyHex string) error {
	done, err := repos.Settings.GetFlag(ctx, keyFieldCryptoGCM)
	if err != nil {
		return err
	}
	if done {
		return nil
	}

	type fcUser struct {
		ID              primitive.ObjectID `bson:"_id"`
		TwoFactorSecret string             `bson:"twoFactorSecret"`
		BackupCodes     []string           `bson:"twoFactorBackupCodes"`
	}
	cur, err := db.Collection("users").Find(ctx, bson.M{"$or": bson.A{
		bson.M{"twoFactorSecret": bson.M{"$regex": "^enc:"}},
		bson.M{"twoFactorBackupCodes": bson.M{"$regex": "^enc:"}},
	}})
	if err != nil {
		return err
	}
	defer cur.Close(ctx)

	reencrypted := 0
	for cur.Next(ctx) {
		var u fcUser
		if err := cur.Decode(&u); err != nil {
			return err
		}
		set := bson.M{}
		if strings.HasPrefix(u.TwoFactorSecret, "enc:") {
			plain, err := decryptLegacyCBC(u.TwoFactorSecret, keyHex)
			if err != nil {
				return fmt.Errorf("user %s twoFactorSecret: %w", u.ID.Hex(), err)
			}
			enc, err := auth.EncryptField(plain, keyHex)
			if err != nil {
				return fmt.Errorf("user %s twoFactorSecret re-encrypt: %w", u.ID.Hex(), err)
			}
			set["twoFactorSecret"] = enc
		}
		codesChanged := false
		for i, raw := range u.BackupCodes {
			if !strings.HasPrefix(raw, "enc:") {
				continue
			}
			plain, err := decryptLegacyCBC(raw, keyHex)
			if err != nil {
				return fmt.Errorf("user %s backupCodes[%d]: %w", u.ID.Hex(), i, err)
			}
			enc, err := auth.EncryptField(plain, keyHex)
			if err != nil {
				return fmt.Errorf("user %s backupCodes[%d] re-encrypt: %w", u.ID.Hex(), i, err)
			}
			if u.BackupCodes == nil {
				u.BackupCodes = []string{}
			}
			u.BackupCodes[i] = enc
			codesChanged = true
		}
		if codesChanged {
			set["twoFactorBackupCodes"] = u.BackupCodes
		}
		if len(set) == 0 {
			continue
		}
		if _, err := db.Collection("users").UpdateOne(ctx, bson.M{"_id": u.ID},
			bson.M{"$set": set}); err != nil {
			return err
		}
		reencrypted++
	}
	if err := cur.Err(); err != nil {
		return err
	}
	if reencrypted > 0 {
		slog.Info("[Migrate] fieldcrypto gcm re-encrypted", "users", reencrypted)
	}
	return repos.Settings.SetFlag(ctx, keyFieldCryptoGCM, bson.M{"done": true})
}

// decryptLegacyCBC 解密存量 "enc:<iv hex>:<ct hex>"（AES-256-CBC + PKCS7）。
// 仅本迁移使用——fieldcrypto 运行时已不再支持 CBC。
func decryptLegacyCBC(text, keyHex string) (string, error) {
	key, err := hex.DecodeString(keyHex)
	if err != nil || len(key) != 32 {
		return "", errors.New("fieldcrypto legacy: key must be 32 bytes hex")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	parts := strings.SplitN(strings.TrimPrefix(text, "enc:"), ":", 2)
	if len(parts) != 2 {
		return "", errors.New("fieldcrypto legacy: malformed payload")
	}
	iv, err1 := hex.DecodeString(parts[0])
	ct, err2 := hex.DecodeString(parts[1])
	if err1 != nil || err2 != nil || len(iv) != aes.BlockSize || len(ct) == 0 || len(ct)%aes.BlockSize != 0 {
		return "", errors.New("fieldcrypto legacy: malformed payload")
	}
	plain := make([]byte, len(ct))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plain, ct)
	if padLen := int(plain[len(plain)-1]); padLen == 0 || padLen > aes.BlockSize || padLen > len(plain) {
		return "", errors.New("fieldcrypto legacy: invalid padding")
	} else {
		for _, b := range plain[len(plain)-padLen:] {
			if int(b) != padLen {
				return "", errors.New("fieldcrypto legacy: invalid padding byte")
			}
		}
		plain = plain[:len(plain)-padLen]
	}
	return string(plain), nil
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

// migrateEmailUniqueSparse 把 users.email_1 唯一索引迁移为 unique+sparse
// （email 成为可选字段：缺 email 字段的用户文档不参与唯一性，无邮箱用户可共存）。
//
//  1. email_1 已存在且不带 sparse → drop。同名索引改选项必须先删除；启动顺序
//     migrate 先于 indexes.Ensure（cmd/server），删除后由 Ensure 按新 spec 重建。
//  2. $unset 存量 email 为空串/null 的字段：稀疏索引只放过"缺字段"的文档，
//     空串是"字段存在"仍占唯一性槽（多个无邮箱用户会撞唯一约束），必须清成缺字段。
//
// 幂等可重入：已 sparse 的索引不 drop；$unset 对缺字段是 no-op。
func migrateEmailUniqueSparse(ctx context.Context, db *mongo.Database, repos *repository.Repos) error {
	done, err := repos.Settings.GetFlag(ctx, keyEmailUniqueSparse)
	if err != nil {
		return err
	}
	if done {
		return nil
	}

	users := db.Collection("users")

	// 检查 email_1 现存选项：仅当存在且非 sparse 时才需要 drop（sparse 的选项
	// 缺省不出现在索引文档里，按 bool 零值判定即可）。
	cur, err := users.Indexes().List(ctx)
	if err != nil {
		return err
	}
	found, sparse := false, false
	for cur.Next(ctx) {
		var idx struct {
			Name   string `bson:"name"`
			Sparse bool   `bson:"sparse"`
		}
		if err := cur.Decode(&idx); err != nil {
			cur.Close(ctx)
			return err
		}
		if idx.Name == "email_1" {
			found, sparse = true, idx.Sparse
		}
	}
	if err := cur.Err(); err != nil {
		cur.Close(ctx)
		return err
	}
	cur.Close(ctx)
	if found && !sparse {
		if _, err := users.Indexes().DropOne(ctx, "email_1"); err != nil {
			return err
		}
		slog.Info("已删除旧 users.email_1 唯一索引（indexes.Ensure 将以 unique+sparse 重建）")
	}

	// 空串/null 统一清成缺字段（$in 的 null 同时匹配缺字段文档，$unset 对其是 no-op）。
	res, err := users.UpdateMany(ctx,
		bson.M{"email": bson.M{"$in": []any{"", nil}}},
		bson.M{"$unset": bson.M{"email": ""}})
	if err != nil {
		return err
	}
	if res.ModifiedCount > 0 {
		slog.Info("已清理存量空邮箱字段", "count", res.ModifiedCount)
	}
	return repos.Settings.SetFlag(ctx, keyEmailUniqueSparse, bson.M{"done": true})
}
