package runner

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/lab-dev/github-actions-runner-manager/internal/config"
)

// newFakeRunnerDir 造一个像已注册 runner 的目录：有 .runner，有一个会常驻的 run.sh。
// run.sh 刻意不 exec，好让 bash 留在进程表里，argv 与 actions/runner 的形态一致。
func newFakeRunnerDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".runner"), []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}
	writeBlockingRunScript(t, dir)
	return dir
}

func startFakeRunner(t *testing.T, dir string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(filepath.Join(dir, "run.sh"))
	cmd.Dir = dir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("启动假 run.sh 失败: %v", err)
	}
	t.Cleanup(func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_, _ = cmd.Process.Wait()
	})
	return cmd
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

func requireLinux(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("进程探测依赖 /proc，仅在 Linux 上验证")
	}
	if _, err := os.Stat("/proc/self/cmdline"); err != nil {
		t.Skip("当前环境没有可用的 /proc")
	}
}

// 这是「每 5 分钟重复拉起」那个 bug 的直接回归用例：
// runner 在跑的时候 getStatus 必须报 running=true，否则后台巡检
// （info.Status == StatusInstalled && !info.Running）会一轮一轮地再拉起它
func TestGetStatus_RunningRunnerReportsRunning(t *testing.T) {
	requireLinux(t)
	dir := newFakeRunnerDir(t)

	status, running := getStatus(dir)
	if status != StatusInstalled {
		t.Fatalf("有 .runner 时状态应为 %q，得到 %q", StatusInstalled, status)
	}
	if running {
		t.Fatal("还没启动进程，running 应为 false")
	}

	startFakeRunner(t, dir)
	if !waitUntil(func() bool { _, r := getStatus(dir); return r }) {
		t.Fatal("run.sh 已在运行，getStatus 仍报 running=false —— 后台巡检会反复重复拉起它")
	}
	if status, _ := getStatus(dir); status != StatusInstalled {
		t.Fatalf("运行中状态应仍为 %q，得到 %q", StatusInstalled, status)
	}
}

// actions/runner 不写 pid 文件，.path 装的是 PATH 字符串。
// 目录里放什么都不能让一个没在跑的 runner 被判成在跑。
func TestGetStatus_PidLookalikeFilesDoNotFakeRunning(t *testing.T) {
	requireLinux(t)
	dir := newFakeRunnerDir(t)
	if err := os.WriteFile(filepath.Join(dir, "Runner.Listener.pid"),
		[]byte(strconv.Itoa(os.Getpid())), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".path"),
		[]byte("/usr/local/bin:/usr/bin:/bin"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, running := getStatus(dir); running {
		t.Fatal("目录里的 pid 文件不应让没在跑的 runner 被判成在跑")
	}
}

// 同一宿主机上的两个 runner 不能互相串
func TestGetStatus_RunnersDoNotSeeEachOther(t *testing.T) {
	requireLinux(t)
	a, b := newFakeRunnerDir(t), newFakeRunnerDir(t)
	startFakeRunner(t, a)
	if !waitUntil(func() bool { _, r := getStatus(a); return r }) {
		t.Fatal("a 应判定为运行中")
	}
	if _, running := getStatus(b); running {
		t.Fatal("b 没有进程，不应被 a 的进程带成运行中")
	}
}

// 修复前 Stop 走的是 readRunnerPid，pid 文件永远不存在，
// 所以默认模式下界面上的「停止」必然报「未找到 runner pid 文件或 pid 无效」
func TestStop_TerminatesRunningRunner(t *testing.T) {
	requireLinux(t)
	dir := newFakeRunnerDir(t)
	startFakeRunner(t, dir)
	if !waitUntil(func() bool { _, r := getStatus(dir); return r }) {
		t.Fatal("准备阶段：runner 应处于运行中")
	}
	if err := Stop(dir); err != nil {
		t.Fatalf("Stop 失败: %v", err)
	}
	if !waitUntil(func() bool { _, r := getStatus(dir); return !r }) {
		t.Fatal("Stop 之后 runner 仍在运行")
	}
}

// 没在跑的时候 Stop 要说清楚，不能默默成功
func TestStop_NotRunningReportsError(t *testing.T) {
	requireLinux(t)
	dir := newFakeRunnerDir(t)
	if err := Stop(dir); err == nil {
		t.Fatal("没有进程在跑时 Stop 应返回错误")
	}
}

// List 对一整批 runner 只扫一遍 /proc，分派必须正确：
// 跑着的那个报 running，没跑的那个不能被它带成 running
func TestList_PerRunnerRunningStateInDefaultMode(t *testing.T) {
	requireLinux(t)
	base := t.TempDir()
	cfg := &config.Config{Runners: config.RunnersConfig{
		BasePath: base,
		Items: []config.RunnerItem{
			{Name: "alpha", Path: "alpha"},
			{Name: "beta", Path: "beta"},
			{Name: "gamma", Path: "gamma"}, // 目录都不建，应为 missing
		},
	}}
	for _, name := range []string{"alpha", "beta"} {
		dir := filepath.Join(base, name)
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, ".runner"), []byte("{}"), 0644); err != nil {
			t.Fatal(err)
		}
		writeBlockingRunScript(t, dir)
	}
	startFakeRunner(t, filepath.Join(base, "alpha"))

	if !waitUntil(func() bool {
		for _, info := range List(cfg) {
			if info.Name == "alpha" && info.Running {
				return true
			}
		}
		return false
	}) {
		t.Fatal("alpha 已启动，List 仍报未运行")
	}

	byName := map[string]RunnerInfo{}
	for _, info := range List(cfg) {
		byName[info.Name] = info
	}
	if got := byName["alpha"]; got.Status != StatusInstalled || !got.Running {
		t.Fatalf("alpha 应为 installed/true，得到 %q/%v", got.Status, got.Running)
	}
	if got := byName["beta"]; got.Status != StatusInstalled || got.Running {
		t.Fatalf("beta 应为 installed/false，得到 %q/%v —— 不能被 alpha 的进程带成运行中", got.Status, got.Running)
	}
	if got := byName["gamma"]; got.Status != StatusMissing || got.Running {
		t.Fatalf("gamma 目录不存在，应为 missing/false，得到 %q/%v", got.Status, got.Running)
	}
}

// 未注册的目录（没有 .runner）不该因为目录里恰好有进程就被判成 installed
func TestList_UnregisteredDirStaysNew(t *testing.T) {
	requireLinux(t)
	base := t.TempDir()
	dir := filepath.Join(base, "alpha")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	writeBlockingRunScript(t, dir)
	startFakeRunner(t, dir)
	cfg := &config.Config{Runners: config.RunnersConfig{
		BasePath: base,
		Items:    []config.RunnerItem{{Name: "alpha", Path: "alpha"}},
	}}
	list := List(cfg)
	if len(list) != 1 || list[0].Status != StatusNew || list[0].Running {
		t.Fatalf("没有 .runner 时应为 new/false，得到 %+v", list)
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
