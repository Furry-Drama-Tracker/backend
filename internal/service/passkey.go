package service

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	webauthnlib "github.com/go-webauthn/webauthn/webauthn"
	"github.com/go-webauthn/webauthn/protocol"

	"github.com/Furry-Drama-Tracker/backend/internal/auth"
	"github.com/Furry-Drama-Tracker/backend/internal/code"
	"github.com/Furry-Drama-Tracker/backend/internal/errors"
	"github.com/Furry-Drama-Tracker/backend/internal/model"
	"github.com/Furry-Drama-Tracker/backend/internal/repository"
)

// PasskeyCeremonyTTL 是 WebAuthn 仪式会话的有效期（超期未完成的仪式作废；
// 库自身的 SessionData.Expires 也会在 take 时二次校验）。
const PasskeyCeremonyTTL = 5 * time.Minute

// passkeyCeremony 是一次 WebAuthn 仪式的上下文（注册/登录共用）。
type passkeyCeremony struct {
	session        webauthnlib.SessionData
	discoverable   bool   // 登录仪式：可发现凭据（无用户名）模式
	expectedUserID string // 登录仪式：限定用户（identifier 登录）的 users._id hex
}

// ceremonyStore 是 WebAuthn 仪式会话的内存存储（单实例语义，与 code.Store 一致；
// 多实例部署需换共享存储）。key 为随机 ceremony ID。
type ceremonyStore struct {
	mu    sync.Mutex
	items map[string]passkeyCeremony
}

func newCeremonyStore() *ceremonyStore {
	return &ceremonyStore{items: make(map[string]passkeyCeremony)}
}

func (c *ceremonyStore) put(key string, ceremony passkeyCeremony) {
	c.mu.Lock()
	c.items[key] = ceremony
	c.mu.Unlock()
}

// take 取出并删除仪式会话（一次性）；不存在或已过期均返回 false。
func (c *ceremonyStore) take(key string) (passkeyCeremony, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cer, ok := c.items[key]
	if !ok {
		return passkeyCeremony{}, false
	}
	delete(c.items, key)
	if !cer.session.Expires.IsZero() && time.Now().After(cer.session.Expires) {
		return passkeyCeremony{}, false
	}
	return cer, true
}

// uidBytes 是 WebAuthn user handle：UID 的大端 8 字节编码（≤64 字节、无 PII、
// 稳定不可变——UID 是账号的永久锚）。userHandle ↔ UID 双向映射。
func uidBytes(uid int64) []byte {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, uint64(uid))
	return buf
}

func uidFromBytes(b []byte) (int64, error) {
	if len(b) != 8 {
		return 0, fmt.Errorf("invalid user handle length %d", len(b))
	}
	return int64(binary.BigEndian.Uint64(b)), nil
}

// passkeyUser 适配 model.User 到 webauthn.User（凭据列表实时读库，
// 使 Begin* 的 excludeCredentials/allowCredentials 始终反映最新状态）。
type passkeyUser struct {
	s    *AuthService
	user *model.User
}

func (p passkeyUser) WebAuthnID() []byte { return uidBytes(p.user.UID) }

func (p passkeyUser) WebAuthnName() string { return p.user.AccountID }

func (p passkeyUser) WebAuthnDisplayName() string {
	if p.user.Username != "" {
		return p.user.Username
	}
	return p.user.AccountID
}

func (p passkeyUser) WebAuthnCredentials() []webauthnlib.Credential {
	list, err := p.s.Repos.Passkeys.FindByUser(context.Background(), p.user.ID)
	if err != nil {
		return nil
	}
	out := make([]webauthnlib.Credential, 0, len(list))
	for i := range list {
		out = append(out, p.s.toLibCredential(&list[i]))
	}
	return out
}

