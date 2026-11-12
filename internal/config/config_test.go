package config

import (
	"reflect"
	"testing"
)

func TestValidateKeepsLegacyProductionConfigRunning(t *testing.T) {
	c := &Config{
		Database: DatabaseConfig{URI: "mongodb://127.0.0.1:27017/furry", Name: "furry", Pool: 10},
		Server: ServerConfig{
			NodeEnv:      "production",
			AllowOrigins: []string{"https://example.com"},
		},
		JWT: JWTConfig{Secret: "furry-drama-tracker-secret-key-2024"},
	}
	c.IsDev = false

	if err := c.validate(); err != nil {
		t.Fatalf("legacy production config should not block startup: %v", err)
	}
	if !c.JWT.WeakSecret {
		t.Fatal("expected weak secret to be flagged")
	}
	if !c.Server.InsecureOrigins {
		t.Fatal("expected placeholder origin to be flagged")
	}
}

func TestValidateTrustedProxies(t *testing.T) {
	base := func() *Config {
		c := &Config{
			Database: DatabaseConfig{URI: "mongodb://127.0.0.1:27017/furry", Name: "furry", Pool: 10},
			Server: ServerConfig{
				NodeEnv:      "production",
				AllowOrigins: []string{"https://example.com"},
			},
			JWT: JWTConfig{Secret: "furry-drama-tracker-secret-key-2024"},
		}
		c.IsDev = false
		return c
	}

	// 合法 IP 与 CIDR 混合清单应通过。
	c := base()
	c.Server.TrustedProxies = []string{"127.0.0.1", "::1", "10.0.0.0/8"}
	if err := c.validate(); err != nil {
		t.Fatalf("合法 trusted_proxies 不应报错: %v", err)
	}

	// 非法项应阻止启动（避免装配阶段静默退回信任全部来源）。
	c = base()
	c.Server.TrustedProxies = []string{"127.0.0.1", "caddy"}
	if err := c.validate(); err == nil {
		t.Fatal("非法 trusted_proxies 项应阻止启动")
	}
}

func TestProxyIPConfigDefaultsAndOverrides(t *testing.T) {
	// 默认标头清单：CF（ESA/CF 边缘覆盖写入）优先，X-Real-IP/XFF 兜底。
	cfg := defaults()
	wantDefault := []string{"CF-Connecting-IP", "X-Real-IP", "X-Forwarded-For"}
	if !reflect.DeepEqual(cfg.Server.ProxyIPHeaders, wantDefault) {
		t.Fatalf("默认 proxy_ip_headers 应为 %v, got %v", wantDefault, cfg.Server.ProxyIPHeaders)
	}
	if cfg.Server.TrustProxyHeaders != nil {
		t.Fatal("缺省 trust_proxy_headers 应为 nil（沿用 !IsDev）")
	}
	if cfg.Server.TrustedProxies != nil {
		t.Fatal("缺省 trusted_proxies 应为 nil（gin 默认信任全部）")
	}

	// ini 覆盖。
	data := []byte(`
[server]
trust_proxy_headers = true
proxy_ip_headers = X-Real-IP , CF-Connecting-IP
trusted_proxies = 127.0.0.1, ::1
proxy_trusted_platform = True-Client-IP
`)
	if err := applyINI(cfg, data); err != nil {
		t.Fatalf("applyINI: %v", err)
	}
	if cfg.Server.TrustProxyHeaders == nil || !*cfg.Server.TrustProxyHeaders {
		t.Fatal("trust_proxy_headers = true 应解析为非 nil true")
	}
	if want := []string{"X-Real-IP", "CF-Connecting-IP"}; !reflect.DeepEqual(cfg.Server.ProxyIPHeaders, want) {
		t.Fatalf("proxy_ip_headers 应为 %v, got %v", want, cfg.Server.ProxyIPHeaders)
	}
	if want := []string{"127.0.0.1", "::1"}; !reflect.DeepEqual(cfg.Server.TrustedProxies, want) {
		t.Fatalf("trusted_proxies 应为 %v, got %v", want, cfg.Server.TrustedProxies)
	}
	if cfg.Server.ProxyTrustedPlatform != "True-Client-IP" {
		t.Fatalf("proxy_trusted_platform 不符: %q", cfg.Server.ProxyTrustedPlatform)
	}

	// 环境变量优先于 ini。
	t.Setenv("TRUST_PROXY_HEADERS", "0")
	t.Setenv("PROXY_IP_HEADERS", "CF-Connecting-IP")
	applyEnv(cfg)
	if cfg.Server.TrustProxyHeaders == nil || *cfg.Server.TrustProxyHeaders {
		t.Fatal("TRUST_PROXY_HEADERS=0 应覆盖为 false")
	}
	if want := []string{"CF-Connecting-IP"}; !reflect.DeepEqual(cfg.Server.ProxyIPHeaders, want) {
		t.Fatalf("PROXY_IP_HEADERS 应覆盖为 %v, got %v", want, cfg.Server.ProxyIPHeaders)
	}
}

