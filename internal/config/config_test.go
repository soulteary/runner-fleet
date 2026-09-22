package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestLoad_MissingFile(t *testing.T) {
	// 文件不存在时，Load 基于默认配置+环境变量生成并写入文件后返回成功（便于全容器部署仅用 .env）
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("expected success when file missing (generate from defaults): %v", err)
	}
	if cfg.Server.Port != 8080 || cfg.Server.Addr != "0.0.0.0" {
		t.Errorf("expected default server, got port=%d addr=%q", cfg.Server.Port, cfg.Server.Addr)
	}
	if cfg.Runners.BasePath != "./runners" || len(cfg.Runners.Items) != 0 {
		t.Errorf("expected default runners base_path=./runners items=0, got base_path=%q items=%d", cfg.Runners.BasePath, len(cfg.Runners.Items))
	}
	// 应已写入文件，再次 Load 可读回
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("re-load after generate failed: %v", err)
	}
	if loaded.Server.Port != cfg.Server.Port {
		t.Errorf("re-loaded port %d != generated %d", loaded.Server.Port, cfg.Server.Port)
	}
}

func TestLoad_MissingFile_WithEnvGeneratesConfig(t *testing.T) {
	// 文件不存在且设置了 .env 中常用变量时，Load 从默认配置+环境变量生成并写入 config.yaml，内容符合 env
	restore := setEnvsAndRestore(t, map[string]string{
		"CONTAINER_MODE":     "true",
		"VOLUME_HOST_PATH":   "/data/runners",
		"RUNNERS_BASE_PATH":  "/app/runners",
		"SERVER_PORT":        "9090",
		"JOB_DOCKER_BACKEND": "host-socket",
		"RUNNER_IMAGE":       "custom/runner:v1",
		"MANAGER_IMAGE":      "",
		"FLEET_IMAGE_TAG":    "",
	}, []string{"CONTAINER_MODE", "VOLUME_HOST_PATH", "RUNNERS_BASE_PATH", "SERVER_PORT", "JOB_DOCKER_BACKEND", "RUNNER_IMAGE", "MANAGER_IMAGE", "FLEET_IMAGE_TAG"})
	defer restore()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load(missing file) with env should succeed: %v", err)
	}
	if !cfg.Runners.ContainerMode {
		t.Error("expected ContainerMode true from CONTAINER_MODE")
	}
	if cfg.Runners.VolumeHostPath != "/data/runners" {
		t.Errorf("expected VolumeHostPath /data/runners, got %q", cfg.Runners.VolumeHostPath)
	}
	if cfg.Runners.BasePath != "/app/runners" {
		t.Errorf("expected BasePath /app/runners, got %q", cfg.Runners.BasePath)
	}
	if cfg.Server.Port != 9090 {
		t.Errorf("expected Server.Port 9090 from SERVER_PORT, got %d", cfg.Server.Port)
	}
	if cfg.Runners.JobDockerBackend != "host-socket" {
		t.Errorf("expected JobDockerBackend host-socket, got %q", cfg.Runners.JobDockerBackend)
	}
	if cfg.Runners.ContainerImage != "custom/runner:v1" {
		t.Errorf("expected ContainerImage from RUNNER_IMAGE, got %q", cfg.Runners.ContainerImage)
	}
	// 生成的文件应存在且可读回
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected config file to be created at %s: %v", path, err)
	}
	reloaded, err := Load(path)
	if err != nil {
		t.Fatalf("re-load generated file failed: %v", err)
	}
	if reloaded.Runners.BasePath != "/app/runners" || reloaded.Server.Port != 9090 {
		t.Errorf("re-loaded config mismatch: base_path=%q port=%d", reloaded.Runners.BasePath, reloaded.Server.Port)
	}
}

func TestLoad_Defaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	// 空或仅部分字段，应应用默认值
	content := []byte(`
server: {}
runners: {}
`)
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Port != 8080 {
		t.Errorf("expected Server.Port 8080, got %d", cfg.Server.Port)
	}
	if cfg.Runners.BasePath != "./runners" {
		t.Errorf("expected Runners.BasePath ./runners, got %q", cfg.Runners.BasePath)
	}
}

func TestLoad_Save_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	cfg := &Config{
		Server: ServerConfig{Port: 9000, Addr: "127.0.0.1"},
		Runners: RunnersConfig{BasePath: "/tmp/runners", Items: []RunnerItem{
			{Name: "r1", TargetType: "repo", Target: "owner/repo", Labels: []string{"a", "b"}},
		}},
	}
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Server.Port != 9000 || loaded.Server.Addr != "127.0.0.1" {
		t.Errorf("server mismatch: %+v", loaded.Server)
	}
	if loaded.Runners.BasePath != "/tmp/runners" {
		t.Errorf("base_path: got %q", loaded.Runners.BasePath)
	}
	if len(loaded.Runners.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(loaded.Runners.Items))
	}
	if loaded.Runners.Items[0].Name != "r1" || loaded.Runners.Items[0].Target != "owner/repo" {
		t.Errorf("item mismatch: %+v", loaded.Runners.Items[0])
	}
}

