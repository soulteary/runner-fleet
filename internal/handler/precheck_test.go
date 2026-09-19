package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/lab-dev/github-actions-runner-manager/internal/config"
	"github.com/labstack/echo/v4"
)

// noContainers 宿主机上没有任何同名容器
func noContainers(string) (bool, string, bool) { return false, "", true }

// containerNamed 指定容器名存在
func containerNamed(name, status string) containerLookup {
	return func(cn string) (bool, string, bool) {
		if cn == name {
			return true, status, true
		}
		return false, "", true
	}
}

func conflictTypes(conflicts []RunnerConflict) []string {
	out := make([]string, 0, len(conflicts))
	for _, c := range conflicts {
		out = append(out, c.Type)
	}
	return out
}

func hasType(conflicts []RunnerConflict, want string) bool {
	for _, c := range conflicts {
		if c.Type == want {
			return true
		}
	}
	return false
}

func TestCollectRunnerConflicts_NameTaken(t *testing.T) {
	cfg := &config.Config{Runners: config.RunnersConfig{
		BasePath: t.TempDir(),
		Items:    []config.RunnerItem{{Name: "droiddesk", TargetType: "repo", Target: "o/r"}},
	}}
	conflicts := collectRunnerConflicts(cfg, "droiddesk", "", noContainers, false)
	if !hasType(conflicts, ConflictNameTaken) {
		t.Fatalf("同名 Runner 未被检出: %v", conflictTypes(conflicts))
	}
	if !hasErrorConflict(conflicts) {
		t.Fatal("同名应为 error 级")
	}
	// 同名 Runner 自己的目录与容器不该再报一遍
	if len(conflicts) != 1 {
		t.Fatalf("同名时只该有一条冲突，得到 %v", conflictTypes(conflicts))
	}
}

func TestCollectRunnerConflicts_ContainerNameCollision(t *testing.T) {
	base := t.TempDir()
	cfg := &config.Config{Runners: config.RunnersConfig{
		BasePath:      base,
		ContainerMode: true,
		Items:         []config.RunnerItem{{Name: "build.1", TargetType: "org", Target: "o"}},
	}}
	// build.1 与 build-1 都会规范化成 github-runner-build-1
	conflicts := collectRunnerConflicts(cfg, "build-1", "", noContainers, false)
	if !hasType(conflicts, ConflictContainerName) {
		t.Fatalf("容器名冲突未被检出: %v", conflictTypes(conflicts))
	}
	// 非容器模式下容器名无所谓，不该报
	cfg.Runners.ContainerMode = false
	if got := collectRunnerConflicts(cfg, "build-1", "", noContainers, false); hasType(got, ConflictContainerName) {
		t.Fatalf("非容器模式不该报容器名冲突: %v", conflictTypes(got))
	}
}

func TestCollectRunnerConflicts_InstallDirTaken(t *testing.T) {
	base := t.TempDir()
	cfg := &config.Config{Runners: config.RunnersConfig{
		BasePath: base,
		Items:    []config.RunnerItem{{Name: "a", Path: "shared", TargetType: "org", Target: "o"}},
	}}
	conflicts := collectRunnerConflicts(cfg, "b", "shared", noContainers, false)
	if !hasType(conflicts, ConflictInstallDir) {
		t.Fatalf("安装目录冲突未被检出: %v", conflictTypes(conflicts))
	}
}

