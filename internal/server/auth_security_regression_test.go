package server_test

// ADR-019 安全加固回归测试（OpenVuln 审计 BUG-R2 系列修复的行为闸门）。
// 每个用例对应一类已确认缺陷：修复回退时此处必须失败。

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/Furry-Drama-Tracker/backend/internal/auth"
	"github.com/Furry-Drama-Tracker/backend/internal/config"
	"github.com/Furry-Drama-Tracker/backend/internal/repository"
	"github.com/Furry-Drama-Tracker/backend/internal/server"

	altchalib "github.com/altcha-org/altcha-lib-go/v2"
)

// solveCaptcha 获取并求解 altcha 挑战，返回可直接放入登录体 altcha 字段的
// base64 payload（无 dev-token 的请求必须真实求解 PoW）。
func solveCaptcha(t *testing.T, c *client) string {
	t.Helper()
	var ch altchalib.Challenge
	if resp := c.json("GET", "/api/auth/captcha", nil, &ch); resp.StatusCode != 200 || ch.Parameters.Salt == "" {
		t.Fatalf("captcha: %d", resp.StatusCode)
	}
	sol, err := altchalib.SolveChallenge(altchalib.SolveChallengeOptions{
		Challenge: ch,
		DeriveKey: altchalib.DeriveKeySHA(),
	})
	if err != nil {
		t.Fatalf("solve altcha: %v", err)
	}
	raw, _ := json.Marshal(altchalib.Payload{Challenge: ch, Solution: *sol})
	return base64.StdEncoding.EncodeToString(raw)
}

// postJSONNoDev 不携带 x-dev-token 的 POST（client.do 固定附加 dev-token，
// 会整体绕过设备验证门控；测门控必须以真实浏览器姿态请求）。
func postJSONNoDev(c *client, path string, body any, out any) *http.Response {
	var reader io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		reader = bytes.NewReader(b)
	}
	req, _ := http.NewRequest("POST", c.base+path, reader)
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("X-XSRF-TOKEN", c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil
	}
	data, _ := io.ReadAll(resp.Body)
	defer resp.Body.Close()
	if out != nil {
		_ = json.Unmarshal(data, out)
	}
	return resp
}

// testServerWithMailRepos 同 testServerWithRepos，但 Email 配置齐全
// （mailReady=true → 登录设备验证门控生效），并返回仓储。
func testServerWithMailRepos(t *testing.T) (*httptest.Server, *repository.Repos) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{}
	cfg.IsDev = true
	cfg.Server.NodeEnv = "development"
	cfg.Server.Listen = "tcp:127.0.0.1:0"
	cfg.JWT.Secret = strings.Repeat("s", 40)
	cfg.JWT.EncryptionKey = ""
	cfg.JWT.DevAPIToken = "test-dev-token"
	cfg.JWT.AccessTTL = 15 * time.Minute
	cfg.JWT.RefreshTTL = 7 * 24 * time.Hour
	cfg.Security.LoginMaxAttempts = 5
	cfg.Security.LoginLockMinutes = 30
	// SMTP 配置齐全 → 登录流程 mailReady=true（发送在后台 goroutine，拨号失败静默）。
	cfg.Email = config.EmailConfig{Host: "127.0.0.1", Port: 2525, User: "it", Pass: "it", FromName: "it"}
	cfg.Server.AllowOrigins = []string{"http://localhost:3000"}

	db, err := repository.Connect(t.Context(), "mongodb://127.0.0.1:27017/neo_integration_test", "", 10)
	if err != nil {
		t.Fatalf("connect db: %v", err)
	}
	t.Cleanup(func() { _ = db.Drop(t.Context()) })
	repos := repository.NewRepos(db, cfg.Security.LoginMaxAttempts, cfg.Security.LoginLockMinutes)
	signer := auth.NewSigner(cfg.JWT.Secret)
	app := server.NewApp(server.Deps{Config: cfg, DB: db, Repos: repos, Signer: signer})
	ts := httptest.NewServer(app)
	t.Cleanup(ts.Close)
	return ts, repos
}

