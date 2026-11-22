package service

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	altcha "github.com/altcha-org/altcha-lib-go/v2"

	"github.com/Furry-Drama-Tracker/backend/internal/config"
)

const altchaTestSecret = "aaaabbbbccccddddeeeeffff00001111"

func altchaTestSvc() *AuthService {
	cfg := &config.Config{}
	cfg.JWT.AltchaHMACKey = altchaTestSecret
	return &AuthService{Config: cfg}
}

// TestAltchaOfficialRoundtrip 官方库自生成挑战 → 官方库求解 → 官方库验证（Argon2id）。
// 另已手动跨实现验证：官方库生成的挑战可被前端 widget（hash-wasm argon2id worker，
// 挑战显式携带 memoryCost/parallelism，不依赖两端缺省值）求解。
func TestAltchaOfficialRoundtrip(t *testing.T) {
	svc := altchaTestSvc()
	ch, err := svc.CreateCaptcha()
	if err != nil {
		t.Fatalf("create challenge: %v", err)
	}
	if ch.Parameters.Algorithm != "Argon2id" || ch.Parameters.Cost != 1 ||
		ch.Parameters.MemoryCost != 16384 || ch.Parameters.Parallelism != 1 ||
		ch.Parameters.KeyLength != 32 {
		t.Fatalf("challenge 参数不符: %+v", ch.Parameters)
	}
	// memoryCost/parallelism 必须出现在挑战 JSON 里：两端 KDF 缺省值不同
	// （Go 64MiB / widget 16MiB），字段缺失会让验证恒失败。
	raw, err := json.Marshal(ch)
	if err != nil {
		t.Fatalf("marshal challenge: %v", err)
	}
	if !strings.Contains(string(raw), `"memoryCost":16384`) ||
		!strings.Contains(string(raw), `"parallelism":1`) {
		t.Fatalf("挑战 JSON 缺少 memoryCost/parallelism: %s", raw)
	}
	sol, err := altcha.SolveChallenge(altcha.SolveChallengeOptions{
		Challenge: ch, DeriveKey: altcha.DeriveKeyArgon2id(),
	})
	if err != nil {
		t.Fatalf("solve: %v", err)
	}
	payload, _ := json.Marshal(altcha.Payload{Challenge: ch, Solution: *sol})
	payloadB64 := base64.StdEncoding.EncodeToString(payload)
	if !svc.VerifyAltcha(payloadB64, "") {
		t.Fatal("roundtrip 应 verified")
	}
}

// TestAltchaSingleAttempt 确定性 counter=0 模式：keyPrefix 即 counter=0 派生键的
// 前缀，求解首次尝试即命中——老设备的总工作量恰好为 1 次 Argon2id。
func TestAltchaSingleAttempt(t *testing.T) {
	svc := altchaTestSvc()
	ch, err := svc.CreateCaptcha()
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if len(ch.Parameters.KeyPrefix) != 32 {
		t.Fatalf("keyPrefix 应为 32 位 hex（16 字节）: %q", ch.Parameters.KeyPrefix)
	}
	sol, err := altcha.SolveChallenge(altcha.SolveChallengeOptions{
		Challenge: ch, DeriveKey: altcha.DeriveKeyArgon2id(),
	})
	if err != nil {
		t.Fatalf("solve: %v", err)
	}
	if sol.Counter != 0 {
		t.Fatalf("首次尝试（counter=0）即应命中，got counter=%d", sol.Counter)
	}
}

// TestAltchaTamperedRejected 篡改/非法 payload 应被拒绝。
func TestAltchaTamperedRejected(t *testing.T) {
	svc := altchaTestSvc()
	if svc.VerifyAltcha("not-a-valid-payload", "") {
		t.Fatal("非法 payload 应被拒绝")
	}
	if svc.VerifyAltcha("", "") {
		t.Fatal("空 payload 应被拒绝")
	}
	ch, err := svc.CreateCaptcha()
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	sol, err := altcha.SolveChallenge(altcha.SolveChallengeOptions{
		Challenge: ch, DeriveKey: altcha.DeriveKeyArgon2id(),
	})
	if err != nil {
		t.Fatalf("solve: %v", err)
	}
	// 篡改 counter → 派生键不匹配 → 拒绝。
	tampered := *sol
	tampered.Counter++
	payload, _ := json.Marshal(altcha.Payload{Challenge: ch, Solution: tampered})
	if svc.VerifyAltcha(base64.StdEncoding.EncodeToString(payload), "") {
		t.Fatal("篡改 counter 的 payload 应被拒绝")
	}
	// 篡改 derivedKey → 拒绝。
	tampered2 := *sol
	tampered2.DerivedKey = strings.Repeat("ab", len(sol.DerivedKey)/2)
	payload2, _ := json.Marshal(altcha.Payload{Challenge: ch, Solution: tampered2})
	if svc.VerifyAltcha(base64.StdEncoding.EncodeToString(payload2), "") {
		t.Fatal("篡改 derivedKey 的 payload 应被拒绝")
	}
}

