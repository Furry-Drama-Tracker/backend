// neo-server 服务入口。
//
// 用法：
//
//	bin/server --config=/etc/furry-drama-tracker/backend.ini --listen=tcp:0.0.0.0:5000
//	bin/server --listen=unix:/run/furry-drama-be-neo.sock   # Unix socket（"@name" 为 abstract）
//
// 启动顺序：加载配置 → 连接 MongoDB → 装配仓储/签名器 → 装配 Gin → 启动 HTTP →
// 监听 SIGTERM/SIGINT 优雅关停（10s 兜底强退）。M2 起在 listen 前执行启动迁移与 cron。
//
// @title 兽剧聚合平台 API
// @version 1.0.0
// @description 兽剧内容聚合平台后端服务 API 文档（neo-server）
// @description 所有 4xx/5xx 响应体为统一错误信封（模型 ErrorBody）：{"message","error","ref","status"}。
// @description error=错误码 XXXYYYY（模块前缀+序号，注册表 backend/docs/errors.json）；ref=0-999999999 随机参考号（响应头 X-Ref-ID 同值；边缘 ESA 响应恒为 0）；status=HTTP 状态码整数。
// @description message 恒为纯文案，生产 5xx 掩码「服务器内部错误」；出错编号展示格式 CODE-000123456，由前端拼装。
// @host localhost:5000
// @BasePath /api
// @securityDefinitions.apikey bearerAuth
// @in header
// @name Authorization
// @description 输入 JWT access token（不含 Bearer 前缀；亦可通过 accessToken cookie）
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Furry-Drama-Tracker/backend/internal/auth"
	"github.com/Furry-Drama-Tracker/backend/internal/config"
	"github.com/Furry-Drama-Tracker/backend/internal/cron"
	"github.com/Furry-Drama-Tracker/backend/internal/indexes"
	"github.com/Furry-Drama-Tracker/backend/internal/migrate"
	"github.com/Furry-Drama-Tracker/backend/internal/repository"
	"github.com/Furry-Drama-Tracker/backend/internal/server"
	"github.com/Furry-Drama-Tracker/backend/internal/upload"
)