// toLibCredential 把存储凭据转换为库结构（login 仪式的 allowCredentials/签名校验用）。
func (s *AuthService) toLibCredential(pk *model.Passkey) webauthnlib.Credential {
	c := webauthnlib.Credential{
		ID:              decodeB64URLOrRaw(pk.CredentialID),
		PublicKey:       decodeB64URLOrRaw(pk.PublicKey),
		AttestationType: pk.AttestationType,
		Authenticator: webauthnlib.Authenticator{
			SignCount: pk.SignCount,
		},
		Flags: webauthnlib.CredentialFlags{
			BackupEligible: pk.UVE,
			BackupState:    pk.BackupState,
		},
	}
	for _, t := range pk.Transport {
		c.Transport = append(c.Transport, protocol.AuthenticatorTransport(t))
	}
	return c
}

// PasskeyWebAuthn 返回 WebAuthn 引擎实例（构造失败时为 nil，路由统一回 503）。
func (s *AuthService) PasskeyWebAuthn() *webauthnlib.WebAuthn { return s.passkeyWA }

// BeginPasskeyRegistration 生成注册仪式 options（已登录 + 密码再认证后调用）。
// 返回 (creation options, ceremonyKey)。
func (s *AuthService) BeginPasskeyRegistration(ctx context.Context, u *model.User) (*protocol.CredentialCreation, string, error) {
	wa := s.PasskeyWebAuthn()
	if wa == nil {
		return nil, "", errors.New(503, "Passkey 功能未启用")
	}
	creation, sd, err := wa.BeginRegistration(passkeyUser{s: s, user: u})
	if err != nil {
		slog.Warn("webauthn BeginRegistration failed", "err", err)
		return nil, "", errors.New(500, "生成 Passkey 注册参数失败")
	}
	key := auth.RandomHex(16)
	s.passkeyCeremonies.put(key, passkeyCeremony{session: *sd})
	return creation, key, nil
}

// FinishPasskeyRegistration 完成注册仪式并落库凭据。
// r.Body 必须是浏览器 navigator.credentials.create() 的 JSON 结果
// （ceremonyKey/name 经 query 传递，不与凭据 JSON 混在同一 body）。
func (s *AuthService) FinishPasskeyRegistration(ctx context.Context, u *model.User, ceremonyKey, name string, r *http.Request) (*model.Passkey, error) {
	wa := s.PasskeyWebAuthn()
	if wa == nil {
		return nil, errors.New(503, "Passkey 功能未启用")
	}
	cer, ok := s.passkeyCeremonies.take(ceremonyKey)
	if !ok || cer.discoverable {
		return nil, errors.New(400, "注册会话无效或已过期，请重新开始")
	}
	credential, err := wa.FinishRegistration(passkeyUser{s: s, user: u}, cer.session, r)
	if err != nil {
		return nil, errors.New(400, "Passkey 注册校验失败：请确认使用的是本站发起的验证器")
	}
	pk := &model.Passkey{
		CredentialID:    encodeB64URL(credential.ID),
		UserID:          u.ID,
		UID:             u.UID,
		Name:            trimPasskeyName(name),
		PublicKey:       encodeB64URL(credential.PublicKey),
		AttestationType: credential.AttestationType,
		SignCount:       credential.Authenticator.SignCount,
		UVE:             credential.Flags.BackupEligible,
		BackupState:     credential.Flags.BackupState,
	}
	for _, t := range credential.Transport {
		pk.Transport = append(pk.Transport, string(t))
	}
	if err := s.Repos.Passkeys.Create(ctx, pk); err != nil {
		if repository.IsDuplicateKey(err) {
			return nil, errors.New(400, "该 Passkey 已注册过")
		}
		return nil, errors.New(500, "服务器错误")
	}
	s.Audit(ctx, u, "PASSKEY_ADDED", "auth", "User added a passkey", "")
	return pk, nil
}