func TestLoadAndSave(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	cfg := &Config{
		Runners: RunnersConfig{BasePath: dir, Items: []RunnerItem{}},
	}
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	err := LoadAndSave(path, func(c *Config) error {
		c.Runners.Items = append(c.Runners.Items, RunnerItem{Name: "added", TargetType: "org", Target: "myorg"})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	loaded, _ := Load(path)
	if len(loaded.Runners.Items) != 1 || loaded.Runners.Items[0].Name != "added" {
		t.Errorf("LoadAndSave roundtrip failed: %+v", loaded.Runners.Items)
	}
}

func TestRunnerItem_InstallPath(t *testing.T) {
	base := "/base"
	tests := []struct {
		path, name string
		want       string
	}{
		{"", "r1", "/base/r1"},
		{"sub", "r1", "/base/sub"},
	}
	for _, tt := range tests {
		r := RunnerItem{Path: tt.path, Name: tt.name}
		got := r.InstallPath(base)
		if got != filepath.FromSlash(tt.want) && filepath.Clean(got) != filepath.Clean(tt.want) {
			t.Errorf("InstallPath(%q, %q) = %q, want %q", tt.path, tt.name, got, tt.want)
		}
	}
}

func TestValidate_InvalidJobBackend(t *testing.T) {
	cfg := &Config{
		Runners: RunnersConfig{
			BasePath:         "./runners",
			JobDockerBackend: "bad-backend",
		},
	}
	err := Validate(cfg)
	if err == nil || !strings.Contains(err.Error(), "job_docker_backend") {
		t.Fatalf("expected job_docker_backend validation error, got: %v", err)
	}
}

func TestValidate_BackendModeMismatch(t *testing.T) {
	cfg := &Config{
		Runners: RunnersConfig{
			BasePath:         "./runners",
			ContainerMode:    false,
			JobDockerBackend: "host-socket",
		},
	}
	err := Validate(cfg)
	if err == nil || !strings.Contains(err.Error(), "container_mode=false") {
		t.Fatalf("expected mode mismatch error, got: %v", err)
	}
}

func TestValidate_VolumeHostPathRules(t *testing.T) {
	cfg1 := &Config{
		Runners: RunnersConfig{
			BasePath:         "./runners",
			ContainerMode:    false,
			JobDockerBackend: "dind",
			VolumeHostPath:   "/abs/path",
		},
	}
	if err := Validate(cfg1); err == nil || !strings.Contains(err.Error(), "volume_host_path") {
		t.Fatalf("expected volume_host_path mode validation error, got: %v", err)
	}

	cfg2 := &Config{
		Runners: RunnersConfig{
			BasePath:         "/app/runners",
			ContainerMode:    true,
			JobDockerBackend: "dind",
		},
	}
	if err := Validate(cfg2); err == nil || !strings.Contains(err.Error(), "runners.volume_host_path must be set") {
		t.Fatalf("expected missing volume_host_path validation error, got: %v", err)
	}

	cfg3 := &Config{
		Runners: RunnersConfig{
			BasePath:         "/app/runners",
			ContainerMode:    true,
			JobDockerBackend: "dind",
			VolumeHostPath:   "relative/path",
		},
	}
	if err := Validate(cfg3); err == nil || !strings.Contains(err.Error(), "must be an absolute path") {
		t.Fatalf("expected absolute path validation error, got: %v", err)
	}
}

func TestNormalizedContainerName(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"r1", "github-runner-r1"},
		{"a.b", "github-runner-a-b"},
		{"a-b", "github-runner-a-b"},
		{"x_y", "github-runner-x_y"},
		{"..", "github-runner-runner"}, // sanitize 后为空，fallback 为 "runner"
		{"", "github-runner-runner"},
	}
	for _, tt := range tests {
		got := NormalizedContainerName(tt.name)
		if got != tt.want {
			t.Errorf("NormalizedContainerName(%q) = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestIsSafeRunnerNameOrPath(t *testing.T) {
	for _, s := range []string{"", "..", "/", "\\", "a/b", "a..b"} {
		if IsSafeRunnerNameOrPath(s) {
			t.Errorf("IsSafeRunnerNameOrPath(%q) should be false", s)
		}
	}
	for _, s := range []string{"a", "r1", "a-b", "x_y"} {
		if !IsSafeRunnerNameOrPath(s) {
			t.Errorf("IsSafeRunnerNameOrPath(%q) should be true", s)
		}
	}
}

func TestValidateTarget(t *testing.T) {
	if err := ValidateTarget("org", "myorg"); err != nil {
		t.Errorf("org myorg: %v", err)
	}
	if err := ValidateTarget("org", "owner/repo"); err == nil || !strings.Contains(err.Error(), "cannot contain /") {
		t.Errorf("org owner/repo should error: %v", err)
	}
	if err := ValidateTarget("repo", "owner/repo"); err != nil {
		t.Errorf("repo owner/repo: %v", err)
	}
	if err := ValidateTarget("repo", "owner"); err == nil || !strings.Contains(err.Error(), "owner/repo") {
		t.Errorf("repo owner should error: %v", err)
	}
	if err := ValidateTarget("invalid", "x"); err == nil {
		t.Error("invalid type should error")
	}
	if err := ValidateTarget("org", ""); err == nil {
		t.Error("empty target should error")
	}
}

func TestValidate_ContainerNameConflict(t *testing.T) {
	cfg := &Config{
		Runners: RunnersConfig{
			BasePath:         "./runners",
			ContainerMode:    true,
			JobDockerBackend: "dind",
			Items: []RunnerItem{
				{Name: "a.b", TargetType: "org", Target: "org1"},
				{Name: "a-b", TargetType: "org", Target: "org1"},
			},
		},
	}
	err := Validate(cfg)
	if err == nil || !strings.Contains(err.Error(), "container name conflict") {
		t.Fatalf("expected container name conflict error, got: %v", err)
	}
}

func TestLoad_TrimAndDefaultDindHost(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := []byte(`
server: {}
runners:
  container_mode: true
  job_docker_backend: "  dind  "
  dind_host: "   "
  volume_host_path: "   /tmp/runners   "
`)
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Runners.JobDockerBackend != "dind" {
		t.Fatalf("expected backend dind, got %q", cfg.Runners.JobDockerBackend)
	}
	if cfg.Runners.DindHost != "runner-dind" {
		t.Fatalf("expected default dind_host runner-dind, got %q", cfg.Runners.DindHost)
	}
	if cfg.Runners.VolumeHostPath != "/tmp/runners" {
		t.Fatalf("expected trimmed volume_host_path /tmp/runners, got %q", cfg.Runners.VolumeHostPath)
	}
}

func TestLoad_ContainerImageWhitespaceUsesDefault(t *testing.T) {
	// 未设置 FLEET_IMAGE_TAG 时默认使用 v1.9.0-runner
	restore := setEnvAndRestore(t, "FLEET_IMAGE_TAG", "")
	defer restore()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := []byte(`
server: {}
runners:
  container_mode: true
  container_image: "   "
  job_docker_backend: dind
  volume_host_path: /tmp/runners
`)
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "ghcr.io/soulteary/runner-fleet:v1.9.0-runner"
	if cfg.Runners.ContainerImage != want {
		t.Fatalf("expected default container image %q, got %q", want, cfg.Runners.ContainerImage)
	}
}

// setEnvAndRestore 设置环境变量并返回用于恢复的 defer 函数；空 value 表示 Unsetenv
func setEnvAndRestore(t *testing.T, key, value string) func() {
	t.Helper()
	old, had := os.LookupEnv(key)
	if value == "" {
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
		return func() {
			if had {
				_ = os.Setenv(key, old)
			} else {
				_ = os.Unsetenv(key)
			}
		}
	}
	if err := os.Setenv(key, value); err != nil {
		t.Fatal(err)
	}
	return func() {
		if had {
			_ = os.Setenv(key, old)
		} else {
			_ = os.Unsetenv(key)
		}
	}
}

func TestLoad_ContainerImageDefaultWithFleetImageTag(t *testing.T) {
	// 设置 FLEET_IMAGE_TAG=main 时默认使用 main-runner
	restore := setEnvAndRestore(t, "FLEET_IMAGE_TAG", "main")
	defer restore()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := []byte(`
server: {}
runners:
  container_mode: true
  container_image: ""
  job_docker_backend: dind
  volume_host_path: /tmp/runners
`)
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "ghcr.io/soulteary/runner-fleet:main-runner"
	if cfg.Runners.ContainerImage != want {
		t.Fatalf("expected container image %q when FLEET_IMAGE_TAG=main, got %q", want, cfg.Runners.ContainerImage)
	}
}

func TestSave_ValidateOnWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	cfg := &Config{
		Runners: RunnersConfig{
			BasePath:         "./runners",
			ContainerMode:    false,
			JobDockerBackend: "host-socket",
			Items:            []RunnerItem{},
		},
	}
	err := cfg.Save(path)
	if err == nil || !strings.Contains(err.Error(), "container_mode=false") {
		t.Fatalf("expected save-time validation error, got: %v", err)
	}
}

func TestValidate_InvalidRunnerNameOrPathFromConfig(t *testing.T) {
	cfg := &Config{
		Runners: RunnersConfig{
			BasePath:         "./runners",
			ContainerMode:    false,
			JobDockerBackend: "dind",
			Items: []RunnerItem{
				{Name: "bad/name", TargetType: "org", Target: "org1"},
			},
		},
	}
	if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), "name contains an illegal character") {
		t.Fatalf("expected invalid name validation error, got: %v", err)
	}

	cfg2 := &Config{
		Runners: RunnersConfig{
			BasePath:         "./runners",
			ContainerMode:    false,
			JobDockerBackend: "dind",
			Items: []RunnerItem{
				{Name: "ok", Path: "../escape", TargetType: "org", Target: "org1"},
			},
		},
	}
	if err := Validate(cfg2); err == nil || !strings.Contains(err.Error(), "path contains an illegal character") {
		t.Fatalf("expected invalid path validation error, got: %v", err)
	}
}

