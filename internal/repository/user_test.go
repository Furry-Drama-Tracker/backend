package repository_test

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"

	"github.com/Furry-Drama-Tracker/backend/internal/model"
	"github.com/Furry-Drama-Tracker/backend/internal/repository"
)

// rawCreatedAt 从 bson.M 原始文档取 createdAt。
// 解码进 bson.M 时 BSON date 是 primitive.DateTime（毫秒数）而非 time.Time。
func rawCreatedAt(t *testing.T, raw bson.M) time.Time {
	t.Helper()
	switch v := raw["createdAt"].(type) {
	case primitive.DateTime:
		return v.Time()
	case time.Time:
		return v
	default:
		t.Fatalf("createdAt 落库类型 = %T，期望 BSON date", raw["createdAt"])
		return time.Time{}
	}
}

// 本文件是 UserRepo 的仓储集成测试。
//
// 依赖一个可写的 MongoDB，通过环境变量 FDT_TEST_MONGO_URI 指定
//（如 mongodb://127.0.0.1:27018）；未设置时整个文件跳过，
// 使 go test ./... 在无数据库环境下仍可通过。
//
// 安全提示：不要把 FDT_TEST_MONGO_URI 指向生产库——测试会 Drop
// 独立测试库 neo_user_test 并写入测试用户。

const userTestDBName = "neo_user_test"

// userTestEnv 持有测试库连接，供仓储与原始文档断言使用。
type userTestEnv struct {
	db *mongo.Database
}

func (e *userTestEnv) ctx() context.Context             { return context.Background() }
func (e *userTestEnv) coll(name string) *mongo.Collection { return e.db.Collection(name) }

// newUserTestEnv 连接 FDT_TEST_MONGO_URI 指定的 MongoDB，
// Drop 独立测试库并返回环境与指向 users 集合的仓储。
func newUserTestEnv(t *testing.T) (*userTestEnv, *repository.UserRepo) {
	t.Helper()
	uri := os.Getenv("FDT_TEST_MONGO_URI")
	if uri == "" {
		t.Skip("FDT_TEST_MONGO_URI 未设置，跳过 UserRepo 集成测试")
	}
	db, err := repository.Connect(context.Background(), uri, userTestDBName, 10)
	if err != nil {
		t.Fatalf("connect db: %v", err)
	}
	if err := db.Drop(context.Background()); err != nil {
		t.Fatalf("drop db: %v", err)
	}
	t.Cleanup(func() { _ = db.Drop(context.Background()) })
	return &userTestEnv{db: db}, repository.NewUserRepo(db.Collection("users"), 5, 30)
}

// TestUserRepoCreateFillsZeroCreatedAt 覆盖注册路径：
// service.Register 构造 User 时不设置 CreatedAt，Create 必须兜底为当前时间，
// 否则零值 time.Time 会以 0001-01-01 落库。
func TestUserRepoCreateFillsZeroCreatedAt(t *testing.T) {
	env, repo := newUserTestEnv(t)

	before := time.Now()
	u := &model.User{
		AccountID:       "zerocreated",
		Username:        "零值时间用户",
		Email:           "zero-created@test.local",
		Password:        "hash-not-real",
		IsEmailVerified: true,
		Role:            "user",
	}
	if !u.CreatedAt.IsZero() {
		t.Fatalf("前置条件失败：CreatedAt 应为零值")
	}
	if err := repo.Create(env.ctx(), u); err != nil {
		t.Fatalf("create: %v", err)
	}

	// struct 回填：非零且落在调用窗口内。
	if u.CreatedAt.IsZero() {
		t.Fatal("Create 后 CreatedAt 仍为零值，未兜底")
	}
	if u.CreatedAt.Before(before.Add(-time.Second)) || u.CreatedAt.After(time.Now().Add(time.Second)) {
		t.Fatalf("CreatedAt = %v，超出合理时间窗口", u.CreatedAt)
	}
	if u.ID.IsZero() {
		t.Fatal("Create 后 _id 未回填到 struct")
	}

	// 落库形态：从原始文档读 createdAt，确认为真实日期而非 0001-01-01。
	var raw bson.M
	if err := env.coll("users").FindOne(env.ctx(), bson.M{"_id": u.ID}).Decode(&raw); err != nil {
		t.Fatalf("read back raw doc: %v", err)
	}
	stored := rawCreatedAt(t, raw)
	if stored.Year() <= 1 {
		t.Fatalf("createdAt 落库为 %v，仍为零值日期", stored)
	}
	if diff := time.Since(stored); diff < -2*time.Second || diff > 2*time.Second {
		t.Fatalf("createdAt 落库值 %v 偏离当前时间达 %v", stored, diff)
	}
}