func TestCollectRunnerConflicts_DirState(t *testing.T) {
	base := t.TempDir()
	cfg := &config.Config{Runners: config.RunnersConfig{BasePath: base}}

	// 已注册的残留目录：再次注册会失败，按 error 处理
	registered := filepath.Join(base, "leftover")
	if err := os.MkdirAll(registered, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(registered, ".runner"), []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}
	// 带注册 token：config.sh 会拒绝重复配置，属于硬失败
	conflicts := collectRunnerConflicts(cfg, "leftover", "", noContainers, true)
	if !hasType(conflicts, ConflictDirRegistered) || !hasErrorConflict(conflicts) {
		t.Fatalf("已注册的残留目录未按 error 检出: %v", conflictTypes(conflicts))
	}
	// 不带 token：这是「把已有 Runner 接管进配置」的正常用法，不能挡
	adopt := collectRunnerConflicts(cfg, "leftover", "", noContainers, false)
	if !hasType(adopt, ConflictDirAdopt) {
		t.Fatalf("接管已注册目录未给出提示: %v", conflictTypes(adopt))
	}
	if hasErrorConflict(adopt) {
		t.Fatalf("不带 token 接管已注册目录不该被阻塞: %v", conflictTypes(adopt))
	}

	// 非空但未注册：可以继续，只提示
	halfInstalled := filepath.Join(base, "half")
	if err := os.MkdirAll(halfInstalled, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(halfInstalled, "config.sh"), []byte("#!/bin/sh"), 0644); err != nil {
		t.Fatal(err)
	}
	conflicts = collectRunnerConflicts(cfg, "half", "", noContainers, false)
	if !hasType(conflicts, ConflictDirExists) {
		t.Fatalf("非空目录未被检出: %v", conflictTypes(conflicts))
	}
	if hasErrorConflict(conflicts) {
		t.Fatal("非空但未注册的目录不该阻塞添加")
	}

	// 空目录与不存在的目录都不算冲突
	if got := collectRunnerConflicts(cfg, "fresh", "", noContainers, false); len(got) != 0 {
		t.Fatalf("全新名称不该有冲突: %v", conflictTypes(got))
	}
}

func TestCollectRunnerConflicts_ExistingContainer(t *testing.T) {
	cfg := &config.Config{Runners: config.RunnersConfig{BasePath: t.TempDir(), ContainerMode: true}}
	lookup := containerNamed("github-runner-droiddesk", "running")

	conflicts := collectRunnerConflicts(cfg, "droiddesk", "", lookup, false)
	if !hasType(conflicts, ConflictContainerExists) || !hasErrorConflict(conflicts) {
		t.Fatalf("宿主机上的同名残留容器未按 error 检出: %v", conflictTypes(conflicts))
	}

	// docker 不可用时（known=false）不能把添加流程挡住
	unknown := func(string) (bool, string, bool) { return false, "", false }
	if got := collectRunnerConflicts(cfg, "droiddesk", "", unknown, false); len(got) != 0 {
		t.Fatalf("docker 不可用时应跳过容器检查: %v", conflictTypes(got))
	}
	// lookup 为 nil 同理
	if got := collectRunnerConflicts(cfg, "droiddesk", "", nil, false); len(got) != 0 {
		t.Fatalf("无 lookup 时应跳过容器检查: %v", conflictTypes(got))
	}
}

func TestSuggestRunnerName_SkipsTakenNames(t *testing.T) {
	base := t.TempDir()
	cfg := &config.Config{Runners: config.RunnersConfig{
		BasePath: base,
		Items: []config.RunnerItem{
			{Name: "droiddesk", TargetType: "repo", Target: "o/r"},
			{Name: "droiddesk-2", TargetType: "repo", Target: "o/r"},
		},
	}}
	if got := suggestRunnerName(cfg, "droiddesk", "", noContainers, false); got != "droiddesk-3" {
		t.Fatalf("suggested = %q, want droiddesk-3", got)
	}
}