// BeginPasskeyLogin 生成登录仪式 options。identifier 为空 → 可发现凭据
// （无用户名登录）；非空（用户名或邮箱）→ 限定该用户的 allowCredentials。
// 账号不存在时返回可发现模式 options（不泄露"账号不存在"差异）。
func (s *AuthService) BeginPasskeyLogin(ctx context.Context, identifier string) (*protocol.CredentialAssertion, string, error) {
	wa := s.PasskeyWebAuthn()
	if wa == nil {
		return nil, "", errors.New(503, "Passkey 功能未启用")
	}
	var (
		assertion *protocol.CredentialAssertion
		sd        *webauthnlib.SessionData
		err       error
	)
	id := trimLower(identifier)
	if id == "" {
		assertion, sd, err = wa.BeginDiscoverableLogin()
	} else if user, ferr := s.Repos.Users.FindByLoginIdentifier(ctx, id); ferr != nil {
		assertion, sd, err = wa.BeginDiscoverableLogin()
	} else {
		assertion, sd, err = wa.BeginLogin(passkeyUser{s: s, user: user})
	}
	if err != nil {
		slog.Warn("webauthn BeginLogin failed", "err", err)
		return nil, "", errors.New(500, "生成 Passkey 登录参数失败")
	}
	key := auth.RandomHex(16)
	cer := passkeyCeremony{session: *sd, discoverable: id == ""}
	// BeginLogin 成功时记录限定用户；可发现模式留空。
	if id != "" {
		if user, ferr := s.Repos.Users.FindByLoginIdentifier(ctx, id); ferr == nil {
			cer.expectedUserID = user.ID.Hex()
		}
	}
	s.passkeyCeremonies.put(key, cer)
	return assertion, key, nil
}

// finishPasskeyLogin 完成登录仪式：校验签名、按 user handle 解析用户、
// 更新签名计数与备份标志。返回 (用户, 凭据)。
func (s *AuthService) finishPasskeyLogin(ctx context.Context, ceremonyKey string, r *http.Request) (*model.User, *webauthnlib.Credential, error) {
	wa := s.PasskeyWebAuthn()
	if wa == nil {
		return nil, nil, errors.New(503, "Passkey 功能未启用")
	}
	cer, ok := s.passkeyCeremonies.take(ceremonyKey)
	if !ok {
		return nil, nil, errors.New(400, "登录会话无效或已过期，请重新开始")
	}

	var (
		credential *webauthnlib.Credential
		err        error
	)
	if cer.discoverable {
		// 可发现凭据：先解析浏览器请求体，再按 user handle（UID 大端 8 字节）查用户。
		parsedResponse, perr := protocol.ParseCredentialRequestResponseBody(r.Body)
		if perr != nil {
			return nil, nil, errors.New(400, "Passkey 校验失败：请重试或改用密码登录")
		}
		credential, err = wa.ValidateDiscoverableLogin(func(rawID, userHandle []byte) (webauthnlib.User, error) {
			uid, uerr := uidFromBytes(userHandle)
			if uerr != nil {
				return nil, fmt.Errorf("invalid user handle")
			}
			u, ferr := s.Repos.Users.FindByUID(ctx, uid)
			if ferr != nil {
				return nil, fmt.Errorf("user not found for handle")
			}
			return passkeyUser{s: s, user: u}, nil
		}, cer.session, parsedResponse)
	} else {
		expected, ferr := s.Repos.Users.FindByID(ctx, cer.expectedUserID)
		if ferr != nil {
			return nil, nil, errors.New(400, "登录会话无效或已过期，请重新开始")
		}
		credential, err = wa.FinishLogin(passkeyUser{s: s, user: expected}, cer.session, r)
	}
	if err != nil {
		return nil, nil, errors.New(400, "Passkey 校验失败：请重试或改用密码登录")
	}

	// 按凭据 ID 找回我们的记录（user handle 校验已保证账号正确）。
	pk, perr := s.Repos.Passkeys.FindByCredentialID(ctx, encodeB64URL(credential.ID))
	if perr != nil {
		return nil, nil, errors.New(400, "该 Passkey 已被删除，请改用密码登录")
	}
	user, uerr := s.Repos.Users.FindByIDWithAuth(ctx, pk.UserID)
	if uerr != nil {
		return nil, nil, errors.New(400, "用户不存在")
	}
	// 签名计数更新（凭据克隆检测）+ 备份标志同步。
	if credential.Authenticator.SignCount > pk.SignCount || credential.Flags.BackupState != pk.BackupState {
		_ = s.Repos.Passkeys.UpdateSignCount(ctx, pk.ID, credential.Authenticator.SignCount)
		_ = s.Repos.Passkeys.UpdateNameAndBackupState(ctx, pk.ID, pk.Name,
			credential.Flags.BackupEligible, credential.Flags.BackupState)
	}
	return user, credential, nil
}

