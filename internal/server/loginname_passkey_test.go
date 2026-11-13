package server_test

// 用户名登录 + 改名 + force2FASetup 门禁集成测试。
import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestUsernameLoginAndUID 用户名（accountId）登录与 UID 发放。
func TestUsernameLoginAndUID(t *testing.T) {
	ts, repos := testServerWithRepos(t)
	c := newClient(t, ts.URL)
	accountID := "itlogin" + fmt.Sprintf("%d", time.Now().UnixNano()%1e6)
	email := accountID + "@test.com"

	var reg map[string]any
	if resp := c.json("POST", "/api/auth/register", map[string]any{
		"accountId": accountID, "username": "用户名登录测试",
		"email": email, "password": "pass1234",
	}, &reg); resp.StatusCode != 200 {
		t.Fatalf("register: %d body=%v", resp.StatusCode, reg)
	}

	// 用户名登录（免 dev-token，altcha 真实求解）。
	fresh := newClient(t, ts.URL)
	var login map[string]any
	if resp := fresh.json("POST", "/api/auth/login", map[string]any{
		"identifier": accountID, "password": "pass1234", "altcha": solveCaptcha(t, fresh),
	}, &login); resp.StatusCode != 200 || login["_id"] == "" {
		t.Fatalf("username login: %d body=%v", resp.StatusCode, login)
	}
	// 响应带 uid 与 accountId。
	if login["uid"] == nil || login["accountId"] != accountID {
		t.Fatalf("login response missing uid/accountId: %v", login)
	}
	uidNum, ok := login["uid"].(float64)
	if !ok || uidNum < 0 {
		t.Fatalf("uid must be a non-negative number: %v", login["uid"])
	}

	// UID 直查回读一致。
	u, err := repos.Users.FindByUID(t.Context(), int64(uidNum))
	if err != nil || u.AccountID != accountID {
		t.Fatalf("FindByUID(%v): %v", uidNum, err)
	}
}

// TestLoginNameChangeFlow 改名全流程：改成功 → 旧名进保留期 → 冷却内再改被拒
// → 旧名在保留期内不可注册 → 新用户名可登录。
func TestLoginNameChangeFlow(t *testing.T) {
	ts, _ := testServerWithRepos(t)
	c := newClient(t, ts.URL)
	accountID := "itchg" + fmt.Sprintf("%d", time.Now().UnixNano()%1e6)
	// 显式注册（不用 registerAndLogin——那个助手会自己生成 accountId）。
	var reg map[string]any
	if resp := c.json("POST", "/api/auth/register", map[string]any{
		"accountId": accountID, "username": "改名测试", "email": accountID + "@test.com",
		"password": "pass1234",
	}, &reg); resp.StatusCode != 200 {
		t.Fatalf("register: %d body=%v", resp.StatusCode, reg)
	}
	var login map[string]any
	if resp := c.json("POST", "/api/auth/login", map[string]any{
		"identifier": accountID, "password": "pass1234",
	}, &login); resp.StatusCode != 200 || login["_id"] == "" {
		t.Fatalf("login: %d body=%v", resp.StatusCode, login)
	}

	// 改名成功。
	var chg map[string]any
	if resp := c.json("PUT", "/api/users/login-name", map[string]any{
		"newLoginName": accountID + "2", "currentPassword": "pass1234",
	}, &chg); resp.StatusCode != 200 || chg["accountId"] != accountID+"2" {
		t.Fatalf("rename: %d body=%v", resp.StatusCode, chg)
	}

	// 旧名进保留期：注册被拒。
	c2 := newClient(t, ts.URL)
	var squatter map[string]any
	if resp := c2.json("POST", "/api/auth/register", map[string]any{
		"accountId": accountID, "username": "抢注", "email": "x_" + accountID + "@test.com",
		"password": "pass1234",
	}, &squatter); resp.StatusCode != 400 {
		t.Fatalf("register reserved name must 400: %d body=%v", resp.StatusCode, squatter)
	}

	// 冷却内再改被拒。
	var again map[string]any
	if resp := c.json("PUT", "/api/users/login-name", map[string]any{
		"newLoginName": accountID + "3", "currentPassword": "pass1234",
	}, &again); resp.StatusCode != 400 || again["message"] == "" {
		t.Fatalf("rename within cooldown must 400: %d body=%v", resp.StatusCode, again)
	}

	// 新用户名可登录。
	fresh := newClient(t, ts.URL)
	var loginNew map[string]any
	if resp := fresh.json("POST", "/api/auth/login", map[string]any{
		"identifier": accountID + "2", "password": "pass1234", "altcha": solveCaptcha(t, fresh),
	}, &loginNew); resp.StatusCode != 200 || loginNew["_id"] == "" {
		t.Fatalf("login with new name: %d body=%v", resp.StatusCode, loginNew)
	}
}

