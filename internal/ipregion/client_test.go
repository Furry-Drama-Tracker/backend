package ipregion

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestLocalIP(t *testing.T) {
	c := NewClient(nil)
	for _, ip := range []string{"127.0.0.1", "::1", "::ffff:127.0.0.1"} {
		if got := c.GetRegion(context.Background(), ip); got != "本地" {
			t.Fatalf("ip %s should be 本地, got %q", ip, got)
		}
	}
}

func TestCacheAndFallback(t *testing.T) {
	var calls int
	c := NewClient(func(ctx context.Context, ip string) string {
		calls++
		return "中国 · 上海 · 上海 · 虹口区"
	})
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if got := c.GetRegion(ctx, "8.8.8.8"); got != "中国 · 上海 · 上海 · 虹口区" {
			t.Fatalf("region mismatch: %q", got)
		}
	}
	if calls != 1 {
		t.Fatalf("expected 1 fetch (cached), got %d", calls)
	}
}

func TestCacheEviction(t *testing.T) {
	c := NewClient(func(ctx context.Context, ip string) string { return "X" })
	ctx := context.Background()
	// 超过上限触发淘汰，但不应 panic，且最近访问仍命中。
	for i := 0; i < cacheMax+50; i++ {
		c.GetRegion(ctx, fmt.Sprintf("10.0.0.%d", i%250))
	}
	if got := c.GetRegion(ctx, "10.0.0.0"); got != "X" {
		t.Fatalf("expected hit, got %q", got)
	}
}

func TestUnknownOnFetchError(t *testing.T) {
	c := NewClient(func(ctx context.Context, ip string) string { return "未知" })
	if got := c.GetRegion(context.Background(), "1.2.3.4"); got != "未知" {
		t.Fatalf("expected 未知, got %q", got)
	}
}

// TestDisabledNoOutbound 禁用态（默认装配）不得外呼：本地 IP 仍识别，其余一律 "未知"。
func TestDisabledNoOutbound(t *testing.T) {
	c := NewDisabled()
	// 显式断言没有接上任何外呼实现（数据出境合规的硬约束）。
	if reflect.ValueOf(c.fetch).Pointer() == reflect.ValueOf(newAliyunFetch("x", "https://example.invalid")).Pointer() {
		t.Fatal("NewDisabled 不得装配阿里云纯真 API 外呼")
	}
	for _, ip := range []string{"127.0.0.1", "::1", "::ffff:127.0.0.1"} {
		if got := c.GetRegion(context.Background(), ip); got != "本地" {
			t.Fatalf("ip %s 应返回 本地, got %q", ip, got)
		}
	}
	for _, ip := range []string{"8.8.8.8", "2001:4860:4860::8888", "10.0.0.1"} {
		if got := c.GetRegion(context.Background(), ip); got != "未知" {
			t.Fatalf("禁用态下 ip %s 应返回 未知, got %q", ip, got)
		}
	}
}

// TestAliyunFetchSuccess 校验纯真 API 的成功解析：请求路径、ip 参数、
// APPCODE 鉴权头、外层 code/success 判定与 "country · province · city · districts" 拼接。
func TestAliyunFetchSuccess(t *testing.T) {
	var gotPath, gotAuth, gotIP string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotIP = r.URL.Query().Get("ip")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code":    200,
			"success": true,
			"message": "操作成功",
			"data": map[string]any{
				"ip":        "112.65.29.128",
				"country":   "中国",
				"province":  "上海",
				"city":      "上海",
				"districts": "虹口区",
				"isp":       "中国联通",
			},
		})
	}))
	defer srv.Close()

	fetch := newAliyunFetch("test-appcode", srv.URL+"/search/ip/geo")
	if got := fetch(context.Background(), "112.65.29.128"); got != "中国 · 上海 · 上海 · 虹口区" {
		t.Fatalf("unexpected region: %q", got)
	}
	if gotPath != "/search/ip/geo" {
		t.Fatalf("unexpected path: %q", gotPath)
	}
	if gotAuth != "APPCODE test-appcode" {
		t.Fatalf("unexpected auth header: %q", gotAuth)
	}
	if gotIP != "112.65.29.128" {
		t.Fatalf("unexpected ip param: %q", gotIP)
	}
}

