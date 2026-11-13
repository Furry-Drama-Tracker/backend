// cmd/useradmin 是用户维护 CLI：给能直接访问 MongoDB 的运维环境救砖用。
//
// 典型场景：
//   - 管理员丢失 2FA 设备被锁在强制设置流程外 → clear-2fa（清 2FA + 全会话下线，
//     下次登录重新走强制设置）
//   - 忘记密码/重置密码邮件链路故障 → reset-password
//   - 密码连错触发账号锁定 → unlock
//   - 恢复/移交管理员权限 → set-role
//
// 用法（配置与 server 相同：MONGO_URI/JWT_SECRET 等来自 env/.env/ini）：
//
//	go run ./cmd/useradmin list [关键字]
//	go run ./cmd/useradmin show <uid|accountId|email|_id>
//	go run ./cmd/useradmin set-role <id> creator|admin|superadmin|user [--yes]
//	go run ./cmd/useradmin reset-password <id> [--generate|--stdin] [--yes]
//	go run ./cmd/useradmin clear-2fa <id> [--yes]
//	go run ./cmd/useradmin unlock <id> [--yes]
//	go run ./cmd/useradmin rename <id> <newName> [--force] [--yes]
//	go run ./cmd/useradmin set-email <id> <email> [--verify] [--yes]
//	go run ./cmd/useradmin verify-email <id> [--yes]
//	go run ./cmd/useradmin revoke-sessions <id> [--yes]
//
// 破坏性操作需交互确认，--yes 跳过（脚本/CI 用）。rename --force 可越过黑名单与
// 冷却（不能越过占用检查）；旧名照常写入保留期。
package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"flag"
	"fmt"
	"log"
	"math/big"
	"os"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/Furry-Drama-Tracker/backend/internal/auth"
	"github.com/Furry-Drama-Tracker/backend/internal/config"
	"github.com/Furry-Drama-Tracker/backend/internal/middleware"
	"github.com/Furry-Drama-Tracker/backend/internal/model"
	"github.com/Furry-Drama-Tracker/backend/internal/repository"
	"github.com/Furry-Drama-Tracker/backend/internal/service"
)