// TestForce2FASetupGate 管理员未开 2FA：me 带 force2FASetup，写操作被拦截，
// GET 与 2FA 开启流程不受影响；开着 2FA 的 admin 写操作放行。
func TestForce2FASetupGate(t *testing.T) {
	ts, repos := testServerWithRepos(t)
	c := newClient(t, ts.URL)
	email := "itgate_" + fmt.Sprintf("%d", time.Now().UnixNano()%1e7) + "@test.com"
	c.registerAndLogin(email)

	// 先开 2FA（creator 可开），再升为 admin——twoFactorEnabled=true，不被拦。
	promoteRole(t, repos, userIDByEmail(t, repos, email), "creator")
	enable2FA(t, c)
	promoteRole(t, repos, userIDByEmail(t, repos, email), "admin")

	var prof map[string]any
	if resp := c.json("PUT", "/api/users/profile", map[string]any{"username": "新昵称"}, &prof); resp.StatusCode != 200 {
		t.Fatalf("admin with 2FA write must pass: %d body=%v", resp.StatusCode, prof)
	}

	// 无 2FA 的 admin：写操作 403 force2FASetup。
	email2 := "itgate2_" + fmt.Sprintf("%d", time.Now().UnixNano()%1e7) + "@test.com"
	c2 := newClient(t, ts.URL)
	c2.registerAndLogin(email2)
	// 只升角色不碰 2FA（模拟真实场景：老管理员从未开启 2FA）。
	promoteRoleOnly(t, repos, userIDByEmail(t, repos, email2), "admin")

	var me map[string]any
	if resp := c2.json("GET", "/api/auth/me", nil, &me); resp.StatusCode != 200 || me["force2FASetup"] != true {
		t.Fatalf("me must carry force2FASetup=true: %d body=%v", resp.StatusCode, me)
	}
	var blocked map[string]any
	if resp := c2.json("PUT", "/api/users/profile", map[string]any{"username": "x"}, &blocked); resp.StatusCode != 403 || blocked["force2FASetup"] != true {
		t.Fatalf("admin without 2FA write must 403 force2FASetup: %d body=%v", resp.StatusCode, blocked)
	}
	// GET 不被拦。
	if resp := c2.json("GET", "/api/auth/me", nil, &me); resp.StatusCode != 200 {
		t.Fatalf("GET must not be blocked: %d", resp.StatusCode)
	}
	// 2FA 开启流程放行（此账号已是 admin）。
	var setup map[string]any
	if resp := c2.json("POST", "/api/2fa/enable", nil, &setup); resp.StatusCode != 200 || setup["secret"] == nil {
		t.Fatalf("2fa enable must be allowed under gate: %d body=%v", resp.StatusCode, setup)
	}
}

// TestUsernameCaseInsensitiveLogin 大小写折叠登录（GitHub 式）：
// 注册带大写（"CaseTest01"），用全小写登录命中同一账号，响应保留原样大小写；
// 折叠冲突（再注册 "casetest01"）被唯一性拒绝。
func TestUsernameCaseInsensitiveLogin(t *testing.T) {
	ts, _ := testServerWithRepos(t)
	c := newClient(t, ts.URL)
	accountID := "CaseTest" + fmt.Sprintf("%d", time.Now().UnixNano()%1e6)
	email := "case_" + fmt.Sprintf("%d", time.Now().UnixNano()%1e7) + "@test.com"

	var reg map[string]any
	if resp := c.json("POST", "/api/auth/register", map[string]any{
		"accountId": accountID, "username": "大小写测试", "email": email, "password": "pass1234",
	}, &reg); resp.StatusCode != 200 {
		t.Fatalf("register with uppercase: %d body=%v", resp.StatusCode, reg)
	}

	// 全小写登录（免 dev-token，altcha 真实求解）。
	fresh := newClient(t, ts.URL)
	var login map[string]any
	if resp := fresh.json("POST", "/api/auth/login", map[string]any{
		"identifier": strings.ToLower(accountID), "password": "pass1234", "altcha": solveCaptcha(t, fresh),
	}, &login); resp.StatusCode != 200 || login["_id"] == "" {
		t.Fatalf("lowercase login must hit same account: %d body=%v", resp.StatusCode, login)
	}
	if login["accountId"] != accountID {
		t.Fatalf("response must preserve original casing: %v", login["accountId"])
	}

	// 折叠冲突：同折叠形式的变体不可注册。
	c2 := newClient(t, ts.URL)
	var dup map[string]any
	if resp := c2.json("POST", "/api/auth/register", map[string]any{
		"accountId": strings.ToLower(accountID), "username": "变体", "email": "dup_" + email,
		"password": "pass1234",
	}, &dup); resp.StatusCode != 400 {
		t.Fatalf("case-variant of existing name must 400: %d body=%v", resp.StatusCode, dup)
	}
}
