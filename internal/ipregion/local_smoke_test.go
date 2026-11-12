package ipregion

// 本地冒烟测试：用真实数据源验证端到端解析，默认全部跳过。
//
// czdb 离线库：设 IPREGION_CZDB_SMOKE_V4 / IPREGION_CZDB_SMOKE_V6
// （对应协议的 .czdb 文件路径，至少一项）与 IPREGION_CZDB_SMOKE_KEY
// （解密密钥）后启用：
//
//	IPREGION_CZDB_SMOKE_V4=C:\path\cz88_public_v4.czdb \
//	  IPREGION_CZDB_SMOKE_V6=C:\path\cz88_public_v6.czdb \
//	  IPREGION_CZDB_SMOKE_KEY=<key> \
//	  go test ./internal/ipregion/ -run TestCzdbClientRealSmoke -v
//
// 阿里云云市场纯真 API：设 IPREGION_ALIYUN_SMOKE_APPCODE=<AppCode> 后启用。
//
//	IPREGION_ALIYUN_SMOKE_APPCODE=<appcode> \
//	  go test ./internal/ipregion/ -run TestAliyunClientRealSmoke -v
import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestCzdbClientRealSmoke(t *testing.T) {
	v4Path := os.Getenv("IPREGION_CZDB_SMOKE_V4")
	v6Path := os.Getenv("IPREGION_CZDB_SMOKE_V6")
	key := os.Getenv("IPREGION_CZDB_SMOKE_KEY")
	if key == "" || (v4Path == "" && v6Path == "") {
		t.Skip("未设置 IPREGION_CZDB_SMOKE_V4/_V6 / IPREGION_CZDB_SMOKE_KEY，跳过真实库冒烟")
	}

	c, err := NewCzdbClient(v4Path, v6Path, key)
	if err != nil {
		t.Fatalf("加载真实库失败: %v", err)
	}
	ctx := context.Background()
	cases := []string{
		"114.114.114.114",      // CN IPv4（114DNS）
		"223.5.5.5",            // CN IPv4（AliDNS）
		"8.8.8.8",              // US IPv4（Google）
		"2400:3200::1",         // CN IPv6（AliDNS）
		"2606:4700:4700::1111", // US IPv6（Cloudflare）
		"10.0.0.1",             // 内网，大概率未命中
	}
	for _, ip := range cases {
		t.Logf("%-24s => %s", ip, c.GetRegion(ctx, ip))
	}
	// 宽松断言：配置了 v4 库时，任一国内 IPv4 解析出「中国」，且无 panic。
	if v4Path != "" && !strings.Contains(c.GetRegion(ctx, "114.114.114.114"), "中国") {
		t.Fatalf("114.114.114.114 应解析出中国地域")
	}
}

func TestAliyunClientRealSmoke(t *testing.T) {
	appcode := os.Getenv("IPREGION_ALIYUN_SMOKE_APPCODE")
	if appcode == "" {
		t.Skip("未设置 IPREGION_ALIYUN_SMOKE_APPCODE，跳过真实 API 冒烟")
	}

	c := NewAliyunClient(appcode)
	ctx := context.Background()
	cases := []string{
		"114.114.114.114", // CN IPv4
		"8.8.8.8",         // US IPv4
		"2400:3200::1",    // CN IPv6
	}
	results := map[string]string{}
	for _, ip := range cases {
		results[ip] = c.GetRegion(ctx, ip)
		t.Logf("%-24s => %s", ip, results[ip])
	}
	if !strings.Contains(results["114.114.114.114"], "中国") {
		t.Fatalf("114.114.114.114 应解析出中国地域, got %q", results["114.114.114.114"])
	}
}
