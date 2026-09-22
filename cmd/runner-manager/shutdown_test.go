package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/soulteary/runner-fleet/internal/runner"
	"github.com/soulteary/runner-fleet/internal/runnerproc"
)

// mkTrappingRunnerDir 与 mkRunnerDir 相同，只是 run.sh 多一条 TERM trap，
// 用来区分「肯退，但要一点时间收尾」和「根本不理 SIGTERM」两种 Runner。
//
// 收尾必须耗时可观测：一个 `trap 'exit 0' TERM` 的 bash 从收到信号到 /proc 里
// cmdline 变空只要 0.7 毫秒，比一个轮询间隔还短，于是「发完信号就返回」的实现
// 也照样能通过——那就什么都没测到。
//
// 延迟用 `exec 3<>fifo` + `read -t` 而不是 `sleep N`：后者要 fork，而在 fork 与
// execve 之间子进程的 cmdline 仍是 `/bin/bash <dir>/run.sh`，会被 runnerproc
// 认成第二个监护脚本（见 writeBlockingRunScript 的说明）。
func mkTrappingRunnerDir(t *testing.T, base, name string, windDown time.Duration) string {
	t.Helper()
	dir := filepath.Join(base, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".runner"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{".test-block", ".test-delay"} {
		if err := syscall.Mkfifo(filepath.Join(dir, f), 0o600); err != nil {
			t.Fatalf("创建 FIFO %s 失败: %v", f, err)
		}
	}
	// windDown <= 0 表示「忽略 SIGTERM」：trap '' TERM 把它设成 SIG_IGN
	trap := ""
	if windDown > 0 {
		trap = `exec 3<>"` + filepath.Join(dir, ".test-delay") + `"; ` +
			`read -r -t ` + strconv.Itoa(int(windDown/time.Second)) + ` -u 3; exit 0`
	}
	script := "#!/bin/bash\ntrap '" + trap + "' TERM\n" +
		"read -r -t 120 < \"" + filepath.Join(dir, ".test-block") + "\"\n"
	if err := os.WriteFile(filepath.Join(dir, "run.sh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	// 忽略 SIGTERM 的那一版只能靠 SIGKILL 收场，否则它会挂在 FIFO 的 open 上不返回
	t.Cleanup(func() {
		for _, pid := range runnerproc.Find(dir) {
			_ = syscall.Kill(-pid, syscall.SIGKILL)
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})
	return dir
}

func startRunnerAt(t *testing.T, dir string) {
	t.Helper()
	if err := runner.Start(dir); err != nil {
		t.Fatalf("启动 %s 的 run.sh 失败: %v", dir, err)
	}
	if !waitUntil(func() bool { return runnerproc.Running(dir) }) {
		t.Fatalf("准备阶段：%s 的 run.sh 应已在运行", dir)
	}
}

// 默认模式下 run.sh 是 Manager 的子进程，同在一个 PID namespace 里：
// Manager 一退出，内核对 namespace 里剩下的进程一律 SIGKILL，Job 不会被优雅取消。
// 关闭路径必须先停 Runner 并等它们收尾。
//
// 变异：把 stopDefaultModeRunners 里的等待去掉（发完 SIGTERM 就 return），
// 本条变红——两个 run.sh 都还在 trap 里收尾。
func TestStopDefaultModeRunners_StopsRunningAndWaits(t *testing.T) {
	requireLinux(t)
	const windDown = 1 * time.Second
	base := t.TempDir()
	a := mkTrappingRunnerDir(t, base, "a", windDown)
	b := mkTrappingRunnerDir(t, base, "b", windDown)
	startRunnerAt(t, a)
	startRunnerAt(t, b)

	const grace = 10 * time.Second
	start := time.Now()
	stopDefaultModeRunners(context.Background(), cfgWith(base, "a", "b"), grace)
	elapsed := time.Since(start)

	for _, dir := range []string{a, b} {
		if runnerproc.Running(dir) {
			t.Errorf("%s 的 run.sh 仍在运行", dir)
		}
	}
	// 真的等到了收尾完：发完信号就返回的话，这里只有几毫秒
	if elapsed < windDown*7/10 {
		t.Fatalf("没有等 run.sh 收尾就返回了：耗时 %s，收尾需要 %s", elapsed, windDown)
	}
	if elapsed >= grace {
		t.Fatalf("应在 run.sh 退出后就返回，实际用满了宽限期 %s", elapsed)
	}
}

// Runner 赖着不走时不能一直等：等下去 docker stop 的计时器一到，
// 连 Manager 自己都是被 SIGKILL 的，日志里连一句「还没停下来」都留不下。
//
// 变异：去掉超时，本条会一直挂住，直到 go test 的超时才失败。
func TestStopDefaultModeRunners_GivesUpAfterGrace(t *testing.T) {
	requireLinux(t)
	base := t.TempDir()
	stubborn := mkTrappingRunnerDir(t, base, "stubborn", 0) // trap '' TERM
	startRunnerAt(t, stubborn)

	const grace = 500 * time.Millisecond
	start := time.Now()
	stopDefaultModeRunners(context.Background(), cfgWith(base, "stubborn"), grace)
	elapsed := time.Since(start)

	if elapsed < grace {
		t.Fatalf("应等满宽限期再放弃，实际只等了 %s", elapsed)
	}
	if elapsed > grace+4*runnerPollInterval+2*time.Second {
		t.Fatalf("超过宽限期太多才返回：%s（宽限期 %s）", elapsed, grace)
	}
	// 放弃不等于收拾掉：SIGKILL 由内核在 Manager 退出后自己发，这里不该代劳
	if !runnerproc.Running(stubborn) {
		t.Fatal("忽略 SIGTERM 的 run.sh 不该在宽限期内消失，用例前提不成立")
	}
}

// 没在跑的 Runner 不该被当成「等它退出」的对象，否则一个空闲的 Manager
// 每次重启都要白等一个宽限期
func TestStopDefaultModeRunners_IdleRunnersCostNothing(t *testing.T) {
	requireLinux(t)
	base := t.TempDir()
	mkTrappingRunnerDir(t, base, "idle", time.Second)

	start := time.Now()
	stopDefaultModeRunners(context.Background(), cfgWith(base, "idle"), 10*time.Second)
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("没有 runner 在跑时不该等待，实际 %s", elapsed)
	}
}

// 上下文被取消（外层兜底超时到了）时立刻返回，不再耗着宽限期
func TestStopDefaultModeRunners_HonorsContext(t *testing.T) {
	requireLinux(t)
	base := t.TempDir()
	stubborn := mkTrappingRunnerDir(t, base, "stubborn", 0)
	startRunnerAt(t, stubborn)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	stopDefaultModeRunners(ctx, cfgWith(base, "stubborn"), time.Minute)
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("上下文取消后应立即返回，实际 %s", elapsed)
	}
}

// 防回归：不设超时的 http.Server 会无限期地等一个不再说话的对端，
// 一个半开连接就能占住一个 goroutine 和一个 fd 到进程结束。
// 这些字段没有任何编译期约束，删掉一行照样能跑，所以在这里钉住。
func TestNewHTTPServer_HasTimeouts(t *testing.T) {
	srv := newHTTPServer(":8080", echo.New())

	if srv.Addr != ":8080" {
		t.Fatalf("监听地址应为 :8080，实际 %q", srv.Addr)
	}
	for _, tc := range []struct {
		name string
		got  time.Duration
	}{
		{"ReadHeaderTimeout", srv.ReadHeaderTimeout},
		{"ReadTimeout", srv.ReadTimeout},
		{"IdleTimeout", srv.IdleTimeout},
	} {
		if tc.got <= 0 {
			t.Errorf("%s 应为正数，实际 %v", tc.name, tc.got)
		}
	}
	if srv.MaxHeaderBytes <= 0 {
		t.Errorf("MaxHeaderBytes 应为正数，实际 %d", srv.MaxHeaderBytes)
	}
	// WriteTimeout 必须留空：它从「响应头开始写」起算，而 RecreateRunner 的
	// lifecycleContext 最长 90 秒。任何小于这个值的 WriteTimeout 都会在容器
	// **已经重建成功**之后掐断连接，界面上只看到一个网络错误，人会再点一次。
	if srv.WriteTimeout != 0 {
		t.Errorf("Manager 不应设置 WriteTimeout（见 newHTTPServer 的注释），实际 %v", srv.WriteTimeout)
	}
}

// 写接口收的都是很小的 JSON。没有上限时一个持续发送的请求体会被一路读进内存，
// 而 Basic Auth 是在这之后才校验的。
func TestNewEchoServer_LimitsRequestBody(t *testing.T) {
	e := newEchoServer()

	// 2MB，超过 1M 上限；BodyLimit 挂在 CSRF 与鉴权之前，所以这里不必伪造请求头
	req := httptest.NewRequest(http.MethodPost, "/api/runners", bytes.NewReader(make([]byte, 2<<20)))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("2MB 请求体应被拒为 413，实际 %d: %s", rec.Code, rec.Body.String())
	}
}