// TestDeletionRequestCancelPreservesCredentials 注销申请/取消不得触碰凭据字段。
// 回归点：旧实现对投影加载的 user 整文档 Save，password/twoFactorSecret/
// backupCodes 被零值覆盖——申请一次即凭据清零，2FA 用户永久锁死。
func TestDeletionRequestCancelPreservesCredentials(t *testing.T) {
	ts, repos := testServerWithRepos(t)
	c := newClient(t, ts.URL)
	email := "itdel_" + fmt.Sprintf("%d", time.Now().UnixNano()%1e7) + "@test.com"

	c.registerAndLogin(email)
	var login map[string]any
	if resp := c.json("POST", "/api/auth/login", map[string]any{"email": email, "password": "pass1234"}, &login); resp.StatusCode != 200 || login["_id"] == "" {
		t.Fatalf("login: %d body=%v", resp.StatusCode, login)
	}
	uid := login["_id"].(string)

	// 角色化 2FA：创作者及以上才可开启。
	promoteRole(t, repos, userIDByEmail(t, repos, email), "creator")
	secret, _ := enable2FA(t, c)

	// 申请注销（密码正确）。
	var req map[string]any
	if resp := c.json("POST", "/api/auth/request-deletion", map[string]any{"password": "pass1234"}, &req); resp.StatusCode != 200 {
		t.Fatalf("request-deletion: %d body=%v", resp.StatusCode, req)
	}

	ctx := t.Context()
	u, err := repos.Users.FindByIDWithAllSecrets(ctx, uid)
	if err != nil {
		t.Fatalf("find user: %v", err)
	}
	if u.DeletionRequestedAt == nil {
		t.Fatal("deletionRequestedAt must be set")
	}
	// 凭据必须原样保留（旧实现此处 password=""）。
	if !strings.HasPrefix(u.Password, "$2") {
		t.Fatalf("password hash wiped: %q", u.Password)
	}
	if !u.TwoFactorEnabled {
		t.Fatal("twoFactorEnabled must remain true")
	}

	// 取消注销。
	var cancel map[string]any
	if resp := c.json("POST", "/api/auth/cancel-deletion", nil, &cancel); resp.StatusCode != 200 {
		t.Fatalf("cancel-deletion: %d body=%v", resp.StatusCode, cancel)
	}
	u2, err := repos.Users.FindByIDWithAllSecrets(ctx, uid)
	if err != nil {
		t.Fatalf("find user after cancel: %v", err)
	}
	if u2.DeletionRequestedAt != nil {
		t.Fatal("deletionRequestedAt must be cleared")
	}
	if u2.Password != u.Password {
		t.Fatal("password hash must be untouched by cancel")
	}
	if !u2.TwoFactorEnabled {
		t.Fatal("twoFactorEnabled must survive cancel")
	}

	// 凭据健在的端到端证明：完整登录链（密码 → 2FA 挑战 → TOTP）仍然可用。
	var relg map[string]any
	if resp := c.json("POST", "/api/auth/login", map[string]any{"email": email, "password": "pass1234"}, &relg); resp.StatusCode != 200 || relg["need2FA"] != true {
		t.Fatalf("re-login after deletion cycle must reach 2FA: %d body=%v", resp.StatusCode, relg)
	}
	var done map[string]any
	if resp := c.json("POST", "/api/auth/login-2fa", map[string]any{
		"email": email, "twoFactorToken": totpCode(secret, time.Now()), "twoFactorChallenge": relg["twoFactorChallenge"].(string),
	}, &done); resp.StatusCode != 200 || done["_id"] == nil {
		t.Fatalf("login-2fa after deletion cycle: %d body=%v", resp.StatusCode, done)
	}
}

// TestNewDeviceLoginNoGate 新设备登录不再强制验证码（策略变更：事前拦截 → 事后通知）。
// 回归点：旧实现任何未识别设备都要求邮箱验证码（needDeviceVerify 403）；
// 现行为：未识别设备直接发会话 + 异步通知邮件（fdt-device cookie 仍作为
// 已知设备判定与通知触发的依据）。
func TestNewDeviceLoginNoGate(t *testing.T) {
	ts, repos := testServerWithRepos(t)
	c := newClient(t, ts.URL)
	email := "itdev_" + fmt.Sprintf("%d", time.Now().UnixNano()%1e7) + "@test.com"

	c.registerAndLogin(email)
	var me map[string]any
	if resp := c.json("GET", "/api/auth/me", nil, &me); resp.StatusCode != 200 || me["_id"] == "" {
		t.Fatalf("login: %d body=%v", resp.StatusCode, me)
	}
	uid := me["_id"].(string)

	// 免 dev-token 的登录只对已验证邮箱放行（补验证，与旧用例一致）。
	if err := repos.Users.UpdateVerified(t.Context(), uid); err != nil {
		t.Fatalf("verify email: %v", err)
	}

	// 登录会话必须携带设备令牌哈希（已知设备判定的信任依据不变）。
	oid, perr := primitive.ObjectIDFromHex(uid)
	if perr != nil {
		t.Fatalf("bad uid: %v", perr)
	}
	sessions, err := repos.Sessions.FindActiveByUser(t.Context(), oid)
	if err != nil || len(sessions) == 0 {
		t.Fatalf("active sessions: %v n=%d", err, len(sessions))
	}
	if sessions[0].DeviceTokenHash == "" {
		t.Fatal("session must persist deviceTokenHash")
	}
	if sessions[0].DeviceInfo.UserAgent == "" {
		t.Fatal("UA stays as display field")
	}

	// 同一设备（cookie jar 持有 fdt-device）登录 → 直接发会话。
	var known map[string]any
	if resp := postJSONNoDev(c, "/api/auth/login", map[string]any{"email": email, "password": "pass1234", "altcha": solveCaptcha(t, c)}, &known); resp.StatusCode != 200 || known["_id"] == "" {
		t.Fatalf("same-device login must succeed: %d body=%v", resp.StatusCode, known)
	}

	// 新客户端（无设备令牌、UA 完全相同）：不再有 needDeviceVerify 拦截，直接登录成功。
	fresh := newClient(t, ts.URL)
	var unknown map[string]any
	if resp := postJSONNoDev(fresh, "/api/auth/login", map[string]any{"email": email, "password": "pass1234", "altcha": solveCaptcha(t, fresh)}, &unknown); resp.StatusCode != 200 || unknown["_id"] == "" {
		t.Fatalf("new-device login must succeed without device gate: %d body=%v", resp.StatusCode, unknown)
	}
	if unknown["needDeviceVerify"] != nil {
		t.Fatalf("needDeviceVerify must no longer appear: %v", unknown)
	}
}

