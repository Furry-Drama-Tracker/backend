// seed-admin 创建初始超级管理员账户（幂等）。
//
// 用法：
//
//	MONGO_URI=mongodb://127.0.0.1:27017/furry_drama_tracker \
//	SUPERADMIN_EMAIL=admin@example.com \
//	SUPERADMIN_ACCOUNT_ID=admin \
//	SUPERADMIN_PASSWORD='强密码' \
//	go run ./cmd/seed-admin
package main

import (
	"context"
	"log"
	"os"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/Furry-Drama-Tracker/backend/internal/auth"
	"github.com/Furry-Drama-Tracker/backend/internal/model"
	"github.com/Furry-Drama-Tracker/backend/internal/repository"
	"github.com/Furry-Drama-Tracker/backend/internal/service"
)

func main() {
	uri := strings.TrimSpace(os.Getenv("MONGO_URI"))
	if uri == "" {
		log.Fatal("缺少 MONGO_URI")
	}
	email := strings.ToLower(strings.TrimSpace(os.Getenv("SUPERADMIN_EMAIL")))
	accountID := strings.TrimSpace(os.Getenv("SUPERADMIN_ACCOUNT_ID"))
	password := os.Getenv("SUPERADMIN_PASSWORD")
	if email == "" || accountID == "" || password == "" {
		log.Fatal("缺少 SUPERADMIN_EMAIL / SUPERADMIN_ACCOUNT_ID / SUPERADMIN_PASSWORD")
	}
	if msg := service.ValidatePassword(password); msg != "" {
		log.Fatalf("超级管理员密码不合规: %s", msg)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := repository.Connect(ctx, uri, "", 10)
	if err != nil {
		log.Fatalf("连接 MongoDB 失败: %v", err)
	}
	repos := repository.NewRepos(db, 5, 30)

	if exists, err := repos.Users.ExistsByEmail(ctx, email); err != nil {
		log.Fatalf("检查邮箱失败: %v", err)
	} else if exists {
		log.Printf("超级管理员邮箱已存在，未做修改: %s", email)
		return
	}
	if exists, err := repos.Users.ExistsByAccountID(ctx, accountID); err != nil {
		log.Fatalf("检查账号ID失败: %v", err)
	} else if exists {
		log.Fatalf("账号ID已被占用: %s", accountID)
	}
	// 新用户名校验（黑名单/保留名）。存量账号不校验，但 seed 创建的是新账号。
	if msg := service.ValidateLoginName(accountID, os.Getenv("USERNAME_BLACKLIST")); msg != "" {
		log.Fatalf("账号ID不合规: %s", msg)
	}

	// UID 分配：仅全库无用户时（首次建站）显式取 0（首个用户 = 0，仅注册顺序，
	// 无角色语义）；库中已有用户则走计数器取下一个号，不会抢 0。
	totalUsers, err := repos.Users.CountAll(ctx)
	if err != nil {
		log.Fatalf("统计用户数失败: %v", err)
	}
	uid := int64(0)
	if totalUsers > 0 {
		uid, err = repos.Counters.NextUID(ctx)
		if err != nil {
			log.Fatalf("发号失败: %v", err)
		}
	} else if err := repos.Counters.SetUIDCounter(ctx, 0); err != nil {
		log.Fatalf("初始化发号器失败: %v", err)
	}

	hash, err := auth.Hash(password)
	if err != nil {
		log.Fatalf("密码哈希失败: %v", err)
	}
	now := time.Now()
	user := &model.User{
		ID:                     primitive.NewObjectID(),
		UID:                    uid,
		AccountID:              accountID,
		Username:               "admin",
		Email:                  email,
		Password:               hash,
		Role:                   "superadmin",
		IsEmailVerified:        true,
		LastLoginAt:            &now,
		CreatedAt:              now,
		EmailNotificationPrefs: model.DefaultEmailNotificationPrefs(),
		BackgroundPrefs:        model.DefaultBackgroundPrefs(),
		PersonalWallpapers:     []model.Wallpaper{},
	}
	if err := repos.Users.Create(ctx, user); err != nil {
		log.Fatalf("创建超级管理员失败: %v", err)
	}
	log.Printf("超级管理员创建成功: %s (uid=%d) %s", accountID, uid, email)
}