func TestValidate_TargetRulesFromConfig(t *testing.T) {
	cfg := &Config{
		Runners: RunnersConfig{
			BasePath:         "./runners",
			ContainerMode:    false,
			JobDockerBackend: "dind",
			Items: []RunnerItem{
				{Name: "r1", TargetType: "repo", Target: "owner"},
			},
		},
	}
	if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), "owner/repo") {
		t.Fatalf("expected repo target validation error, got: %v", err)
	}

	cfg2 := &Config{
		Runners: RunnersConfig{
			BasePath:         "./runners",
			ContainerMode:    false,
			JobDockerBackend: "dind",
			Items: []RunnerItem{
				{Name: "r1", TargetType: "org", Target: "owner/repo"},
			},
		},
	}
	if err := Validate(cfg2); err == nil || !strings.Contains(err.Error(), "cannot contain /") {
		t.Fatalf("expected org target validation error, got: %v", err)
	}
}

func TestValidate_InstallPathConflict(t *testing.T) {
	cfg := &Config{
		Runners: RunnersConfig{
			BasePath:         "./runners",
			ContainerMode:    false,
			JobDockerBackend: "dind",
			Items: []RunnerItem{
				{Name: "r1", Path: "same", TargetType: "org", Target: "org1"},
				{Name: "r2", Path: "same", TargetType: "org", Target: "org1"},
			},
		},
	}
	if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), "install directory conflict") {
		t.Fatalf("expected install path conflict validation error, got: %v", err)
	}
}

// setEnvsAndRestore 设置多组环境变量并返回恢复函数；keys 为要恢复的 key 列表（含未设置的）
func setEnvsAndRestore(t *testing.T, envs map[string]string, keysToRestore []string) func() {
	t.Helper()
	old := make(map[string]string)
	for _, k := range keysToRestore {
		if v, had := os.LookupEnv(k); had {
			old[k] = v
		}
	}
	for k, v := range envs {
		if err := os.Setenv(k, v); err != nil {
			t.Fatal(err)
		}
		if _, in := old[k]; !in {
			old[k] = "" // 标记为未设置，恢复时 Unsetenv
		}
	}
	return func() {
		for k, v := range old {
			if v == "" {
				_ = os.Unsetenv(k)
			} else {
				_ = os.Setenv(k, v)
			}
		}
	}
}

