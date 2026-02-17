package main

import (
	"context"
	"embed"
	"flag"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/lab-dev/github-actions-runner-manager/internal/config"
	"github.com/lab-dev/github-actions-runner-manager/internal/handler"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
)

//go:embed templates/*.html
var templateFS embed.FS

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
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
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

	e.Renderer = newTemplateRenderer()
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
