package main

import (
	"context"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"hash/fnv"
	"html/template"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/soulteary/runner-fleet/internal/config"
	"github.com/soulteary/runner-fleet/internal/githubcheck"
	"github.com/soulteary/runner-fleet/internal/handler"
	"github.com/soulteary/runner-fleet/internal/runner"
	secure "github.com/soulteary/secure-kit/v2"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed i18n/*.json
var i18nFS embed.FS

//go:embed static/app.css static/app.js
var staticFS embed.FS

// 三者均由构建时 -ldflags "-X main.Xxx=..." 注入。
// Commit/BuildDate 未注入时为空，version-kit 会把它们从输出里省掉。
var (
	Version   = "dev"
	Commit    string
	BuildDate string
)

// assetVersion 是内嵌静态资源的内容指纹，拼进 /static/app.css?v= 里。
// 用内容哈希而不是 Version：dev 构建的版本号恒为 "dev"，改完样式刷新页面
// 拿到的还会是缓存里的旧文件。内容一变指纹就变，浏览器自然会去取新的。
func assetVersion() string {
	h := fnv.New64a()
	entries, err := staticFS.ReadDir("static")
	if err != nil {
		return "dev"
	}
	// ReadDir 按文件名排序，因此同样的内容每次得到同样的指纹
	for _, e := range entries {
		b, err := staticFS.ReadFile("static/" + e.Name())
		if err != nil {
			continue
		}
		_, _ = h.Write([]byte(e.Name()))
		_, _ = h.Write(b)
	}
	return strconv.FormatUint(h.Sum64(), 36)
}

// staticHandler 提供内嵌的 /static 资源。
// 带 ?v=（页面自己拼的那种）就长缓存——地址里已经有内容指纹，内容变了地址也会变；
// 直接敲地址不带 v 的则只许协商缓存，免得手工请求拿到一份再也刷不掉的副本。
func staticHandler() echo.HandlerFunc {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(err)
	}
	srv := http.StripPrefix("/static", http.FileServer(http.FS(sub)))
	return func(c echo.Context) error {
		if c.QueryParam("v") != "" {
			c.Response().Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			c.Response().Header().Set("Cache-Control", "no-cache")
		}
		srv.ServeHTTP(c.Response(), c.Request())
		return nil
	}
}

type templateRenderer struct {
	templates *template.Template
}

func (t *templateRenderer) Render(w io.Writer, name string, data interface{}, c echo.Context) error {
	// ParseFS 在不同 Go 版本中模板名可能为 basename 或完整路径，两种都尝试
	for _, templateName := range []string{name, "templates/" + name} {
		if t.templates.Lookup(templateName) != nil {
			return t.templates.ExecuteTemplate(w, templateName, data)
		}
	}
	return t.templates.ExecuteTemplate(w, name, data) // 最后尝试一次，以返回明确错误
}

func newTemplateRenderer() *templateRenderer {
	tpl := template.Must(template.ParseFS(templateFS, "templates/*.html"))
	return &templateRenderer{templates: tpl}
}

func main() {
	configPath := flag.String("config", "config/config.yaml", "配置文件路径")
	showVersion := flag.Bool("version", false, "显示版本号后退出")
	flag.Parse()

	handler.Version = Version
	handler.AssetVersion = assetVersion()
	handler.Commit = Commit
	handler.BuildDate = BuildDate

	// -version 打印完整构建信息（含提交与构建时间），本机执行、不对外；
	// HTTP 的 /version 仍只给版本号，见 handler.VersionInfo 的说明。
	if *showVersion {
		fmt.Println(handler.BuildInfo().Full())
		os.Exit(0)
	}

	setupLogging()

	handler.ConfigPath = *configPath
	handler.StartRegistrationWorker()
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}
	if cfg.Runners.ContainerMode && runner.ManagerDockerHostIsDind() {
		log.Printf("警告: 容器模式已开启，但 DOCKER_HOST 指向 TCP（DinD）。Manager 必须使用宿主机 Docker（socket）才能创建/启停 Runner 容器。请在 .env 中移除或注释 DOCKER_HOST=tcp://runner-dind:2375")
	}
	// 启动自检：把配置类问题提前到这里暴露，而不是等第一个 Job 跑挂才发现。
	// 只做只读检查，任何一项失败都不阻止启动（避免打断既有部署）。
	preflightCtx, preflightCancel := context.WithTimeout(context.Background(), 20*time.Second)
	runner.LogPreflight(runner.Preflight(preflightCtx, cfg))
	preflightCancel()

	e := newEchoServer()

	addr := listenAddr(cfg)
	srv := &http.Server{Addr: addr, Handler: e}
	go runAutoStartRunners(*configPath)
	go runRegistrationCheck(*configPath)
	go func() {
		log.Printf("监听 %s", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	<-quit
	log.Println("正在关闭服务...")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Fatal("关闭服务失败:", err)
	}
	log.Println("已退出")
}

// runAutoStartRunners 启动后延迟执行一次：将已注册但未在运行的 runner 全部拉起（便于 DinD/管理器重启后恢复）
func runAutoStartRunners(configPath string) {
	const delay = 15 * time.Second
	time.Sleep(delay)
	cfg, err := config.Load(configPath)
	if err != nil {
		log.Printf("自动启动 runner 时加载配置失败: %v", err)
		return
	}
	startIdleRunners(context.Background(), cfg, "自动启动")
}

// startIdleRunners 把「已注册但没在跑」的 runner 拉起来，供启动后的一次性拉起与定时巡检共用。
//
// 判据取自 ListWithLiveStatus 而不是 List：容器模式下 Manager 与 Runner 不在同一个
// PID namespace，List 的 Running 恒为 false，照着它拉会把每个 runner 每轮都再起一遍。
// 探测不出来的（StatusUnknown）一律跳过——不确知它没在跑，就别动它。
func startIdleRunners(ctx context.Context, cfg *config.Config, action string) {
	for _, info := range runner.ListWithLiveStatus(ctx, cfg) {
		if info.Status != runner.StatusInstalled || info.Running {
			continue
		}
		if err := runner.StartIfInstalled(ctx, cfg, info.Name, info.InstallDir); err != nil {
			log.Printf("%s runner %s 失败: %v", action, info.Name, err)
		} else {
			log.Printf("已%s runner: %s", action, info.Name)
		}
	}
}

// runRegistrationCheck 每 5 分钟加载配置并检查各 runner 是否已在 GitHub 显示，写入 .github_status.json 供界面展示
func runRegistrationCheck(configPath string) {
	const interval = 5 * time.Minute
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	// 启动后稍等再执行第一次，避免与 HTTP 启动竞争
	time.Sleep(30 * time.Second)
	firstRun := true
	for {
		cfg, err := config.Load(configPath)
		if err == nil {
			githubcheck.Run(cfg)
			// 首次不执行拉起，避免与 runAutoStartRunners(15s) 重叠导致重复启动同一 runner
			if !firstRun {
				startIdleRunners(context.Background(), cfg, "定时拉起")
			}
			firstRun = false
		}
		<-ticker.C
	}
}

// httpErrorHandler 统一把错误渲染成 {"message": ...}；echo.HTTPError 保留它自己的状态码
func httpErrorHandler(err error, c echo.Context) {
	code := http.StatusInternalServerError
	msg := err.Error()
	if he, ok := err.(*echo.HTTPError); ok {
		code = he.Code
		if m, ok := he.Message.(string); ok {
			msg = m
		}
	}
	_ = c.JSON(code, map[string]string{"message": msg})
}

// safeMethods 是不改变状态的方法，CSRF 与之无关
var safeMethods = map[string]bool{
	http.MethodGet:     true,
	http.MethodHead:    true,
	http.MethodOptions: true,
}

// trustedOrigins 读取 TRUSTED_ORIGINS（逗号分隔），供 csrfGuardMiddleware 放行额外来源
func trustedOrigins() []string {
	raw := strings.TrimSpace(os.Getenv("TRUSTED_ORIGINS"))
	if raw == "" {
		return nil
	}
	var out []string
	for _, item := range strings.Split(raw, ",") {
		if v := strings.TrimSpace(item); v != "" {
			out = append(out, strings.TrimRight(v, "/"))
		}
	}
	return out
}

// originMatchesHost 判断 Origin 与本次请求的 Host 是否同源（只比较 authority）
func originMatchesHost(origin, host string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	return u.Host == host
}

// csrfGuardMiddleware 拒绝跨站发起的写请求。
//
// 写接口原先既收 JSON 也收 form 编码，而跨站 form 提交是 CORS 意义上的「简单请求」——
// 不触发预检，浏览器照发，并自动带上该源已缓存的 Basic Auth 凭据。于是诱导管理员
// 打开一个恶意页面就能添加或启停 Runner。PUT / DELETE 必定触发预检，本来就到不了这里，
// 真正敞着的是 POST：/api/runners 与 /api/runners/:name/{start,stop,recreate}。
//
// 判据优先取 Sec-Fetch-Site：它由浏览器本地计算，不受反向代理改写 Host 影响
// （Chrome 76+ / Firefox 90+ / Safari 16.4+）。取不到时回落到比较 Origin 与 Host——
// 所有现代浏览器的跨站 POST 都会带 Origin，所以这一层够用。
//
// 两者都没有的请求不是浏览器发的（curl、CI 脚本）：它们不持有用户的 cookie 或
// Basic Auth 缓存，不构成 CSRF，因此放行——否则这套 API 就没法脚本化了。
// 反代改写 Host 导致误杀时，用 TRUSTED_ORIGINS 显式放行。
func csrfGuardMiddleware(trusted []string) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			req := c.Request()
			if safeMethods[req.Method] {
				return next(c)
			}
			origin := strings.TrimRight(strings.TrimSpace(req.Header.Get("Origin")), "/")
			if origin != "" {
				for _, t := range trusted {
					if strings.EqualFold(origin, t) {
						return next(c)
					}
				}
			}
			// 浏览器自报的发起方，最可靠
			if site := strings.TrimSpace(req.Header.Get("Sec-Fetch-Site")); site != "" {
				if site == "same-origin" {
					return next(c)
				}
				return echo.NewHTTPError(http.StatusForbidden,
					"跨站请求被拒绝（Sec-Fetch-Site: "+site+"）。写接口只接受同源调用；如确需跨源访问，用 TRUSTED_ORIGINS 放行该来源")
			}
			// 老浏览器不发 Sec-Fetch-Site，但跨站 POST 一定带 Origin
			if origin != "" {
				if originMatchesHost(origin, req.Host) {
					return next(c)
				}
				return echo.NewHTTPError(http.StatusForbidden,
					"跨站请求被拒绝（Origin "+origin+" 与本服务 "+req.Host+" 不一致）。如确需跨源访问，用 TRUSTED_ORIGINS 放行该来源")
			}
			// 非浏览器请求：不会自动携带凭据，放行
			return next(c)
		}
	}
}

// basicAuthMiddleware 按环境变量构造 Basic Auth 中间件，同时返回生效的用户名。
//
// 未设置 BASIC_AUTH_PASSWORD 时返回 nil —— 此时整个鉴权中间件不会挂载，
// 所有接口（不只是 /health）都可无凭据访问。这是刻意保留的默认行为，
// 便于在内网或 localhost 上零配置起步；对外暴露前务必设置密码。
func basicAuthMiddleware() (echo.MiddlewareFunc, string) {
	pw := os.Getenv("BASIC_AUTH_PASSWORD")
	if pw == "" {
		return nil, ""
	}
	expectedUser := strings.TrimSpace(os.Getenv("BASIC_AUTH_USER"))
	if expectedUser == "" {
		expectedUser = "admin"
	}
	return middleware.BasicAuthWithConfig(middleware.BasicAuthConfig{
		// /health 与 /ready 供 Ingress / K8s 探针使用，必须免鉴权：
		// 探针配置里通常带不了 Basic Auth 凭据
		Skipper: func(c echo.Context) bool {
			return c.Path() == "/health" || c.Path() == "/ready"
		},
		Validator: func(username, password string, c echo.Context) (bool, error) {
			// 定长比较：用 == 会让比对耗时随匹配前缀长度变化。
			//
			// 用 secure.ConstantTimeEqual 而不是 subtle.ConstantTimeCompare：
			// 后者在两侧长度不等时立即返回 0，于是耗时会随「猜的长度是否等于
			// 真实长度」而变，把口令长度泄漏出去。前者先把两侧补到同长再比。
			userOk := secure.ConstantTimeEqual(username, expectedUser)
			passOk := secure.ConstantTimeEqual(password, pw)
			return userOk && passOk, nil
		},
	}), expectedUser
}

// loadI18n 读取内嵌的语言文件
func loadI18n(lang string) (map[string]string, error) {
	data, err := i18nFS.ReadFile("i18n/" + lang + ".json")
	if err != nil {
		return nil, err
	}
	var t map[string]string
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, err
	}
	return t, nil
}

// newEchoServer 组装整个 HTTP 服务：中间件顺序、错误格式、模板、i18n 与路由表。
//
// 从 main 里拆出来是为了能整体测试。单独测 csrfGuardMiddleware 只能证明这个函数
// 判得对，证明不了它真的挂在了写接口前面，而「中间件写好了但没挂上」恰恰是这类
// 防护最典型的失效方式——v1.6.0 把鉴权、路由表、监听地址拆出来也是同一个理由。
func newEchoServer() *echo.Echo {
	e := echo.New()
	e.HideBanner = true
	e.Use(middleware.Recover(), requestLogger(), middleware.Secure())
	e.Use(metricsMiddleware())
	e.HTTPErrorHandler = httpErrorHandler
	// 放在鉴权之前：跨站请求无论带不带凭据，都该在这里就结束
	trusted := trustedOrigins()
	e.Use(csrfGuardMiddleware(trusted))
	if len(trusted) > 0 {
		log.Printf("CSRF 白名单来源: %s", strings.Join(trusted, ", "))
	}
	if mw, user := basicAuthMiddleware(); mw != nil {
		e.Use(mw)
		log.Printf("Basic Auth 已启用（用户: %s）", user)
	}
	e.Renderer = newTemplateRenderer()
	handler.I18nLoader = loadI18n
	registerRoutes(e)
	return e
}

// registerRoutes 挂载全部路由
func registerRoutes(e *echo.Echo) {
	e.GET("/health", handler.Health)
	e.GET("/ready", handler.Ready)
	e.GET("/version", handler.VersionInfo)
	// 不在 Skipper 里，因此配了 Basic Auth 后 /metrics 同样需要凭据
	e.GET(metricsPath, metricsHandler())
	e.GET("/", handler.Index)
	// HEAD 一并注册：只挂 GET 的话，探活脚本或代理对静态资源发 HEAD 会吃到 405
	staticH := staticHandler()
	e.GET("/static/*", staticH)
	e.HEAD("/static/*", staticH)
	e.GET("/api/runners", handler.ListRunners)
	e.GET("/api/runner-rows", handler.RunnerRows)
	e.GET("/api/runners/:name", handler.GetRunner)
	e.POST("/api/runners", handler.AddRunner)
	// 静态路径，放在 /api/runners/:name 之外，避免与名为 check 的 Runner 抢路由
	e.GET("/api/runner-precheck", handler.PrecheckRunner)
	e.PUT("/api/runners/:name", handler.UpdateRunner)
	e.DELETE("/api/runners/:name", handler.RemoveRunnerByName)
	e.POST("/api/runners/:name/start", handler.StartRunner)
	e.POST("/api/runners/:name/stop", handler.StopRunner)
	e.POST("/api/runners/:name/recreate", handler.RecreateRunner)
}

// listenAddr 由配置推出监听地址；未配置端口时回落到 :8080
func listenAddr(cfg *config.Config) string {
	if cfg == nil || cfg.Server.Port <= 0 {
		return ":8080"
	}
	if cfg.Server.Addr != "" {
		return fmt.Sprintf("%s:%d", cfg.Server.Addr, cfg.Server.Port)
	}
	return fmt.Sprintf(":%d", cfg.Server.Port)
}
