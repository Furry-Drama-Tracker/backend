// Package config 加载 neo-server 配置：ini 为主，环境变量覆盖。
//
// 设计（用户已确认 Q10/Q14）：
//   - 默认读取 OS 对应的默认路径（Linux: /etc/furry-drama-tracker/backend.ini，macOS: /Library/Application Support/furry-drama-tracker/backend.ini，Windows: C:\ProgramData\furry-drama-tracker\backend.ini），--config=/path 覆盖
//   - 同名环境变量优先于 ini（供 systemd 注入密钥、CI 注入 DEV_API_TOKEN 等）
//   - 启动致命校验：缺少 JWT secret 或 MONGO_URI，或 JWT secret < 32 字符 → 退出
//
// 兼容既有 .env 变量名（运行时契约）：JWT_SECRET / MONGO_URI / ENCRYPTION_KEY / ALTCHA_HMAC_KEY /
// FRONTEND_URL / SITE_URL / ALLOWED_ORIGINS / EMAIL_* / VAPID_* / DEMO_EMAILS /
// DEV_API_TOKEN / PORT / NODE_ENV / SKIP_RATE_LIMIT。
//
// .env 文件支持：启动时自动读取当前目录的 .env 文件（如果存在），
// 将其中未在真实环境中设置的变量注入到 os.Environ，实现与 .env 惯例的向下兼容。
// 优先级：真实环境变量 > .env 文件 > ini 配置文件。
package config

import (
	"fmt"
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"gopkg.in/ini.v1"
)

const (
	// MinSecretLen 是启动致命校验的 secret 最小长度。
	MinSecretLen = 32

	defaultFrontendURL = "http://localhost:3000"
	defaultUploadsDir  = "./uploads"
	defaultIconsDir    = "./uploads/icons"
	// 默认开发源清单（本地开发端口）。
	defaultOrigins = "http://localhost:3000,http://localhost:3001,http://localhost:3002,http://localhost:5000"
)

// WebAuthnConfig Passkey（WebAuthn）配置。
type WebAuthnConfig struct {
	// RPID 是 WebAuthn Relying Party ID（通常是站点域名，如 "furry09.com"）。
	// 为空时从 FRONTEND_URL/SITE_URL 的 host 推导。
	RPID string
	// RPName 是注册时展示给用户的站点名（空时用 email FromName 或域名）。
	RPName string
	// Origins 是允许的 WebAuthn 仪式来源（完整 origin，如 https://xxx）。
	// 为空时从 FRONTEND_URL/SITE_URL 推导；开发环境自动追加 localhost:3000-3002。
	Origins []string
}

// OAuthConfig 外部身份提供方配置（QQ 互联预留；上线前仅占位）。
type OAuthConfig struct {
	QQAppID  string
	QQAppKey string
}

// Config 是全部运行配置。
type Config struct {
	Server   ServerConfig
	Database DatabaseConfig
	JWT      JWTConfig
	Email    EmailConfig
	VAPID    VAPIDConfig
	Security SecurityConfig
	WebAuthn WebAuthnConfig
	OAuth    OAuthConfig
	// IsDev 等价于 NODE_ENV !== 'production'，控制 cookie secure、错误栈、CORS 等。
	IsDev bool
}