func main() {
	flag.Parse()
	args := flag.Args()
	if len(args) == 0 {
		usage()
		os.Exit(2)
	}
	cfg, err := config.Load("")
	if err != nil {
		log.Fatalf("配置加载失败: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := repository.Connect(ctx, cfg.Database.URI, cfg.Database.Name, 5)
	if err != nil {
		log.Fatalf("MongoDB 连接失败: %v", err)
	}
	repos := repository.NewRepos(db, cfg.Security.LoginMaxAttempts, cfg.Security.LoginLockMinutes)

	cmd := args[0]
	rest := args[1:]
	yes := os.Getenv("USERADMIN_YES") == "1" || hasFlag(rest, "--yes")
	rest = dropFlag(rest, "--yes")

	switch cmd {
	case "list":
		runList(ctx, repos, rest)
	case "show":
		needArg(rest, 1, "show <uid|accountId|email|_id>")
		runShow(ctx, repos, rest[0])
	case "set-role":
		needArg(rest, 2, "set-role <id> <role>")
		runSetRole(ctx, repos, cfg, rest[0], rest[1], yes)
	case "reset-password":
		needArg(rest, 1, "reset-password <id> [--generate|--stdin]")
		runResetPassword(ctx, repos, rest[0], rest[1:], yes)
	case "clear-2fa":
		needArg(rest, 1, "clear-2fa <id>")
		runClear2FA(ctx, repos, cfg, rest[0], yes)
	case "unlock":
		needArg(rest, 1, "unlock <id>")
		runUnlock(ctx, repos, rest[0], yes)
	case "rename":
		needArg(rest, 2, "rename <id> <newName>")
		force := hasFlag(rest, "--force")
		rest = dropFlag(rest, "--force")
		runRename(ctx, repos, cfg, rest[0], rest[1], force, yes)
	case "set-email":
		needArg(rest, 2, "set-email <id> <email>")
		verify := hasFlag(rest, "--verify")
		rest = dropFlag(rest, "--verify")
		runSetEmail(ctx, repos, rest[0], rest[1], verify, yes)
	case "verify-email":
		needArg(rest, 1, "verify-email <id>")
		runVerifyEmail(ctx, repos, rest[0], yes)
	case "revoke-sessions":
		needArg(rest, 1, "revoke-sessions <id>")
		runRevokeSessions(ctx, repos, rest[0], yes)
	default:
		fmt.Fprintf(os.Stderr, "未知命令: %s\n\n", cmd)
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `用法: useradmin <命令> [参数] [--yes]

命令:
  list [关键字]                          按用户名/邮箱/uid 模糊查（不带关键字列最近 20 个）
  show <id>                              查看用户详情
  set-role <id> <role>                   改角色（user|creator|admin|superadmin）
  reset-password <id> [--generate|--stdin]  重置密码并吊销全部会话
  clear-2fa <id>                         清除 2FA 并吊销全部会话（管理员锁死自救）
  unlock <id>                            清除登录失败锁定
  rename <id> <newName> [--force]        改用户名（--force 越过黑名单/冷却）
  set-email <id> <email> [--verify]      改绑定邮箱（--verify 同时标记已验证）
  verify-email <id>                      标记邮箱已验证
  revoke-sessions <id>                   吊销该用户全部会话
`)
}

func needArg(args []string, n int, usageText string) {
	if len(args) < n {
		fmt.Fprintf(os.Stderr, "参数不足，用法: %s\n", usageText)
		os.Exit(2)
	}
}

func hasFlag(args []string, f string) bool {
	for _, a := range args {
		if a == f {
			return true
		}
	}
	return false
}

func dropFlag(args []string, f string) []string {
	out := args[:0]
	for _, a := range args {
		if a != f {
			out = append(out, a)
		}
	}
	return out
}

// resolveUser 定位用户：纯数字 → uid；24 位 hex → _id；含 @ → 邮箱；否则 accountId。
func resolveUser(ctx context.Context, repos *repository.Repos, arg string) (*model.User, error) {
	arg = strings.TrimSpace(arg)
	if n, ok := isAllDigits(arg); ok {
		return repos.Users.FindByUID(ctx, n)
	}
	if oid, err := primitive.ObjectIDFromHex(arg); err == nil {
		return repos.Users.FindByID(ctx, oid)
	}
	if strings.Contains(arg, "@") {
		return repos.Users.FindByEmail(ctx, strings.ToLower(arg))
	}
	return repos.Users.FindByAccountID(ctx, strings.ToLower(arg))
}

func isAllDigits(s string) (int64, bool) {
	if s == "" {
		return 0, false
	}
	var n int64
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int64(c-'0')
	}
	return n, true
}

func runList(ctx context.Context, repos *repository.Repos, args []string) {
	query := ""
	if len(args) > 0 {
		query = strings.TrimSpace(args[0])
	}
	var users []model.User
	var err error
	if query == "" {
		users, err = repos.Users.AdminFindRoles(ctx, []string{middleware.RoleUser, middleware.RoleCreator, middleware.RoleAdmin, middleware.RoleSuperAdmin}, 1, 20)
	} else {
		users, err = repos.Users.AdminFindSearch(ctx, query, 1, 20)
	}
	if err != nil {
		log.Fatalf("查询失败: %v", err)
	}
	if len(users) == 0 {
		fmt.Println("（无匹配用户）")
		return
	}
	fmt.Printf("%-6s %-24s %-20s %-28s %-10s %s\n", "UID", "用户名", "昵称", "邮箱", "角色", "2FA")
	for _, u := range users {
		fmt.Printf("%-6d %-24s %-20s %-28s %-10s %s\n",
			u.UID, u.AccountID, u.Username, u.Email, u.Role, yesNo(u.TwoFactorEnabled))
	}
}

func runShow(ctx context.Context, repos *repository.Repos, arg string) {
	u, err := resolveUser(ctx, repos, arg)
	if err != nil {
		log.Fatalf("找不到用户 %q: %v", arg, err)
	}
	fmt.Printf("UID:        %d\n", u.UID)
	fmt.Printf("_id:        %s\n", u.ID.Hex())
	fmt.Printf("用户名:     %s\n", u.AccountID)
	fmt.Printf("昵称:       %s\n", u.Username)
	fmt.Printf("邮箱:       %s（已验证: %s）\n", u.Email, yesNo(u.IsEmailVerified))
	fmt.Printf("角色:       %s\n", u.Role)
	fmt.Printf("2FA:        %s\n", yesNo(u.TwoFactorEnabled))
	if u.LoginNameChangedAt != nil {
		fmt.Printf("上次改名:   %s\n", u.LoginNameChangedAt.Local().Format("2006-01-02 15:04:05"))
	}
	if u.LockUntil > 0 && u.LockUntil > time.Now().UnixMilli() {
		fmt.Printf("锁定至:     %s（登录失败过多）\n", time.UnixMilli(u.LockUntil).Local().Format("2006-01-02 15:04:05"))
	}
	if u.DeletionRequestedAt != nil {
		fmt.Printf("注销申请:   %s（宽限 7 天）\n", u.DeletionRequestedAt.Local().Format("2006-01-02 15:04:05"))
	}
}

func runSetRole(ctx context.Context, repos *repository.Repos, cfg *config.Config, arg, role string, yes bool) {
	switch role {
	case middleware.RoleUser, middleware.RoleCreator, middleware.RoleAdmin, middleware.RoleSuperAdmin:
	default:
		log.Fatalf("无效角色 %q（可选 user|creator|admin|superadmin）", role)
	}
	u, err := resolveUser(ctx, repos, arg)
	if err != nil {
		log.Fatalf("找不到用户 %q: %v", arg, err)
	}
	if u.Role == middleware.RoleSuperAdmin && role != middleware.RoleSuperAdmin {
		count, cerr := repos.Users.AdminCountRole(ctx, middleware.RoleSuperAdmin)
		if cerr == nil && count <= 1 {
			log.Fatal("拒绝操作：这是最后一个超级管理员")
		}
	}
	if !confirm(yes, fmt.Sprintf("将用户 %s(uid=%d) 的角色从 %s 改为 %s？", u.AccountID, u.UID, u.Role, role)) {
		return
	}
	if _, err := repos.Users.AdminFindByIDAndSetRole(ctx, u.ID.Hex(), role); err != nil {
		log.Fatalf("修改角色失败: %v", err)
	}
	// 角色化 2FA 策略与站点行为保持一致：降级为普通用户时自动关闭 2FA 并下线全部会话。
	if role == middleware.RoleUser && u.TwoFactorEnabled {
		_ = repos.Users.DisableTwoFactor(ctx, u.ID)
		_ = repos.Sessions.DeactivateAllByUser(ctx, u.ID, model.RevokeAdmin)
		fmt.Println("已随降级关闭 2FA 并吊销全部会话")
	}
	fmt.Printf("角色已更新: %s → %s\n", u.AccountID, role)
	if (role == middleware.RoleAdmin || role == middleware.RoleSuperAdmin) && !u.TwoFactorEnabled {
		fmt.Println("提示：该账号未开启 2FA，下次登录将被要求完成强制设置（force2FASetup）。")
	}
	_ = cfg
}

func runResetPassword(ctx context.Context, repos *repository.Repos, arg string, rest []string, yes bool) {
	u, err := resolveUser(ctx, repos, arg)
	if err != nil {
		log.Fatalf("找不到用户 %q: %v", arg, err)
	}
	var (
		newPass string
		source  string
	)
	switch {
	case hasFlag(rest, "--generate"):
		newPass = generatePassword()
		source = "随机生成（展示一次，请立即保存）"
	case hasFlag(rest, "--stdin"):
		reader := bufio.NewReader(os.Stdin)
		line, _ := reader.ReadString('\n')
		newPass = strings.TrimRight(line, "\r\n")
		source = "stdin"
	default:
		log.Fatal("必须指定密码来源：--generate 或 --stdin")
	}
	if msg := service.ValidatePassword(newPass); msg != "" {
		log.Fatalf("密码不合规: %s", msg)
	}
	if !confirm(yes, fmt.Sprintf("重置用户 %s(uid=%d) 的密码并吊销其全部会话？", u.AccountID, u.UID)) {
		return
	}
	hash, err := auth.Hash(newPass)
	if err != nil {
		log.Fatalf("密码哈希失败: %v", err)
	}
	now := time.Now()
	if err := repos.Users.UpdatePassword(ctx, u.ID, hash, now); err != nil {
		log.Fatalf("写库失败: %v", err)
	}
	if err := repos.Sessions.DeactivateAllByUser(ctx, u.ID, model.RevokePasswordReset); err != nil {
		log.Fatalf("吊销会话失败: %v", err)
	}
	fmt.Printf("密码已重置（%s）。%s\n", source, newPass)
}

func runClear2FA(ctx context.Context, repos *repository.Repos, cfg *config.Config, arg string, yes bool) {
	u, err := resolveUser(ctx, repos, arg)
	if err != nil {
		log.Fatalf("找不到用户 %q: %v", arg, err)
	}
	if !u.TwoFactorEnabled && u.TwoFactorSecret == "" && len(u.TwoFactorBackupCodes) == 0 {
		fmt.Println("该账号未开启 2FA，无需操作")
		return
	}
	if !confirm(yes, fmt.Sprintf("清除用户 %s(uid=%d) 的 2FA 并吊销其全部会话？该用户下次登录需重新设置。", u.AccountID, u.UID)) {
		return
	}
	if err := repos.Users.DisableTwoFactor(ctx, u.ID); err != nil {
		log.Fatalf("清除 2FA 失败: %v", err)
	}
	if err := repos.Sessions.DeactivateAllByUser(ctx, u.ID, model.RevokeAdmin); err != nil {
		log.Fatalf("吊销会话失败: %v", err)
	}
	fmt.Println("2FA 已清除，全部会话已吊销。该用户下次登录将走正常的登录流程（管理员角色会再次被要求完成 2FA 设置）。")
	_ = cfg
}

func runUnlock(ctx context.Context, repos *repository.Repos, arg string, yes bool) {
	u, err := resolveUser(ctx, repos, arg)
	if err != nil {
		log.Fatalf("找不到用户 %q: %v", arg, err)
	}
	if !confirm(yes, fmt.Sprintf("清除用户 %s(uid=%d) 的登录失败锁定？", u.AccountID, u.UID)) {
		return
	}
	if err := repos.Users.ResetLoginAttempts(ctx, u.ID); err != nil {
		log.Fatalf("解锁失败: %v", err)
	}
	fmt.Println("已解锁")
}

func runRename(ctx context.Context, repos *repository.Repos, cfg *config.Config, arg, newName string, force, yes bool) {
	// 保留原样大小写（GitHub 式折叠语义）；查重/保留期由仓库层按折叠处理。
	newName = strings.TrimSpace(newName)
	u, err := resolveUser(ctx, repos, arg)
	if err != nil {
		log.Fatalf("找不到用户 %q: %v", arg, err)
	}
	if !force {
		if msg := service.ValidateLoginName(newName, cfg.Security.UsernameBlacklist); msg != "" {
			log.Fatalf("新用户名不合规: %s（--force 可越过黑名单/格式检查）", msg)
		}
		if u.LoginNameChangedAt != nil {
			cooldown := time.Duration(cfg.Security.LoginNameCooldownDays) * 24 * time.Hour
			if cooldown > 0 && time.Now().Before(u.LoginNameChangedAt.Add(cooldown)) {
				log.Fatalf("改名冷却中（上次改名 %s，--force 可越过）", u.LoginNameChangedAt.Local().Format("2006-01-02 15:04:05"))
			}
		}
	}
	if exists, _ := repos.Users.AccountIDExistsExcluding(ctx, newName, u.ID); exists {
		log.Fatalf("新用户名 %q 已被占用", newName)
	}
	reserveDays := cfg.Security.LoginNameReserveDays
	if reserveDays <= 0 {
		reserveDays = 90
	}
	if !confirm(yes, fmt.Sprintf("将用户 %s(uid=%d) 的用户名改为 %s？旧名将保留 %d 天。", u.AccountID, u.UID, newName, reserveDays)) {
		return
	}
	if err := repos.LoginNameRes.Reserve(ctx, strings.ToLower(u.AccountID), u.ID.Hex(),
		time.Now().Add(time.Duration(reserveDays)*24*time.Hour)); err != nil {
		log.Fatalf("写入保留期失败: %v", err)
	}
	if err := repos.Users.UpdateLoginName(ctx, u.ID, newName, time.Now()); err != nil {
		log.Fatalf("改名失败: %v", err)
	}
	fmt.Printf("用户名已更新: %s → %s\n", u.AccountID, newName)
}

func runSetEmail(ctx context.Context, repos *repository.Repos, arg, email string, verify, yes bool) {
	email = strings.ToLower(strings.TrimSpace(email))
	if !service.EmailRegex.MatchString(email) {
		log.Fatalf("邮箱格式不正确: %s", email)
	}
	u, err := resolveUser(ctx, repos, arg)
	if err != nil {
		log.Fatalf("找不到用户 %q: %v", arg, err)
	}
	if exists, _ := repos.Users.ExistsByEmail(ctx, email); exists && email != u.Email {
		log.Fatalf("邮箱 %q 已被其它账号使用", email)
	}
	verified := verify
	if !confirm(yes, fmt.Sprintf("将用户 %s(uid=%d) 的邮箱从 %s 改为 %s（已验证: %s）？",
		u.AccountID, u.UID, u.Email, email, yesNo(verified))) {
		return
	}
	if verified {
		if err := repos.Users.SetEmailVerifiedAndEmail(ctx, u.ID, email); err != nil {
			log.Fatalf("写库失败: %v", err)
		}
	} else {
		if err := repos.Users.SetEmailPendingVerification(ctx, u.ID, email); err != nil {
			log.Fatalf("写库失败: %v", err)
		}
	}
	fmt.Println("邮箱已更新")
}

func runVerifyEmail(ctx context.Context, repos *repository.Repos, arg string, yes bool) {
	u, err := resolveUser(ctx, repos, arg)
	if err != nil {
		log.Fatalf("找不到用户 %q: %v", arg, err)
	}
	if u.IsEmailVerified {
		fmt.Println("该账号邮箱已验证，无需操作")
		return
	}
	if !confirm(yes, fmt.Sprintf("将用户 %s(uid=%d) 的邮箱标记为已验证？", u.AccountID, u.UID)) {
		return
	}
	if err := repos.Users.UpdateVerified(ctx, u.ID); err != nil {
		log.Fatalf("写库失败: %v", err)
	}
	fmt.Println("已标记为已验证")
}

func runRevokeSessions(ctx context.Context, repos *repository.Repos, arg string, yes bool) {
	u, err := resolveUser(ctx, repos, arg)
	if err != nil {
		log.Fatalf("找不到用户 %q: %v", arg, err)
	}
	if !confirm(yes, fmt.Sprintf("吊销用户 %s(uid=%d) 的全部会话（强制全端下线）？", u.AccountID, u.UID)) {
		return
	}
	if err := repos.Sessions.DeactivateAllByUser(ctx, u.ID, model.RevokeAdmin); err != nil {
		log.Fatalf("吊销失败: %v", err)
	}
	fmt.Println("全部会话已吊销")
}

// confirm 交互确认（--yes / USERADMIN_YES=1 跳过）。
func confirm(yes bool, prompt string) bool {
	if yes {
		return true
	}
	fmt.Printf("%s [y/N] ", prompt)
	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	line = strings.TrimSpace(strings.ToLower(line))
	return line == "y" || line == "yes"
}

func yesNo(b bool) string {
	if b {
		return "是"
	}
	return "否"
}

// generatePassword 生成 16 字符高熵密码（字母数字，无易混淆字符）。
func generatePassword() string {
	const charset = "abcdefghjkmnpqrstuvwxyzABCDEFGHJKMNPQRSTUVWXYZ23456789"
	out := make([]byte, 16)
	max := big.NewInt(int64(len(charset)))
	for i := range out {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			log.Fatalf("随机源失败: %v", err)
		}
		out[i] = charset[n.Int64()]
	}
	// 保证至少一位数字与一位字母（charset 混排 + 随机 16 位实际必然满足，
	// 这里做显式兜底替换）。
	hasLetter, hasDigit := false, false
	for _, c := range out {
		if c >= '0' && c <= '9' {
			hasDigit = true
		} else {
			hasLetter = true
		}
	}
	if !hasDigit {
		out[0] = '2'
	}
	if !hasLetter {
		out[1] = 'h'
	}
	return string(out)
}