// TestEmailBoundLoginRateLimit 登录限流按目标邮箱计数。
// 回归点：旧实现 key=spec:IP——来源 IP 可轮换（伪造代理标头），对同一账号的
// 猜测无上限；现 key=spec:email 后同一邮箱共享计数桶，不同邮箱互不影响。
func TestEmailBoundLoginRateLimit(t *testing.T) {
	ts := testServer(t)
	c := newClient(t, ts.URL)
	victim := "itrl_" + fmt.Sprintf("%d", time.Now().UnixNano()%1e7) + "@test.com"
	other := "itrl2_" + fmt.Sprintf("%d", time.Now().UnixNano()%1e7) + "@test.com"

	if resp := c.register(victim); resp.StatusCode != 200 {
		t.Fatalf("register victim: %d", resp.StatusCode)
	}
	// 5 次错误密码（AuthSpec 5/15min 用满 victim 桶）。
	for i := 0; i < 5; i++ {
		var out map[string]any
		if resp := c.json("POST", "/api/auth/login", map[string]any{"email": victim, "password": "wrongpass1"}, &out); resp.StatusCode != 400 {
			t.Fatalf("wrong login #%d: %d body=%v", i+1, resp.StatusCode, out)
		}
	}
	// 第 6 次：victim 桶已满 → 429（与来源无关，key 绑定邮箱）。
	var sixth map[string]any
	if resp := c.json("POST", "/api/auth/login", map[string]any{"email": victim, "password": "wrongpass1"}, &sixth); resp.StatusCode != 429 {
		t.Fatalf("6th login for same email must be 429: %d body=%v", resp.StatusCode, sixth)
	}
	// 其它邮箱不受影响（各自独立的桶）。
	if resp := c.registerAndLogin(other); resp.StatusCode != 200 {
		t.Fatalf("different email must have its own bucket: %d", resp.StatusCode)
	}
}

// TestDisable2FAWithoutStoredCredentials 2FA 密文与备份码俱失（历史版本覆写残留）
// 时，持有效会话的所有者可经 /2fa/disable 直接关闭 2FA 重新启用。
// 回归点：旧实现继续要求验证码——空 secret 永远无法通过，账号永久锁死。
func TestDisable2FAWithoutStoredCredentials(t *testing.T) {
	ts, repos := testServerWithRepos(t)
	c := newClient(t, ts.URL)
	email := "it2fawipe_" + fmt.Sprintf("%d", time.Now().UnixNano()%1e7) + "@test.com"

	c.registerAndLogin(email)
	var me map[string]any
	if resp := c.json("GET", "/api/auth/me", nil, &me); resp.StatusCode != 200 || me["_id"] == "" {
		t.Fatalf("login: %d body=%v", resp.StatusCode, me)
	}
	uid := me["_id"].(string)

	// 构造"凭据俱失"残留态：enabled=true 但 secret 为空、备份码为空。
	if err := repos.Users.SetTwoFactorSetup(t.Context(), uid, "", nil, true); err != nil {
		t.Fatalf("simulate wiped 2FA: %v", err)
	}

	// 登录卡在 2FA 挑战（任何码都无法通过——旧缺陷症状）。
	var login map[string]any
	if resp := c.json("POST", "/api/auth/login", map[string]any{"email": email, "password": "pass1234"}, &login); resp.StatusCode != 200 || login["need2FA"] != true {
		t.Fatalf("wiped 2FA login must reach challenge: %d body=%v", resp.StatusCode, login)
	}

	// 恢复通道：无需有效验证码即可关闭。
	var disable map[string]any
	if resp := c.json("POST", "/api/2fa/disable", map[string]any{"token": "000000"}, &disable); resp.StatusCode != 200 {
		t.Fatalf("empty-credential 2FA disable must succeed: %d body=%v", resp.StatusCode, disable)
	}

	// 重新可登录（无 2FA 挑战）。
	var relg map[string]any
	if resp := c.json("POST", "/api/auth/login", map[string]any{"email": email, "password": "pass1234"}, &relg); resp.StatusCode != 200 || relg["_id"] == "" {
		t.Fatalf("login after recovery: %d body=%v", resp.StatusCode, relg)
	}
	if relg["need2FA"] == true {
		t.Fatal("2FA must be off after recovery disable")
	}
}

