package migrate

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Furry-Drama-Tracker/backend/internal/auth"
	"github.com/Furry-Drama-Tracker/backend/internal/indexes"
	"github.com/Furry-Drama-Tracker/backend/internal/repository"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const testMongoURI = "mongodb://127.0.0.1:27017"

// fieldcrypto_gcm_v1 测试用 key（32 字节 hex）。
const (
	testFieldKeyAB = "abababababababababababababababababababababababababababababababab"
	testFieldKeyCD = "cdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcd"
)

// testDB 连接本地 mongod 并创建/清理独立的测试库。
func testDB(t *testing.T) (*mongo.Database, func()) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(testMongoURI))
	if err != nil {
		t.Fatalf("connect mongod: %v", err)
	}
	if err := client.Ping(ctx, nil); err != nil {
		client.Disconnect(context.Background())
		t.Fatalf("mongod unreachable at %s: %v", testMongoURI, err)
	}
	db := client.Database("neo_migrate_test")
	_ = db.Drop(context.Background())
	cleanup := func() {
		_ = db.Drop(context.Background())
		_ = client.Disconnect(context.Background())
	}
	return db, cleanup
}

// mustInsert 插入单个 bson 文档，失败即终止测试。
func mustInsert(t *testing.T, coll *mongo.Collection, doc any) {
	t.Helper()
	if _, err := coll.InsertOne(context.Background(), doc); err != nil {
		t.Fatalf("insert %v: %v", doc, err)
	}
}

// TestMigrateAccountIDBackfill 验证 accountId 回填：
// 非单词字符转下划线、小写、与已有 accountId 冲突时追加 _N 后缀、已有值保持不变、标记写入。
func TestMigrateAccountIDBackfill(t *testing.T) {
	db, cleanup := testDB(t)
	defer cleanup()
	ctx := context.Background()
	users := db.Collection("users")

	// 已有 accountId 的旧用户（制造 "bob" 冲突）
	mustInsert(t, users, bson.M{"username": "Bob Old", "email": "bobold@example.com", "accountId": "bob"})

	// 缺 accountId 的用户
	cases := []struct {
		username string
		email    string
		want     string
	}{
		{"Alice Smith", "alice@example.com", "alice_smith"},
		{"Bob", "bob@example.com", "bob_1"},        // 与已有 "bob" 冲突 → _1
		{"User_Two", "ut@example.com", "user_two"}, // 下划线是单词字符，原样保留
		{"王小明", "wang@example.com", "___"},         // CJK 均非单词字符 → 逐个转 _
	}
	for _, c := range cases {
		mustInsert(t, users, bson.M{"username": c.username, "email": c.email})
	}

	repos := repository.NewRepos(db, 5, 30)
	if err := migrateAccountID(ctx, repos); err != nil {
		t.Fatalf("migrateAccountID: %v", err)
	}

	for _, c := range cases {
		var doc bson.M
		if err := users.FindOne(ctx, bson.M{"email": c.email}).Decode(&doc); err != nil {
			t.Fatalf("find %s: %v", c.email, err)
		}
		if got, _ := doc["accountId"].(string); got != c.want {
			t.Errorf("user %s: accountId = %q, want %q", c.email, got, c.want)
		}
	}

	// 已有 accountId 的用户不变，且未被打扰
	var old bson.M
	if err := users.FindOne(ctx, bson.M{"email": "bobold@example.com"}).Decode(&old); err != nil {
		t.Fatalf("find bobold: %v", err)
	}
	if got, _ := old["accountId"].(string); got != "bob" {
		t.Errorf("existing user accountId changed to %q, want bob", got)
	}
	if got, _ := old["username"].(string); got != "Bob Old" {
		t.Errorf("existing user username changed to %q, want Bob Old", got)
	}

	// 迁移标记已写入且为 {done:true}
	done, err := repos.Settings.GetFlag(ctx, keyAccountIDMigration)
	if err != nil {
		t.Fatalf("GetFlag: %v", err)
	}
	if !done {
		t.Fatal("accountId migration flag not set")
	}

	// 幂等：再次运行不报错、不改动
	if err := migrateAccountID(ctx, repos); err != nil {
		t.Fatalf("migrateAccountID (2nd run): %v", err)
	}
	var again bson.M
	if err := users.FindOne(ctx, bson.M{"email": "bob@example.com"}).Decode(&again); err != nil {
		t.Fatalf("find bob after 2nd run: %v", err)
	}
	if got, _ := again["accountId"].(string); got != "bob_1" {
		t.Errorf("2nd run changed accountId to %q, want bob_1", got)
	}
}

