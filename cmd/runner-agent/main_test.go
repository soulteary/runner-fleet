package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/lab-dev/github-actions-runner-manager/internal/runnerproc"
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

// newRunnerDir 造一个像已注册 runner 的安装目录，并把它设为本 Agent 的 RUNNER_INSTALL_DIR。
// run.sh 刻意不 exec，好让 bash 留在进程表里，argv 与 actions/runner 的形态一致。
func newRunnerDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".runner"), []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}
	writeBlockingRunScript(t, dir)
	t.Setenv("RUNNER_INSTALL_DIR", dir)
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

func postStart(t *testing.T) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	handleStart(rec, httptest.NewRequest(http.MethodPost, "/start", nil))
	return rec
}

func TestGetStatus_ReflectsRunningProcess(t *testing.T) {
	requireLinux(t)
	dir := newRunnerDir(t)

	if status, running := getStatus(dir); status != "installed" || running {
		t.Fatalf("启动前应为 installed/false，得到 %q/%v", status, running)
	}
	if rec := postStart(t); rec.Code != http.StatusOK {
		t.Fatalf("/start 返回 %d: %s", rec.Code, rec.Body.String())
	}
	if !waitUntil(func() bool { _, r := getStatus(dir); return r }) {
		t.Fatal("run.sh 已启动，getStatus 仍报未运行")
	}
}

// Manager 每 5 分钟巡检一次「已注册未运行」的 runner 并拉起。
// 修复前 getStatus 恒为 false，这道幂等护栏形同虚设，每一轮都多起一个 run.sh。
func TestHandleStart_IsIdempotent(t *testing.T) {
	requireLinux(t)
	dir := newRunnerDir(t)

	if rec := postStart(t); rec.Code != http.StatusOK {
		t.Fatalf("首次 /start 返回 %d: %s", rec.Code, rec.Body.String())
	}
	if !waitUntil(func() bool { _, r := getStatus(dir); return r }) {
		t.Fatal("首次 /start 后应处于运行中")
	}

	// 模拟后续 9 轮定时拉起
	for i := 0; i < 9; i++ {
		rec := postStart(t)
		if rec.Code != http.StatusOK {
			t.Fatalf("第 %d 次 /start 返回 %d: %s", i+2, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "already running") {
			t.Fatalf("第 %d 次 /start 应答复 already running，实际 %s", i+2, rec.Body.String())
		}
	}

	if got := runnerproc.Find(dir); len(got) != 1 {
		t.Fatalf("10 次 /start 之后应只有 1 个 runner 进程，实际 %d 个: %v", len(got), got)
	}
}

func TestHandleStop_TerminatesRunner(t *testing.T) {
	requireLinux(t)
	dir := newRunnerDir(t)
	if rec := postStart(t); rec.Code != http.StatusOK {
		t.Fatalf("/start 返回 %d: %s", rec.Code, rec.Body.String())
	}
	if !waitUntil(func() bool { _, r := getStatus(dir); return r }) {
		t.Fatal("准备阶段：应处于运行中")
	}

	rec := httptest.NewRecorder()
	handleStop(rec, httptest.NewRequest(http.MethodPost, "/stop", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("/stop 返回 %d: %s", rec.Code, rec.Body.String())
	}
	if !waitUntil(func() bool { _, r := getStatus(dir); return !r }) {
		t.Fatal("/stop 之后仍在运行")
	}
}

// 没在跑的时候 /stop 要报错，不能默默成功
func TestHandleStop_NotRunningReportsError(t *testing.T) {
	requireLinux(t)
	newRunnerDir(t)
	rec := httptest.NewRecorder()
	handleStop(rec, httptest.NewRequest(http.MethodPost, "/stop", nil))
	if rec.Code == http.StatusOK {
		t.Fatalf("没有进程在跑时 /stop 不应返回 200: %s", rec.Body.String())
	}
}

// 目录里放着长得像 pid 文件的东西也不能把状态带偏：
// actions/runner 不写 Runner.Listener.pid，.path 装的是 PATH 字符串
func TestGetStatus_PidLookalikeFilesAreIgnored(t *testing.T) {
	requireLinux(t)
	dir := newRunnerDir(t)
	if err := os.WriteFile(filepath.Join(dir, "Runner.Listener.pid"),
		[]byte(strconv.Itoa(os.Getpid())), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".path"),
		[]byte("/usr/local/bin:/usr/bin:/bin"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, running := getStatus(dir); running {
		t.Fatal("没有进程在跑，pid 文件不应让状态变成运行中")
	}
}

// 未注册的目录（没有 .runner）应为 new
func TestGetStatus_UnregisteredDirIsNew(t *testing.T) {
	dir := t.TempDir()
	if status, running := getStatus(dir); status != "new" || running {
		t.Fatalf("没有 .runner 时应为 new/false，得到 %q/%v", status, running)
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