func TestLoad_EnvOverrides_ServerPortAndContainerModeAndVolumeHostPath(t *testing.T) {
	restore := setEnvsAndRestore(t, map[string]string{
		"MANAGER_PORT":       "9000",
		"CONTAINER_MODE":     "true",
		"VOLUME_HOST_PATH":   "/host/runners",
		"JOB_DOCKER_BACKEND": "host-socket",
		"CONTAINER_NETWORK":  "mynet",
		"RUNNERS_BASE_PATH":  "/app/runners",
		"RUNNER_IMAGE":       "custom/runner:tag",
		"FLEET_IMAGE_TAG":    "", // 避免影响 MANAGER_IMAGE 推导测试
		"MANAGER_IMAGE":      "", // 本测试用 RUNNER_IMAGE 显式指定
	}, []string{"MANAGER_PORT", "SERVER_PORT", "CONTAINER_MODE", "VOLUME_HOST_PATH", "JOB_DOCKER_BACKEND", "CONTAINER_NETWORK", "RUNNERS_BASE_PATH", "RUNNER_IMAGE", "FLEET_IMAGE_TAG", "MANAGER_IMAGE"})
	defer restore()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := []byte(`
server: { port: 8080 }
runners: { base_path: ./runners, items: [] }
`)
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Port != 9000 {
		t.Errorf("expected Server.Port 9000 from MANAGER_PORT, got %d", cfg.Server.Port)
	}
	if !cfg.Runners.ContainerMode {
		t.Error("expected ContainerMode true from CONTAINER_MODE")
	}
	if cfg.Runners.VolumeHostPath != "/host/runners" {
		t.Errorf("expected VolumeHostPath /host/runners, got %q", cfg.Runners.VolumeHostPath)
	}
	if cfg.Runners.JobDockerBackend != "host-socket" {
		t.Errorf("expected job_docker_backend host-socket, got %q", cfg.Runners.JobDockerBackend)
	}
	if cfg.Runners.ContainerNetwork != "mynet" {
		t.Errorf("expected container_network mynet, got %q", cfg.Runners.ContainerNetwork)
	}
	if cfg.Runners.BasePath != "/app/runners" {
		t.Errorf("expected base_path /app/runners, got %q", cfg.Runners.BasePath)
	}
	if cfg.Runners.ContainerImage != "custom/runner:tag" {
		t.Errorf("expected container_image from RUNNER_IMAGE, got %q", cfg.Runners.ContainerImage)
	}
}