// TestAliyunFetchFailures 校验失败兜底：空 AppCode 不外呼；
// 非 200 外层 code / 失败 body / 空字段均返回 "未知"。
func TestAliyunFetchFailures(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		switch {
		case r.URL.Query().Get("ip") == "1.1.1.2":
			w.WriteHeader(http.StatusForbidden) // AppCode 无效：网关 403
		case r.URL.Query().Get("ip") == "1.1.1.3":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"code":403,"success":false,"message":"无权限","data":null}`))
		case r.URL.Query().Get("ip") == "1.1.1.4":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"code":200,"success":true,"message":"操作成功","data":{"ip":"1.1.1.4"}}`))
		case r.URL.Query().Get("ip") == "1.1.1.6":
			// 区县缺失时 API 返回字面 "未知"，应与空值一并跳过。
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"code":200,"success":true,"message":"操作成功","data":{"country":"美国","province":"加利福尼亚州","city":"山景城","districts":"未知"}}`))
		default:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`not-json`))
		}
	}))
	defer srv.Close()

	if got := newAliyunFetch("", srv.URL)(context.Background(), "1.1.1.1"); got != "未知" {
		t.Fatalf("空 AppCode 应返回 未知, got %q", got)
	}
	if calls != 0 {
		t.Fatalf("空 AppCode 不得外呼, got %d calls", calls)
	}
	for ip, want := range map[string]string{
		"1.1.1.2": "未知", // HTTP 403
		"1.1.1.3": "未知", // 外层 code != 200
		"1.1.1.4": "未知", // data 为空
		"1.1.1.5": "未知", // 非 JSON
		"1.1.1.6": "美国 · 加利福尼亚州 · 山景城", // 字面 "未知" 区县被过滤
	} {
		if got := newAliyunFetch("test-appcode", srv.URL)(context.Background(), ip); got != want {
			t.Fatalf("ip %s 应返回 %q, got %q", ip, want, got)
		}
	}
}

// TestAliyunClientWiring 端到端校验 NewAliyunClient：装配纯真 API 外呼
// （鉴权头 + 解析）且缓存生效（两次查询只外呼一次）。
func TestAliyunClientWiring(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "APPCODE test-appcode" {
			t.Errorf("unexpected auth header: %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":200,"success":true,"message":"操作成功","data":{"country":"中国","province":"上海","city":"上海","districts":"虹口区"}}`))
	}))
	defer srv.Close()

	orig := geoAPIURL
	geoAPIURL = srv.URL + "/search/ip/geo"
	defer func() { geoAPIURL = orig }()

	c := NewAliyunClient("test-appcode")
	for i := 0; i < 2; i++ {
		if got := c.GetRegion(context.Background(), "112.65.29.128"); got != "中国 · 上海 · 上海 · 虹口区" {
			t.Fatalf("unexpected region: %q", got)
		}
	}
	if calls != 1 {
		t.Fatalf("expected 1 outbound call (cached), got %d", calls)
	}
}

