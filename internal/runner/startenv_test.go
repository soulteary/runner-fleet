package runner

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

// Start 起的是 run.sh，而 run.sh 起的是 Job。默认模式下 Runner 是 Manager 的子进程，
// 于是 docker-compose.yml 用 environment: 注入 Manager 的 BASIC_AUTH_PASSWORD 会一路
// 传到 Job 的环境里——workflow 里任何一个 env / printenv 步骤，或者某个会打印环境变量的
// 第三方 Action，都会把管理后台密码写进 Job 日志。它不是 Actions secret，GitHub 不打码。
//
// 这条用例盯的就是那条路径：真的把 run.sh 拉起来，让它把自己拿到的环境落盘，再看里面有什么。
func TestStart_DoesNotLeakCredentials(t *testing.T) {
	requireEnvDumpOS(t)
	t.Setenv("BASIC_AUTH_PASSWORD", "x")
	t.Setenv("DOCKER_HOST", "tcp://d:2375")

	dir := t.TempDir()
	writeEnvDumpingRunScript(t, dir)
	killRunnerProcsOnCleanup(t, dir)

	if err := Start(dir); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	dump := waitForEnvDump(t, dir)

	// 凭据不该在里面
	if v, ok := envDumpLookup(dump, "BASIC_AUTH_PASSWORD"); ok {
		t.Errorf("run.sh 的环境里有 BASIC_AUTH_PASSWORD=%q；一个 env 步骤就能把它打进 Job 日志", v)
	}
	// 其余变量照常透传：DinD 就靠继承来的 DOCKER_HOST 找到 daemon，
	// 把它一起滤掉会静默地让容器模式下的 Job 连不上 Docker。
	if v, ok := envDumpLookup(dump, "DOCKER_HOST"); !ok || v != "tcp://d:2375" {
		t.Errorf("DOCKER_HOST = %q（存在: %v），期望原样透传的 tcp://d:2375", v, ok)
	}
}

// ---- 下面几个是本文件与 agent 侧那条同名用例共用的做法，两处各有一份 ----

func requireEnvDumpOS(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("用例依赖 bash 与 FIFO")
	}
}

// writeEnvDumpingRunScript 写一个先把自己的环境落盘、再一直挂住的 run.sh。
//
// 挂住的写法与 writeBlockingRunScript 一致，理由见那里的注释：read 是 builtin，
// 打开一个没有写端的 FIFO 会就地阻塞，全程不 fork，进程数因此是确定的。
// 多出来的只有第一行——env 是本用例唯一要看的东西。
func writeEnvDumpingRunScript(t *testing.T, dir string) {
	t.Helper()
	fifo := filepath.Join(dir, ".test-block")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatalf("创建 FIFO 失败: %v", err)
	}
	// cmd.Dir 就是安装目录，所以 $PWD 即 dir。先写临时文件再 mv，
	// 免得轮询正好读到写了一半的内容。
	script := "#!/bin/bash\nenv > \"$PWD/" + envDumpFile + ".tmp\"\n" +
		"mv \"$PWD/" + envDumpFile + ".tmp\" \"$PWD/" + envDumpFile + "\"\n" +
		"read -r -t 120 < \"" + fifo + "\"\n"
	if err := os.WriteFile(filepath.Join(dir, "run.sh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

const envDumpFile = ".env.out"

// killRunnerProcsOnCleanup 用例结束时收掉 run.sh。
// 脚本自带 read -t 120 兜底，但那 120 秒里进程是留着的，不该指望它。
func killRunnerProcsOnCleanup(t *testing.T, dir string) {
	t.Helper()
	t.Cleanup(func() {
		for _, pid := range runnerproc.Find(dir) {
			_ = syscall.Kill(-pid, syscall.SIGKILL)
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})
}

// waitForEnvDump 等 run.sh 把环境落盘。Start 只负责 fork，写文件是子进程的事，
// 所以这里只能轮询。
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

// envDumpLookup 在 env(1) 的输出里按变量名取值。
//
// 不能用 strings.Contains(dump, "BASIC_AUTH_PASSWORD")：那样 BASIC_AUTH_PASSWORD_FILE
// 也算命中，于是一个本该保留的变量会让用例误报；反过来，值里带换行的变量也会把
// 整块输出搅乱。按行取 KEY= 前缀是这里唯一说得准的读法。
func envDumpLookup(dump, name string) (string, bool) {
	for _, line := range strings.Split(dump, "\n") {
		k, v, ok := strings.Cut(line, "=")
		if ok && k == name {
			return v, true
		}
	}
	return "", false
}