func TestLoad_EnvOverrides_SERVER_PORT(t *testing.T) {
	restore := setEnvsAndRestore(t, map[string]string{"SERVER_PORT": "7000"}, []string{"SERVER_PORT", "MANAGER_PORT"})
	defer restore()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("server: {}\nrunners: { items: [] }"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Port != 7000 {
		t.Errorf("expected Server.Port 7000 from SERVER_PORT, got %d", cfg.Server.Port)
	}
}

func TestLoad_ContainerImageFromManagerImage(t *testing.T) {
	restore := setEnvsAndRestore(t, map[string]string{
		"MANAGER_IMAGE":   "ghcr.io/soulteary/runner-fleet:v1.0.1", // version-check-ignore：这里的 v1.0.1 是 MANAGER_IMAGE 的输入样本，不是当前版本
		"CONTAINER_MODE":  "true",
		"FLEET_IMAGE_TAG": "",
		"RUNNER_IMAGE":    "",
		"CONTAINER_IMAGE": "",
	}, []string{"MANAGER_IMAGE", "CONTAINER_MODE", "FLEET_IMAGE_TAG", "RUNNER_IMAGE", "CONTAINER_IMAGE"})
	defer restore()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := []byte(`
server: {}
runners:
  container_mode: false
  items: []
  job_docker_backend: host-socket
  volume_host_path: /tmp/runners
`)
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "ghcr.io/soulteary/runner-fleet:v1.0.1-runner" // version-check-ignore：同上，断言的是上面那个输入推导出的结果
	if cfg.Runners.ContainerImage != want {
		t.Fatalf("expected container_image %q from MANAGER_IMAGE, got %q", want, cfg.Runners.ContainerImage)
	}
}

func TestLoad_ContainerImageFromManagerImage_NoTag(t *testing.T) {
	restore := setEnvsAndRestore(t, map[string]string{
		"MANAGER_IMAGE":   "ghcr.io/soulteary/runner-fleet",
		"CONTAINER_MODE":  "true",
		"FLEET_IMAGE_TAG": "",
	}, []string{"MANAGER_IMAGE", "CONTAINER_MODE", "FLEET_IMAGE_TAG"})
	defer restore()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := []byte(`
server: {}
runners:
  items: []
  job_docker_backend: host-socket
  volume_host_path: /tmp/runners
`)
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "ghcr.io/soulteary/runner-fleet:latest-runner"
	if cfg.Runners.ContainerImage != want {
		t.Fatalf("expected container_image %q when MANAGER_IMAGE has no tag, got %q", want, cfg.Runners.ContainerImage)
	}
}

func TestLoad_EnvOverrides_SERVER_ADDR(t *testing.T) {
	restore := setEnvsAndRestore(t, map[string]string{"SERVER_ADDR": "127.0.0.1"}, []string{"SERVER_ADDR"})
	defer restore()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("server: {}\nrunners: { items: [] }"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Addr != "127.0.0.1" {
		t.Errorf("expected Server.Addr 127.0.0.1 from SERVER_ADDR, got %q", cfg.Server.Addr)
	}
}

func TestLoad_EnvOverrides_RUNNERS_VOLUME_HOST_PATH(t *testing.T) {
	restore := setEnvsAndRestore(t, map[string]string{
		"CONTAINER_MODE":           "true",
		"VOLUME_HOST_PATH":         "/first",
		"RUNNERS_VOLUME_HOST_PATH": "/second",
		"JOB_DOCKER_BACKEND":       "host-socket",
		"RUNNER_IMAGE":             "img:runner",
	}, []string{"CONTAINER_MODE", "VOLUME_HOST_PATH", "RUNNERS_VOLUME_HOST_PATH", "JOB_DOCKER_BACKEND", "RUNNER_IMAGE"})
	defer restore()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := []byte(`
server: {}
runners: { base_path: /app/runners, items: [], volume_host_path: /yaml }
`)
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Runners.VolumeHostPath != "/second" {
		t.Errorf("expected VolumeHostPath /second (RUNNERS_VOLUME_HOST_PATH overrides), got %q", cfg.Runners.VolumeHostPath)
	}
}

func TestLoad_EnvOverrides_CONTAINER_IMAGE_overrides_RUNNER_IMAGE(t *testing.T) {
	restore := setEnvsAndRestore(t, map[string]string{
		"CONTAINER_MODE":     "true",
		"RUNNER_IMAGE":       "runner:one",
		"CONTAINER_IMAGE":    "container:two",
		"JOB_DOCKER_BACKEND": "host-socket",
		"VOLUME_HOST_PATH":   "/tmp/r",
	}, []string{"CONTAINER_MODE", "RUNNER_IMAGE", "CONTAINER_IMAGE", "JOB_DOCKER_BACKEND", "VOLUME_HOST_PATH"})
	defer restore()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("server: {}\nrunners: { base_path: /app/runners, items: [] }"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Runners.ContainerImage != "container:two" {
		t.Errorf("expected container_image container:two (CONTAINER_IMAGE wins), got %q", cfg.Runners.ContainerImage)
	}
}

func TestLoad_EnvOverrides_InvalidPortIgnored(t *testing.T) {
	for _, envVal := range []string{"abc", "0", "-1", "  "} {
		restore := setEnvsAndRestore(t, map[string]string{"MANAGER_PORT": envVal}, []string{"MANAGER_PORT"})
		dir := t.TempDir()
		path := filepath.Join(dir, "config.yaml")
		if err := os.WriteFile(path, []byte("server: { port: 8080 }\nrunners: { items: [] }"), 0644); err != nil {
			restore()
			t.Fatal(err)
		}
		cfg, err := Load(path)
		restore()
		if err != nil {
			t.Fatalf("MANAGER_PORT=%q: Load failed: %v", envVal, err)
		}
		if cfg.Server.Port != 8080 {
			t.Errorf("MANAGER_PORT=%q: expected port unchanged 8080, got %d", envVal, cfg.Server.Port)
		}
	}
}

func TestLoad_EnvOverrides_CONTAINER_MODE_1(t *testing.T) {
	restore := setEnvsAndRestore(t, map[string]string{"CONTAINER_MODE": "1"}, []string{"CONTAINER_MODE"})
	defer restore()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := []byte(`
server: {}
runners:
  items: []
  job_docker_backend: host-socket
  volume_host_path: /tmp/runners
  container_image: custom:runner
`)
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Runners.ContainerMode {
		t.Error("expected ContainerMode true when CONTAINER_MODE=1")
	}
}

func TestLoad_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("server:\n  port: ["), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestValidate_EmptyItemName(t *testing.T) {
	cfg := &Config{
		Runners: RunnersConfig{
			BasePath:         "./runners",
			ContainerMode:    false,
			JobDockerBackend: "dind",
			Items: []RunnerItem{
				{Name: "  ", TargetType: "org", Target: "myorg"},
			},
		},
	}
	if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), "name cannot be empty") {
		t.Fatalf("expected empty name validation error, got: %v", err)
	}
}

func TestLoadAndSave_LoadFails(t *testing.T) {
	// 文件不存在时 Load 会从默认配置生成，LoadAndSave 应成功并应用 fn
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	err := LoadAndSave(path, func(c *Config) error {
		c.Server.Port = 9000
		return nil
	})
	if err != nil {
		t.Fatalf("LoadAndSave with missing file should succeed (config generated): %v", err)
	}
	loaded, _ := Load(path)
	if loaded.Server.Port != 9000 {
		t.Errorf("expected fn change persisted, got port %d", loaded.Server.Port)
	}
}

func TestLoadAndSave_FnReturnsError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	cfg := &Config{Runners: RunnersConfig{BasePath: dir, Items: []RunnerItem{}}}
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	err := LoadAndSave(path, func(c *Config) error {
		return fmt.Errorf("intentional failure")
	})
	if err == nil || err.Error() != "intentional failure" {
		t.Fatalf("expected fn error to propagate, got: %v", err)
	}
}

func TestLoad_ContainerImageFromManagerImage_ImageWithRegistryPort(t *testing.T) {
	restore := setEnvsAndRestore(t, map[string]string{
		"MANAGER_IMAGE":   "host:5000/my/repo:v1.0",
		"CONTAINER_MODE":  "true",
		"FLEET_IMAGE_TAG": "",
	}, []string{"MANAGER_IMAGE", "CONTAINER_MODE", "FLEET_IMAGE_TAG"})
	defer restore()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := []byte(`
server: {}
runners:
  items: []
  job_docker_backend: host-socket
  volume_host_path: /tmp/runners
`)
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "host:5000/my/repo:v1.0-runner"
	if cfg.Runners.ContainerImage != want {
		t.Fatalf("expected container_image %q (last colon separates tag), got %q", want, cfg.Runners.ContainerImage)
	}
}

func TestLoad_DockerGIDFromFileAndEnv(t *testing.T) {
	// docker_gid 可在配置文件中设置，并由环境变量 DOCKER_GID 覆盖（与 .env 中 group_add 用的同一变量）
	restore := setEnvsAndRestore(t, map[string]string{"DOCKER_GID": ""}, []string{"DOCKER_GID"})
	defer restore()
	if err := os.Unsetenv("DOCKER_GID"); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := []byte(`
server: { port: 8080 }
runners:
  base_path: ./runners
  items: []
  container_mode: true
  volume_host_path: /host/runners
  job_docker_backend: host-socket
  docker_gid: 113
`)
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Runners.DockerGID != 113 {
		t.Errorf("expected docker_gid 113 from file, got %d", cfg.Runners.DockerGID)
	}

	if err := os.Setenv("DOCKER_GID", "996"); err != nil {
		t.Fatal(err)
	}
	cfg, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Runners.DockerGID != 996 {
		t.Errorf("expected docker_gid 996 from DOCKER_GID env, got %d", cfg.Runners.DockerGID)
	}
}

