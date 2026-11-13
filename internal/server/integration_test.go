package server_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/Furry-Drama-Tracker/backend/internal/auth"
	"github.com/Furry-Drama-Tracker/backend/internal/config"
	"github.com/Furry-Drama-Tracker/backend/internal/model"
	"github.com/Furry-Drama-Tracker/backend/internal/repository"
	"github.com/Furry-Drama-Tracker/backend/internal/server"
)

// testServer 组装测试服务器（真实 mongod）。
func testServer(t *testing.T) *httptest.Server {
	t.Helper()
	ts, _ := testServerWithRepos(t)
	return ts
}

// testServerWithRepos 同 testServer，但额外返回仓储（需要直接查库断言的测试用）。
func testServerWithRepos(t *testing.T) (*httptest.Server, *repository.Repos) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{}
	cfg.IsDev = true
	cfg.Server.NodeEnv = "development"
	cfg.Server.Listen = "tcp:127.0.0.1:0"
	cfg.JWT.Secret = strings.Repeat("s", 40)
	cfg.JWT.EncryptionKey = ""
	cfg.JWT.DevAPIToken = "test-dev-token"
	cfg.JWT.DemoEmails = []string{"demo@furry09.com"}
	cfg.JWT.AccessTTL = 15 * time.Minute
	cfg.JWT.RefreshTTL = 7 * 24 * time.Hour
	cfg.Security.LoginMaxAttempts = 5
	cfg.Security.LoginLockMinutes = 30
	// 改名策略与生产默认一致（产品语义测试）。
	cfg.Security.LoginNameCooldownDays = 7
	cfg.Security.LoginNameReserveDays = 90
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

// client 是带 cookie jar + CSRF 自动回填的测试客户端。
type client struct {
	http  *http.Client
	base  string
	token string
}

func newClient(t *testing.T, base string) *client {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	c := &client{http: &http.Client{Jar: jar}, base: base}
	c.fetchCSRF()
	return c
}