// ServerConfig 服务监听与站点配置。
type ServerConfig struct {
	// Listen 是监听地址，形如 "tcp:0.0.0.0:5000" 或 "unix:/run/xxx.sock"（"@name" 为 abstract）。
	Listen string
	Port   int
	// NodeEnv 等价于 NODE_ENV（development|production）。
	NodeEnv     string
	FrontendURL string
	SiteURL     string
	UploadsDir  string
	// IconsDir 是 SVG 图标文件的存储目录（可配置；默认 ./uploads/icons）。
	IconsDir     string
	AllowOrigins []string
	// InsecureOrigins 表示生产环境 CORS 源仍是 localhost/example.com 等占位值。
	// 仅告警不阻止启动，避免已有服务因配置检查而中断。
	InsecureOrigins bool

	// TrustProxyHeaders 是否信任反向代理标头识别客户端 IP（gin ClientIP）。
	// nil=未配置 → 沿用历史行为：生产（!IsDev）信任、开发不信任。
	TrustProxyHeaders *bool
	// ProxyIPHeaders 是按序尝试的代理标头清单（c.ClientIP 逐个取首个有效 IP）。
	// 缺省 CF-Connecting-IP（ESA/CF 等边缘覆盖写入，最可信）> X-Real-IP > X-Forwarded-For。
	ProxyIPHeaders []string
	// TrustedProxies 是受信代理的 IP/CIDR 清单；nil=未配置 → gin 默认信任全部
	// 来源（等价于历史手写实现：不校验请求来源）。配置后（如本机反代 127.0.0.1,::1）
	// 非受信来源直连伪造的代理标头将被整体忽略。
	TrustedProxies []string
	// ProxyTrustedPlatform 是无条件最高优先的单一标头（gin TrustedPlatform）。
	// 空=不启用。该分支不校验请求来源与 IP 格式（非空即信），
	// 仅应在前置网关保证覆盖写入该头时设置。
	ProxyTrustedPlatform string
}

// DatabaseConfig MongoDB 连接配置。
type DatabaseConfig struct {
	URI    string
	Name   string
	Pool   int
	Min    int
	Select int
	Sock   int
}

// JWTConfig 令牌与密钥。
type JWTConfig struct {
	Secret        string
	EncryptionKey string
	AltchaHMACKey string
	AccessTTL     time.Duration
	RefreshTTL    time.Duration
	DevAPIToken   string
	DemoEmails    []string
	// WeakSecret 表示当前 secret 是示例/历史弱密钥。生产环境仅告警，不阻止启动，
	// 以便已有服务平滑升级；后续应尽快轮换。
	WeakSecret bool
}

// EmailConfig SMTP 配置（EMAail_* 回退值；运行时优先 SiteContent 配置）。
type EmailConfig struct {
	Host     string
	Port     int
	User     string
	Pass     string
	FromName string
}

// VAPIDConfig Web Push 配置。
type VAPIDConfig struct {
	PublicKey  string
	PrivateKey string
	Subject    string
}

// SecurityConfig 安全与限流参数。
type SecurityConfig struct {
	LoginMaxAttempts int
	LoginLockMinutes int
	CSRFMaxAgeHours  int
	RateLimitSkip    bool
	// UsernameBlacklist 是用户名黑名单补充词表（逗号分隔，[security].username_blacklist
	// 或 USERNAME_BLACKLIST）。与内置词表合并后做子串匹配。
	UsernameBlacklist string
	// LoginNameCooldownDays 是改名冷却天数（默认 7）。
	LoginNameCooldownDays int
	// LoginNameReserveDays 是旧名保留天数（默认 90，期间他人不可注册该名）。
	LoginNameReserveDays int
	// IPRegionEnabled 控制 IP → 地域解析（登录/注册/设备确认记录与提醒邮件）。
	// 默认 false：该能力会外呼第三方位置服务，默认关闭；
	// 需要时用 [security].ip_region_enabled 或 IP_REGION_ENABLED 显式开启，
	// 待换成本地离线库后可放开（见 docs/decisions.md ADR-017/ADR-018）。
	IPRegionEnabled bool
	// IPRegionProvider 是首选数据源：aliyun（默认，阿里云云市场纯真 API）或
	// czdb（纯真离线库）。另一数据源配置齐全时自动兜底（见 ADR-018）。
	IPRegionProvider string
	// IPRegionAppCode 是阿里云云市场纯真 IP 地域 API（cmapi00053387）的鉴权凭证。
	// ini [security].ip_region_appcode 或环境变量 IP_REGION_APPCODE。
	IPRegionAppCode string
	// IPRegionCzdbPathV4 / IPRegionCzdbPathV6 分别是纯真离线库（czdb）的
	// IPv4/IPv6 库文件路径（.czdb），IPRegionCzdbKey 是配套解密密钥
	// （两库共用，随库由纯真官方提供）。
	IPRegionCzdbPathV4 string
	IPRegionCzdbPathV6 string
	IPRegionCzdbKey    string
}