// TestMigrateRunRoleAndCreatorProfile 验证 Run 的 role 迁移与 CreatorProfile 迁移：
// adminAccess→role、字段 $rename、角色用户补建主页、settings 三标记、全程幂等。
func TestMigrateRunRoleAndCreatorProfile(t *testing.T) {
	db, cleanup := testDB(t)
	defer cleanup()
	ctx := context.Background()
	users := db.Collection("users")

	u1 := primitive.NewObjectID() // user + adminAccess → admin
	u2 := primitive.NewObjectID() // creator + adminAccess → 保持 creator
	u3 := primitive.NewObjectID() // superadmin → 保持
	u4 := primitive.NewObjectID() // 普通 user → 不变
	for _, doc := range []any{
		bson.M{"_id": u1, "accountId": "alice", "username": "alice", "email": "alice@example.com", "role": "user", "adminAccess": true},
		bson.M{"_id": u2, "accountId": "creator", "username": "creator", "email": "creator@example.com", "role": "creator", "adminAccess": true},
		bson.M{"_id": u3, "accountId": "sa", "username": "sa", "email": "sa@example.com", "role": "superadmin"},
		bson.M{"_id": u4, "accountId": "user", "username": "user", "email": "user@example.com", "role": "user"},
	} {
		mustInsert(t, users, doc)
	}

	// 旧 CreatorProfile：adminId 字段 → 迁移后应为 creatorId
	oldProfileID := primitive.NewObjectID()
	oldCreator := primitive.NewObjectID()
	profiles := db.Collection("creatorprofiles")
	mustInsert(t, profiles, bson.M{"_id": oldProfileID, "adminId": oldCreator, "displayName": "Old"})

	repos := repository.NewRepos(db, 5, 30)
	// 测试 key：32 字节 hex（值无关紧要，fieldcrypto_gcm_v1 在无 enc: 数据时为空跑）。
	if err := Run(ctx, db, repos, "ababababababababababababababababababababababababababababababababab"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// role 迁移断言
	roleOf := func(id primitive.ObjectID) string {
		var doc bson.M
		if err := users.FindOne(ctx, bson.M{"_id": id}).Decode(&doc); err != nil {
			t.Fatalf("find user: %v", err)
		}
		return doc["role"].(string)
	}
	if got := roleOf(u1); got != "admin" {
		t.Errorf("u1 role = %q, want admin", got)
	}
	if got := roleOf(u2); got != "creator" {
		t.Errorf("u2 role = %q, want creator", got)
	}
	if got := roleOf(u3); got != "superadmin" {
		t.Errorf("u3 role = %q, want superadmin", got)
	}
	if got := roleOf(u4); got != "user" {
		t.Errorf("u4 role = %q, want user", got)
	}

	// adminAccess 字段应全部清除
	var cnt int64
	cnt, _ = users.CountDocuments(ctx, bson.M{"adminAccess": bson.M{"$exists": true}})
	if cnt != 0 {
		t.Errorf("adminAccess field still present on %d users", cnt)
	}

	// $rename 断言：旧 adminId 文档 → creatorId
	var renamed bson.M
	if err := profiles.FindOne(ctx, bson.M{"_id": oldProfileID}).Decode(&renamed); err != nil {
		t.Fatalf("find renamed profile: %v", err)
	}
	if _, has := renamed["adminId"]; has {
		t.Error("adminId field still present after rename")
	}
	if got, _ := renamed["creatorId"].(primitive.ObjectID); got != oldCreator {
		t.Errorf("renamed creatorId = %v, want %v", got, oldCreator)
	}

	// 角色用户补建主页：u1(admin)/u2(creator)/u3(superadmin) 有，u4(user) 无
	profileFor := func(creatorID primitive.ObjectID) *bson.M {
		var doc bson.M
		err := profiles.FindOne(ctx, bson.M{"creatorId": creatorID}).Decode(&doc)
		if err == mongo.ErrNoDocuments {
			return nil
		}
		if err != nil {
			t.Fatalf("find profile for %v: %v", creatorID, err)
		}
		return &doc
	}
	for _, id := range []primitive.ObjectID{u1, u2, u3} {
		if p := profileFor(id); p == nil {
			t.Errorf("expected CreatorProfile for role user %v", id)
		}
	}
	if p := profileFor(u4); p != nil {
		t.Error("unexpected CreatorProfile for plain user")
	}
	if p := profileFor(u3); p != nil {
		if bio, _ := (*p)["bio"].(string); bio != "站点管理员，负责内容审核与平台运营。" {
			t.Errorf("superadmin bio = %q", bio)
		}
	}
	if p := profileFor(u1); p != nil {
		if bio, _ := (*p)["bio"].(string); bio != "这位创作者还没有填写个人简介。" {
			t.Errorf("admin bio = %q", bio)
		}
	}

	// settings 三个标记全部写入
	for _, key := range []string{keyAccountIDMigration, keyRoleMigration, keyCreatorProfileMigration} {
		done, err := repos.Settings.GetFlag(ctx, key)
		if err != nil {
			t.Fatalf("GetFlag(%s): %v", key, err)
		}
		if !done {
			t.Errorf("migration flag %s not set", key)
		}
	}

	// 幂等：再次 Run 不报错、不产生重复主页
	if err := Run(ctx, db, repos, "abababababababababababababababababababababababababababababab"); err != nil {
		t.Fatalf("Run (2nd): %v", err)
	}
	profCount, _ := profiles.CountDocuments(ctx, bson.M{})
	if profCount != 4 { // 1 旧文档(已重命名) + 3 补建
		t.Errorf("profile count after 2nd run = %d, want 4", profCount)
	}
}

// TestGenerateAccountID 纯函数单测：JS /[^\w]/g 替换与 toLowerCase。
func TestGenerateAccountID(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Alice Smith", "alice_smith"},
		{"Bob", "bob"},
		{"User_Two", "user_two"},
		{"王小明", "___"},
		{"  Mixed--Chars  ", "__mixed__chars__"},
	}
	for _, c := range cases {
		if got := generateAccountID(c.in); got != c.want {
			t.Errorf("generateAccountID(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestMigrateLoginNameLowerBackfill 回填折叠小写字段（GitHub 式大小写策略）：
// 原样大小写保留、accountIdLower 回填、大小写变体冲突给后出现者加 _N、幂等。
func TestMigrateLoginNameLowerBackfill(t *testing.T) {
	db, cleanup := testDB(t)
	defer cleanup()
	ctx := context.Background()
	users := db.Collection("users")

	oidAlice := primitive.NewObjectID()
	oidBob := primitive.NewObjectID()
	mustInsert(t, users, bson.M{"_id": oidAlice, "username": "Alice", "email": "a@example.com", "accountId": "Alice"})
	mustInsert(t, users, bson.M{"_id": oidBob, "username": "ALICE2", "email": "b@example.com", "accountId": "ALICE"})
	mustInsert(t, users, bson.M{"_id": primitive.NewObjectID(), "username": "Lower", "email": "c@example.com", "accountId": "lowerok"})

	repos := repository.NewRepos(db, 5, 30)
	if err := migrateLoginNameLowerBackfill(ctx, db, repos); err != nil {
		t.Fatalf("migrateLoginNameLowerBackfill: %v", err)
	}

	var got struct {
		AccountID      string `bson:"accountId"`
		AccountIDLower string `bson:"accountIdLower"`
	}
	// 先出现的 Alice：accountId 原样保留，lower 回填 alice。
	if err := users.FindOne(ctx, bson.M{"_id": oidAlice}).Decode(&got); err != nil || got.AccountID != "Alice" || got.AccountIDLower != "alice" {
		t.Fatalf("Alice must keep case + get lower: %+v err=%v", got, err)
	}
	// 后出现的 ALICE（折叠冲突）：改名 alice_1。
	if err := users.FindOne(ctx, bson.M{"_id": oidBob}).Decode(&got); err != nil || got.AccountID != "alice_1" || got.AccountIDLower != "alice_1" {
		t.Fatalf("ALICE variant must be renamed alice_1: %+v err=%v", got, err)
	}
	// 无冲突者原样保留。
	if err := users.FindOne(ctx, bson.M{"_id": primitive.NewObjectID()}).Err(); err == nil {
		t.Fatal("unexpected user matched by empty id")
	}
	if err := users.FindOne(ctx, bson.M{"accountId": "lowerok"}).Err(); err != nil {
		t.Fatalf("lowerok must remain: %v", err)
	}

	// 幂等：二次执行不再改动。
	if err := migrateLoginNameLowerBackfill(ctx, db, repos); err != nil {
		t.Fatalf("second run: %v", err)
	}
	if err := users.FindOne(ctx, bson.M{"_id": oidAlice}).Decode(&got); err != nil || got.AccountID != "Alice" {
		t.Fatalf("second run must not change names: %+v", got)
	}
}

// emailIndex 查询 users.email_1 索引的存在性与选项（sparse 未设置时驱动解不出该键）。
func emailIndex(t *testing.T, users *mongo.Collection) (found, unique, sparse bool) {
	t.Helper()
	ctx := context.Background()
	cur, err := users.Indexes().List(ctx)
	if err != nil {
		t.Fatalf("list indexes: %v", err)
	}
	defer cur.Close(ctx)
	for cur.Next(ctx) {
		var idx bson.M
		if err := cur.Decode(&idx); err != nil {
			t.Fatalf("decode index: %v", err)
		}
		if name, _ := idx["name"].(string); name == "email_1" {
			u, _ := idx["unique"].(bool)
			s, _ := idx["sparse"].(bool)
			return true, u, s
		}
	}
	if err := cur.Err(); err != nil {
		t.Fatalf("list indexes: %v", err)
	}
	return false, false, false
}

// TestMigrateEmailUniqueSparse 验证 email 可选字段迁移：
// 旧非 sparse 唯一索引被删除并由 indexes.Ensure 重建为 unique+sparse、
// 存量 email:"" 清成缺字段、多个无邮箱用户可共存、真实邮箱唯一性保留、幂等。
func TestMigrateEmailUniqueSparse(t *testing.T) {
	db, cleanup := testDB(t)
	defer cleanup()
	ctx := context.Background()
	users := db.Collection("users")

	// 旧索引形态：unique 非 sparse（email 必填时代的产物）。
	if _, err := users.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "email", Value: 1}},
		Options: options.Index().SetName("email_1").SetUnique(true),
	}); err != nil {
		t.Fatalf("create legacy index: %v", err)
	}

	oidEmpty := primitive.NewObjectID()   // 存量 email:""
	oidMissing := primitive.NewObjectID() // 存量缺 email 字段
	mustInsert(t, users, bson.M{"_id": oidEmpty, "accountId": "empty", "email": ""})
	mustInsert(t, users, bson.M{"_id": oidMissing, "accountId": "missing"})
	mustInsert(t, users, bson.M{"_id": primitive.NewObjectID(), "accountId": "real", "email": "a@example.com"})

	repos := repository.NewRepos(db, 5, 30)
	if err := migrateEmailUniqueSparse(ctx, db, repos); err != nil {
		t.Fatalf("migrateEmailUniqueSparse: %v", err)
	}
	// 与生产启动顺序一致（cmd/server：migrate.Run → indexes.Ensure）。
	if err := indexes.Ensure(ctx, db); err != nil {
		t.Fatalf("indexes.Ensure: %v", err)
	}

	// 索引断言：email_1 重建为 unique+sparse。
	found, unique, sparse := emailIndex(t, users)
	if !found || !unique || !sparse {
		t.Fatalf("email_1 after migration: found=%v unique=%v sparse=%v, want found+unique+sparse", found, unique, sparse)
	}

	// 空串清理断言：email 字段缺省（而非 ""），缺字段文档不动。
	var doc bson.M
	if err := users.FindOne(ctx, bson.M{"_id": oidEmpty}).Decode(&doc); err != nil {
		t.Fatalf("find empty-email user: %v", err)
	}
	if v, has := doc["email"]; has {
		t.Errorf("legacy email:\"\" not cleared, still present: %v", v)
	}
	if err := users.FindOne(ctx, bson.M{"_id": oidMissing}).Decode(&doc); err != nil {
		t.Fatalf("find missing-email user: %v", err)
	}
	if _, has := doc["email"]; has {
		t.Error("missing-email user must stay field-less")
	}

	// 功能断言：多个无邮箱用户共存（旧索引下第二个即撞唯一约束）。
	for i := 0; i < 3; i++ {
		mustInsert(t, users, bson.M{"accountId": fmt.Sprintf("qq%d", i)})
	}
	// 真实邮箱唯一性保留。
	if _, err := users.InsertOne(ctx, bson.M{"accountId": "dup", "email": "a@example.com"}); err == nil {
		t.Fatal("duplicate email must still be rejected")
	}

	// 标记已写入，幂等：二次运行不报错。
	done, err := repos.Settings.GetFlag(ctx, keyEmailUniqueSparse)
	if err != nil || !done {
		t.Fatalf("migration flag: done=%v err=%v, want true", done, err)
	}
	if err := migrateEmailUniqueSparse(ctx, db, repos); err != nil {
		t.Fatalf("second run: %v", err)
	}
}

