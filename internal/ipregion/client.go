// Package ipregion 解析 IP 地域：
//   - 本地 IP（127.0.0.1 / ::1 / ::ffff:127.0.0.1）直接返回 "本地"
//   - 数据源二选一作首选（[security].ip_region_provider，见 ADR-018）：
//       aliyun = 阿里云云市场纯真 IP 地域 API（cmapi00053387，5s 超时）
//       czdb   = 纯真离线库（github.com/tagphi/czdb-search-golang，MEMORY 模式）
//     另一数据源配置齐全时自动兜底（首选返回 "未知" 时改走它）
//   - 输出统一为 "country · province · city · districts" 风格
//   - 缓存用 hashicorp/golang-lru（上限 1000 条）+ 24h TTL（过期条目在命中时淘汰）
//
// 默认装配走 NewDisabled：IP 地域属敏感能力，未显式开启
// （[security].ip_region_enabled / IP_REGION_ENABLED，见 ADR-017）时一律不外呼。
// 开启后由 NewConfiguredClient 按配置装配（AppCode/库路径来自 ini 或环境变量）；
// 测试或自定义实现可注入 NewClient(<查询函数>)，service 侧无改动。
package ipregion

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	lru "github.com/hashicorp/golang-lru/v2"
	db "github.com/tagphi/czdb-search-golang/pkg/db"
)

// cacheTTL 是地域缓存有效期。
const cacheTTL = 24 * time.Hour

// cacheMax 是地域缓存上限。
const cacheMax = 1000

// geoAPIURL 是阿里云云市场纯真 IP 地域 API（cmapi00053387）的查询地址。
// var 仅为测试注入 httptest 地址。
var geoAPIURL = "https://cz88geo.market.alicloudapi.com/search/ip/geo"

// localIPs 是视为本地的 IP。
var localIPs = map[string]bool{
	"127.0.0.1":        true,
	"::1":              true,
	"::ffff:127.0.0.1": true,
}

// cacheEntry 是缓存的单个地域条目（带过期时间）。
type cacheEntry struct {
	region    string
	expiresAt time.Time
}

// Client 解析 IP 地域；fetch 可注入（测试用）。
type Client struct {
	fetch func(ctx context.Context, ip string) string
	cache *lru.Cache[string, cacheEntry]
}

// NewClient 构造 IP 地域客户端。fetch 为空时使用默认查询（不发外呼，一律 "未知"）。
func NewClient(fetch func(ctx context.Context, ip string) string) *Client {
	c, err := lru.New[string, cacheEntry](cacheMax)
	if err != nil {
		// 构造参数固定合法，正常不会失败。
		c, _ = lru.New[string, cacheEntry](cacheMax)
	}
	cl := &Client{cache: c}
	if fetch != nil {
		cl.fetch = fetch
	} else {
		cl.fetch = func(ctx context.Context, ip string) string { return "未知" }
	}
	return cl
}

// NewAliyunClient 构造走阿里云云市场纯真 IP 地域 API（cmapi00053387）的客户端。
// appcode 为云市场 AppCode（[security].ip_region_appcode / IP_REGION_APPCODE，ADR-018）；
// 为空时不发起任何外呼（地域记 "未知"）。
func NewAliyunClient(appcode string) *Client {
	return NewClient(newAliyunFetch(appcode, geoAPIURL))
}

// NewDisabled 构造不做任何网络外呼的客户端（数据出境合规的默认态，见 ADR-017）：
// 本地 IP 仍返回 "本地"，其余 IP 一律返回 "未知"。
func NewDisabled() *Client {
	return NewClient(nil)
}

// czdb 库的协议标识，及库内 IPType 取值（lib pkg/utils：IPV4=4 / IPV6=6）。
const (
	czdbV4 = "v4"
	czdbV6 = "v6"

	czdbIPTypeV4 = int32(4)
	czdbIPTypeV6 = int32(6)
)