// Load 加载配置：先读 .env（如果存在），再读 ini（path 为空则尝试 DefaultPath，
// 缺文件静默跳过），最后用环境变量覆盖同名项。
//
// 优先级：真实环境变量 > .env 文件 > ini 配置文件 > 默认值。
func Load(path string) (*Config, error) {
	cfg := defaults()

	// 0) 加载 .env 文件（当前目录，如果存在）——仅为未在真实环境中设置的变量注入值
	loadDotEnv()
	// 1) ini
	if path == "" {
		path = DefaultPath
	}
	if data, err := os.ReadFile(path); err == nil {
		if err := applyINI(cfg, data); err != nil {
			return nil, fmt.Errorf("解析配置文件 %s 失败: %w", path, err)
		}
	} else if path != DefaultPath {
		return nil, fmt.Errorf("配置文件不存在: %s", path)
	}

	// 2) env 覆盖（真实环境变量 + .env 注入的变量）
	applyEnv(cfg)

	// 3) 派生 IsDev
	cfg.IsDev = !strings.EqualFold(cfg.Server.NodeEnv, "production")

	// 4) CORS 源兜底：开发环境允许 localhost；生产环境从站点地址派生，
	// 避免 ALLOWED_ORIGINS 留空时把所有同源 POST 都拒绝。
	if len(cfg.Server.AllowOrigins) == 0 {
		if cfg.IsDev {
			cfg.Server.AllowOrigins = splitTrim(defaultOrigins)
		} else {
			for _, u := range []string{cfg.Server.FrontendURL, cfg.Server.SiteURL} {
				u = strings.TrimRight(strings.TrimSpace(u), "/")
				if u != "" {
					cfg.Server.AllowOrigins = append(cfg.Server.AllowOrigins, u)
				}
			}
		}
	}

	// 5) WebAuthn 兜底：RPID/Origins 未配置时从站点地址 host 推导；
	//    开发环境自动允许 localhost:3000-3002（对齐 CORS 默认源）。
	if cfg.WebAuthn.RPID == "" {
		if u := originHost(cfg.Server.FrontendURL); u != "" {
			cfg.WebAuthn.RPID = u
		} else if u := originHost(cfg.Server.SiteURL); u != "" {
			cfg.WebAuthn.RPID = u
		}
	}
	if len(cfg.WebAuthn.Origins) == 0 {
		for _, u := range []string{cfg.Server.FrontendURL, cfg.Server.SiteURL} {
			u = strings.TrimRight(strings.TrimSpace(u), "/")
			if u != "" {
				cfg.WebAuthn.Origins = append(cfg.WebAuthn.Origins, u)
			}
		}
	}
	if cfg.IsDev {
		for _, o := range []string{"http://localhost:3000", "http://localhost:3001", "http://localhost:3002"} {
			if !containsStr(cfg.WebAuthn.Origins, o) {
				cfg.WebAuthn.Origins = append(cfg.WebAuthn.Origins, o)
			}
		}
	}
	if cfg.WebAuthn.RPName == "" {
		if cfg.Email.FromName != "" {
			cfg.WebAuthn.RPName = cfg.Email.FromName
		} else {
			cfg.WebAuthn.RPName = cfg.WebAuthn.RPID
		}
	}

	// 6) 致命校验
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// loadDotEnv 读取当前目录的 .env 文件（如果存在），将其中未在真实环境中
// 已设置的变量注入到 os.Environ。委托给 godotenv：支持 # 注释、export 前缀、
// 引号包裹的值等 dotenv 语法。文件不存在时静默跳过。
//
// godotenv.Load 默认 Overload=false，仅在变量尚未设置时注入，
// 因此真实环境变量优先级高于 .env 文件。
func loadDotEnv() {
	_ = godotenv.Load() // 文件不存在时静默跳过
}

// DefaultPath 是配置文件默认路径，按 OS 区分：
//   - Windows:  C:\ProgramData\furry-drama-tracker\backend.ini
//   - Linux:    /etc/furry-drama-tracker/backend.ini
//   - macOS:    /Library/Application Support/furry-drama-tracker/backend.ini
var DefaultPath string

func init() {
	switch runtime.GOOS {
	case "windows":
		DefaultPath = `C:\ProgramData\furry-drama-tracker\backend.ini`
	case "darwin":
		DefaultPath = "/Library/Application Support/furry-drama-tracker/backend.ini"
	default: // linux 及其它 Unix
		DefaultPath = "/etc/furry-drama-tracker/backend.ini"
	}
}

// defaults 返回带默认值的配置。
func defaults() *Config {
	return &Config{
		Server: ServerConfig{
			Listen:       "tcp:127.0.0.1:5000",
			Port:         5000,
			NodeEnv:      "development",
			FrontendURL:  defaultFrontendURL,
			SiteURL:      defaultFrontendURL,
			UploadsDir:   defaultUploadsDir,
			IconsDir:     defaultIconsDir,
			AllowOrigins: nil,
			ProxyIPHeaders: []string{
				"CF-Connecting-IP", "X-Real-IP", "X-Forwarded-For",
			},
		},
		Database: DatabaseConfig{
			Pool: 10, Min: 2, Select: 10, Sock: 45,
		},
		JWT: JWTConfig{
			AccessTTL:  15 * time.Minute,
			RefreshTTL: 7 * 24 * time.Hour,
			DemoEmails: []string{"demo@furry09.com"},
		},
		Email: EmailConfig{Port: 465},
		Security: SecurityConfig{
			LoginMaxAttempts:      5,
			LoginLockMinutes:      30,
			CSRFMaxAgeHours:       24,
			IPRegionProvider:      "aliyun",
			LoginNameCooldownDays: 7,
			LoginNameReserveDays:  90,
		},
	}
}

// applyINI 把 ini 内容合并进 cfg（仅覆盖已存在的键）。
func applyINI(cfg *Config, data []byte) error {
	f, err := ini.Load(data)
	if err != nil {
		return err
	}
	server := f.Section("server")
	cfg.Server.Listen = server.Key("listen").String()
	cfg.Server.FrontendURL = firstNonEmpty(server.Key("frontend_url").String(), cfg.Server.FrontendURL)
	cfg.Server.SiteURL = firstNonEmpty(server.Key("site_url").String(), cfg.Server.SiteURL)
	cfg.Server.UploadsDir = firstNonEmpty(server.Key("uploads_dir").String(), cfg.Server.UploadsDir)
	cfg.Server.IconsDir = firstNonEmpty(server.Key("icons_dir").String(), cfg.Server.IconsDir)
	if v := server.Key("allow_origins").String(); v != "" {
		cfg.Server.AllowOrigins = splitTrim(v)
	}
	if v := server.Key("node_env").String(); v != "" {
		cfg.Server.NodeEnv = v
	}
	if port := server.Key("port").MustInt(0); port > 0 {
		cfg.Server.Port = port
	}
	if v := server.Key("trust_proxy_headers").String(); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.Server.TrustProxyHeaders = &b
		}
	}
	if v := server.Key("proxy_ip_headers").String(); v != "" {
		cfg.Server.ProxyIPHeaders = splitTrim(v)
	}
	if v := server.Key("trusted_proxies").String(); v != "" {
		cfg.Server.TrustedProxies = splitTrim(v)
	}
	cfg.Server.ProxyTrustedPlatform = server.Key("proxy_trusted_platform").String()

	db := f.Section("database")
	cfg.Database.URI = db.Key("uri").String()
	cfg.Database.Name = db.Key("name").String()
	if v := db.Key("pool_size").MustInt(0); v > 0 {
		cfg.Database.Pool = v
	}

	jwt := f.Section("jwt")
	cfg.JWT.Secret = jwt.Key("secret").String()
	cfg.JWT.EncryptionKey = jwt.Key("encryption_key").String()
	cfg.JWT.AltchaHMACKey = jwt.Key("altcha_hmac_key").String()
	if v := jwt.Key("access_ttl").String(); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.JWT.AccessTTL = d
		}
	}
	if v := jwt.Key("refresh_ttl").String(); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.JWT.RefreshTTL = d
		}
	}
	cfg.JWT.DevAPIToken = jwt.Key("dev_api_token").String()
	if v := jwt.Key("demo_emails").String(); v != "" {
		cfg.JWT.DemoEmails = splitTrim(v)
	}

	email := f.Section("email")
	cfg.Email.Host = email.Key("host").String()
	cfg.Email.Port = email.Key("port").MustInt(465)
	cfg.Email.User = email.Key("user").String()
	cfg.Email.Pass = email.Key("pass").String()
	cfg.Email.FromName = email.Key("from_name").String()

	vapid := f.Section("vapid")
	cfg.VAPID.PublicKey = vapid.Key("public_key").String()
	cfg.VAPID.PrivateKey = vapid.Key("private_key").String()
	cfg.VAPID.Subject = vapid.Key("subject").String()

	wa := f.Section("webauthn")
	cfg.WebAuthn.RPID = wa.Key("rp_id").String()
	cfg.WebAuthn.RPName = wa.Key("rp_name").String()
	if v := wa.Key("origins").String(); v != "" {
		cfg.WebAuthn.Origins = splitTrim(v)
	}

	oa := f.Section("oauth")
	cfg.OAuth.QQAppID = oa.Key("qq_app_id").String()
	cfg.OAuth.QQAppKey = oa.Key("qq_app_key").String()

	sec := f.Section("security")
	cfg.Security.LoginMaxAttempts = sec.Key("login_max_attempts").MustInt(5)
	cfg.Security.LoginLockMinutes = sec.Key("login_lock_minutes").MustInt(30)
	cfg.Security.CSRFMaxAgeHours = sec.Key("csrf_max_age_h").MustInt(24)
	if v := sec.Key("username_blacklist").String(); v != "" {
		cfg.Security.UsernameBlacklist = v
	}
	if v := sec.Key("login_name_cooldown_days").MustInt(0); v > 0 {
		cfg.Security.LoginNameCooldownDays = v
	}
	if v := sec.Key("login_name_reserve_days").MustInt(0); v > 0 {
		cfg.Security.LoginNameReserveDays = v
	}
	// 缺省 false：IP 地域解析默认关闭（数据出境合规，ADR-017）。
	cfg.Security.IPRegionEnabled = sec.Key("ip_region_enabled").MustBool(false)
	provider := strings.ToLower(strings.TrimSpace(sec.Key("ip_region_provider").String()))
	if provider != "" {
		cfg.Security.IPRegionProvider = provider
	}
	cfg.Security.IPRegionAppCode = strings.TrimSpace(sec.Key("ip_region_appcode").String())
	cfg.Security.IPRegionCzdbPathV4 = strings.TrimSpace(sec.Key("ip_region_czdb_path_v4").String())
	cfg.Security.IPRegionCzdbPathV6 = strings.TrimSpace(sec.Key("ip_region_czdb_path_v6").String())
	cfg.Security.IPRegionCzdbKey = strings.TrimSpace(sec.Key("ip_region_czdb_key").String())
	return nil
}