// TestEmailOTPSecondFactor 邮件 OTP 作为第二因素：登录挑战返回方法列表、
// 发码接口凭挑战令牌工作、错误码按 2FA 失败处理。
// 回归点：设备验证码链已移除，邮件 OTP 现挂在 2FA 挑战链上。
func TestEmailOTPSecondFactor(t *testing.T) {
	ts, repos := testServerWithRepos(t)
	c := newClient(t, ts.URL)
	email := "itotp_" + fmt.Sprintf("%d", time.Now().UnixNano()%1e7) + "@test.com"
	c.registerAndLogin(email)
	promoteRole(t, repos, userIDByEmail(t, repos, email), "creator")
	secret, _ := enable2FA(t, c)

	// emailOtp 仅对已验证邮箱开放（dev-token 注册跳过了验证门槛，此处补验证）。
	if err := repos.Users.UpdateVerified(t.Context(), userIDByEmail(t, repos, email)); err != nil {
		t.Fatalf("verify email: %v", err)
	}

	// 登录 → need2FA，且 twoFactorMethods 含 totp 与 emailOtp（邮箱已验证）。
	var login map[string]any
	if resp := c.json("POST", "/api/auth/login", map[string]any{"email": email, "password": "pass1234"}, &login); resp.StatusCode != 200 || login["need2FA"] != true {
		t.Fatalf("login: %d body=%v", resp.StatusCode, login)
	}
	challenge, _ := login["twoFactorChallenge"].(string)
	if challenge == "" {
		t.Fatalf("missing challenge: %v", login)
	}
	methods, _ := login["twoFactorMethods"].([]any)
	var hasTOTP, hasEmail bool
	for _, m := range methods {
		if s, ok := m.(string); ok {
			if s == "totp" {
				hasTOTP = true
			}
			if s == "emailOtp" {
				hasEmail = true
			}
		}
	}
	if !hasTOTP || !hasEmail {
		t.Fatalf("twoFactorMethods must include totp+emailOtp: %v", methods)
	}

	// 发码：挑战缺失 → 400；挑战非法 → 400。
	var sent map[string]any
	if resp := c.json("POST", "/api/auth/2fa/email-otp/send", map[string]any{}, &sent); resp.StatusCode != 400 {
		t.Fatalf("send without challenge must 400: %d body=%v", resp.StatusCode, sent)
	}
	if resp := c.json("POST", "/api/auth/2fa/email-otp/send", map[string]any{"twoFactorChallenge": challenge}, &sent); resp.StatusCode != 200 {
		t.Fatalf("send with valid challenge must 200 (SMTP 未配置时码仍入内存存储): %d body=%v", resp.StatusCode, sent)
	}
	if sent["email"] != email {
		t.Fatalf("send response must carry email: %v", sent)
	}

	// 错误码经 login-2fa（method=emailOtp）→ 400。
	var wrong map[string]any
	resp := c.json("POST", "/api/auth/login-2fa", map[string]any{
		"email": email, "twoFactorChallenge": challenge, "method": "emailOtp", "twoFactorToken": "000000",
	}, &wrong)
	if resp.StatusCode != 400 {
		t.Fatalf("wrong emailOtp must 400: %d body=%v", resp.StatusCode, wrong)
	}

	// 正常 TOTP 路径仍可用（回归保护）。
	var done map[string]any
	if resp := c.json("POST", "/api/auth/login-2fa", map[string]any{
		"email": email, "twoFactorToken": totpCode(secret, time.Now()), "twoFactorChallenge": challenge,
	}, &done); resp.StatusCode != 200 || done["_id"] == nil {
		t.Fatalf("totp path after emailOtp rejection: %d body=%v", resp.StatusCode, done)
	}
}