func TestLoad_DockerGIDDefaultsToZero(t *testing.T) {
	// 未配置时为 0，表示由 Manager 自动探测 docker.sock 所属组
	restore := setEnvsAndRestore(t, map[string]string{"DOCKER_GID": ""}, []string{"DOCKER_GID"})
	defer restore()
	if err := os.Unsetenv("DOCKER_GID"); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("server: { port: 8080 }\nrunners: { base_path: ./runners, items: [] }\n"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Runners.DockerGID != 0 {
		t.Errorf("expected docker_gid default 0, got %d", cfg.Runners.DockerGID)
	}
}

func TestValidate_NegativeDockerGID(t *testing.T) {
	c := &Config{}
	c.Runners.BasePath = "./runners"
	c.Runners.JobDockerBackend = "dind"
	c.Runners.DockerGID = -5
	err := Validate(c)
	if err == nil || !strings.Contains(err.Error(), "docker_gid") {
		t.Fatalf("expected docker_gid validation error, got: %v", err)
	}
}

func containerModeConfig(items ...RunnerItem) *Config {
	c := &Config{}
	c.Runners.BasePath = "/data/runners"
	c.Runners.ContainerMode = true
	c.Runners.VolumeHostPath = "/data/runners"
	c.Runners.JobDockerBackend = "dind"
	c.Runners.Items = items
	return c
}

func TestContainerImageFor_ItemOverridesGlobal(t *testing.T) {
	restore := setEnvsAndRestore(t, map[string]string{"FLEET_IMAGE_TAG": "", "MANAGER_IMAGE": ""}, []string{"FLEET_IMAGE_TAG", "MANAGER_IMAGE"})
	defer restore()

	c := containerModeConfig(
		RunnerItem{Name: "droiddesk", TargetType: "repo", Target: "o/r", ContainerImage: "reg/flutter-runner:1"},
		RunnerItem{Name: "plain", TargetType: "repo", Target: "o/r2"},
	)
	c.Runners.ContainerImage = "reg/global-runner:1"

	if got := c.ContainerImageFor("droiddesk"); got != "reg/flutter-runner:1" {
		t.Errorf("item 覆盖未生效: %q", got)
	}
	if got := c.ContainerImageFor("plain"); got != "reg/global-runner:1" {
		t.Errorf("未回落全局: %q", got)
	}
	// 未知名称也回落全局，避免创建容器时拿到空镜像
	if got := c.ContainerImageFor("not-configured"); got != "reg/global-runner:1" {
		t.Errorf("未知 runner 未回落全局: %q", got)
	}

	c.Runners.ContainerImage = ""
	if got, want := c.ContainerImageFor("plain"), DefaultRunnerContainerImage(); got != want {
		t.Errorf("全局也为空时应回落默认镜像: got %q want %q", got, want)
	}
}

func TestJobDockerBackendFor_ItemOverridesGlobal(t *testing.T) {
	c := containerModeConfig(
		RunnerItem{Name: "iso", TargetType: "repo", Target: "o/r", JobDockerBackend: "  NONE  "},
		RunnerItem{Name: "plain", TargetType: "repo", Target: "o/r2"},
	)
	c.Runners.JobDockerBackend = "host-socket"

	if got := c.JobDockerBackendFor("iso"); got != "none" {
		t.Errorf("item 覆盖未生效或未规范化大小写/空白: %q", got)
	}
	if got := c.JobDockerBackendFor("plain"); got != "host-socket" {
		t.Errorf("未回落全局: %q", got)
	}

	c.Runners.JobDockerBackend = ""
	if got := c.JobDockerBackendFor("plain"); got != DefaultJobDockerBackend {
		t.Errorf("全局也为空时应回落 %q: got %q", DefaultJobDockerBackend, got)
	}
}

func TestValidate_ItemJobDockerBackend(t *testing.T) {
	c := containerModeConfig(RunnerItem{Name: "r", TargetType: "repo", Target: "o/r", JobDockerBackend: "podman"})
	err := Validate(c)
	if err == nil || !strings.Contains(err.Error(), "items[0].job_docker_backend") {
		t.Fatalf("非法后端应报错，got: %v", err)
	}

	c = containerModeConfig(RunnerItem{Name: "r", TargetType: "repo", Target: "o/r", JobDockerBackend: "host-socket"})
	if err := Validate(c); err != nil {
		t.Fatalf("合法后端不应报错: %v", err)
	}
}

func TestValidate_ItemContainerFieldsRequireContainerMode(t *testing.T) {
	// 与 runners.volume_host_path 一致：容器模式专属字段不允许在非容器模式下设置
	base := func(item RunnerItem) *Config {
		c := &Config{}
		c.Runners.BasePath = "./runners"
		c.Runners.JobDockerBackend = "dind"
		c.Runners.Items = []RunnerItem{item}
		return c
	}
	err := Validate(base(RunnerItem{Name: "r", TargetType: "repo", Target: "o/r", ContainerImage: "reg/x:1"}))
	if err == nil || !strings.Contains(err.Error(), "items[0].container_image") {
		t.Fatalf("非容器模式下设置 container_image 应报错，got: %v", err)
	}
	err = Validate(base(RunnerItem{Name: "r", TargetType: "repo", Target: "o/r", JobDockerBackend: "none"}))
	if err == nil || !strings.Contains(err.Error(), "items[0].job_docker_backend") {
		t.Fatalf("非容器模式下设置 job_docker_backend 应报错，got: %v", err)
	}
	if err := Validate(base(RunnerItem{Name: "r", TargetType: "repo", Target: "o/r"})); err != nil {
		t.Fatalf("未设置这两个字段时不应报错: %v", err)
	}
}

func TestLoad_ItemOverridesRoundTrip(t *testing.T) {
	// per-runner 字段能从 yaml 读入，且 omitempty 保证未设置时不会写回噪声字段
	path := filepath.Join(t.TempDir(), "config.yaml")
	content := []byte(`
server: { port: 8080 }
runners:
  base_path: /data/runners
  container_mode: true
  volume_host_path: /data/runners
  job_docker_backend: dind
  items:
    - name: droiddesk
      target_type: repo
      target: soulteary/droiddesk
      container_image: reg/flutter-runner:1
      job_docker_backend: none
    - name: plain
      target_type: repo
      target: soulteary/other
`)
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.ContainerImageFor("droiddesk"); got != "reg/flutter-runner:1" {
		t.Errorf("container_image 未读入: %q", got)
	}
	if got := cfg.JobDockerBackendFor("droiddesk"); got != "none" {
		t.Errorf("item job_docker_backend 未读入: %q", got)
	}
	if got := cfg.JobDockerBackendFor("plain"); got != "dind" {
		t.Errorf("未覆盖的 runner 应回落全局: %q", got)
	}

	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(saved), "container_image:") != 2 {
		// 一次来自 runners.container_image（无 omitempty），一次来自 droiddesk；plain 不应出现
		t.Errorf("omitempty 未生效，写回内容:\n%s", saved)
	}
}

