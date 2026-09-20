package main

import (
	"context"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/lab-dev/github-actions-runner-manager/internal/config"
	"github.com/lab-dev/github-actions-runner-manager/internal/githubcheck"
	"github.com/lab-dev/github-actions-runner-manager/internal/handler"
	"github.com/lab-dev/github-actions-runner-manager/internal/runner"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed i18n/*.json
var i18nFS embed.FS

// Version 由构建时 -ldflags "-X main.Version=..." 注入
var Version = "dev"

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

	if *showVersion {
		fmt.Println(Version)
		os.Exit(0)
	}

	handler.ConfigPath = *configPath
	handler.Version = Version
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

	e := echo.New()
	e.HideBanner = true
	e.Use(middleware.Recover(), middleware.RequestLogger(), middleware.Secure())
	e.HTTPErrorHandler = httpErrorHandler

	if mw, user := basicAuthMiddleware(); mw != nil {
		e.Use(mw)
		log.Printf("Basic Auth 已启用（用户: %s）", user)
	}

	e.Renderer = newTemplateRenderer()
	handler.I18nLoader = loadI18n
	registerRoutes(e)

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
		// /health 供 Ingress / K8s 探针使用，必须免鉴权
		Skipper: func(c echo.Context) bool {
			return c.Path() == "/health"
		},
		Validator: func(username, password string, c echo.Context) (bool, error) {
			// 定长比较：用 == 会让比对耗时随匹配前缀长度变化
			userOk := subtle.ConstantTimeCompare([]byte(username), []byte(expectedUser)) == 1
			passOk := subtle.ConstantTimeCompare([]byte(password), []byte(pw)) == 1
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

// registerRoutes 挂载全部路由
func registerRoutes(e *echo.Echo) {
	e.GET("/health", handler.Health)
	e.GET("/version", handler.VersionInfo)
	e.GET("/", handler.Index)
	e.GET("/api/runners", handler.ListRunners)
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
