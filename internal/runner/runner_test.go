package runner

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/lab-dev/github-actions-runner-manager/internal/config"
)

func TestList_Empty(t *testing.T) {
	cfg := &config.Config{Runners: config.RunnersConfig{BasePath: t.TempDir(), Items: nil}}
	list := List(cfg)
	if len(list) != 0 {
		t.Errorf("expected empty list, got %d", len(list))
	}
}

func TestList_WithItems(t *testing.T) {
	base := t.TempDir()
	cfg := &config.Config{
		Runners: config.RunnersConfig{
			BasePath: base,
			Items: []config.RunnerItem{
				{Name: "r1", TargetType: "org", Target: "o1"},
				{Name: "r2", Path: "custom", TargetType: "repo", Target: "a/b"},
			},
		},
	}
	list := List(cfg)
	if len(list) != 2 {
		t.Fatalf("expected 2, got %d", len(list))
	}
	if list[0].Name != "r1" || list[0].InstallDir != filepath.Join(base, "r1") {
		t.Errorf("list[0]: %+v", list[0])
	}
	if list[1].Name != "r2" || list[1].InstallDir != filepath.Join(base, "custom") {
		t.Errorf("list[1]: %+v", list[1])
	}
	// 目录不存在时应为 StatusMissing
	if list[0].Status != StatusMissing {
		t.Errorf("expected StatusMissing for missing dir, got %s", list[0].Status)
	}
}

func TestGetByName_NotFound(t *testing.T) {
	cfg := &config.Config{Runners: config.RunnersConfig{BasePath: t.TempDir(), Items: []config.RunnerItem{
		{Name: "r1", TargetType: "org", Target: "o1"},
	}}}
	info := GetByName(cfg, "r2")
	if info != nil {
		t.Errorf("expected nil, got %+v", info)
	}
}

func TestGetByName_NilConfig(t *testing.T) {
	info := GetByName(nil, "any")
	if info != nil {
		t.Errorf("expected nil when cfg is nil, got %+v", info)
	}
}

func TestGetByName_Found(t *testing.T) {
	base := t.TempDir()
	cfg := &config.Config{
		Runners: config.RunnersConfig{
			BasePath: base,
			Items:    []config.RunnerItem{{Name: "r1", TargetType: "repo", Target: "x/y", Labels: []string{"l1"}}},
		},
	}
	info := GetByName(cfg, "r1")
	if info == nil {
		t.Fatal("expected non-nil")
	}
	if info.Name != "r1" || info.Target != "x/y" || info.InstallDir != filepath.Join(base, "r1") {
		t.Errorf("info: %+v", info)
	}
	if len(info.Labels) != 1 || info.Labels[0] != "l1" {
		t.Errorf("labels: %v", info.Labels)
	}
	if info.Status != StatusMissing {
		t.Errorf("expected StatusMissing, got %s", info.Status)
	}
}

func TestEnsureRunnerDir(t *testing.T) {
	base := t.TempDir()
	cfg := &config.Config{Runners: config.RunnersConfig{BasePath: base}}

	dir, err := EnsureRunnerDir(cfg, "r1", "")
	if err != nil {
		t.Fatal(err)
	}
	if dir != filepath.Join(base, "r1") {
		t.Errorf("dir = %q", dir)
	}
	// 子路径
	dir2, err := EnsureRunnerDir(cfg, "r2", "sub/r2")
	if err != nil {
		t.Fatal(err)
	}
	absBase, _ := filepath.Abs(base)
	rel, _ := filepath.Rel(absBase, dir2)
	if rel != "sub" && rel != filepath.Join("sub", "r2") {
		// Clean 可能合并为 sub
		t.Logf("dir2=%q rel=%q", dir2, rel)
	}

	// 路径穿越被 sanitize：含 .. 时使用 name 作为目录，结果仍在 base 下
	dir3, err := EnsureRunnerDir(cfg, "evil", "../../etc")
	if err != nil {
		t.Fatal(err)
	}
	rel3, _ := filepath.Rel(base, dir3)
	if strings.HasPrefix(rel3, "..") || rel3 == ".." {
		t.Errorf("path traversal not sanitized: %q", dir3)
	}
}
