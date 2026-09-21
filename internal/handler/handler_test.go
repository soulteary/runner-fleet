package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/soulteary/runner-fleet/internal/config"
)

func init() {
	dir := os.TempDir()
	ConfigPath = filepath.Join(dir, "handler-test-config.yaml")
	cfg := &config.Config{
		Server:  config.ServerConfig{Port: 8080},
		Runners: config.RunnersConfig{BasePath: dir, Items: []config.RunnerItem{}},
	}
	_ = cfg.Save(ConfigPath)
}

// /health 仍是存活探针：进程活着恒为 200，不挂任何依赖检查。
// status 取值仍是 "ok"（health-kit 的 StatusHealthy 就是这个字符串），
// 只是多了一个 service 字段——对 status == "ok" 的判断是兼容的。
func TestHealth(t *testing.T) {
	e := echo.New()
	e.GET("/health", Health)
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d", rec.Code)
	}
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("响应不是 JSON: %v (%s)", err, rec.Body.String())
	}
	if m["status"] != "ok" {
		t.Errorf("status = %v，期望 ok（负载均衡与监控可能在读这个值）", m["status"])
	}
	if m["service"] != "runner-fleet" {
		t.Errorf("service = %v，期望 runner-fleet", m["service"])
	}
	// 始终免鉴权的端点，不能吐出逐项检查结果
	if _, ok := m["checks"]; ok {
		t.Errorf("/health 不应包含 checks：%v", m)
	}
}

func TestVersionInfo(t *testing.T) {
	Version = "test-1.0"
	defer func() { Version = "" }()
	e := echo.New()
	e.GET("/version", VersionInfo)
	req := httptest.NewRequest(http.MethodGet, "/version", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d", rec.Code)
	}
	var m map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&m); err != nil {
		t.Fatal(err)
	}
	if m["version"] != "test-1.0" {
		t.Errorf("version = %q", m["version"])
	}
}

func TestVersionInfo_EmptyDefaultsToDev(t *testing.T) {
	Version = ""
	defer func() { Version = "" }()
	e := echo.New()
	e.GET("/version", VersionInfo)
	req := httptest.NewRequest(http.MethodGet, "/version", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d", rec.Code)
	}
	var m map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&m); err != nil {
		t.Fatal(err)
	}
	if m["version"] != "dev" {
		t.Errorf("version = %q, want dev", m["version"])
	}
}

func TestAddRunner_InvalidName(t *testing.T) {
	e := echo.New()
	e.POST("/api/runners", AddRunner)
	body := map[string]string{
		"name":        "bad..name",
		"target_type": "org",
		"target":      "myorg",
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/runners", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid name, got %d", rec.Code)
	}
}

func TestUpdateRunner_NameImmutable(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	cfg := &config.Config{
		Runners: config.RunnersConfig{
			BasePath: dir,
			Items:    []config.RunnerItem{{Name: "r1", TargetType: "org", Target: "o1"}},
		},
	}
	_ = cfg.Save(cfgPath)
	ConfigPath = cfgPath
	defer func() { ConfigPath = filepath.Join(os.TempDir(), "handler-test-config.yaml") }()

	e := echo.New()
	e.PUT("/api/runners/:name", UpdateRunner)
	body := map[string]any{"name": "r2", "target_type": "org", "target": "o1"}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPut, "/api/runners/r1", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 when body name differs from URL, got %d", rec.Code)
	}
}

func TestAddRunner_WhitespaceName(t *testing.T) {
	e := echo.New()
	e.POST("/api/runners", AddRunner)
	body := map[string]string{
		"name":        "   ",
		"target_type": "org",
		"target":      "myorg",
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/runners", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for whitespace name, got %d", rec.Code)
	}
}

