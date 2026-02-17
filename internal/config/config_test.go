package config

import (
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
	if cfg.Runners.ContainerImage != "ghcr.io/soulteary/runner-fleet-runner:main" {
		t.Fatalf("expected default container image, got %q", cfg.Runners.ContainerImage)
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