// applyEnv 用环境变量覆盖 ini 值。
func applyEnv(cfg *Config) {
	setStr(&cfg.JWT.Secret, os.Getenv("JWT_SECRET"))
	setStr(&cfg.Database.URI, os.Getenv("MONGO_URI"))
	setStr(&cfg.JWT.EncryptionKey, os.Getenv("ENCRYPTION_KEY"))
	setStr(&cfg.JWT.AltchaHMACKey, os.Getenv("ALTCHA_HMAC_KEY"))
	setStr(&cfg.Server.FrontendURL, os.Getenv("FRONTEND_URL"))
	setStr(&cfg.Server.SiteURL, os.Getenv("SITE_URL"))
	setStr(&cfg.Server.IconsDir, os.Getenv("ICONS_DIR"))
	if v := os.Getenv("ALLOWED_ORIGINS"); v != "" {
		cfg.Server.AllowOrigins = splitTrim(v)
	}
	setStr(&cfg.Email.Host, os.Getenv("EMAIL_HOST"))
	if v := os.Getenv("EMAIL_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			cfg.Email.Port = p
		}
	}
	setStr(&cfg.Email.User, os.Getenv("EMAIL_USER"))
	setStr(&cfg.Email.Pass, os.Getenv("EMAIL_PASS"))
	setStr(&cfg.Email.FromName, os.Getenv("EMAIL_FROM_NAME"))
	setStr(&cfg.VAPID.PublicKey, os.Getenv("VAPID_PUBLIC_KEY"))
	setStr(&cfg.VAPID.PrivateKey, os.Getenv("VAPID_PRIVATE_KEY"))
	setStr(&cfg.VAPID.Subject, os.Getenv("VAPID_SUBJECT"))
	if v := os.Getenv("DEMO_EMAILS"); v != "" {
		cfg.JWT.DemoEmails = splitTrim(v)
	}
	setStr(&cfg.JWT.DevAPIToken, os.Getenv("DEV_API_TOKEN"))
	if v := os.Getenv("PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			cfg.Server.Port = p
		}
	}
	if v := os.Getenv("NODE_ENV"); v != "" {
		cfg.Server.NodeEnv = v
	}
	if v := os.Getenv("TRUST_PROXY_HEADERS"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.Server.TrustProxyHeaders = &b
		}
	}
	if v := os.Getenv("PROXY_IP_HEADERS"); v != "" {
		cfg.Server.ProxyIPHeaders = splitTrim(v)
	}
	if v := os.Getenv("TRUSTED_PROXIES"); v != "" {
		cfg.Server.TrustedProxies = splitTrim(v)
	}
	setStr(&cfg.Server.ProxyTrustedPlatform, os.Getenv("PROXY_TRUSTED_PLATFORM"))
	if v := os.Getenv("SKIP_RATE_LIMIT"); v == "1" {
		cfg.Security.RateLimitSkip = true
	}
	if v := os.Getenv("IP_REGION_ENABLED"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.Security.IPRegionEnabled = b
		}
	}
	if v := os.Getenv("IP_REGION_PROVIDER"); v != "" {
		cfg.Security.IPRegionProvider = strings.ToLower(strings.TrimSpace(v))
	}
	setStr(&cfg.Security.IPRegionAppCode, os.Getenv("IP_REGION_APPCODE"))
	setStr(&cfg.Security.IPRegionCzdbPathV4, os.Getenv("IP_REGION_CZDB_PATH_V4"))
	setStr(&cfg.Security.IPRegionCzdbPathV6, os.Getenv("IP_REGION_CZDB_PATH_V6"))
	setStr(&cfg.Security.IPRegionCzdbKey, os.Getenv("IP_REGION_CZDB_KEY"))
	setStr(&cfg.Security.UsernameBlacklist, os.Getenv("USERNAME_BLACKLIST"))
	if v := os.Getenv("LOGIN_NAME_COOLDOWN_DAYS"); v != "" {
		if d, err := strconv.Atoi(v); err == nil && d > 0 {
			cfg.Security.LoginNameCooldownDays = d
		}
	}
	if v := os.Getenv("LOGIN_NAME_RESERVE_DAYS"); v != "" {
		if d, err := strconv.Atoi(v); err == nil && d > 0 {
			cfg.Security.LoginNameReserveDays = d
		}
	}
	setStr(&cfg.WebAuthn.RPID, os.Getenv("WEBAUTHN_RP_ID"))
	setStr(&cfg.WebAuthn.RPName, os.Getenv("WEBAUTHN_RP_NAME"))
	if v := os.Getenv("WEBAUTHN_ORIGINS"); v != "" {
		cfg.WebAuthn.Origins = splitTrim(v)
	}
	setStr(&cfg.OAuth.QQAppID, os.Getenv("QQ_APPID"))
	setStr(&cfg.OAuth.QQAppKey, os.Getenv("QQ_APPKEY"))
}