func TestPrecheckRunner_ReportsConflictAndSuggestion(t *testing.T) {
	base := t.TempDir()
	cfgPath := filepath.Join(base, "config.yaml")
	cfg := &config.Config{
		Server: config.ServerConfig{Port: 8080},
		Runners: config.RunnersConfig{
			BasePath: base,
			Items:    []config.RunnerItem{{Name: "droiddesk", TargetType: "repo", Target: "o/r"}},
		},
	}
	if err := cfg.Save(cfgPath); err != nil {
		t.Fatal(err)
	}
	oldConfigPath := ConfigPath
	ConfigPath = cfgPath
	defer func() { ConfigPath = oldConfigPath }()

	e := echo.New()
	e.GET("/api/runner-precheck", PrecheckRunner)
	req := httptest.NewRequest(http.MethodGet, "/api/runner-precheck?name=droiddesk", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	var resp PrecheckResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Available {
		t.Fatal("已存在同名 Runner，available 应为 false")
	}
	if !hasType(resp.Conflicts, ConflictNameTaken) {
		t.Fatalf("conflicts = %v", conflictTypes(resp.Conflicts))
	}
	if resp.SuggestedName != "droiddesk-2" {
		t.Fatalf("suggested_name = %q, want droiddesk-2", resp.SuggestedName)
	}
}

func TestPrecheckRunner_AvailableName(t *testing.T) {
	base := t.TempDir()
	cfgPath := filepath.Join(base, "config.yaml")
	cfg := &config.Config{
		Server:  config.ServerConfig{Port: 8080},
		Runners: config.RunnersConfig{BasePath: base, Items: []config.RunnerItem{}},
	}
	if err := cfg.Save(cfgPath); err != nil {
		t.Fatal(err)
	}
	oldConfigPath := ConfigPath
	ConfigPath = cfgPath
	defer func() { ConfigPath = oldConfigPath }()

	e := echo.New()
	e.GET("/api/runner-precheck", PrecheckRunner)
	req := httptest.NewRequest(http.MethodGet, "/api/runner-precheck?name=brand-new", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	var resp PrecheckResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.Available || len(resp.Conflicts) != 0 {
		t.Fatalf("全新名称应可用: %+v", resp)
	}
	if resp.SuggestedName != "" {
		t.Fatalf("可用时不该给建议名，得到 %q", resp.SuggestedName)
	}
}

func TestPrecheckRunner_RejectsUnsafeName(t *testing.T) {
	e := echo.New()
	e.GET("/api/runner-precheck", PrecheckRunner)
	for _, name := range []string{"../etc", "a/b", ""} {
		req := httptest.NewRequest(http.MethodGet, "/api/runner-precheck?name="+name, nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("name=%q status = %d, want 400", name, rec.Code)
		}
	}
}

// TestAddRunner_ConflictInsteadOfSilentRename 同名时必须明确报冲突并给出建议名，
// 而不是像以前那样静默建出 droiddesk-ab12cd —— 用户以为自己在操作 droiddesk。
func TestAddRunner_ConflictInsteadOfSilentRename(t *testing.T) {
	base := t.TempDir()
	cfgPath := filepath.Join(base, "config.yaml")
	cfg := &config.Config{
		Server: config.ServerConfig{Port: 8080},
		Runners: config.RunnersConfig{
			BasePath: base,
			Items:    []config.RunnerItem{{Name: "droiddesk", TargetType: "repo", Target: "o/r"}},
		},
	}
	if err := cfg.Save(cfgPath); err != nil {
		t.Fatal(err)
	}
	oldConfigPath := ConfigPath
	ConfigPath = cfgPath
	defer func() { ConfigPath = oldConfigPath }()

	e := echo.New()
	e.POST("/api/runners", AddRunner)
	body := `{"name":"droiddesk","target_type":"repo","target":"o/r"}`
	req := httptest.NewRequest(http.MethodPost, "/api/runners", bytes.NewBufferString(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409, body = %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		SuggestedName string           `json:"suggested_name"`
		Conflicts     []RunnerConflict `json:"conflicts"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.SuggestedName != "droiddesk-2" {
		t.Fatalf("suggested_name = %q", resp.SuggestedName)
	}
	if !hasType(resp.Conflicts, ConflictNameTaken) {
		t.Fatalf("conflicts = %v", conflictTypes(resp.Conflicts))
	}

	// 配置不该被改动
	after, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Runners.Items) != 1 {
		t.Fatalf("冲突时不该写入新 Runner，当前 %d 个", len(after.Runners.Items))
	}
}

// TestAddRunner_AutoRenameKeepsOldBehavior 显式要求时仍可自动改名（兼容既有脚本）
func TestAddRunner_AutoRenameKeepsOldBehavior(t *testing.T) {
	base := t.TempDir()
	cfgPath := filepath.Join(base, "config.yaml")
	cfg := &config.Config{
		Server: config.ServerConfig{Port: 8080},
		Runners: config.RunnersConfig{
			BasePath: base,
			Items:    []config.RunnerItem{{Name: "droiddesk", TargetType: "repo", Target: "o/r"}},
		},
	}
	if err := cfg.Save(cfgPath); err != nil {
		t.Fatal(err)
	}
	oldConfigPath := ConfigPath
	ConfigPath = cfgPath
	defer func() { ConfigPath = oldConfigPath }()

	e := echo.New()
	e.POST("/api/runners", AddRunner)
	body := `{"name":"droiddesk","target_type":"repo","target":"o/r","auto_rename":true}`
	req := httptest.NewRequest(http.MethodPost, "/api/runners", bytes.NewBufferString(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Name != "droiddesk-2" {
		t.Fatalf("name = %q, want droiddesk-2", resp.Name)
	}
}