// TestUserRepoCreateKeepsExplicitCreatedAt 覆盖管理员建号路径：
// handler.AdminRegisterAccount 显式赋 CreatedAt，兜底不得覆盖显式值。
// BSON date 精度为毫秒，先对齐再比对。
func TestUserRepoCreateKeepsExplicitCreatedAt(t *testing.T) {
	env, repo := newUserTestEnv(t)

	explicit := time.Date(2026, 5, 4, 12, 30, 9, 123000000, time.UTC)
	u := &model.User{
		AccountID:       "explicitcreated",
		Username:        "显式时间用户",
		Email:           "explicit-created@test.local",
		Password:        "hash-not-real",
		IsEmailVerified: true,
		Role:            "user",
		CreatedAt:       explicit,
	}
	if err := repo.Create(env.ctx(), u); err != nil {
		t.Fatalf("create: %v", err)
	}

	if !u.CreatedAt.Equal(explicit) {
		t.Fatalf("CreatedAt 被改写：want %v, got %v", explicit, u.CreatedAt)
	}

	var raw bson.M
	if err := env.coll("users").FindOne(env.ctx(), bson.M{"_id": u.ID}).Decode(&raw); err != nil {
		t.Fatalf("read back raw doc: %v", err)
	}
	stored := rawCreatedAt(t, raw)
	if !stored.Equal(explicit) {
		t.Fatalf("createdAt 落库值被改写：want %v, got %v", explicit, stored)
	}
}

// TestTryIncLoginAttemptsQuotaAndTOCTOU 覆盖 TryIncLoginAttempts 的配额语义：
//  1. 串行恰好放行 maxAttempts 次，之后拒绝且 lockUntil 落库；
//  2. 遗留文档缺 loginAttempts 字段（$lt 数值比较不匹配缺失）同样可占名额；
//  3. 过期锁先重置计数再占用；ResetLoginAttempts 清零后恢复可用；
//  4. 并发下成功数恒等于 maxAttempts（钉住 TOCTOU 修复：读快照→事后计数的
//     check-then-act 窗口曾使并发猜测预算膨胀到并发数）。
func TestTryIncLoginAttemptsQuotaAndTOCTOU(t *testing.T) {
	env, repo := newUserTestEnv(t)

	mustRaw := func(t *testing.T, doc bson.M) primitive.ObjectID {
		t.Helper()
		id := primitive.NewObjectID()
		doc["_id"] = id
		if _, err := env.coll("users").InsertOne(env.ctx(), doc); err != nil {
			t.Fatalf("insert raw: %v", err)
		}
		return id
	}
	readRaw := func(t *testing.T, id primitive.ObjectID) bson.M {
		t.Helper()
		var raw bson.M
		if err := env.coll("users").FindOne(env.ctx(), bson.M{"_id": id}).Decode(&raw); err != nil {
			t.Fatalf("read raw: %v", err)
		}
		return raw
	}

	// 1) 串行配额：恰好 5 次成功，第 6 次拒绝并上锁。
	lockedID := mustRaw(t, bson.M{"accountID": "tryquota"})
	for i := 0; i < 5; i++ {
		if !repo.TryIncLoginAttempts(env.ctx(), lockedID) {
			t.Fatalf("第 %d 次应占到名额", i+1)
		}
	}
	if repo.TryIncLoginAttempts(env.ctx(), lockedID) {
		t.Fatal("配额耗尽后应拒绝")
	}
	raw := readRaw(t, lockedID)
	if lu, _ := raw["lockUntil"].(int64); lu <= time.Now().UnixMilli() {
		t.Fatalf("配额耗尽应落 lockUntil, got %v", raw["lockUntil"])
	}

	// 2) 遗留文档：无 loginAttempts 字段也能占名额（$exists:false 分支）。
	legacyID := mustRaw(t, bson.M{"accountID": "trylegacy"})
	if !repo.TryIncLoginAttempts(env.ctx(), legacyID) {
		t.Fatal("缺 loginAttempts 字段的遗留文档应可占名额")
	}
	if v, _ := readRaw(t, legacyID)["loginAttempts"].(int32); v != 1 {
		t.Fatalf("占用后 loginAttempts 应为 1, got %v", readRaw(t, legacyID)["loginAttempts"])
	}

	// 3) 过期锁：先重置计数（$inc 后为 1）并清除锁；Reset 后恢复可用。
	expiredID := mustRaw(t, bson.M{"accountID": "tryexpired",
		"loginAttempts": int64(5), "lockUntil": time.Now().Add(-time.Minute).UnixMilli()})
	if !repo.TryIncLoginAttempts(env.ctx(), expiredID) {
		t.Fatal("过期锁应重置并放行")
	}
	raw = readRaw(t, expiredID)
	if v, _ := raw["loginAttempts"].(int32); v != 1 {
		t.Fatalf("过期锁重置后首次占用应为 1, got %v", raw["loginAttempts"])
	}
	if _, ok := raw["lockUntil"]; ok {
		t.Fatalf("过期锁应被清除, got %v", raw["lockUntil"])
	}
	if err := repo.ResetLoginAttempts(env.ctx(), expiredID); err != nil {
		t.Fatalf("reset: %v", err)
	}

	// 4) 并发：32 个 goroutine 同时占名额，成功数必须恒为 maxAttempts(5)。
	concID := mustRaw(t, bson.M{"accountID": "tryconc"})
	const goroutines = 32
	results := make(chan bool, goroutines)
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- repo.TryIncLoginAttempts(env.ctx(), concID)
		}()
	}
	wg.Wait()
	close(results)
	successes := 0
	for ok := range results {
		if ok {
			successes++
		}
	}
	if successes != 5 {
		t.Fatalf("并发占用成功数应恒为 maxAttempts(5)（TOCTOU 修复回归）, got %d", successes)
	}
}