// NewCzdbClient 构造走纯真离线库（czdb）的客户端，无网络外呼。
// v4Path/v6Path 分别为 IPv4/IPv6 的 .czdb 库文件路径，任一留空表示该协议
// 不查（记 "未知"）；key 为官方随库下发的解密密钥（两库共用）。
// MEMORY 模式（线程安全）全量载入内存，构造期预热后即释放文件句柄，
// 查询为纯内存读，可安全并发。库文件损坏/密钥错误时返回错误
// （调用方记告警并回退其它数据源）。
func NewCzdbClient(v4Path, v6Path, key string) (*Client, error) {
	searchers := map[string]*db.DBSearcher{}
	for proto, path := range map[string]string{czdbV4: v4Path, czdbV6: v6Path} {
		if path == "" {
			continue
		}
		s, err := initCzdbSearcher(proto, path, key)
		if err != nil {
			return nil, err
		}
		searchers[proto] = s
	}
	if len(searchers) == 0 {
		return nil, fmt.Errorf("未指定任何 czdb 库路径")
	}
	return NewClient(func(ctx context.Context, ip string) string {
		p := net.ParseIP(strings.TrimSpace(ip))
		if p == nil {
			return "未知"
		}
		s := searchers[czdbV6]
		if p.To4() != nil {
			s = searchers[czdbV4]
		}
		if s == nil {
			return "未知" // 该协议未配置库
		}
		region, err := db.Search(ip, s)
		if err != nil || region == "" || region == "IP not found" {
			return "未知"
		}
		return formatCzdb(region)
	}), nil
}

// initCzdbSearcher 初始化单个协议的 czdb 搜索器。MEMORY 模式构造期预热
// 完成全量入内存后即关闭文件句柄，之后查询为纯内存读，可安全并发。
func initCzdbSearcher(proto, path, key string) (*db.DBSearcher, error) {
	s, err := db.InitDBSearcher(path, key, db.MEMORY)
	if err != nil {
		return nil, fmt.Errorf("加载 czdb %s 离线库失败: %w", proto, err)
	}
	// 库内实际协议与参数声明不一致时提前报错（文件放反了 v4/v6 参数）。
	if (s.IPType == czdbIPTypeV4) != (proto == czdbV4) {
		db.CloseDBSearcher(s)
		return nil, fmt.Errorf("czdb %s 路径指向的库不是 %s 库", proto, strings.ToUpper(proto))
	}
	// 预热：MEMORY 模式全量载入在首次 Search 时懒加载且无锁（库实现如此），
	// 构造期串行完成后关闭文件句柄，之后查询为纯内存读，可安全并发。
	warm := "255.255.255.255"
	if proto == czdbV6 {
		warm = "2001:db8::1"
	}
	_, _ = db.Search(warm, s)
	db.CloseDBSearcher(s)
	return s, nil
}

// WithFallback 返回以 c 为首选、fb 为兜底的客户端：c 返回 "未知"
// （外呼失败或未命中）时改走 fb。任一为 nil 时返回另一侧（均 nil 则返回 nil）。
func (c *Client) WithFallback(fb *Client) *Client {
	if c == nil {
		return fb
	}
	if fb == nil {
		return c
	}
	return NewClient(func(ctx context.Context, ip string) string {
		if r := c.fetch(ctx, ip); r != "未知" {
			return r
		}
		return fb.fetch(ctx, ip)
	})
}

// NewConfiguredClient 按 [security] 的 IP 地域配置装配数据源：
// provider（aliyun 默认 / czdb）选首选，另一数据源配置齐全时兜底；
// aliyun 需 AppCode，czdb 需库路径（v4/v6 至少一个，密钥缺失或库加载失败
// 记入返回错误）。无任何可用数据源时返回 nil（调用方应保持 NewDisabled）。
func NewConfiguredClient(provider, aliyunAppCode, czdbV4Path, czdbV6Path, czdbKey string) (*Client, error) {
	var aliyun, cz *Client
	var czErr error
	if aliyunAppCode != "" {
		aliyun = NewAliyunClient(aliyunAppCode)
	}
	if czdbV4Path != "" || czdbV6Path != "" {
		cz, czErr = NewCzdbClient(czdbV4Path, czdbV6Path, czdbKey)
	}
	switch provider {
	case "czdb":
		return cz.WithFallback(aliyun), czErr
	default: // aliyun（含未配置与未知值）
		return aliyun.WithFallback(cz), czErr
	}
}

