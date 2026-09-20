package runnerproc

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
	"testing"
	"time"
)

// fakeProc 搭一个假的 procfs：pid -> argv，供分类用例使用，不依赖真实进程
func fakeProc(t *testing.T, procs map[int][]string) {
	t.Helper()
	root := t.TempDir()
	for pid, argv := range procs {
		dir := filepath.Join(root, strconv.Itoa(pid))
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatalf("建假 proc 目录失败: %v", err)
		}
		var buf []byte
		for _, a := range argv {
			buf = append(buf, []byte(a)...)
			buf = append(buf, 0)
		}
		if err := os.WriteFile(filepath.Join(dir, "cmdline"), buf, 0644); err != nil {
			t.Fatalf("写假 cmdline 失败: %v", err)
		}
	}
	old := procRoot
	procRoot = root
	t.Cleanup(func() { procRoot = old })
}

func TestFind_ClaimsListenerAndSupervisors(t *testing.T) {
	const dir = "/runner"
	fakeProc(t, map[int][]string{
		// run.sh：内核走 shebang，解释器在 argv[0]，脚本在 argv[1]
		11: {"/bin/bash", dir + "/run.sh"},
		// run.sh 复制出来并调用的 run-helper.sh
		12: {"/bin/bash", dir + "/run-helper.sh"},
		// 真正的监听器，由 run-helper.sh 以绝对路径拉起
		13: {dir + "/bin/Runner.Listener", "run"},
		// 无关进程
		14: {"/usr/sbin/sshd", "-D"},
	})
	got := Find(dir)
	want := []int{11, 12, 13}
	if len(got) != len(want) {
		t.Fatalf("Find 返回 %v，期望 %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Find 返回 %v，期望 %v", got, want)
		}
	}
	if !Running(dir) {
		t.Fatal("Running 应为 true")
	}
}

// 监护脚本必须排在监听器前面：Stop 按这个顺序发信号，
// 先停 run.sh 才不会被它的 while 循环重新拉起一个监听器
func TestFind_SupervisorsSortBeforeListener(t *testing.T) {
	const dir = "/runner"
	fakeProc(t, map[int][]string{
		// 故意让监听器的 pid 比监护脚本小
		7:  {dir + "/bin/Runner.Listener", "run"},
		90: {"/bin/bash", dir + "/run.sh"},
	})
	got := Find(dir)
	if len(got) != 2 || got[0] != 90 || got[1] != 7 {
		t.Fatalf("Find 返回 %v，期望监护脚本 90 在监听器 7 之前", got)
	}
}

// 只有监听器、没有监护脚本也算在跑（比如 run.sh 已被替换或直接跑 Runner.Listener）
func TestFind_ListenerAloneCounts(t *testing.T) {
	const dir = "/runner"
	fakeProc(t, map[int][]string{
		21: {dir + "/bin/Runner.Listener", "run"},
	})
	if !Running(dir) {
		t.Fatal("只有 Runner.Listener 在跑时也应判定为运行中")
	}
}

// 只有监护脚本、监听器不在，也算在跑：run-helper.sh 对退出码 2 的处理是
// sleep 5 后返回、由 run.sh 重新拉起，那 5 秒里不能判成已死
func TestFind_SupervisorAloneCountsDuringListenerRestart(t *testing.T) {
	const dir = "/runner"
	fakeProc(t, map[int][]string{
		22: {"/bin/bash", dir + "/run.sh"},
	})
	if !Running(dir) {
		t.Fatal("监听器重启间隙里 run.sh 仍在，应判定为运行中")
	}
}

// 同一台机器上多个 runner，各认各的目录，不能互相串
func TestFind_DoesNotClaimAnotherInstallDir(t *testing.T) {
	fakeProc(t, map[int][]string{
		31: {"/bin/bash", "/runners/droiddesk-2/run.sh"},
		32: {"/runners/droiddesk-2/bin/Runner.Listener", "run"},
	})
	if Running("/runners/droiddesk-3") {
		t.Fatal("droiddesk-3 没有进程，不应被 droiddesk-2 的进程认领")
	}
	if !Running("/runners/droiddesk-2") {
		t.Fatal("droiddesk-2 应判定为运行中")
	}
}