func TestUpdateRunner_TrimmedBodyNameAccepted(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	cfg := &config.Config{
		Runners: config.RunnersConfig{
			BasePath: dir,
			Items:    []config.RunnerItem{{Name: "r1", TargetType: "org", Target: "o1"}},
		},
	}
	_ = cfg.Save(cfgPath)
	ConfigPath = cfgPath
	defer func() { ConfigPath = filepath.Join(os.TempDir(), "handler-test-config.yaml") }()

	e := echo.New()
	e.PUT("/api/runners/:name", UpdateRunner)
	body := map[string]any{"name": "  r1  ", "target_type": "org", "target": "o1"}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPut, "/api/runners/r1", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 when body name trims to URL name, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestStartRunner_ProbeFailureFallsBackToInstalledStatus(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	installDir := filepath.Join(dir, "r1")
	if err := os.MkdirAll(installDir, 0755); err != nil {
		t.Fatal(err)
	}
	// 标记为已注册状态
	if err := os.WriteFile(filepath.Join(installDir, ".runner"), []byte("ok"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		Runners: config.RunnersConfig{
			BasePath:      dir,
			ContainerMode: true,
			Items: []config.RunnerItem{
				{Name: "r1", TargetType: "org", Target: "o1"},
			},
		},
	}
	if err := cfg.Save(cfgPath); err != nil {
		t.Fatal(err)
	}

	oldConfigPath := ConfigPath
	ConfigPath = cfgPath
	defer func() { ConfigPath = oldConfigPath }()

	// 让容器状态探测失败（docker 命令不可执行）
	t.Setenv("PATH", "")
	// 启动阶段走容器模式快速失败分支：若到达此处说明没有被 400 提前拦截
	t.Setenv("DOCKER_HOST", "tcp://runner-dind:2375")

	e := echo.New()
	e.POST("/api/runners/:name/start", StartRunner)
	req := httptest.NewRequest(http.MethodPost, "/api/runners/r1/start", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 when start is attempted after probe failure, got %d body=%s", rec.Code, rec.Body.String())
	}
}

type ctxKey string

// TestLifecycleContext_KeepsRunningAfterRequestCancel 守住「启停不随请求取消」这条线。
//
// 回归背景：界面上点「启动」后若页面刷新（添加 Runner 成功后有 5 秒自动刷新，
// 消息框的关闭按钮也会 reload），浏览器会取消在途请求。启停上下文若派生自
// c.Request().Context()，取消会顺着 exec.CommandContext 变成对 docker 子进程的 SIGKILL，
// 于是 docker create 被砍在半路，只留下「输出: (无输出): signal: killed」，
// 而容器可能已在 daemon 侧建好，下一次启动撞上名字冲突。
func TestLifecycleContext_KeepsRunningAfterRequestCancel(t *testing.T) {
	parent, cancel := context.WithCancel(context.WithValue(context.Background(), ctxKey("trace"), "abc"))
	ctx, done := lifecycleContext(parent, 30*time.Second)
	defer done()

	cancel() // 模拟浏览器刷新导致请求中断

	select {
	case <-ctx.Done():
		t.Fatalf("启停上下文跟随请求一起被取消了: %v", ctx.Err())
	case <-time.After(50 * time.Millisecond):
	}

	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("启停上下文必须带超时，否则 docker 卡住时会永久占用")
	}
	if remaining := time.Until(deadline); remaining <= 0 || remaining > 30*time.Second {
		t.Fatalf("超时时间不合预期: %v", remaining)
	}
	if v, _ := ctx.Value(ctxKey("trace")).(string); v != "abc" {
		t.Fatalf("请求上下文中的值应当保留，得到 %q", v)
	}
}

// TestLifecycleContext_TimesOut 超时仍然有效：docker 卡死时不会无限等待。
func TestLifecycleContext_TimesOut(t *testing.T) {
	ctx, done := lifecycleContext(context.Background(), 10*time.Millisecond)
	defer done()

	select {
	case <-ctx.Done():
		if ctx.Err() != context.DeadlineExceeded {
			t.Fatalf("err = %v, want DeadlineExceeded", ctx.Err())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("超时未生效")
	}
}

// TestRecreateRunner_RejectsNonContainerMode 非容器模式下没有 Runner 容器，重建无从谈起
func TestRecreateRunner_RejectsNonContainerMode(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	installDir := filepath.Join(dir, "r1")
	if err := os.MkdirAll(installDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(installDir, ".runner"), []byte("ok"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Server: config.ServerConfig{Port: 8080},
		Runners: config.RunnersConfig{
			BasePath: dir,
			Items:    []config.RunnerItem{{Name: "r1", TargetType: "org", Target: "o1"}},
		},
	}
	if err := cfg.Save(cfgPath); err != nil {
		t.Fatal(err)
	}
	oldConfigPath := ConfigPath
	ConfigPath = cfgPath
	defer func() { ConfigPath = oldConfigPath }()

	e := echo.New()
	e.POST("/api/runners/:name/recreate", RecreateRunner)
	req := httptest.NewRequest(http.MethodPost, "/api/runners/r1/recreate", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", rec.Code, rec.Body.String())
	}
}

// TestRecreateRunner_RejectsUnregistered 没注册的 Runner 不存在容器，别去删
func TestRecreateRunner_RejectsUnregistered(t *testing.T) {
	// 断言走 i18n 的键而不是某一种语言的措辞：消息现在按请求语言翻译，
	// 再断言中文原文等于把这条用例钉死在一种语言上。
	defer withI18n(map[string]string{
		"api.recreate_requires_registered": "ONLY-REGISTERED %s",
	})()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.MkdirAll(filepath.Join(dir, "r1"), 0755); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Server: config.ServerConfig{Port: 8080},
		Runners: config.RunnersConfig{
			BasePath:      dir,
			ContainerMode: true,
			Items:         []config.RunnerItem{{Name: "r1", TargetType: "org", Target: "o1"}},
		},
	}
	if err := cfg.Save(cfgPath); err != nil {
		t.Fatal(err)
	}
	oldConfigPath := ConfigPath
	ConfigPath = cfgPath
	defer func() { ConfigPath = oldConfigPath }()

	e := echo.New()
	e.POST("/api/runners/:name/recreate", RecreateRunner)
	req := httptest.NewRequest(http.MethodPost, "/api/runners/r1/recreate", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "ONLY-REGISTERED") {
		t.Fatalf("应说明只有已注册的 Runner 可重建，实际: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "new") {
		t.Fatalf("消息里应带上当前状态，实际: %s", rec.Body.String())
	}
}