// validate 执行启动致命校验。
func (c *Config) validate() error {
	if c.JWT.Secret == "" {
		return fmt.Errorf("缺少 JWT secret（ini [jwt].secret 或环境变量 JWT_SECRET）")
	}
	if len(c.JWT.Secret) < MinSecretLen {
		return fmt.Errorf("JWT secret 长度必须 >= %d 字符", MinSecretLen)
	}
	// 弱密钥只告警不阻止启动：已有服务可能正在使用历史密钥，
	// 强制失败会导致拉取新代码后无法启动。main 会输出醒目安全警告。
	c.JWT.WeakSecret = isWeakSecret(c.JWT.Secret)
	if c.JWT.EncryptionKey != "" && len(c.JWT.EncryptionKey) < MinSecretLen {
		return fmt.Errorf("ENCRYPTION_KEY 长度必须 >= %d 字符", MinSecretLen)
	}
	if c.Database.URI == "" {
		return fmt.Errorf("缺少 MONGO_URI（ini [database].uri 或环境变量 MONGO_URI）")
	}
	// trusted_proxies 是启动致命校验：格式错误直接退出，避免到装配阶段
	// 才发现（SetTrustedProxies 失败会静默退回信任全部来源的错误状态）。
	for _, p := range c.Server.TrustedProxies {
		if net.ParseIP(p) == nil {
			if _, _, err := net.ParseCIDR(p); err != nil {
				return fmt.Errorf("trusted_proxies 项 %q 不是合法 IP 或 CIDR 段", p)
			}
		}
	}
	if !c.IsDev {
		if len(c.Server.AllowOrigins) == 0 {
			c.Server.InsecureOrigins = true
		}
		for _, o := range c.Server.AllowOrigins {
			if strings.Contains(o, "localhost") || strings.Contains(o, "127.0.0.1") || strings.Contains(o, "example.com") {
				c.Server.InsecureOrigins = true
			}
		}
	}
	return nil
}