// TestFormatCzdb 校验 czdb tab 分隔输出的整理：过滤空值与 "null"、
// 最多取 4 列、全空返回 "未知"。
func TestFormatCzdb(t *testing.T) {
	cases := []struct{ in, want string }{
		// 官方库实测形态：地域段(– 连接)<TAB>归属描述，描述丢弃，– 拆段
		{"中国–江苏–南京\t南京信风网络科技有限公司GreatbitDNS服务器", "中国 · 江苏 · 南京"},
		{"美国–加利福尼亚州–圣克拉拉–山景城\t谷歌公司DNS服务器", "美国 · 加利福尼亚州 · 圣克拉拉 · 山景城"},
		{"中国–浙江\t阿里云", "中国 · 浙江"},
		{"IANA\t局域网IP(Private-Use)", "IANA"},
		{"中国–上海\t上海\t多余段", "中国 · 上海"},
		// tab 列形态（geo mapping 启用，首个 tab 段不含 –）
		{"中国\t上海\t上海\t虹口区\t中国联通", "中国 · 上海 · 上海 · 虹口区"},
		{"中国\t广东\t深圳\t南山", "中国 · 广东 · 深圳 · 南山"},
		{"中国\tnull\tnull\tnull\t电信", "中国"},
		{"\tnull\t\t", "未知"},
		{"美国\t加利福尼亚\t洛杉矶", "美国 · 加利福尼亚 · 洛杉矶"},
		// 纯地域段无描述
		{"中国–浙江", "中国 · 浙江"},
		{"", "未知"},
		{"null", "未知"},
	}
	for _, tc := range cases {
		if got := formatCzdb(tc.in); got != tc.want {
			t.Fatalf("formatCzdb(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestCzdbClientInvalidPath 非法路径构造必须报错，且不 panic。
func TestCzdbClientInvalidPath(t *testing.T) {
	if _, err := NewCzdbClient(t.TempDir()+"/missing.czdb", "", "key"); err == nil {
		t.Fatal("不存在的库文件应返回错误")
	}
	if _, err := NewCzdbClient("", "", "key"); err == nil {
		t.Fatal("未指定任何库路径应返回错误")
	}
}

// TestWithFallback 校验兜底链：首选成功不触发兜底，首选 "未知" 改走兜底。
func TestWithFallback(t *testing.T) {
	primary := NewClient(func(ctx context.Context, ip string) string {
		if ip == "1.1.1.1" {
			return "首选 · 结果"
		}
		return "未知"
	})
	fallback := NewClient(func(ctx context.Context, ip string) string { return "兜底 · 结果" })

	if got := primary.GetRegion(context.Background(), "1.1.1.1"); got != "首选 · 结果" {
		t.Fatalf("primary hit 应直接返回, got %q", got)
	}
	chained := primary.WithFallback(fallback)
	if got := chained.GetRegion(context.Background(), "1.1.1.1"); got != "首选 · 结果" {
		t.Fatalf("chained primary hit 应直接返回, got %q", got)
	}
	if got := chained.GetRegion(context.Background(), "2.2.2.2"); got != "兜底 · 结果" {
		t.Fatalf("chained primary miss 应走兜底, got %q", got)
	}
	if primary.WithFallback(nil) != primary {
		t.Fatal("兜底为 nil 时应原样返回")
	}
	var nilClient *Client
	if nilClient.WithFallback(fallback) != fallback {
		t.Fatal("nil 接收方应返回兜底侧")
	}
}

// TestNewConfiguredClientWiring 校验按配置装配：首选/兜底方向、
// 未配置任何数据源返回 nil、czdb 加载失败仍返回可用数据源（行为断言）。
func TestNewConfiguredClientWiring(t *testing.T) {
	if c, err := NewConfiguredClient("aliyun", "", "", "", ""); c != nil {
		t.Fatal("无任何数据源配置应返回 nil")
	} else if err != nil {
		t.Fatalf("无配置不应报错: %v", err)
	}

	// aliyun 首选：AppCode 未配置时仅剩 czdb（此处路径非法，应报错且返回 nil）。
	if c, err := NewConfiguredClient("aliyun", "", t.TempDir()+"/missing.czdb", "", "key"); c != nil {
		t.Fatal("czdb 加载失败且无其它数据源应返回 nil")
	} else if err == nil {
		t.Fatal("czdb 加载失败应报错")
	}

	// czdb 首选但加载失败：aliyun 可用时应返回 aliyun 且同时报错。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":200,"success":true,"message":"操作成功","data":{"country":"中国","province":"上海","city":"上海","districts":"虹口区"}}`))
	}))
	defer srv.Close()
	orig := geoAPIURL
	geoAPIURL = srv.URL + "/search/ip/geo"
	defer func() { geoAPIURL = orig }()

	c, err := NewConfiguredClient("czdb", "appcode", t.TempDir()+"/missing.czdb", "", "key")
	if c == nil {
		t.Fatal("czdb 失败但 aliyun 可用时应返回 aliyun 客户端")
	}
	if err == nil {
		t.Fatal("czdb 加载失败应同时返回错误")
	}
	if got := c.GetRegion(context.Background(), "112.65.29.128"); got != "中国 · 上海 · 上海 · 虹口区" {
		t.Fatalf("应走 aliyun 数据源解析, got %q", got)
	}
}