// formatCzdb 把 czdb 输出整理成 "country · province · city · districts" 风格。
// 兼容两种形态：
//   - 官方库实测形态（列选择为 0，lib 直接返回 otherData）：
//     "国家–省–市[–区]<TAB>归属描述"（– 为 en-dash，U+2013）。归属/运营商
//     描述是 IP 归属而非用户地域，丢弃之，地域按 – 拆段最多 4 段；
//   - tab 分隔列（启用 geo mapping 的库版本）：列序固定
//     （国家/省/市/区县/运营商/…），取前 4 列非空值，不让后续列顶替空缺。
//
// 区分启发式：首段含 – 或 tab 段不足 3 列时按单行形态处理。
func formatCzdb(region string) string {
	region = strings.TrimSpace(region)
	if region == "" || region == "null" {
		return "未知"
	}
	fields := strings.Split(region, "\t")
	singleLine := len(fields) < 3 || strings.ContainsRune(fields[0], '–')
	if !singleLine {
		// geo mapping 列形态。
		parts := []string{}
		for i, f := range fields {
			if i >= 4 {
				break
			}
			f = strings.TrimSpace(f)
			if f == "" || f == "null" {
				continue
			}
			parts = append(parts, f)
		}
		if len(parts) == 0 {
			return "未知"
		}
		return strings.Join(parts, " · ")
	}
	// 单行形态：仅保留首段（地域），按 – 拆段最多 4 段。
	if len(fields) > 1 {
		region = fields[0]
	}
	parts := []string{}
	for i, f := range strings.Split(region, "–") {
		if i >= 4 {
			break
		}
		f = strings.TrimSpace(f)
		if f == "" || f == "null" {
			continue
		}
		parts = append(parts, f)
	}
	if len(parts) == 0 {
		return "未知"
	}
	return strings.Join(parts, " · ")
}

// GetRegion 返回 IP 地域，带 LRU + 24h TTL 缓存。
func (c *Client) GetRegion(ctx context.Context, ip string) string {
	if localIPs[ip] {
		return "本地"
	}
	if v, ok := c.cache.Get(ip); ok {
		if time.Now().Before(v.expiresAt) {
			return v.region
		}
		c.cache.Remove(ip) // 过期：淘汰并重新外呼
	}
	region := c.fetch(ctx, ip)
	c.cache.Add(ip, cacheEntry{region: region, expiresAt: time.Now().Add(cacheTTL)})
	return region
}

// newAliyunFetch 构造纯真 API 查询函数：GET apiURL?ip=<ip>，鉴权用云市场统一
// 的 Authorization: APPCODE 头（5s 超时）。apiURL 可替换（测试注入 httptest）。
// 返回 "country · province · city · districts" 或 "未知"。
func newAliyunFetch(appcode, apiURL string) func(ctx context.Context, ip string) string {
	return func(ctx context.Context, ip string) string {
		if appcode == "" {
			return "未知"
		}
		reqCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		q := url.Values{}
		q.Set("ip", ip)
		req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, apiURL+"?"+q.Encode(), nil)
		if err != nil {
			return "未知"
		}
		req.Header.Set("Authorization", "APPCODE "+appcode)
		req.Header.Set("User-Agent", "furry-drama-be-neo")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return "未知"
		}
		defer resp.Body.Close()
		var data struct {
			Code    int  `json:"code"`
			Success bool `json:"success"`
			Data    struct {
				Country   string `json:"country"`
				Province  string `json:"province"`
				City      string `json:"city"`
				Districts string `json:"districts"`
			} `json:"data"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
			return "未知"
		}
		if resp.StatusCode != http.StatusOK || data.Code != 200 || !data.Success {
			return "未知"
		}
		parts := []string{}
		for _, p := range []string{data.Data.Country, data.Data.Province, data.Data.City, data.Data.Districts} {
			// 区县级库对缺失的区县会返回字面 "未知"，与空值一并跳过。
			if p != "" && p != "未知" {
				parts = append(parts, p)
			}
		}
		if len(parts) == 0 {
			return "未知"
		}
		return strings.Join(parts, " · ")
	}
}
