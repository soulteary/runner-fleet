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
	configPath := flag.String("config", "config.yaml", "配置文件路径")
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

	e := echo.New()
	e.HideBanner = true
	e.Use(middleware.Recover(), middleware.RequestLogger(), middleware.Secure())
	e.HTTPErrorHandler = func(err error, c echo.Context) {
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

	if pw := os.Getenv("BASIC_AUTH_PASSWORD"); pw != "" {
		expectedUser := strings.TrimSpace(os.Getenv("BASIC_AUTH_USER"))
		if expectedUser == "" {
			expectedUser = "admin"
		}
		expectedPassword := pw
		e.Use(middleware.BasicAuthWithConfig(middleware.BasicAuthConfig{
			Skipper: func(c echo.Context) bool {
				return c.Path() == "/health"
			},
			Validator: func(username, password string, c echo.Context) (bool, error) {
				userOk := subtle.ConstantTimeCompare([]byte(username), []byte(expectedUser)) == 1
				passOk := subtle.ConstantTimeCompare([]byte(password), []byte(expectedPassword)) == 1
				return userOk && passOk, nil
			},
		}))
		log.Printf("Basic Auth 已启用（用户: %s）", expectedUser)
	}

	e.Renderer = newTemplateRenderer()
	handler.I18nLoader = func(lang string) (map[string]string, error) {
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
	e.GET("/health", handler.Health)
	e.GET("/version", handler.VersionInfo)
	e.GET("/", handler.Index)
	e.GET("/api/runners", handler.ListRunners)
	e.GET("/api/runners/:name", handler.GetRunner)
	e.POST("/api/runners", handler.AddRunner)
	e.PUT("/api/runners/:name", handler.UpdateRunner)
	e.DELETE("/api/runners/:name", handler.RemoveRunnerByName)
	e.POST("/api/runners/:name/start", handler.StartRunner)
	e.POST("/api/runners/:name/stop", handler.StopRunner)

	addr := ":8080"
	if cfg.Server.Port > 0 {
		if cfg.Server.Addr != "" {
			addr = fmt.Sprintf("%s:%d", cfg.Server.Addr, cfg.Server.Port)
		} else {
			addr = fmt.Sprintf(":%d", cfg.Server.Port)
		}
	}
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
	list := runner.List(cfg)
	ctx := context.Background()
	for _, info := range list {
		if info.Status == runner.StatusInstalled && !info.Running {
			if err := runner.StartIfInstalled(ctx, cfg, info.Name, info.InstallDir); err != nil {
				log.Printf("自动启动 runner %s 失败: %v", info.Name, err)
			} else {
				log.Printf("已自动启动 runner: %s", info.Name)
			}
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
				list := runner.List(cfg)
				ctx := context.Background()
				for _, info := range list {
					if info.Status == runner.StatusInstalled && !info.Running {
						if err := runner.StartIfInstalled(ctx, cfg, info.Name, info.InstallDir); err != nil {
							log.Printf("定时拉起 runner %s 失败: %v", info.Name, err)
						} else {
							log.Printf("已定时拉起 runner: %s", info.Name)
						}
					}
				}
			}
			firstRun = false
		}
		<-ticker.C
	}
}