// PasskeyLogin POST /api/auth/passkey/verify：Passkey 单因素登录直接通过，
// 即使账号开启 2FA 也不再挑战第二因素（抗钓鱼的硬件级因素视为足够）。
// 通过后走共享门槛：注销宽限/邮箱未验证 → 签发会话 + 新设备通知。
func (s *AuthService) PasskeyLogin(c *gin.Context, ceremonyKey string) (*LoginResult, error) {
	ctx := c.Request.Context()
	user, _, err := s.finishPasskeyLogin(ctx, ceremonyKey, c.Request)
	if err != nil {
		return nil, err
	}

	if user.DeletionRequestedAt != nil {
		deleteAt := user.DeletionRequestedAt.Add(DeletionGraceDays * 24 * time.Hour)
		if time.Now().After(deleteAt) {
			if err := s.purgeUser(ctx, user.ID); err == nil {
				return nil, errors.New(400, "用户名或密码错误")
			}
		}
	}
	if !user.IsEmailVerified && !s.SkipVerification(user) && !s.devTokenActive(c.GetHeader("x-dev-token")) {
		verifyCode := s.EmailVerifyCodes.Mint(code.Entry{
			UserID:    user.ID.Hex(),
			Email:     user.Email,
			ExpiresAt: time.Now().Add(CodeTTL),
			Attempts:  0,
		})
		if s.Mail != nil {
			go s.Mail.SendVerificationCodeEmail(context.Background(), user.Email, verifyCode, "login")
		}
		return &LoginResult{Email: user.Email, NeedVerification: true}, nil
	}

	ua := c.Request.UserAgent()
	parsed := auth.ParseUserAgent(ua)
	di := auth.BuildDeviceInfo(nil, parsed, ua, c.GetHeader("Accept-Language"))
	ip := s.ClientIP(c)
	region := "未知"
	if s.IPRegion != nil {
		region = s.IPRegion.GetRegion(ctx, ip)
	}
	now := time.Now()
	_ = s.Repos.Users.UpdateDeviceInfoAndLogin(ctx, user.ID, di, now, ip, region)
	if err := s.IssueSession(c, user, di, ip, "LOGIN_PASSKEY"); err != nil {
		return nil, errors.New(500, "服务器错误")
	}
	if s.Mail != nil && !s.isKnownDevice(ctx, c, user.ID) {
		go s.sendNewDeviceNotification(user, parsed, ip, region)
	}
	return &LoginResult{User: user}, nil
}

// ListPasskeys 列出用户凭据。
func (s *AuthService) ListPasskeys(ctx context.Context, userID any) ([]model.Passkey, error) {
	return s.Repos.Passkeys.FindByUser(ctx, userID)
}

// DeletePasskey 删除用户的一条凭据（属主校验在 repo filter）。
func (s *AuthService) DeletePasskey(ctx context.Context, u *model.User, pkID any) error {
	if err := s.Repos.Passkeys.DeleteByID(ctx, pkID, u.ID); err != nil {
		return errors.New(500, "服务器错误")
	}
	s.Audit(ctx, u, "PASSKEY_REMOVED", "auth", "User removed a passkey", "")
	return nil
}

// trimPasskeyName 清洗用户自定义设备名（空则给默认名）。
func trimPasskeyName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "我的 Passkey"
	}
	if len([]rune(name)) > 40 {
		name = string([]rune(name)[:40])
	}
	return name
}

func trimLower(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// encodeB64URL / decodeB64URLOrRaw 凭据字节 ↔ base64url（无填充）编码。
func encodeB64URL(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeB64URLOrRaw(s string) []byte {
	if b, err := base64.RawURLEncoding.DecodeString(s); err == nil {
		return b
	}
	return []byte(s)
}