// TestMigrateEmailUniqueSparseNoDropWhenSparse 验证 email_1 已是 sparse 时迁移不 drop
// （迁移后未调用 Ensure，索引必须仍在），且迁移前无索引/缺字段文档均无副作用。
func TestMigrateEmailUniqueSparseNoDropWhenSparse(t *testing.T) {
	db, cleanup := testDB(t)
	defer cleanup()
	ctx := context.Background()
	users := db.Collection("users")

	// 新库直建（indexes.Ensure 新 spec：unique+sparse）。
	if err := indexes.Ensure(ctx, db); err != nil {
		t.Fatalf("indexes.Ensure: %v", err)
	}
	mustInsert(t, users, bson.M{"accountId": "noemail1"})
	mustInsert(t, users, bson.M{"accountId": "noemail2"})

	repos := repository.NewRepos(db, 5, 30)
	if err := migrateEmailUniqueSparse(ctx, db, repos); err != nil {
		t.Fatalf("migrateEmailUniqueSparse: %v", err)
	}

	// 已 sparse 的索引不得被 drop（此处刻意不调用 Ensure：若被误删会立即暴露）。
	found, unique, sparse := emailIndex(t, users)
	if !found || !unique || !sparse {
		t.Fatalf("email_1 must survive migration: found=%v unique=%v sparse=%v", found, unique, sparse)
	}
	// 无邮箱文档不受影响。
	cnt, err := users.CountDocuments(ctx, bson.M{"email": bson.M{"$exists": false}})
	if err != nil || cnt != 2 {
		t.Fatalf("no-email docs = %d (err=%v), want 2", cnt, err)
	}

	done, err := repos.Settings.GetFlag(ctx, keyEmailUniqueSparse)
	if err != nil || !done {
		t.Fatalf("migration flag: done=%v err=%v, want true", done, err)
	}
}

