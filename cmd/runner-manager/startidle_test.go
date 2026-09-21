package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"

	"github.com/soulteary/runner-fleet/internal/config"
	"github.com/soulteary/runner-fleet/internal/runner"
	"github.com/soulteary/runner-fleet/internal/runnerproc"
)

func requireLinux(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("进程探测依赖 /proc，仅在 Linux 上验证")
	}
	if _, err := os.Stat("/proc/self/cmdline"); err != nil {
		t.Skip("当前环境没有可用的 /proc")
	}
}

// mkRunnerDir 造一个 runner 目录。registered 决定它有没有 .runner（即是否「已注册」）。
// run.sh 刻意不 exec，好让 bash 留在进程表里，argv 与 actions/runner 的形态一致。
func mkRunnerDir(t *testing.T, base, name string, registered bool) string {
	t.Helper()
	dir := filepath.Join(base, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if registered {
		if err := os.WriteFile(filepath.Join(dir, ".runner"), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeBlockingRunScript(t, dir)
	t.Cleanup(func() {
		for _, pid := range runnerproc.Find(dir) {
			_ = syscall.Kill(-pid, syscall.SIGKILL)
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})
	return dir
}

func waitUntil(cond func() bool) bool {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return cond()
}

func cfgWith(base string, names ...string) *config.Config {
	cfg := &config.Config{Runners: config.RunnersConfig{BasePath: base}}
	for _, n := range names {
		cfg.Runners.Items = append(cfg.Runners.Items, config.RunnerItem{Name: n, Path: n})
	}
	return cfg
}

// 只拉「已注册且没在跑」的那些：拉起没注册的会凭空建目录/容器，
// 再拉一遍已经在跑的则会在同一个 Runner 上多起一个 listener
func TestStartIdleRunners_StartsOnlyRegisteredAndIdle(t *testing.T) {
	requireLinux(t)
	base := t.TempDir()
	idle := mkRunnerDir(t, base, "idle", true)
	busy := mkRunnerDir(t, base, "busy", true)
	unreg := mkRunnerDir(t, base, "unreg", false)

	// busy 先跑起来
	cmd := exec.Command(filepath.Join(busy, "run.sh"))
	cmd.Dir = busy
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { _ = cmd.Wait() }()
	if !waitUntil(func() bool { return runnerproc.Running(busy) }) {
		t.Fatal("准备阶段：busy 应处于运行中")
	}
	busyPids := runnerproc.Find(busy)

	startIdleRunners(context.Background(), cfgWith(base, "idle", "busy", "unreg"), "测试拉起")

	if !waitUntil(func() bool { return runnerproc.Running(idle) }) {
		t.Fatal("已注册且没在跑的 idle 应被拉起")
	}
	if got := runnerproc.Find(busy); len(got) != len(busyPids) {
		t.Fatalf("已在运行的 busy 不该被再拉一次：进程从 %v 变成 %v", busyPids, got)
	}
	if runnerproc.Running(unreg) {
		t.Fatal("没有 .runner 的 unreg 不该被拉起")
	}
}

// 配置为空或没有 runner 时应安静返回，不 panic
func TestStartIdleRunners_HandlesEmptyConfig(t *testing.T) {
	startIdleRunners(context.Background(), nil, "测试拉起")
	startIdleRunners(context.Background(), cfgWith(t.TempDir()), "测试拉起")
}

// 目录不存在（status=missing）的 runner 不该被拉起——
// 那会凭空建出目录来，容器模式下还会让 Docker 以 root 属主创建挂载源
func TestStartIdleRunners_SkipsMissingInstallDir(t *testing.T) {
	requireLinux(t)
	base := t.TempDir()
	cfg := cfgWith(base, "never-created")
	startIdleRunners(context.Background(), cfg, "测试拉起")
	if _, err := os.Stat(filepath.Join(base, "never-created")); !os.IsNotExist(err) {
		t.Fatal("不该为不存在的 runner 建出目录")
	}
}

// 与 runner.List 的判据保持一致：状态与运行态都取自同一处
func TestStartIdleRunners_MatchesListStatus(t *testing.T) {
	requireLinux(t)
	base := t.TempDir()
	dir := mkRunnerDir(t, base, "a", true)
	cfg := cfgWith(base, "a")

	list := runner.List(cfg)
	if len(list) != 1 || list[0].Status != runner.StatusInstalled || list[0].Running {
		t.Fatalf("前提不成立：应为 installed 且未运行，得到 %+v", list)
	}
	startIdleRunners(context.Background(), cfg, "测试拉起")
	if !waitUntil(func() bool {
		l := runner.List(cfg)
		return len(l) == 1 && l[0].Running
	}) {
		t.Fatalf("拉起后 List 应报告运行中，Find=%v", runnerproc.Find(dir))
	}
}

// writeBlockingRunScript 写一个会一直挂住、且**不产生任何子进程**的 run.sh。
//
// 不能用 `sleep 120`：bash 会 fork 一个子进程去 exec sleep，而在 fork 与 execve 之间，
// 子进程的 /proc/<pid>/cmdline 仍是 bash 的 argv（`/bin/bash <dir>/run.sh`），
// 于是被认成第二个监护脚本。窗口只有几微秒，本机几乎碰不到，CI 上机器一忙就中招——
// 「10 次 /start 之后应只有 1 个 runner 进程，实际 2 个: [2727 2728]」正是这么来的。
//
// read 是 builtin，重定向由当前 shell 自己完成，打开一个没有写端的 FIFO 会就地阻塞，
// 全程不 fork，进程数因此是确定的。
func writeBlockingRunScript(t *testing.T, dir string) {
	t.Helper()
	fifo := filepath.Join(dir, ".test-block")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatalf("创建 FIFO 失败: %v", err)
	}
	script := "#!/bin/bash\nread -r -t 120 < \"" + fifo + "\"\n"
	if err := os.WriteFile(filepath.Join(dir, "run.sh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}
