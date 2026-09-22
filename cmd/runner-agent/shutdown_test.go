package main

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/soulteary/runner-fleet/internal/runnerproc"
)

// termIgnored 与 termExitsAfterDelay 是 run.sh 对 SIGTERM 的两种反应，
// 用来区分「肯退，但要一点时间收尾」和「根本不理 SIGTERM」。
//
// 收尾必须耗时可观测，否则这组用例证明不了 shutdown 真的在等：一个
// `trap 'exit 0' TERM` 的 bash 从收到信号到 /proc 里 cmdline 变空只要 0.7 毫秒，
// 比一个轮询间隔还短，于是「发完信号就返回」的实现也照样能通过。
func termIgnored(string) string { return "" }

// termExitsAfterDelay 让 trap 先在一个 FIFO 上阻塞给定的时长，再退出。
//
// 用 `exec 3<>fifo` + `read -t` 而不是 `sleep N`：后者要 fork，而在 fork 与 execve
// 之间子进程的 cmdline 仍是 `/bin/bash <dir>/run.sh`，会被 runnerproc 认成第二个
// 监护脚本（见 writeBlockingRunScript 的说明）。读写方式打开 FIFO 不会阻塞在 open 上，
// 之后的 read 才落到 -t 的超时里，整段延迟不产生任何子进程。
func termExitsAfterDelay(d time.Duration) func(dir string) string {
	return func(dir string) string {
		return `exec 3<>"` + filepath.Join(dir, ".test-delay") + `"; ` +
			`read -r -t ` + strconv.Itoa(int(d/time.Second)) + ` -u 3; exit 0`
	}
}

// termExitsNow 立刻退出。只用在「本来就没有 runner 在跑」那条用例里：
// 它 0.7 毫秒就消失，测不出等待行为。
func termExitsNow(string) string { return "exit 0" }

