package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/soulteary/runner-fleet/internal/config"
)

// Runner 安装目录里有 config.sh 写下的 .credentials_rsaparams——Runner 向 GitHub
// 表明身份的 RSA 私钥。actions/runner 不给这些文件设权限，目录的权限位就是最后一道门。
func TestEnsureRunnerDir_CreatesPrivateDirectory(t *testing.T) {
	base := t.TempDir()
	cfg := &config.Config{Runners: config.RunnersConfig{BasePath: base}}

	dir, err := EnsureRunnerDir(cfg, "alpha", "")
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		t.Fatalf("安装目录权限为 %o，其他用户可进入；凭据文件会跟着 umask 落成 0644", perm)
	}
	if info.Mode().Perm() != RunnerDirMode {
		t.Fatalf("安装目录权限应为 %o，得到 %o", RunnerDirMode, info.Mode().Perm())
	}
}

// base_path 不跟着收紧：它只是容器,里面没有凭据,而且收紧它会牵动宿主机挂载
func TestEnsureRunnerDir_LeavesBasePathTraversable(t *testing.T) {
	base := filepath.Join(t.TempDir(), "runners")
	cfg := &config.Config{Runners: config.RunnersConfig{BasePath: base}}

	if _, err := EnsureRunnerDir(cfg, "alpha", ""); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(base)
	if err != nil {
		t.Fatalf("base_path 应被创建出来: %v", err)
	}
	if info.Mode().Perm()&0o055 == 0 {
		t.Fatalf("base_path 权限为 %o，不应跟着收紧", info.Mode().Perm())
	}
}

// 已存在的目录不被改动：UID 不匹配的部署下擅自收紧会把本来能跑的弄坏，
// 该由启动自检点名、人看过再决定
func TestEnsureRunnerDir_DoesNotChangeExistingDirectory(t *testing.T) {
	base := t.TempDir()
	existing := filepath.Join(base, "alpha")
	if err := os.MkdirAll(existing, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(existing, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Runners: config.RunnersConfig{BasePath: base}}

	if _, err := EnsureRunnerDir(cfg, "alpha", ""); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(existing)
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("已存在目录的权限被改成了 %o", info.Mode().Perm())
	}
}

// 自检要点名这些目录，并给出能直接照做的 chmod
func TestPreflight_ReportsWorldTraversableRunnerDirs(t *testing.T) {
	base := t.TempDir()
	loose := filepath.Join(base, "loose")
	tight := filepath.Join(base, "tight")
	if err := os.MkdirAll(loose, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(loose, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(tight, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(tight, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Runners: config.RunnersConfig{BasePath: base, Items: []config.RunnerItem{
		{Name: "loose", Path: "loose"},
		{Name: "tight", Path: "tight"},
	}}}

	got := checkRunnerDirPermissions(cfg)
	if got.Level != CheckWarn {
		t.Fatalf("有目录可被他人进入时应告警，得到 %+v", got)
	}
	if !strings.Contains(got.Message, loose) {
		t.Fatalf("应点名 %s，实际消息 %q", loose, got.Message)
	}
	if strings.Contains(got.Message, tight) {
		t.Fatalf("0700 的目录不该被点名，实际消息 %q", got.Message)
	}
	if got.Hint != "chmod 700 "+loose {
		t.Fatalf("修复建议应是可直接执行的 chmod，得到 %q", got.Hint)
	}
}

func TestPreflight_QuietWhenAllRunnerDirsArePrivate(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "a")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Runners: config.RunnersConfig{BasePath: base,
		Items: []config.RunnerItem{{Name: "a", Path: "a"}}}}
	if got := checkRunnerDirPermissions(cfg); got.Level != CheckOK {
		t.Fatalf("全部为 0700 时不应告警，得到 %+v", got)
	}
}

// 还没建出来的目录不归本项管，别把「尚未安装」报成权限问题
func TestPreflight_IgnoresMissingRunnerDirs(t *testing.T) {
	cfg := &config.Config{Runners: config.RunnersConfig{BasePath: t.TempDir(),
		Items: []config.RunnerItem{{Name: "never-created", Path: "never-created"}}}}
	if got := checkRunnerDirPermissions(cfg); got.Level != CheckOK {
		t.Fatalf("目录不存在时不应告警，得到 %+v", got)
	}
}