func TestResourceLimits_Validate(t *testing.T) {
	cases := []struct {
		name    string
		limits  ResourceLimits
		wantErr string // 空表示应通过
	}{
		{"全空不限制", ResourceLimits{}, ""},
		{"正常取值", ResourceLimits{CPUs: "1.5", Memory: "4g", MemorySwap: "8g", PidsLimit: 512}, ""},
		{"pids 不限", ResourceLimits{PidsLimit: -1}, ""},
		{"内存纯字节数", ResourceLimits{Memory: "1073741824"}, ""},
		{"swap 设为 -1", ResourceLimits{Memory: "4g", MemorySwap: "-1"}, ""},
		{"swap 等于 memory", ResourceLimits{Memory: "4g", MemorySwap: "4g"}, ""},
		{"cpus 非数字", ResourceLimits{CPUs: "two"}, "cpus"},
		{"cpus 为 0", ResourceLimits{CPUs: "0"}, "cpus"},
		{"cpus 为 0.0", ResourceLimits{CPUs: "0.0"}, "cpus"},
		{"cpus 科学计数法 docker 不接受", ResourceLimits{CPUs: "1e3"}, "cpus"},
		{"内存单位非法", ResourceLimits{Memory: "4gb"}, "memory"},
		{"pids 小于 -1", ResourceLimits{PidsLimit: -2}, "pids_limit"},
		{"只设 swap 不设 memory", ResourceLimits{MemorySwap: "4g"}, "memory_swap"},
		{"swap 小于 memory", ResourceLimits{Memory: "4g", MemorySwap: "1g"}, "memory_swap"},
		{"swap 小于 memory（跨单位）", ResourceLimits{Memory: "2g", MemorySwap: "1024m"}, "memory_swap"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.limits.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("应通过校验，got: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("应报含 %q 的错误，got: %v", tc.wantErr, err)
			}
		})
	}
}

func TestParseDockerSize(t *testing.T) {
	cases := map[string]int64{
		"1024":  1024,
		"1b":    1,
		"1k":    1024,
		"1m":    1024 * 1024,
		"1g":    1024 * 1024 * 1024,
		"1G":    1024 * 1024 * 1024,
		"1.5g":  1024 * 1024 * 1024 * 3 / 2,
		"2048m": 2 * 1024 * 1024 * 1024,
	}
	for in, want := range cases {
		got, err := parseDockerSize(in)
		if err != nil {
			t.Errorf("parseDockerSize(%q) 报错: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("parseDockerSize(%q) = %d, want %d", in, got, want)
		}
	}
	for _, bad := range []string{"", "4gb", "abc", "-1"} {
		if _, err := parseDockerSize(bad); err == nil {
			t.Errorf("parseDockerSize(%q) 应报错", bad)
		}
	}
}

func TestResourceLimits_Args(t *testing.T) {
	if got := (ResourceLimits{}).Args(); len(got) != 0 {
		t.Errorf("未配置时不应产生参数: %v", got)
	}
	got := strings.Join(ResourceLimits{CPUs: " 2 ", Memory: "4g", PidsLimit: 512}.Args(), " ")
	want := "--cpus 2 --memory 4g --pids-limit 512"
	if got != want {
		t.Errorf("Args() = %q, want %q", got, want)
	}
}

func TestLoad_ResourceLimitsRejectedEarly(t *testing.T) {
	// 非法取值应在 Load 阶段就报错，而不是等到 docker create 失败
	path := filepath.Join(t.TempDir(), "config.yaml")
	content := []byte(`
server: { port: 8080 }
runners:
  base_path: ./runners
  items: []
  resources:
    cpus: "many"
`)
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "cpus") {
		t.Fatalf("应在加载时拒绝非法 cpus，got: %v", err)
	}
}

// TestLoad_EnvOverrides_PortTrimsSurroundingWhitespace 钉住「端口先 trim 再解析」。
//
// .env 与 compose 的 environment: 块里带一个尾随空格是常事。cli-kit 的
// env.GetInt 不做 trim，直接换用它会让 "MANAGER_PORT=9090 " 静默失效、
// 继续听配置里的端口——一个没有任何日志的部署故障。这个用例就是拦它的。
func TestLoad_EnvOverrides_PortTrimsSurroundingWhitespace(t *testing.T) {
	for _, envVal := range []string{" 9090", "9090 ", "  9090  "} {
		restore := setEnvsAndRestore(t, map[string]string{"MANAGER_PORT": envVal}, []string{"MANAGER_PORT"})
		path := filepath.Join(t.TempDir(), "config.yaml")
		if err := os.WriteFile(path, []byte("server: { port: 8080 }\nrunners: { items: [] }"), 0644); err != nil {
			restore()
			t.Fatal(err)
		}
		cfg, err := Load(path)
		restore()
		if err != nil {
			t.Fatalf("MANAGER_PORT=%q: Load failed: %v", envVal, err)
		}
		if cfg.Server.Port != 9090 {
			t.Errorf("MANAGER_PORT=%q: 期望 trim 后生效为 9090，got %d", envVal, cfg.Server.Port)
		}
	}
}

