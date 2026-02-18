package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoad_MissingFile(t *testing.T) {
	_, err := Load(filepath.Join(os.TempDir(), "nonexistent-config.yaml"))
	if err == nil {
		t.Fatal("expected error for missing file")
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
	if err := Validate(cfg2); err == nil || !strings.Contains(err.Error(), "必须设置 runners.volume_host_path") {
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
	if err := Validate(cfg3); err == nil || !strings.Contains(err.Error(), "绝对路径") {
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
	if err := ValidateTarget("org", "owner/repo"); err == nil || !strings.Contains(err.Error(), "不能包含 /") {
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
	if err == nil || !strings.Contains(err.Error(), "容器名冲突") {
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
	// 未设置 FLEET_IMAGE_TAG 时默认使用 v1.0.0-runner
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
	want := "ghcr.io/soulteary/runner-fleet:v1.0.0-runner"
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
	if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), "name 包含非法字符") {
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
	if err := Validate(cfg2); err == nil || !strings.Contains(err.Error(), "path 包含非法字符") {
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
	if err := Validate(cfg2); err == nil || !strings.Contains(err.Error(), "不能包含 /") {
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
	if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), "安装目录冲突") {
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
		"MANAGER_IMAGE":   "ghcr.io/soulteary/runner-fleet:v1.0.1",
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
	want := "ghcr.io/soulteary/runner-fleet:v1.0.1-runner"
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
	if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), "name 不能为空") {
		t.Fatalf("expected empty name validation error, got: %v", err)
	}
}

func TestLoadAndSave_LoadFails(t *testing.T) {
	err := LoadAndSave(filepath.Join(os.TempDir(), "nonexistent-config.yaml"), func(c *Config) error {
		return nil
	})
	if err == nil {
		t.Fatal("expected error when config file missing")
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