// isWeakSecret 拒绝模板/示例/历史默认密钥，避免“长度够但可预测”的密钥上线。
func isWeakSecret(s string) bool {
	l := strings.ToLower(strings.TrimSpace(s))
	if strings.HasPrefix(l, "change-me") || strings.Contains(l, "example") {
		return true
	}
	switch l {
	case "furry-drama-tracker-secret-key-2024",
		"secret", "jwt-secret", "changeme", "password":
		return true
	}
	return false
}

func setStr(dst *string, v string) {
	if v != "" {
		*dst = v
	}
}

func firstNonEmpty(v, fallback string) string {
	if v != "" {
		return v
	}
	return fallback
}

// splitTrim 按逗号切分并 trim 去空。
func splitTrim(s string) []string {
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

// originHost 提取 URL 的 host（去掉 scheme/端口默认语义，仅用于 WebAuthn RPID 推导）。
func originHost(u string) string {
	u = strings.TrimSpace(u)
	if u == "" {
		return ""
	}
	// 简单解析：去掉 scheme 后取到第一个 / 为止。
	if i := strings.Index(u, "://"); i >= 0 {
		u = u[i+3:]
	}
	if i := strings.IndexAny(u, "/?#"); i >= 0 {
		u = u[:i]
	}
	// 去掉端口（RPID 是纯域名，不含端口）。
	if i := strings.LastIndex(u, ":"); i >= 0 && !strings.Contains(u, "]") {
		u = u[:i]
	} else if i := strings.LastIndex(u, "]:"); i >= 0 {
		u = u[:i+1]
	}
	return strings.ToLower(strings.Trim(u, "[]"))
}

// containsStr 判断列表是否包含某字符串。
func containsStr(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