func (c *client) fetchCSRF() {
	resp, err := c.http.Get(c.base + "/api/csrf-token")
	if err != nil {
		return
	}
	defer resp.Body.Close()
	var body struct {
		CSRF string `json:"csrfToken"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&body)
	c.token = body.CSRF
}

func (c *client) do(method, path string, body any, headers map[string]string) (*http.Response, []byte) {
	var reader io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		reader = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.base+path, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("x-dev-token", "test-dev-token")
	if c.token != "" {
		req.Header.Set("X-XSRF-TOKEN", c.token)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return resp, nil
	}
	data, _ := io.ReadAll(resp.Body)
	defer resp.Body.Close()
	return resp, data
}

func (c *client) json(method, path string, body any, out any) *http.Response {
	resp, data := c.do(method, path, body, nil)
	if out != nil {
		_ = json.Unmarshal(data, out)
	}
	return resp
}

func (c *client) register(email string) *http.Response {
	var out map[string]any
	return c.json("POST", "/api/auth/register", map[string]any{
		"accountId": "it" + strings.ReplaceAll(time.Now().Format("150405.000000"), ".", "")[:10],
		"username":  "ituser",
		"email":     email,
		"password":  "pass1234",
	}, &out)
}

// registerAndLogin 注册并登录（dev-token 跳过邮箱/设备验证），返回响应。
func (c *client) registerAndLogin(email string) *http.Response {
	c.register(email)
	var out map[string]any
	return c.json("POST", "/api/auth/login", map[string]any{"email": email, "password": "pass1234"}, &out)
}

// totpCode 生成 RFC6238 TOTP（测试用，与 auth.VerifyTOTP 同参数：SHA1/6位/30s）。
func totpCode(secret string, t time.Time) string {
	key, err := base64.StdEncoding.DecodeString(secret)
	if err != nil {
		return ""
	}
	counter := uint64(t.Unix() / 30)
	h := hmac.New(sha1.New, key)
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], counter)
	h.Write(buf[:])
	sum := h.Sum(nil)
	offset := sum[19] & 0xf
	code := (uint32(sum[offset]&0x7f)<<24 | uint32(sum[offset+1])<<16 | uint32(sum[offset+2])<<8 | uint32(sum[offset+3])) % 1000000
	return fmt.Sprintf("%06d", code)
}

// promoteRole 直接把用户角色改为指定角色（测试辅助）。
// 角色化 2FA：提升为 admin/superadmin 时同步置 twoFactorEnabled，
// 否则后续写操作会被 force2FASetup 门禁拦截。
func promoteRole(t *testing.T, repos *repository.Repos, uid primitive.ObjectID, role string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := repos.Users.AdminFindByIDAndSetRole(ctx, uid.Hex(), role); err != nil {
		t.Fatalf("promote role %s: %v", role, err)
	}
	if role == "admin" || role == "superadmin" {
		if err := repos.Users.EnableTwoFactor(ctx, uid); err != nil {
			t.Fatalf("set twoFactorEnabled: %v", err)
		}
	}
}

// promoteRoleOnly 只改角色不碰 2FA（测试 force2FASetup 门禁用——被提升的 admin
// 保持未开启 2FA 状态）。
func promoteRoleOnly(t *testing.T, repos *repository.Repos, uid primitive.ObjectID, role string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := repos.Users.AdminFindByIDAndSetRole(ctx, uid.Hex(), role); err != nil {
		t.Fatalf("promote role %s: %v", role, err)
	}
}

// userIDByEmail 按邮箱查用户 _id（测试辅助）。
func userIDByEmail(t *testing.T, repos *repository.Repos, email string) primitive.ObjectID {
	t.Helper()
	u, err := repos.Users.FindByEmail(context.Background(), email)
	if err != nil {
		t.Fatalf("find user %s: %v", email, err)
	}
	return u.ID
}

// TestRegisterLoginMeRefresh 注册→登录→me→refresh 主流程。
func TestRegisterLoginMeRefresh(t *testing.T) {
	ts := testServer(t)
	c := newClient(t, ts.URL)
	email := "it_" + fmt.Sprintf("%d", time.Now().UnixNano()%1e7) + "@test.com"

	resp := c.register(email)
	if resp.StatusCode != 200 {
		t.Fatalf("register: %d", resp.StatusCode)
	}
	var login map[string]any
	resp = c.json("POST", "/api/auth/login", map[string]any{"email": email, "password": "pass1234"}, &login)
	if resp.StatusCode != 200 || login["_id"] == "" {
		t.Fatalf("login: %d body=%v", resp.StatusCode, login)
	}
	var me map[string]any
	resp = c.json("GET", "/api/auth/me", nil, &me)
	if resp.StatusCode != 200 || me["email"] != email {
		t.Fatalf("me: %d body=%v", resp.StatusCode, me)
	}
	var refreshed map[string]any
	resp = c.json("POST", "/api/auth/refresh", nil, &refreshed)
	if resp.StatusCode != 200 || refreshed["_id"] == "" {
		t.Fatalf("refresh: %d body=%v", resp.StatusCode, refreshed)
	}
}

// TestLoginWrongPassword 密码错误 → 400 用户名或密码错误。
func TestLoginWrongPassword(t *testing.T) {
	ts := testServer(t)
	c := newClient(t, ts.URL)
	email := "it_" + fmt.Sprintf("%d", time.Now().UnixNano()%1e7) + "@test.com"
	c.register(email)
	var out map[string]any
	resp := c.json("POST", "/api/auth/login", map[string]any{"email": email, "password": "wrongpass1"}, &out)
	if resp.StatusCode != 400 || out["message"] != "用户名或密码错误" {
		t.Fatalf("login wrong: %d body=%v", resp.StatusCode, out)
	}
}

// TestTwoFactorLoginFlow enable → verify-enable → 登录走 2FA → login-2fa。
func TestTwoFactorLoginFlow(t *testing.T) {
	ts, repos := testServerWithRepos(t)
	c := newClient(t, ts.URL)
	email := "it2fa_" + fmt.Sprintf("%d", time.Now().UnixNano()%1e7) + "@test.com"
	c.registerAndLogin(email)
	// 角色化 2FA：普通用户禁开，创作者及以上才可开启。
	promoteRole(t, repos, userIDByEmail(t, repos, email), "creator")

	var setup map[string]any
	resp := c.json("POST", "/api/2fa/enable", nil, &setup)
	if resp.StatusCode != 200 || setup["secret"] == nil {
		t.Fatalf("2fa enable: %d body=%v", resp.StatusCode, setup)
	}
	secret := setup["secret"].(string)
	code := totpCode(secret, time.Now())
	var ver map[string]any
	resp = c.json("POST", "/api/2fa/verify-enable", map[string]any{"token": code}, &ver)
	if resp.StatusCode != 200 {
		t.Fatalf("2fa verify-enable: %d body=%v", resp.StatusCode, ver)
	}

	// 再次登录 → 应返回 need2FA。
	var login map[string]any
	resp = c.json("POST", "/api/auth/login", map[string]any{"email": email, "password": "pass1234"}, &login)
	if resp.StatusCode != 200 || login["need2FA"] != true {
		t.Fatalf("login with 2fa: %d body=%v", resp.StatusCode, login)
	}
	challenge := login["twoFactorChallenge"].(string)
	code2 := totpCode(secret, time.Now())
	var done map[string]any
	resp = c.json("POST", "/api/auth/login-2fa", map[string]any{
		"email": email, "twoFactorToken": code2, "twoFactorChallenge": challenge,
	}, &done)
	if resp.StatusCode != 200 || done["_id"] == "" {
		t.Fatalf("login-2fa: %d body=%v", resp.StatusCode, done)
	}
}

// backupCodesFrom 从 /2fa/enable 响应里取出明文备份码。
func backupCodesFrom(t *testing.T, setup map[string]any) []string {
	t.Helper()
	raw, ok := setup["backupCodes"].([]any)
	if !ok || len(raw) == 0 {
		t.Fatalf("enable response missing backupCodes: %v", setup)
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		t.Fatalf("enable response has no non-empty backup codes: %v", raw)
	}
	return out
}

// enable2FA 完成 enable → verify-enable 两步，返回 secret 与明文备份码。
func enable2FA(t *testing.T, c *client) (string, []string) {
	t.Helper()
	var setup map[string]any
	if resp := c.json("POST", "/api/2fa/enable", nil, &setup); resp.StatusCode != 200 || setup["secret"] == nil {
		t.Fatalf("2fa enable: %d body=%v", resp.StatusCode, setup)
	}
	secret, _ := setup["secret"].(string)
	codes := backupCodesFrom(t, setup)

	var ver map[string]any
	code := totpCode(secret, time.Now())
	if resp := c.json("POST", "/api/2fa/verify-enable", map[string]any{"token": code}, &ver); resp.StatusCode != 200 {
		t.Fatalf("2fa verify-enable: %d body=%v", resp.StatusCode, ver)
	}
	return secret, codes
}

// TestTwoFactorEnableTwiceRejected 已启用 2FA 后再次调用 /2fa/enable 必须被拒绝，
// 否则一次带有效会话的请求就能把 2FA 静默关闭（twoFactorEnabled=false）。
func TestTwoFactorEnableTwiceRejected(t *testing.T) {
	ts, repos := testServerWithRepos(t)
	c := newClient(t, ts.URL)
	email := "it2fa_re_" + fmt.Sprintf("%d", time.Now().UnixNano()%1e7) + "@test.com"
	c.registerAndLogin(email)
	promoteRole(t, repos, userIDByEmail(t, repos, email), "creator")

	enable2FA(t, c)

	// 第二次 enable：必须 400。
	var again map[string]any
	resp := c.json("POST", "/api/2fa/enable", nil, &again)
	if resp.StatusCode != 400 {
		t.Fatalf("second 2fa enable must be rejected: %d body=%v", resp.StatusCode, again)
	}

	// 2FA 仍应处于启用状态：登录继续返回 need2FA。
	var login map[string]any
	resp = c.json("POST", "/api/auth/login", map[string]any{"email": email, "password": "pass1234"}, &login)
	if resp.StatusCode != 200 || login["need2FA"] != true {
		t.Fatalf("2FA must remain enabled after rejected re-enable: %d body=%v", resp.StatusCode, login)
	}
}

// TestTwoFactorTOTPReplayRejected 同一时间步的 TOTP 码只能被接受一次。
func TestTwoFactorTOTPReplayRejected(t *testing.T) {
	ts, repos := testServerWithRepos(t)
	c := newClient(t, ts.URL)
	email := "it2fa_rp_" + fmt.Sprintf("%d", time.Now().UnixNano()%1e7) + "@test.com"
	c.registerAndLogin(email)
	promoteRole(t, repos, userIDByEmail(t, repos, email), "creator")
	secret, _ := enable2FA(t, c)

	// 第一次登录：拿到挑战并用当前步长的码通过。
	var login map[string]any
	if resp := c.json("POST", "/api/auth/login", map[string]any{"email": email, "password": "pass1234"}, &login); resp.StatusCode != 200 {
		t.Fatalf("login: %d body=%v", resp.StatusCode, login)
	}
	challenge, _ := login["twoFactorChallenge"].(string)
	if challenge == "" {
		t.Fatalf("missing challenge: %v", login)
	}
	code := totpCode(secret, time.Now())
	var okBody map[string]any
	if resp := c.json("POST", "/api/auth/login-2fa", map[string]any{
		"email": email, "twoFactorToken": code, "twoFactorChallenge": challenge,
	}, &okBody); resp.StatusCode != 200 || okBody["_id"] == nil {
		t.Fatalf("first login-2fa: %d body=%v", resp.StatusCode, okBody)
	}

	// 同一个码（同一时间步）重放：必须 400。
	var replay map[string]any
	resp := c.json("POST", "/api/auth/login-2fa", map[string]any{
		"email": email, "twoFactorToken": code, "twoFactorChallenge": challenge,
	}, &replay)
	if resp.StatusCode != 400 {
		t.Fatalf("TOTP replay must be rejected: %d body=%v", resp.StatusCode, replay)
	}
}

// TestTwoFactorBackupCodeConsumed 备份码登录一次后即失效（哈希存储 + 一次性消费）。
func TestTwoFactorBackupCodeConsumed(t *testing.T) {
	ts, repos := testServerWithRepos(t)
	c := newClient(t, ts.URL)
	email := "it2fa_bc_" + fmt.Sprintf("%d", time.Now().UnixNano()%1e7) + "@test.com"
	c.registerAndLogin(email)
	promoteRole(t, repos, userIDByEmail(t, repos, email), "creator")
	_, codes := enable2FA(t, c)
	backup := codes[0]

	loginWith := func(token string) (int, map[string]any) {
		var login map[string]any
		if resp := c.json("POST", "/api/auth/login", map[string]any{"email": email, "password": "pass1234"}, &login); resp.StatusCode != 200 {
			t.Fatalf("login: %d body=%v", resp.StatusCode, login)
		}
		challenge, _ := login["twoFactorChallenge"].(string)
		var out map[string]any
		resp := c.json("POST", "/api/auth/login-2fa", map[string]any{
			"email": email, "twoFactorToken": token, "twoFactorChallenge": challenge,
		}, &out)
		return resp.StatusCode, out
	}

	if status, body := loginWith(backup); status != 200 || body["_id"] == nil {
		t.Fatalf("backup code login: %d body=%v", status, body)
	}
	// 已消费：同一备份码必须失效。
	if status, body := loginWith(backup); status != 400 {
		t.Fatalf("consumed backup code must be rejected: %d body=%v", status, body)
	}
	// 其余备份码仍然可用（只消费命中的那一个）。
	if status, body := loginWith(codes[1]); status != 200 || body["_id"] == nil {
		t.Fatalf("other backup code should still work: %d body=%v", status, body)
	}
}

// TestRefreshBurstNotRateLimited 连续轮换 8 次不应触发限流。
// 回归点：/auth/refresh 原先挂在 2FA 桶（5 次/15min），正常多标签页/长会话
// 刷新会被打成 429，前端把 429 当登出 → 用户莫名掉登录。
func TestRefreshBurstNotRateLimited(t *testing.T) {
	ts := testServer(t)
	c := newClient(t, ts.URL)
	email := "it_refresh_" + fmt.Sprintf("%d", time.Now().UnixNano()%1e7) + "@test.com"
	c.registerAndLogin(email)

	const rounds = 8 // 超出旧 2FA 桶的 5/15min 阈值
	for i := 0; i < rounds; i++ {
		var out map[string]any
		resp := c.json("POST", "/api/auth/refresh", nil, &out)
		if resp.StatusCode != 200 {
			t.Fatalf("refresh #%d must not be rate limited: %d body=%v", i+1, resp.StatusCode, out)
		}
	}
}

// TestSessionRevokeReasonAndEvent 验证"会话墓碑 + 认证事件"这条排障链：
// 会话被吊销后写入 revokeReason，下一次带旧 access token 的请求触发
// session_revoked 事件（含原因），两者都能被事后查到。
func TestSessionRevokeReasonAndEvent(t *testing.T) {
	ts, repos := testServerWithRepos(t)
	c := newClient(t, ts.URL)
	email := "it_revoke_" + fmt.Sprintf("%d", time.Now().UnixNano()%1e7) + "@test.com"
	c.register(email)

	var login map[string]any
	if resp := c.json("POST", "/api/auth/login", map[string]any{"email": email, "password": "pass1234"}, &login); resp.StatusCode != 200 {
		t.Fatalf("login: %d body=%v", resp.StatusCode, login)
	}
	uidHex, _ := login["_id"].(string)
	uid, err := primitive.ObjectIDFromHex(uidHex)
	if err != nil {
		t.Fatalf("bad user id %q: %v", uidHex, err)
	}

	// 轮换一次：旧会话留下 rotated 墓碑，新会话成为唯一的 active。
	var rotated map[string]any
	if resp := c.json("POST", "/api/auth/refresh", nil, &rotated); resp.StatusCode != 200 {
		t.Fatalf("refresh: %d body=%v", resp.StatusCode, rotated)
	}
	active, err := repos.Sessions.FindActiveByUser(t.Context(), uid)
	if err != nil {
		t.Fatalf("find active sessions: %v", err)
	}
	if len(active) != 1 {
		t.Fatalf("want exactly 1 active session after rotation, got %d", len(active))
	}
	sessID := active[0].ID

	// 直接吊销该会话（模拟管理员强制下线），随后带旧 access 的请求应 401。
	if err := repos.Sessions.DeactivateByID(t.Context(), sessID, model.RevokeAdmin); err != nil {
		t.Fatalf("deactivate: %v", err)
	}
	var me map[string]any
	if resp := c.json("GET", "/api/auth/me", nil, &me); resp.StatusCode != 401 {
		t.Fatalf("/me after revoke should be 401, got %d body=%v", resp.StatusCode, me)
	}

	// 墓碑：会话本身记下了吊销原因。
	revoked, err := repos.Sessions.FindByID(t.Context(), sessID)
	if err != nil {
		t.Fatalf("reload session: %v", err)
	}
	if revoked.RevokeReason != model.RevokeAdmin {
		t.Fatalf("revokeReason = %q, want %q", revoked.RevokeReason, model.RevokeAdmin)
	}

	// 事件：异步落库，轮询等待（最多 3s）。
	var got *model.AuthEvent
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		events, err := repos.AuthEvents.FindByUserID(t.Context(), uid, time.Now().Add(-time.Hour), 50)
		if err != nil {
			t.Fatalf("find events: %v", err)
		}
		for i := range events {
			if events[i].Event == model.AuthEventSessionRevoked {
				got = &events[i]
				break
			}
		}
		if got != nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if got == nil {
		t.Fatal("session_revoked 事件未落库")
	}
	if got.Reason != model.RevokeAdmin {
		t.Fatalf("event reason = %q, want %q", got.Reason, model.RevokeAdmin)
	}
	if got.Email != email {
		t.Fatalf("event email = %q, want %q", got.Email, email)
	}
	if got.Path == "" || got.IP == "" {
		t.Fatalf("event 缺少 path/ip: %+v", got)
	}
}

// TestRefreshInvalidEventRecorded 没有 refresh cookie 的请求应记录 refresh_invalid
// （区分"客户端没带 cookie"与"token 过期/重用"——掉登录排障的关键分叉）。
func TestRefreshInvalidEventRecorded(t *testing.T) {
	ts, repos := testServerWithRepos(t)
	c := newClient(t, ts.URL) // 全新 jar：没有任何登录 cookie

	var out map[string]any
	resp := c.json("POST", "/api/auth/refresh", nil, &out)
	if resp.StatusCode != 401 {
		t.Fatalf("refresh without cookie: %d body=%v", resp.StatusCode, out)
	}

	var got *model.AuthEvent
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		events, err := repos.AuthEvents.Recent(t.Context(), time.Now().Add(-time.Hour), 50)
		if err != nil {
			t.Fatalf("find events: %v", err)
		}
		for i := range events {
			if events[i].Event == model.AuthEventRefreshInvalid {
				got = &events[i]
				break
			}
		}
		if got != nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if got == nil {
		t.Fatal("refresh_invalid 事件未落库")
	}
	if got.Detail != "cookie missing" {
		t.Fatalf("event detail = %q, want %q", got.Detail, "cookie missing")
	}
}

// TestRefreshReuse 旧 refresh 重用 → 401 reuse（吊销全部 session）。
func TestRefreshReuse(t *testing.T) {
	ts := testServer(t)
	c := newClient(t, ts.URL)
	email := "it_reuse_" + fmt.Sprintf("%d", time.Now().UnixNano()%1e7) + "@test.com"
	c.registerAndLogin(email)

	// 记录当前 refresh cookie 值（refreshToken path=/api/auth，需用匹配 URL 读取）。
	u, _ := url.Parse(c.base + "/api/auth/me")
	var oldRefresh string
	for _, ck := range c.http.Jar.Cookies(u) {
		if ck.Name == "refreshToken" {
			oldRefresh = ck.Value
		}
	}
	if oldRefresh == "" {
		t.Skip("no refresh cookie captured")
	}

	var r1 map[string]any
	if resp := c.json("POST", "/api/auth/refresh", nil, &r1); resp.StatusCode != 200 {
		t.Fatalf("refresh rotate: %d", resp.StatusCode)
	}


	// 用旧 refresh 手动构造请求（裸 client，手动携带 XSRF + refresh cookie）。
	req2, _ := http.NewRequest("POST", c.base+"/api/auth/refresh", nil)
	req2.AddCookie(&http.Cookie{Name: "refreshToken", Value: oldRefresh, Path: "/api/auth"})
	req2.AddCookie(&http.Cookie{Name: "XSRF-TOKEN", Value: c.token, Path: "/"})
	req2.Header.Set("X-XSRF-TOKEN", c.token)
	raw := &http.Client{}
	resp2, err := raw.Do(req2)
	if err != nil {
		t.Fatalf("reuse req: %v", err)
	}
	defer resp2.Body.Close()
	var rb map[string]any
	_ = json.NewDecoder(resp2.Body).Decode(&rb)
	// 轮换后立即重试旧 token：2min 并发宽限内 → 409 concurrentRefresh；超期 → 401 reuse。
	// 两者都证明旧 refresh 不再被接受为成功登录。
	if resp2.StatusCode != 409 && resp2.StatusCode != 401 {
		t.Fatalf("refresh reuse: %d body=%v", resp2.StatusCode, rb)
	}
	if resp2.StatusCode == 409 && rb["messageKey"] != "auth.concurrentRefresh" {
		t.Fatalf("refresh concurrent: %d body=%v", resp2.StatusCode, rb)
	}
	if resp2.StatusCode == 401 && rb["messageKey"] != "auth.refreshTokenReuse" {
		t.Fatalf("refresh reuse: %d body=%v", resp2.StatusCode, rb)
	}
}