// 命令行里出现脚本路径不等于 runner 在跑
func TestFind_IgnoresUnrelatedCommandMentioningTheScript(t *testing.T) {
	const dir = "/runner"
	fakeProc(t, map[int][]string{
		41: {"/usr/bin/grep", dir + "/run.sh"},
		42: {"/usr/bin/tail", "-f", dir + "/run.sh"},
		43: {"/usr/bin/vim", dir + "/bin/Runner.Listener"},
	})
	if Running(dir) {
		t.Fatalf("grep/tail/vim 提到脚本路径不应算作 runner 在跑，Find=%v", Find(dir))
	}
}

// 内核线程的 cmdline 是空的，不能因此 panic 或误判
func TestFind_SkipsEmptyCmdline(t *testing.T) {
	const dir = "/runner"
	fakeProc(t, map[int][]string{
		2: {},
		3: {},
	})
	if Running(dir) {
		t.Fatal("空 cmdline 不应算作 runner 在跑")
	}
}

// 没有 /proc（Windows 或 procfs 未挂载）时报告找不到，不猜
func TestFind_NoProcfsReportsNotRunning(t *testing.T) {
	old := procRoot
	procRoot = filepath.Join(t.TempDir(), "does-not-exist")
	t.Cleanup(func() { procRoot = old })
	if Running("/runner") {
		t.Fatal("没有 procfs 时应报告未运行")
	}
	if got := Find("/runner"); got != nil {
		t.Fatalf("没有 procfs 时 Find 应返回 nil，得到 %v", got)
	}
}

func TestFind_EmptyInstallDir(t *testing.T) {
	if Running("") {
		t.Fatal("空安装目录不应判定为运行中")
	}
}

// 这是本包存在的理由：actions/runner 不写 pid 文件，而 .path 装的是 PATH 字符串。
// 判定必须只看进程，目录里放什么文件都不能左右结论。
func TestRunning_IgnoresPidLookalikeFiles(t *testing.T) {
	dir := t.TempDir()
	// 一个货真价实、确实活着的 pid——按旧实现会被当成 runner 在跑
	if err := os.WriteFile(filepath.Join(dir, "Runner.Listener.pid"),
		[]byte(strconv.Itoa(os.Getpid())), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".path"),
		[]byte("/usr/local/bin:/usr/bin:/bin"), 0644); err != nil {
		t.Fatal(err)
	}
	fakeProc(t, map[int][]string{51: {"/usr/sbin/sshd", "-D"}})
	if Running(dir) {
		t.Fatal("目录里的 pid 文件不应影响判定，只有进程说了算")
	}
}

// 端到端：拿真实的 /proc 和真实的进程验一遍，假 procfs 证明不了这一步
func TestFind_RealProcesses(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("进程探测依赖 /proc，仅在 Linux 上验证")
	}
	if _, err := os.Stat("/proc/self/cmdline"); err != nil {
		t.Skip("当前环境没有可用的 /proc")
	}
	dir := t.TempDir()

	// 监护脚本：照搬 actions/runner 的 shebang 形态，不要 exec，
	// 否则 bash 会被替换掉、argv 就不再是 `/bin/bash <dir>/run.sh`
	script := filepath.Join(dir, "run.sh")
	if err := os.WriteFile(script, []byte("#!/bin/bash\nsleep 120\n"), 0755); err != nil {
		t.Fatal(err)
	}
	// 监听器：要一个真正的可执行文件，argv[0] 才会是它自己的绝对路径；
	// shebang 脚本的 argv[0] 是解释器，模拟不了
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0755); err != nil {
		t.Fatal(err)
	}
	listener := ListenerBinary(dir)
	sleepBin, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("找不到 sleep")
	}
	b, err := os.ReadFile(sleepBin)
	if err != nil {
		t.Skip("读不到 sleep 可执行文件")
	}
	if err := os.WriteFile(listener, b, 0755); err != nil {
		t.Fatal(err)
	}

	if Running(dir) {
		t.Fatal("还没启动任何进程，不应判定为运行中")
	}

	sup := exec.Command(script)
	sup.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := sup.Start(); err != nil {
		t.Fatalf("启动监护脚本失败: %v", err)
	}
	lis := exec.Command(listener, "120")
	lis.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := lis.Start(); err != nil {
		t.Fatalf("启动监听器失败: %v", err)
	}
	t.Cleanup(func() {
		_ = syscall.Kill(-sup.Process.Pid, syscall.SIGKILL)
		_ = lis.Process.Kill()
		_, _ = sup.Process.Wait()
		_, _ = lis.Process.Wait()
	})

	if !waitFor(func() bool { return len(Find(dir)) == 2 }) {
		t.Fatalf("真实进程未被认领，Find=%v（期望监护脚本 %d 与监听器 %d）",
			Find(dir), sup.Process.Pid, lis.Process.Pid)
	}
	got := Find(dir)
	if got[0] != sup.Process.Pid || got[1] != lis.Process.Pid {
		t.Fatalf("Find=%v，期望 [%d %d]（监护脚本在前）", got, sup.Process.Pid, lis.Process.Pid)
	}

	// 另一个目录不该被这两个进程认领
	if Running(t.TempDir()) {
		t.Fatal("无关目录不应被认领")
	}

	// 进程退出后必须转为未运行，否则「已注册未运行则拉起」永远不会触发
	_ = syscall.Kill(-sup.Process.Pid, syscall.SIGKILL)
	_ = lis.Process.Kill()
	_, _ = sup.Process.Wait()
	_, _ = lis.Process.Wait()
	if !waitFor(func() bool { return !Running(dir) }) {
		t.Fatalf("进程已退出，Running 仍为 true，Find=%v", Find(dir))
	}
}

