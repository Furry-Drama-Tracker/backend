package repository_test

import (
	"context"
	"os"
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
