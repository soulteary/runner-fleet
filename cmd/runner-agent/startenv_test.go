package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/soulteary/runner-fleet/internal/runnerproc"
)

// 容器模式下 Agent 的 AGENT_TOKEN 是 Manager 用 -e 注入的（见 containerSpec.createArgs），
// 而 Agent 起 run.sh 时把整个环境原样传下去，于是 Job 的环境里有这个令牌。
// 它只能控制该 Runner 自身，影响比 Basic Auth 密码小，但同样会被一个 env 步骤打进日志。
//
// 注意这里滤的只是传给子进程的那份副本：Agent 自己每次请求都要 os.Getenv("AGENT_TOKEN")
// 去鉴权（expectedToken），对本进程 Unsetenv 会直接把鉴权关掉。
func TestStartRunner_DoesNotLeakAgentToken(t *testing.T) {
	requireEnvDumpOS(t)
	t.Setenv("AGENT_TOKEN", "super-secret")
	t.Setenv("DOCKER_HOST", "tcp://d:2375")

	dir := t.TempDir()
	writeEnvDumpingRunScript(t, dir)
	killRunnerProcsOnCleanup(t, dir)

	if err := startRunner(dir); err != nil {
		t.Fatalf("startRunner 失败: %v", err)
	}
	dump := waitForEnvDump(t, dir)

	if v, ok := envDumpLookup(dump, "AGENT_TOKEN"); ok {
		t.Errorf("run.sh 的环境里有 AGENT_TOKEN=%q；Job 的一个 env 步骤就能把它打进日志", v)
	}
	// Agent 自己还得读得到，否则 /start、/stop 就不鉴权了
	if got := expectedToken(); got != "super-secret" {
		t.Errorf("expectedToken() = %q，Agent 本进程的令牌不该被动过", got)
	}
	if v, ok := envDumpLookup(dump, "DOCKER_HOST"); !ok || v != "tcp://d:2375" {
		t.Errorf("DOCKER_HOST = %q（存在: %v），期望原样透传的 tcp://d:2375", v, ok)
	}
}

// ---- 与 internal/runner 那条同名用例共用的做法，两处各有一份 ----

func requireEnvDumpOS(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("用例依赖 bash 与 FIFO")
	}
}

// writeEnvDumpingRunScript 写一个先把自己的环境落盘、再一直挂住的 run.sh。
// 挂住的写法与 writeBlockingRunScript 一致，理由见那里的注释。
func writeEnvDumpingRunScript(t *testing.T, dir string) {
	t.Helper()
	fifo := filepath.Join(dir, ".test-block")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatalf("创建 FIFO 失败: %v", err)
	}
	script := "#!/bin/bash\nenv > \"$PWD/" + envDumpFile + ".tmp\"\n" +
		"mv \"$PWD/" + envDumpFile + ".tmp\" \"$PWD/" + envDumpFile + "\"\n" +
		"read -r -t 120 < \"" + fifo + "\"\n"
	if err := os.WriteFile(filepath.Join(dir, "run.sh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

const envDumpFile = ".env.out"

func killRunnerProcsOnCleanup(t *testing.T, dir string) {
	t.Helper()
	t.Cleanup(func() {
		for _, pid := range runnerproc.Find(dir) {
			_ = syscall.Kill(-pid, syscall.SIGKILL)
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})
}

func waitForEnvDump(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, envDumpFile)
	deadline := time.Now().Add(10 * time.Second)
	for {
		b, err := os.ReadFile(path)
		if err == nil {
			return string(b)
		}
		if time.Now().After(deadline) {
			t.Fatalf("等不到 %s，run.sh 可能没被拉起来: %v", path, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// envDumpLookup 在 env(1) 的输出里按变量名取值，按行比对 KEY=。
// 不能用 strings.Contains：那样 XAGENT_TOKEN 也算命中。
func envDumpLookup(dump, name string) (string, bool) {
	for _, line := range strings.Split(dump, "\n") {
		k, v, ok := strings.Cut(line, "=")
		if ok && k == name {
			return v, true
		}
	}
	return "", false
}