// TestValidate_ServerPortRange 端口范围在 Validate 就拦下，而不是等 ListenAndServe。
func TestValidate_ServerPortRange(t *testing.T) {
	newCfg := func(port int) *Config {
		return &Config{
			Server:  ServerConfig{Port: port},
			Runners: RunnersConfig{BasePath: "./runners", JobDockerBackend: "dind"},
		}
	}
	// 0 表示「没写」，由 Load 填默认值，Validate 不该对它报错——
	// 代码里大量直接构造 Config 再 Validate 的地方都不设 Server。
	if err := Validate(newCfg(0)); err != nil {
		t.Fatalf("port=0 应视为未设置而放行，got: %v", err)
	}
	for _, port := range []int{8080, 1, 65535} {
		if err := Validate(newCfg(port)); err != nil {
			t.Errorf("port=%d 应合法，got: %v", port, err)
		}
	}
	for _, port := range []int{65536, 70000, -1} {
		err := Validate(newCfg(port))
		if err == nil || !strings.Contains(err.Error(), "server.port") {
			t.Errorf("port=%d 应被拒绝并指出 server.port，got: %v", port, err)
		}
	}
}

// TestLoad_EnvOverrides_PortOutOfRangeFailsLoudly 越界端口要在加载期报错。
//
// 刻意不在 applyEnvOverrides 里悄悄忽略：忽略之后进程会用默认端口起来，
// 界面在 8080 而不是你设的 70000，没有任何提示；报错则一眼看到是哪个配置项。
func TestLoad_EnvOverrides_PortOutOfRangeFailsLoudly(t *testing.T) {
	restore := setEnvsAndRestore(t, map[string]string{"MANAGER_PORT": "70000"}, []string{"MANAGER_PORT"})
	defer restore()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("server: { port: 8080 }\nrunners: { items: [] }"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "server.port") {
		t.Fatalf("MANAGER_PORT=70000 应在 Load 阶段报错并指出 server.port，got: %v", err)
	}
}

// TestValidate_JobDockerBackendCaseInsensitive 配置文件里大小写混写也应接受。
func TestValidate_JobDockerBackendCaseInsensitive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	content := []byte("server: { port: 8080 }\nrunners:\n  base_path: ./runners\n  job_docker_backend: DinD\n  items: []\n")
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("DinD 应被接受，got: %v", err)
	}
	if cfg.Runners.JobDockerBackend != "dind" {
		t.Errorf("期望归一化为 dind，got %q", cfg.Runners.JobDockerBackend)
	}
}

// TestSaveAndLoad_ConcurrentReadersNeverSeePartialConfig 保存进行中的并发读不该看到半截配置。
//
// 这是线上那个现象的复现：os.WriteFile 先 O_TRUNC 再写，前端 15 秒一次的轮询、
// 每个 API 请求的 getConfig 和后台的自启动与 GitHub 检查循环恰好落在这中间时，
// 读到的要么是半截 YAML（一次 500），要么是空文件——而空输入 yaml.Unmarshal 并不报错，
// 返回的是零值配置，界面上就成了「一个 Runner 都没有」。mu 只串行化写者，
// Load 不持锁，挡不住这个；换成 rename 发布之后，读者只可能看到 A 或 B。
//
// 这条测试在旧实现上是概率性变红的（取决于读者是否恰好落在截断与写完之间）；
// 确定性的证明在 internal/atomicfile 的 TestWriteFile_TargetUntouchedUntilRename。
func TestSaveAndLoad_ConcurrentReadersNeverSeePartialConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")

	// 两份配置的 items 条数不同，读到空文件（零值配置）时也认不成其中任何一份
	a := &Config{
		Server:  ServerConfig{Port: 9000, Addr: "127.0.0.1"},
		Runners: RunnersConfig{BasePath: "/tmp/runners", Items: []RunnerItem{{Name: "a1", TargetType: "org", Target: "myorg"}}},
	}
	b := &Config{
		Server: ServerConfig{Port: 9000, Addr: "127.0.0.1"},
		Runners: RunnersConfig{BasePath: "/tmp/runners", Items: []RunnerItem{
			{Name: "b1", TargetType: "repo", Target: "owner/repo"},
			{Name: "b2", TargetType: "repo", Target: "owner/other"},
		}},
	}
	names := func(c *Config) string {
		out := make([]string, 0, len(c.Runners.Items))
		for _, it := range c.Runners.Items {
			out = append(out, it.Name)
		}
		return strings.Join(out, ",")
	}
	wantA, wantB := "a1", "b1,b2"

	if err := a.Save(path); err != nil {
		t.Fatal(err)
	}

	const rounds = 500
	done := make(chan struct{})
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(done)
		for i := 0; i < rounds; i++ {
			for _, c := range []*Config{a, b} {
				if err := c.Save(path); err != nil {
					t.Errorf("Save: %v", err)
					return
				}
			}
		}
	}()

	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
				}
				cfg, err := Load(path)
				if err != nil {
					t.Errorf("Load 撞上了保存中的文件：%v", err)
					return
				}
				if got := names(cfg); got != wantA && got != wantB {
					t.Errorf("Load 读到的既不是 A 也不是 B：items=[%s]（空的那次就是界面上「一个 Runner 都没有」）", got)
					return
				}
			}
		}()
	}
	wg.Wait()
}
