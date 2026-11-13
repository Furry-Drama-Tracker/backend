package service

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"

	"github.com/Furry-Drama-Tracker/backend/internal/auth"
	"github.com/Furry-Drama-Tracker/backend/internal/errors"
	"github.com/Furry-Drama-Tracker/backend/internal/model"
	"github.com/Furry-Drama-Tracker/backend/internal/repository"
)

// 用户名（登录名，即 accountId 字段）规则（POSIX portable filename character set 收紧版）：
//   - 3–32 字符，字母/数字/下划线/连字符，首字符为字母或下划线；大小写均可
//   - 显式拒绝 "." 与 ".."（规格要求；3 字符下限本身已排除，按契约保留检查）
//   - 黑名单子串匹配：用户名包含任一关键词即拒（含 admin/root 等，防假官方名）
//   - 保留名精确匹配：me/self/all 等路由语义词
//   - 大小写策略（GitHub 式）：存储保留原样，登录/唯一性/保留期一律按折叠小写
//     （users.accountIdLower）判定——Admin 与 admin 不可并存，登录不区分大小写
//
// 存量账号不做校验（grandfather，含旧规则遗留字符），校验只在注册/改名生效。

// loginNameRegex 是新用户名的字符集规则（允许大写）。
var loginNameRegex = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]{2,31}$`)

// defaultLoginNameBlacklist 是内置黑名单关键词（子串匹配，小写）。
var defaultLoginNameBlacklist = []string{
	"admin", "root", "system", "support", "staff", "moderator",
	"official", "null", "undefined", "api", "www", "webmaster", "operator",
	"security", "help", "service", "owner",
}

// defaultLoginNameReserved 是保留名（精确匹配，小写）。
var defaultLoginNameReserved = []string{
	"me", "self", "all", "new", "edit", "login", "logout", "register",
	"settings", "profile", "search", "uploads", "static", "assets",
}

// blacklistFromConfig 把配置的逗号分隔词表并入内置词表（小写去空去重）。
func blacklistFromConfig(configured []string) []string {
	if len(configured) == 0 {
		return defaultLoginNameBlacklist
	}
	seen := make(map[string]bool, len(defaultLoginNameBlacklist)+len(configured))
	out := make([]string, 0, len(defaultLoginNameBlacklist)+len(configured))
	for _, w := range append(append([]string{}, defaultLoginNameBlacklist...), configured...) {
		w = strings.ToLower(strings.TrimSpace(w))
		if w != "" && !seen[w] {
			seen[w] = true
			out = append(out, w)
		}
	}
	return out
}

// ValidateLoginName 校验新用户名（原样大小写合法即可，折叠语义由调用方负责）。
// 返回错误文案或空串（文案为前端可见契约，改词需同步前端 i18n 映射）。
// configuredBlacklist 来自 [security].username_blacklist（逗号分隔）。
func ValidateLoginName(name, configuredBlacklist string) string {
	n := strings.TrimSpace(name)
	if len(n) < 3 || len(n) > 32 {
		return "用户名长度需在3-32个字符之间"
	}
	if n == "." || n == ".." {
		return "该用户名不可使用"
	}
	if !loginNameRegex.MatchString(n) {
		return "用户名只能包含字母、数字、下划线和连字符，且以字母或下划线开头"
	}
	fold := strings.ToLower(n)
	if reservedName(fold) {
		return "该用户名为保留名"
	}
	for _, w := range blacklistFromConfig(splitTrimComma(configuredBlacklist)) {
		if strings.Contains(fold, w) {
			return "该用户名包含被保留的关键词"
		}
	}
	return ""
}

// reservedName 判断 name 是否命中保留名（精确匹配）。
func reservedName(n string) bool {
	for _, r := range defaultLoginNameReserved {
		if n == r {
			return true
		}
	}
	return false
}

// splitTrimComma 按逗号切分并去空（service 层局部工具，避免依赖 config 包）。
func splitTrimComma(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// CheckLoginNameAvailable 判断用户名是否可用（注册/改名共用）：
// 格式/黑名单/保留名 → 占用（按折叠小写查 users.accountIdLower）→
// 保留期（loginnamereservations，折叠小写，excludeUserID 为自己时豁免——
// 大小写微调不算被保留期挡住）。返回 (可用, 不可用原因)。
func (s *AuthService) CheckLoginNameAvailable(ctx context.Context, name string) (bool, string) {
	return s.CheckLoginNameAvailableFor(ctx, name, nil)
}

// CheckLoginNameAvailableFor 同上，excludeUserID 非 nil 时保留期检查豁免该用户
// （string 形式的 users._id hex）。
func (s *AuthService) CheckLoginNameAvailableFor(ctx context.Context, name string, excludeUserID any) (bool, string) {
	n := strings.TrimSpace(name)
	if msg := ValidateLoginName(n, s.Config.Security.UsernameBlacklist); msg != "" {
		return false, msg
	}
	fold := strings.ToLower(n)
	exists, err := s.Repos.Users.ExistsByAccountID(ctx, fold)
	if err != nil {
		return false, "服务器错误"
	}
	if exists {
		return false, "该用户名已被占用"
	}
	reserved, err := s.Repos.LoginNameRes.ExistsActive(ctx, fold, time.Now(), excludeUserID)
	if err != nil {
		return false, "服务器错误"
	}
	if reserved {
		return false, "该用户名正在保留期，暂时不可注册"
	}
	return true, ""
}

// ChangeLoginNameInput 是改名请求的解析结果。
type ChangeLoginNameInput struct {
	NewLoginName    string
	CurrentPassword string
}

// ChangeLoginName 用户名改名：密码再认证 + 校验 + 查重（折叠） + 冷却检查，
// 成功后旧名写入保留期（reservedUntil = now + LoginNameReserveDays）。
// 存储保留新名原样大小写；折叠形式与旧名相同的微调（Alice→ALICE）视为合法改名
// （保留期登记豁免自己）。返回 (新用户名, 距下次可改名的剩余天数(0=可改), 错误)。
func (s *AuthService) ChangeLoginName(ctx context.Context, u *model.User, in ChangeLoginNameInput) (string, int, error) {
	newName := strings.TrimSpace(in.NewLoginName)
	if in.CurrentPassword == "" {
		return "", 0, errors.New(400, "请输入当前密码以确认操作")
	}
	// 密码再认证：u 来自中间件公开投影（无 password 哈希），重新带哈希加载。
	full, err := s.Repos.Users.FindByIDWithAuth(ctx, u.ID)
	if err != nil || !auth.Compare(full.Password, in.CurrentPassword) {
		return "", 0, errors.New(400, "当前密码不正确")
	}
	newFold := strings.ToLower(newName)
	if newFold == strings.ToLower(full.AccountID) && newName == full.AccountID {
		return "", 0, errors.New(400, "新用户名与当前用户名相同")
	}
	if msg := ValidateLoginName(newName, s.Config.Security.UsernameBlacklist); msg != "" {
		return "", 0, errors.New(400, msg)
	}
	// 占用检查（折叠：accountIdLower 与保留期两处；保留期豁免自己——
	// 大小写微调后旧名的折叠形式就是新名的折叠形式，不应挡住自己）。
	if newFold != strings.ToLower(full.AccountID) {
		exists, err := s.Repos.Users.ExistsByAccountID(ctx, newFold)
		if err != nil {
			return "", 0, errors.New(500, "服务器错误")
		}
		if exists {
			return "", 0, errors.New(400, "该用户名已被占用")
		}
		reserved, err := s.Repos.LoginNameRes.ExistsActive(ctx, newFold, time.Now(), full.ID.Hex())
		if err != nil {
			return "", 0, errors.New(500, "服务器错误")
		}
		if reserved {
			return "", 0, errors.New(400, "该用户名正在保留期，暂时不可使用")
		}
	}
	// 冷却检查。
	if full.LoginNameChangedAt != nil {
		cooldown := time.Duration(s.Config.Security.LoginNameCooldownDays) * 24 * time.Hour
		if cooldown > 0 {
			eligibleAt := full.LoginNameChangedAt.Add(cooldown)
			if time.Now().Before(eligibleAt) {
				days := int(math.Ceil(time.Until(eligibleAt).Hours() / 24))
				if days < 1 {
					days = 1
				}
				return "", days, errors.New(400, fmt.Sprintf("改名冷却中，还需等待约%d天后才能再次改名", days))
			}
		}
	}
	// 落库：旧名保留（统一存折叠小写，防止 "Alice" 改名后 "ALICE" 抢注绕过保留期）
	// + 定点更新（原子性靠两步顺序写；中途失败的最坏情形是
	// 新名未占用但旧名多了一条保留记录，无安全影响）。
	reserveDays := s.Config.Security.LoginNameReserveDays
	if reserveDays <= 0 {
		reserveDays = 90
	}
	if err := s.Repos.LoginNameRes.Reserve(ctx, strings.ToLower(full.AccountID), full.ID.Hex(),
		time.Now().Add(time.Duration(reserveDays)*24*time.Hour)); err != nil {
		return "", 0, errors.New(500, "服务器错误")
	}
	now := time.Now()
	if err := s.Repos.Users.UpdateLoginName(ctx, full.ID, newName, now); err != nil {
		if repository.IsDuplicateKey(err) {
			return "", 0, errors.New(400, "该用户名已被占用")
		}
		return "", 0, errors.New(500, "服务器错误")
	}
	s.Audit(ctx, full, "LOGIN_NAME_CHANGED", "auth",
		"Login name changed: "+full.AccountID+" -> "+newName, "")
	return newName, 0, nil
}