// TestAltchaLegacySHAChallengeStillVerified 发布过渡期兼容：旧版本签发的
// SHA-256 挑战（5min 有效期内）在部署后仍可验证。
func TestAltchaLegacySHAChallengeStillVerified(t *testing.T) {
	svc := altchaTestSvc()
	expires := time.Now().Add(5 * time.Minute)
	ch, err := altcha.CreateChallenge(altcha.CreateChallengeOptions{
		Algorithm:           "SHA-256",
		DeriveKey:           altcha.DeriveKeySHA(),
		Cost:                10000,
		KeyLength:           32,
		KeyPrefix:           "00",
		HMACSignatureSecret: altchaTestSecret,
		ExpiresAt:           &expires,
	})
	if err != nil {
		t.Fatalf("create legacy challenge: %v", err)
	}
	sol, err := altcha.SolveChallenge(altcha.SolveChallengeOptions{
		Challenge: ch, DeriveKey: altcha.DeriveKeySHA(),
	})
	if err != nil {
		t.Fatalf("solve legacy: %v", err)
	}
	payload, _ := json.Marshal(altcha.Payload{Challenge: ch, Solution: *sol})
	if !svc.VerifyAltcha(base64.StdEncoding.EncodeToString(payload), "") {
		t.Fatal("过渡期 SHA-256 挑战应仍可验证")
	}
}

// TestAltchaUnknownAlgorithmRejected 未知算法 fail-closed：改写 algorithm 字段
// 后挑战签名必然失效，任何 solution 都被拒绝。
func TestAltchaUnknownAlgorithmRejected(t *testing.T) {
	svc := altchaTestSvc()
	expires := time.Now().Add(5 * time.Minute)
	ch, err := altcha.CreateChallenge(altcha.CreateChallengeOptions{
		Algorithm:           "SHA-256",
		DeriveKey:           altcha.DeriveKeySHA(),
		Cost:                1,
		KeyLength:           32,
		KeyPrefix:           "00",
		HMACSignatureSecret: altchaTestSecret,
		ExpiresAt:           &expires,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	sol, err := altcha.SolveChallenge(altcha.SolveChallengeOptions{
		Challenge: ch, DeriveKey: altcha.DeriveKeySHA(),
	})
	if err != nil {
		t.Fatalf("solve: %v", err)
	}
	ch.Parameters.Algorithm = "MD5"
	payload, _ := json.Marshal(altcha.Payload{Challenge: ch, Solution: *sol})
	if svc.VerifyAltcha(base64.StdEncoding.EncodeToString(payload), "") {
		t.Fatal("未知算法的 payload 应被拒绝")
	}
}

// TestAltchaDevTokenBypass dev-token 绕过（仅配置时）。
func TestAltchaDevTokenBypass(t *testing.T) {
	cfg := &config.Config{}
	cfg.JWT.AltchaHMACKey = altchaTestSecret
	cfg.JWT.DevAPIToken = "test-dev-token"
	svc := &AuthService{Config: cfg}
	if !svc.VerifyAltcha("anything", "test-dev-token") {
		t.Fatal("dev-token 应绕过 altcha")
	}
	if svc.VerifyAltcha("anything", "wrong-token") {
		t.Fatal("错误 dev-token 不应绕过")
	}
}

// TestAltchaHMACSecretDerivation 验证 HMAC secret 派生（sha256("altcha-"+JWT_SECRET) 固定派生规则，勿改）。
func TestAltchaHMACSecretDerivation(t *testing.T) {
	cfg := &config.Config{}
	cfg.JWT.Secret = "my-jwt-secret"
	svc := &AuthService{Config: cfg}
	// sha256("altcha-" + "my-jwt-secret") 的 hex。
	expected := "bc0734564b835612beb83b2fb8ae10679b39f58941b6e94ee044173c35f9fd85"
	if got := svc.AltchaHMACSecret(); got != expected {
		t.Fatalf("HMAC secret derivation mismatch:\n got %s\nwant %s", got, expected)
	}
	// 显式配置优先。
	cfg.JWT.AltchaHMACKey = "explicit-key"
	if got := svc.AltchaHMACSecret(); got != "explicit-key" {
		t.Fatalf("explicit config should win: %s", got)
	}
}

// TestAltchaCreateCaptchaExpiry 挑战带 5min 过期。
func TestAltchaCreateCaptchaExpiry(t *testing.T) {
	svc := altchaTestSvc()
	ch, err := svc.CreateCaptcha()
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	now := time.Now().Unix()
	if ch.Parameters.ExpiresAt <= now || ch.Parameters.ExpiresAt > now+600 {
		t.Fatalf("expiresAt 应在 5min 窗口内: %d (now=%d)", ch.Parameters.ExpiresAt, now)
	}
}