func main() {
	var configPath, listen string
	flag.StringVar(&configPath, "config", "", "配置文件路径（默认 "+config.DefaultPath+"）")
	flag.StringVar(&listen, "listen", "", "监听地址：tcp:HOST:PORT 或 unix:/path/to.sock（覆盖 ini）")
	flag.Parse()

	cfg, err := config.Load(configPath)
	if err != nil {
		log.Fatalf("[FATAL] 配置加载失败: %v", err)
	}
	if listen != "" {
		cfg.Server.Listen = listen
	}
	if cfg.JWT.WeakSecret {
		log.Printf("[SECURITY WARN] JWT_SECRET 仍是示例/历史弱密钥，请尽快轮换；轮换前先设置 ENCRYPTION_KEY=旧密钥以兼容 2FA/邮件加密数据")
	}
	if cfg.Server.InsecureOrigins {
		log.Printf("[SECURITY WARN] 生产环境 CORS 源仍包含 localhost/127.0.0.1/example.com 或为空，请尽快配置真实站点域名")
	}
	// 代理标头信任策略（ADR-013）：生产缺省信任代理标头且 trusted_proxies 未配置
	// 时，gin 信任所有来源——任何能直连本进程的客户端都可轮换 CF-Connecting-IP /
	// X-Forwarded-For 伪造客户端 IP：每个 IP 维度的限流桶（含全局 300/min）随请求
	// 轮换失效，审计日志/LastLoginIP/新设备邮件的 IP 归因可被任意伪造。
	// 注意 trusted_proxies 只校验"直连对端"是否受信：socket/loopback 部署下对端
	// 恒为本机反代，防不住经反代透传的客户端伪造标头——必须在反代层覆盖写入
	// 这些标头（见 deploy/nginx.conf.example 与 README 部署说明）。
	if !cfg.IsDev {
		trust := true
		if cfg.Server.TrustProxyHeaders != nil {
			trust = *cfg.Server.TrustProxyHeaders
		}
		if trust && len(cfg.Server.TrustedProxies) == 0 {
			log.Printf("[SECURITY WARN] 生产环境正在信任代理标头识别客户端 IP，但 trusted_proxies 未配置（gin 信任所有来源）：IP 维度限流可被伪造标头轮换绕过、IP 归因可被伪造；请在反代层覆盖写入代理标头并按拓扑配置 trusted_proxies（见 deploy/nginx.conf.example）")
		}
	}
	// IP 地域解析默认关闭（见 docs/decisions.md ADR-017）：
	// 开启后登录/注册/设备确认会把客户端 IP 发给所选数据源
	// （aliyun=阿里云云市场纯真 API / czdb=纯真离线库，见 ADR-018）。
	if cfg.Security.IPRegionEnabled {
		if cfg.Security.IPRegionProvider == "czdb" {
			log.Printf("[SECURITY WARN] IP 地域解析已开启（首选 czdb 离线库）：查询走本地库，库未命中/失败时按配置兜底")
		} else {
			log.Printf("[SECURITY WARN] IP 地域解析已开启（首选阿里云云市场纯真 API）：认证流程会把客户端 IP 发送至该 API，非必要请保持关闭")
		}
		if cfg.Security.IPRegionProvider != "czdb" && cfg.Security.IPRegionAppCode == "" {
			log.Printf("[SECURITY WARN] IP 地域解析未配置 AppCode（[security].ip_region_appcode 或 IP_REGION_APPCODE），aliyun 数据源不可用")
		}
	} else {
		log.Printf("IP 地域解析已禁用（LastLoginRegion 记为 未知）")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	db, err := repository.Connect(ctx, cfg.Database.URI, cfg.Database.Name, cfg.Database.Pool)
	if err != nil {
		log.Fatalf("[FATAL] MongoDB 连接失败: %v", err)
	}
	// 不打印包含账号密码的 Mongo URI。
	log.Printf("MongoDB 连接成功")

	repos := repository.NewRepos(db, cfg.Security.LoginMaxAttempts, cfg.Security.LoginLockMinutes)
	signer := auth.NewSigner(cfg.JWT.Secret)

	// 启动迁移与索引必须在接收请求前完成。
	startupCtx, startupCancel := context.WithTimeout(context.Background(), 60*time.Second)
	// 迁移/索引失败不让已有服务无法启动：记录告警并继续。
	// 首次部署或数据重复时应尽快人工处理后重启。
	if err := migrate.Run(startupCtx, db, repos); err != nil {
		log.Printf("[WARN] 启动迁移失败（服务继续启动，请检查数据）: %v", err)
	}
	if err := indexes.Ensure(startupCtx, db); err != nil {
		log.Printf("[WARN] 索引创建失败（服务继续启动，请检查重复数据/索引冲突）: %v", err)
	}
	startupCancel()

	// 后台清理任务：过期会话、已读通知、到期注销账号。
	cronCtx, cronCancel := context.WithCancel(context.Background())
	defer cronCancel()
	cron.StartSessions(cronCtx, repos)

	// 上传目录与 SVG 图标目录（按配置注入；缺省 ./uploads 与 ./uploads/icons）。
	upload.SetDir(cfg.Server.UploadsDir) // 上传落盘目录（SaveImage 与 /uploads 静态服务共用）
	upload.SetIconsDir(cfg.Server.IconsDir)
	if err := os.MkdirAll(upload.Dir, 0o755); err != nil {
		log.Printf("[WARN] 创建上传目录失败: %v", err)
	}
	if err := os.MkdirAll(upload.IconsDir, 0o755); err != nil {
		log.Printf("[WARN] 创建图标目录失败: %v", err)
	}
	app := server.NewApp(server.Deps{Config: cfg, DB: db, Repos: repos, Signer: signer})

	ln, err := netListen(cfg.Server.Listen)
	if err != nil {
		log.Fatalf("[FATAL] 监听失败: %v", err)
	}
	defer ln.Close()
	log.Printf("server listening on %s", cfg.Server.Listen)

	srv := &http.Server{
		Handler:           app,
		ReadHeaderTimeout: 65 * time.Second, // 慢客户端防护：读头超时
		ReadTimeout:       30 * time.Second, // 请求读超时
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       5 * time.Second, // 空闲连接超时
	}

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- srv.Serve(ln)
	}()

	// 优雅关停：SIGTERM/SIGINT，10s 兜底强退。
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)
	select {
	case sig := <-stop:
		log.Printf("收到信号 %s，开始优雅关停…", sig)
	case err := <-serveErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server error: %v", err)
		}
		return
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("优雅关停超时: %v", err)
	}
	log.Printf("server stopped")
}

// netListen 根据配置创建监听器。
//
//	"tcp:HOST:PORT"        → TCP（默认）
//	"unix:/path/to.sock"   → Unix domain socket（清理残留 + chmod 0660）
//	"unix:@abstract"       → Linux abstract socket
func netListen(spec string) (net.Listener, error) {
	if strings.HasPrefix(spec, "unix:") {
		p := strings.TrimPrefix(spec, "unix:")
		if strings.HasPrefix(p, "@") {
			// abstract socket：无需清理、无文件权限。
			return net.Listen("unix", p)
		}
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		ln, err := net.Listen("unix", p)
		if err != nil {
			return nil, err
		}
		if err := os.Chmod(p, 0o660); err != nil {
			ln.Close()
			return nil, err
		}
		return ln, nil
	}
	// 默认 TCP："tcp:..." 或裸 "HOST:PORT"。
	addr := strings.TrimPrefix(spec, "tcp:")
	return net.Listen("tcp", addr)
}