// TestIPRegionDisabledByDefault 校验 IP 地域解析的数据出境合规开关：
// 缺省必须关闭；ini / 环境变量可显式开启或关闭（见 ADR-017）。
func TestIPRegionDisabledByDefault(t *testing.T) {
	cfg := defaults()
	if cfg.Security.IPRegionEnabled {
		t.Fatal("IP 地域解析缺省必须关闭（不外呼第三方）")
	}

	if err := applyINI(cfg, []byte("[security]\nip_region_enabled = true\n")); err != nil {
		t.Fatalf("applyINI: %v", err)
	}
	if !cfg.Security.IPRegionEnabled {
		t.Fatal("ip_region_enabled = true 应开启 IP 地域解析")
	}

	t.Setenv("IP_REGION_ENABLED", "false")
	applyEnv(cfg)
	if cfg.Security.IPRegionEnabled {
		t.Fatal("IP_REGION_ENABLED=false 应覆盖 ini 为关闭")
	}
}

// TestIPRegionSources 校验数据源配置（ADR-018）：provider 缺省 aliyun；
// ini 可设置 provider/AppCode/czdb 路径与密钥；env 同名变量优先于 ini。
func TestIPRegionSources(t *testing.T) {
	cfg := defaults()
	if cfg.Security.IPRegionProvider != "aliyun" {
		t.Fatalf("provider 缺省应为 aliyun, got %q", cfg.Security.IPRegionProvider)
	}

	if err := applyINI(cfg, []byte("[security]\n"+
		"ip_region_provider = czdb\n"+
		"ip_region_appcode = app-ini\n"+
		"ip_region_czdb_path_v4 = /data/geo_v4.czdb\n"+
		"ip_region_czdb_path_v6 = /data/geo_v6.czdb\n"+
		"ip_region_czdb_key = key-ini\n")); err != nil {
		t.Fatalf("applyINI: %v", err)
	}
	s := cfg.Security
	if s.IPRegionProvider != "czdb" || s.IPRegionAppCode != "app-ini" ||
		s.IPRegionCzdbPathV4 != "/data/geo_v4.czdb" || s.IPRegionCzdbPathV6 != "/data/geo_v6.czdb" ||
		s.IPRegionCzdbKey != "key-ini" {
		t.Fatalf("ini 数据源配置未生效: %+v", s)
	}

	t.Setenv("IP_REGION_PROVIDER", "aliyun")
	t.Setenv("IP_REGION_APPCODE", "app-env")
	t.Setenv("IP_REGION_CZDB_PATH_V4", "/data/v4.zip")
	t.Setenv("IP_REGION_CZDB_PATH_V6", "/data/v6.zip")
	t.Setenv("IP_REGION_CZDB_KEY", "key-env")
	applyEnv(cfg)
	s = cfg.Security
	if s.IPRegionProvider != "aliyun" || s.IPRegionAppCode != "app-env" ||
		s.IPRegionCzdbPathV4 != "/data/v4.zip" || s.IPRegionCzdbPathV6 != "/data/v6.zip" ||
		s.IPRegionCzdbKey != "key-env" {
		t.Fatalf("env 应覆盖 ini: %+v", s)
	}
}