// legacyCBCEncrypt 用旧版 CBC 算法构造 "enc:<iv hex>:<ct hex>" 测试夹具（PKCS7 填充）。
func legacyCBCEncrypt(t *testing.T, keyHex, plain string) string {
	t.Helper()
	key, err := hex.DecodeString(keyHex)
	if err != nil || len(key) != 32 {
		t.Fatalf("bad test key: %v", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	iv := make([]byte, aes.BlockSize)
	if _, err := rand.Read(iv); err != nil {
		t.Fatalf("rand: %v", err)
	}
	padded := []byte(plain)
	padLen := aes.BlockSize - len(padded)%aes.BlockSize
	for i := 0; i < padLen; i++ {
		padded = append(padded, byte(padLen))
	}
	ct := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(ct, padded)
	return "enc:" + hex.EncodeToString(iv) + ":" + hex.EncodeToString(ct)
}

// TestMigrateFieldCryptoGCM 验证存量 enc:（CBC）一次性重加密为 enc2:（GCM）：
// secret 与备份码数组条目均转换、enc2:/bc:/明文条目原样保留、幂等重跑、标记写入、
// 解密坏数据时启动失败。
func TestMigrateFieldCryptoGCM(t *testing.T) {
	keyHex := testFieldKeyAB
	db, cleanup := testDB(t)
	defer cleanup()
	ctx := context.Background()
	repos := repository.NewRepos(db, 5, 30)
	users := db.Collection("users")

	secretPlain := "totp-secret-plain"
	encSecret := legacyCBCEncrypt(t, keyHex, secretPlain)
	keptGCM, err := auth.EncryptField("already-gcm", keyHex)
	if err != nil {
		t.Fatalf("EncryptField: %v", err)
	}
	uLegacy := primitive.NewObjectID()
	uGCM := primitive.NewObjectID()
	uPlain := primitive.NewObjectID()
	mustInsert(t, users, bson.M{"_id": uLegacy,
		"twoFactorSecret":      encSecret,
		"twoFactorBackupCodes": []string{"bc:hashed", legacyCBCEncrypt(t, keyHex, "old1"), "old-plaintext"}})
	mustInsert(t, users, bson.M{"_id": uGCM, "twoFactorSecret": keptGCM})
	mustInsert(t, users, bson.M{"_id": uPlain, "twoFactorSecret": "legacy-plaintext"})

	if err := migrateFieldCryptoGCM(ctx, db, repos, keyHex); err != nil {
		t.Fatalf("migrateFieldCryptoGCM: %v", err)
	}

	var doc bson.M
	if err := users.FindOne(ctx, bson.M{"_id": uLegacy}).Decode(&doc); err != nil {
		t.Fatalf("find legacy user: %v", err)
	}
	gotSecret := doc["twoFactorSecret"].(string)
	if !strings.HasPrefix(gotSecret, "enc2:") {
		t.Fatalf("secret should be re-encrypted to enc2:, got %q", gotSecret[:8])
	}
	if plain, err := auth.DecryptField(gotSecret, keyHex); err != nil || plain != secretPlain {
		t.Fatalf("re-encrypted secret should decrypt: got %q err %v", plain, err)
	}
	codes := doc["twoFactorBackupCodes"].(primitive.A)
	if len(codes) != 3 {
		t.Fatalf("backup codes length = %d, want 3", len(codes))
	}
	if codes[0] != "bc:hashed" {
		t.Errorf("bc: entry must stay: got %v", codes[0])
	}
	if c1, ok := codes[1].(string); !ok || !strings.HasPrefix(c1, "enc2:") {
		t.Errorf("enc: code should be re-encrypted, got %v", codes[1])
	} else if plain, err := auth.DecryptField(c1, keyHex); err != nil || plain != "old1" {
		t.Errorf("re-encrypted code should decrypt to old1: got %q err %v", plain, err)
	}
	if codes[2] != "old-plaintext" {
		t.Errorf("plaintext entry must stay: got %v", codes[2])
	}
	// enc2:/明文用户不受影响。
	var gcmDoc, plainDoc bson.M
	if err := users.FindOne(ctx, bson.M{"_id": uGCM}).Decode(&gcmDoc); err != nil || gcmDoc["twoFactorSecret"] != keptGCM {
		t.Errorf("enc2: user must be untouched: %v err %v", gcmDoc["twoFactorSecret"], err)
	}
	if err := users.FindOne(ctx, bson.M{"_id": uPlain}).Decode(&plainDoc); err != nil || plainDoc["twoFactorSecret"] != "legacy-plaintext" {
		t.Errorf("plaintext user must be untouched: %v err %v", plainDoc["twoFactorSecret"], err)
	}
	// 幂等：重跑不再改动（flag 已置位）。
	if err := migrateFieldCryptoGCM(ctx, db, repos, keyHex); err != nil {
		t.Fatalf("second run: %v", err)
	}
	done, err := repos.Settings.GetFlag(ctx, keyFieldCryptoGCM)
	if err != nil || !done {
		t.Fatalf("migration flag: done=%v err=%v, want true", done, err)
	}
}

// TestMigrateFieldCryptoGCMBadData 验证解密失败（key 不符的坏数据）时迁移报错——
// 静默跳过会把故障推迟到 2FA 使用时刻，fail-closed 中断启动。
func TestMigrateFieldCryptoGCMBadData(t *testing.T) {
	keyHex := testFieldKeyCD
	db, cleanup := testDB(t)
	defer cleanup()
	ctx := context.Background()
	repos := repository.NewRepos(db, 5, 30)
	// 用不同 key 构造的 enc: 数据 → 当前 key 解不开。
	mustInsert(t, db.Collection("users"), bson.M{
		"_id": primitive.NewObjectID(), "twoFactorSecret": legacyCBCEncrypt(t, testFieldKeyAB, "x")})
	if err := migrateFieldCryptoGCM(ctx, db, repos, keyHex); err == nil {
		t.Fatal("migration should fail on undecryptable data")
	}
}