// waitFor 轮询等待条件成立，最多 5 秒
func waitFor(cond func() bool) bool {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return cond()
}

// FindMany 一次扫描要把进程分派对，不能把 a 的进程算到 b 头上
func TestFindMany_PartitionsAcrossInstallDirs(t *testing.T) {
	a, b, c := "/runners/a", "/runners/b", "/runners/c"
	fakeProc(t, map[int][]string{
		61: {"/bin/bash", a + "/run.sh"},
		62: {a + "/bin/Runner.Listener", "run"},
		63: {b + "/bin/Runner.Listener", "run"},
		64: {"/usr/sbin/sshd", "-D"},
	})
	got := FindMany([]string{a, b, c})
	if len(got[a]) != 2 || got[a][0] != 61 || got[a][1] != 62 {
		t.Fatalf("a 应为 [61 62]，得到 %v", got[a])
	}
	if len(got[b]) != 1 || got[b][0] != 63 {
		t.Fatalf("b 应为 [63]，得到 %v", got[b])
	}
	if _, ok := got[c]; ok {
		t.Fatalf("c 没有进程，不应出现在结果里，得到 %v", got[c])
	}
}

// 前缀相近的目录不能互相串：/runners/a 与 /runners/ab
func TestFindMany_PrefixSimilarDirsDoNotCollide(t *testing.T) {
	a, ab := "/runners/a", "/runners/ab"
	fakeProc(t, map[int][]string{
		71: {ab + "/bin/Runner.Listener", "run"},
	})
	got := FindMany([]string{a, ab})
	if _, ok := got[a]; ok {
		t.Fatalf("/runners/a 不应认领 /runners/ab 的进程，得到 %v", got[a])
	}
	if len(got[ab]) != 1 || got[ab][0] != 71 {
		t.Fatalf("/runners/ab 应为 [71]，得到 %v", got[ab])
	}
}

// 同一目录的不同写法各自都要拿到结果
func TestFindMany_SameDirDifferentSpellings(t *testing.T) {
	fakeProc(t, map[int][]string{
		81: {"/runners/a/bin/Runner.Listener", "run"},
	})
	got := FindMany([]string{"/runners/a", "/runners/a/", "/runners/./a"})
	for _, k := range []string{"/runners/a", "/runners/a/", "/runners/./a"} {
		if len(got[k]) != 1 || got[k][0] != 81 {
			t.Fatalf("%q 应为 [81]，得到 %v", k, got[k])
		}
	}
}

func TestFindMany_EmptyInput(t *testing.T) {
	fakeProc(t, map[int][]string{91: {"/runners/a/bin/Runner.Listener", "run"}})
	if got := FindMany(nil); got != nil {
		t.Fatalf("空入参应返回 nil，得到 %v", got)
	}
	if got := FindMany([]string{"", ""}); got != nil {
		t.Fatalf("全为空串应返回 nil，得到 %v", got)
	}
}
