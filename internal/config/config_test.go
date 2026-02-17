package config

import (
	"os"
	"path/filepath"
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