// newTrappingRunnerDir 与 newRunnerDir 相同，只是 run.sh 多一条 TERM trap。
// 主体的阻塞方式沿用 writeBlockingRunScript：打开一个没有写端的 FIFO 会就地阻塞，
// read 是 builtin，全程不 fork，进程表里因此只有一个监护脚本。
func newTrappingRunnerDir(t *testing.T, trap func(dir string) string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".runner"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{".test-block", ".test-delay"} {
		if err := syscall.Mkfifo(filepath.Join(dir, name), 0o600); err != nil {
			t.Fatalf("创建 FIFO %s 失败: %v", name, err)
		}
	}
	script := "#!/bin/bash\ntrap '" + trap(dir) + "' TERM\n" +
		"read -r -t 120 < \"" + filepath.Join(dir, ".test-block") + "\"\n"
	if err := os.WriteFile(filepath.Join(dir, "run.sh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RUNNER_INSTALL_DIR", dir)
	// 忽略 SIGTERM 的那一版只能靠 SIGKILL 收场，否则它会挂在 FIFO 的 open 上不返回，
	// 把 t.TempDir() 的清理一起拖住
	t.Cleanup(func() {
		for _, pid := range runnerproc.Find(dir) {
			_ = syscall.Kill(-pid, syscall.SIGKILL)
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})
	return dir
}

// startAndWait 拉起 run.sh 并等到它出现在进程表里
func startAndWait(t *testing.T, dir string) {
	t.Helper()
	if err := startRunner(dir); err != nil {
		t.Fatalf("启动 run.sh 失败: %v", err)
	}
	if !waitUntil(func() bool { return runnerproc.Running(dir) }) {
		t.Fatal("准备阶段：run.sh 应已在运行")
	}
}

// docker stop -t 30 的 30 秒宽限期原先根本到不了 Runner：Agent 是容器里的 PID 1，
// 它不订阅 SIGTERM，Go 运行时于是立刻结束进程，内核紧接着 SIGKILL 掉 namespace 里
// 剩下的一切。shutdown 要把这段宽限期真的用在等 Runner 收尾上。
//
// 变异：去掉 shutdown 里的等待（发完 SIGTERM 就 return），本条变红——
// run.sh 还在 trap 里收尾，stopped 为 false。
func TestShutdown_StopsRunnerAndWaits(t *testing.T) {
	requireLinux(t)
	const windDown = 1 * time.Second
	dir := newTrappingRunnerDir(t, termExitsAfterDelay(windDown))
	startAndWait(t, dir)

	const grace = 10 * time.Second
	start := time.Now()
	stopped := shutdown(context.Background(), dir, grace)
	elapsed := time.Since(start)

	if !stopped {
		t.Fatalf("run.sh 会在 %s 内退出，shutdown 应报告已停止（耗时 %s）", windDown, elapsed)
	}
	if runnerproc.Running(dir) {
		t.Fatal("shutdown 返回已停止，进程表里却还有 runner")
	}
	// 真的等到了它收尾完：发完信号就返回的话，这里只有几毫秒
	if elapsed < windDown*7/10 {
		t.Fatalf("没有等 run.sh 收尾就返回了：耗时 %s，收尾需要 %s", elapsed, windDown)
	}
	// 又不是等满宽限期：那说明没在轮询，只是睡够了时间
	if elapsed >= grace {
		t.Fatalf("应在 run.sh 退出后就返回，实际用满了宽限期 %s", elapsed)
	}
}

// 反过来：Runner 赖着不走时不能一直等。等下去 docker stop 的计时器一到，
// 连 Agent 自己都是被 SIGKILL 的，日志里连一句「还没停下来」都留不下。
//
// 变异：去掉 shutdown 里的超时，本条会一直挂住，直到 go test 的超时才失败，
// 所以这个包要带 -timeout 跑（Makefile 的 check 走 go test -race，默认 10 分钟）。
func TestShutdown_GivesUpAfterGrace(t *testing.T) {
	requireLinux(t)
	// trap '' TERM 把 SIGTERM 设成 SIG_IGN，脚本不会被打断
	dir := newTrappingRunnerDir(t, termIgnored)
	startAndWait(t, dir)

	const grace = 500 * time.Millisecond
	start := time.Now()
	stopped := shutdown(context.Background(), dir, grace)
	elapsed := time.Since(start)

	if stopped {
		t.Fatal("run.sh 忽略了 SIGTERM，shutdown 不该报告已停止")
	}
	if elapsed < grace {
		t.Fatalf("应等满宽限期再放弃，实际只等了 %s", elapsed)
	}
	// 上限留足余量：轮询间隔 200ms，慢机器上多走一轮也算正常
	if elapsed > grace+4*runnerPollInterval+2*time.Second {
		t.Fatalf("超过宽限期太多才返回：%s（宽限期 %s）", elapsed, grace)
	}
}

// 没有 Runner 在跑时 shutdown 立刻返回，不该白等一个宽限期。
// 容器里常态就是这样：Runner 空闲时被停掉，之后才 docker stop 容器。
func TestShutdown_NoRunnerReturnsImmediately(t *testing.T) {
	requireLinux(t)
	dir := newTrappingRunnerDir(t, termExitsNow)

	start := time.Now()
	if !shutdown(context.Background(), dir, 10*time.Second) {
		t.Fatal("没有 runner 在跑时应直接报告已停止")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("没有 runner 在跑时不该等待，实际 %s", elapsed)
	}
}

// 防回归：不设超时的 http.Server 会无限期地等一个不再说话的对端，
// 一个半开连接就能占住一个 goroutine 和一个 fd 到进程结束，而 Runner 容器是长期存活的。
// 这些字段没有任何编译期约束，删掉一行照样能跑，所以在这里钉住。
func TestNewAgentServer_HasTimeouts(t *testing.T) {
	srv := newAgentServer("8081", newAgentMux())

	if srv.Addr != ":8081" {
		t.Fatalf("监听地址应为 :8081，实际 %q", srv.Addr)
	}
	for _, tc := range []struct {
		name string
		got  time.Duration
	}{
		{"ReadHeaderTimeout", srv.ReadHeaderTimeout},
		{"ReadTimeout", srv.ReadTimeout},
		{"WriteTimeout", srv.WriteTimeout},
		{"IdleTimeout", srv.IdleTimeout},
	} {
		if tc.got <= 0 {
			t.Errorf("%s 应为正数，实际 %v", tc.name, tc.got)
		}
	}
	if srv.MaxHeaderBytes <= 0 {
		t.Errorf("MaxHeaderBytes 应为正数，实际 %d", srv.MaxHeaderBytes)
	}
	// Agent 的处理函数都是即时返回的，WriteTimeout 在这一侧没有 Manager 那边的顾虑
	if srv.WriteTimeout > time.Minute {
		t.Errorf("WriteTimeout 给得过松：%v", srv.WriteTimeout)
	}
}

// 独立 mux 而不是 DefaultServeMux：后者是包级全局变量，
// 同一路径注册两次就 panic，测试因此只能构造一次 server。
func TestNewAgentMux_ServesHealthAndIsReusable(t *testing.T) {
	t.Setenv("AGENT_TOKEN", "a-token")
	mux := newAgentMux()

	req, err := http.NewRequest(http.MethodGet, "/health", nil)
	if err != nil {
		t.Fatal(err)
	}
	if h, pattern := mux.Handler(req); pattern == "" || h == nil {
		t.Fatal("/health 应有处理函数")
	}
	// 再构造一次：用 DefaultServeMux 的话这里就 panic 了
	if second := newAgentMux(); second == nil {
		t.Fatal("newAgentMux 应可反复构造")
	}
}
