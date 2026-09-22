package runner

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/soulteary/runner-fleet/internal/runnerproc"
)

// wantStopArgs 是 docker stop 该带的参数前缀。
//
// 从 runnerproc.StopGracePeriod 推出来而不是写「stop -t 30」：宽限期只该在那一个
// 常量里定义。写死 30 的话，把常量调大之后这里依然绿着——而 Agent 已经在按新的
// 宽限期等 Runner 退出，docker 却还是 30 秒就 SIGKILL，Job 照样被拦腰砍断。
func wantStopArgs(runnerName string) string {
	secs := strconv.Itoa(int(runnerproc.StopGracePeriod / time.Second))
	return "stop -t " + secs + " " + ContainerName(runnerName)
}

// fakeDockerProgram 在 PATH 前面放一个假 docker，行为完全由 body 决定。
//
// 与 recreate_test.go 里的 fakeDocker 的区别是它能失败：退出码与输出都可控，
// 而启停 / 删除这条路径上几乎所有分支（容器不存在、权限不足、daemon 连不上）
// 都只在 docker 返回非 0 时才走到。body 是一段 sh，可以直接 case "$1" in。
func fakeDockerProgram(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	logPath := filepath.Join(dir, "calls.log")
	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$*\" >> " + logPath + "\n" +
		body + "\n"
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	// 只前置，不替换：脚本自身还要用到 cat、printf 等
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return logPath
}

func readCalls(t *testing.T, logPath string) string {
	t.Helper()
	b, err := os.ReadFile(logPath)
	if err != nil {
		return ""
	}
	return string(b)
}

// ---- StopRunnerContainer ----

func TestStopRunnerContainerSucceeds(t *testing.T) {
	logPath := fakeDockerProgram(t, "exit 0")
	if err := StopRunnerContainer(context.Background(), "demo"); err != nil {
		t.Fatalf("停止失败: %v", err)
	}
	calls := readCalls(t, logPath)
	if !strings.Contains(calls, wantStopArgs("demo")) {
		t.Fatalf("没有按预期调用 %q，实际调用:\n%s", wantStopArgs("demo"), calls)
	}
}

// 停一个已经不存在的容器不是错误：删除流程、界面重复点击都会走到这里，
// 报错只会让调用方以为自己漏了什么。
func TestStopRunnerContainerTreatsMissingContainerAsDone(t *testing.T) {
	logPath := fakeDockerProgram(t, `
case "$1" in
  stop)    echo "Error response from daemon: No such container: x" >&2; exit 1 ;;
  inspect) echo "Error: No such object: x" >&2; exit 1 ;;
esac
exit 0`)
	if err := StopRunnerContainer(context.Background(), "demo"); err != nil {
		t.Fatalf("容器不存在时应视作已停止，却返回: %v", err)
	}
	// 判定「容器是否真的不在了」必须另外 inspect 一次，不能只看 stop 的输出
	if !strings.Contains(readCalls(t, logPath), "inspect") {
		t.Fatal("stop 失败后应再 inspect 确认容器是否存在")
	}
}

// 容器还在却停不下来，是真失败，不能吞掉
func TestStopRunnerContainerReportsRealFailure(t *testing.T) {
	fakeDockerProgram(t, `
case "$1" in
  stop)    echo "Error response from daemon: cannot stop container" >&2; exit 1 ;;
  inspect) echo "true" ;;
esac
exit 0`)
	err := StopRunnerContainer(context.Background(), "demo")
	if err == nil {
		t.Fatal("容器仍在时 stop 失败应返回错误")
	}
	if !strings.Contains(err.Error(), "cannot stop container") {
		t.Fatalf("错误里应带上 docker 的原始输出，实际: %v", err)
	}
}

// 权限 / daemon 连不上是这套部署里最常见的一类失败，错误必须直接给出处理办法，
// 否则用户只能看到一句 exit status 1
func TestStopRunnerContainerPermissionErrorCarriesHint(t *testing.T) {
	fakeDockerProgram(t, `
case "$1" in
  stop)    echo "permission denied while trying to connect to the Docker daemon socket" >&2; exit 1 ;;
  inspect) echo "true" ;;
esac
exit 0`)
	err := StopRunnerContainer(context.Background(), "demo")
	if err == nil {
		t.Fatal("期望返回错误")
	}
	if !strings.Contains(err.Error(), "DOCKER_GID") {
		t.Fatalf("权限类错误应附带 group_add / DOCKER_GID 的处理提示，实际: %v", err)
	}
}

// ---- RemoveRunnerContainer ----

func TestRemoveRunnerContainerStopsThenRemoves(t *testing.T) {
	logPath := fakeDockerProgram(t, "exit 0")
	if err := RemoveRunnerContainer(context.Background(), "demo"); err != nil {
		t.Fatalf("删除失败: %v", err)
	}
	calls := readCalls(t, logPath)
	cn := ContainerName("demo")
	stopAt := strings.Index(calls, wantStopArgs("demo"))
	rmAt := strings.Index(calls, "rm -f "+cn)
	if stopAt < 0 || rmAt < 0 {
		t.Fatalf("stop 与 rm 都应被调用，实际调用:\n%s", calls)
	}
	// 先停再删：直接 rm -f 会把正在跑的 Job 拦腰砍断，而 stop 给了 30 秒收尾
	if stopAt > rmAt {
		t.Fatalf("应先 stop 再 rm，实际调用:\n%s", calls)
	}
}

// stop 失败不该挡住 rm：容器可能已经退出，甚至已经不在了
func TestRemoveRunnerContainerIgnoresStopFailure(t *testing.T) {
	logPath := fakeDockerProgram(t, `
case "$1" in
  stop) echo "Error response from daemon: No such container" >&2; exit 1 ;;
  rm)   exit 0 ;;
esac
exit 0`)
	if err := RemoveRunnerContainer(context.Background(), "demo"); err != nil {
		t.Fatalf("stop 失败不应中断删除: %v", err)
	}
	if !strings.Contains(readCalls(t, logPath), "rm -f ") {
		t.Fatal("stop 失败后仍应继续执行 rm")
	}
}

func TestRemoveRunnerContainerTreatsMissingContainerAsDone(t *testing.T) {
	fakeDockerProgram(t, `
case "$1" in
  rm) echo "Error: No such container: x" >&2; exit 1 ;;
esac
exit 0`)
	if err := RemoveRunnerContainer(context.Background(), "demo"); err != nil {
		t.Fatalf("容器不存在时应视作已删除，却返回: %v", err)
	}
}

func TestRemoveRunnerContainerReportsRealFailure(t *testing.T) {
	fakeDockerProgram(t, `
case "$1" in
  rm) echo "Error response from daemon: device or resource busy" >&2; exit 1 ;;
esac
exit 0`)
	err := RemoveRunnerContainer(context.Background(), "demo")
	if err == nil {
		t.Fatal("rm 真失败时应返回错误")
	}
	if !strings.Contains(err.Error(), "device or resource busy") {
		t.Fatalf("错误里应带上 docker 的原始输出，实际: %v", err)
	}
}

// ---- ContainerState ----

func TestContainerStateExisting(t *testing.T) {
	fakeDockerProgram(t, `
case "$1" in
  inspect)
    cat <<'JSON'
[{"State":{"Status":"running","Running":true},"Config":{"Image":"img:tag"}}]
JSON
    ;;
esac
exit 0`)
	exists, status, err := ContainerState(context.Background(), "github-runner-demo")
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	if !exists || status != "running" {
		t.Fatalf("得到 exists=%v status=%q，期望 true/running", exists, status)
	}
}

// 「名字有没有被占用」是这个函数最主要的用途（添加 Runner 前的冲突预检），
// 所以容器不存在是正常答案，不是错误
func TestContainerStateMissingIsNotAnError(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"inspect 报 No such object", `
case "$1" in
  inspect) echo "Error: No such object: x" >&2; exit 1 ;;
esac
exit 0`},
		{"inspect 返回空数组", `
case "$1" in
  inspect) echo "[]" ;;
esac
exit 0`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fakeDockerProgram(t, tc.body)
			exists, status, err := ContainerState(context.Background(), "github-runner-demo")
			if err != nil {
				t.Fatalf("容器不存在不应报错: %v", err)
			}
			if exists || status != "" {
				t.Fatalf("得到 exists=%v status=%q，期望 false/空", exists, status)
			}
		})
	}
}

// 连不上 daemon 与「容器不存在」必须区分开：前者说明整个 Docker 不可用，
// 把它当成「名字没被占用」会让添加流程一路走到 docker create 才炸
func TestContainerStateDockerUnreachableIsAnError(t *testing.T) {
	fakeDockerProgram(t, `
case "$1" in
  inspect) echo "Cannot connect to the Docker daemon at unix:///var/run/docker.sock" >&2; exit 1 ;;
esac
exit 0`)
	exists, _, err := ContainerState(context.Background(), "github-runner-demo")
	if err == nil {
		t.Fatal("Docker 不可用时应返回错误，而不是报告「容器不存在」")
	}
	if exists {
		t.Fatal("出错时 exists 应为 false")
	}
}

// ---- 输出判别（纯函数，但决定上面每条路径走哪个分支）----

func TestContainerNotFound(t *testing.T) {
	cases := []struct {
		out  string
		want bool
	}{
		{"Error response from daemon: No such container: abc", true},
		{"Error: No such object: abc", true},
		{"error: NO SUCH CONTAINER", true}, // 判别不分大小写
		{"Error response from daemon: 没有此容器", true},
		{"未找到容器 abc", true},
		{"没有找到容器 abc", true},
		{"", false},
		{"permission denied", false},
		// 容器存在、只是停不下来，不能被当成「不存在」而静默成功
		{"Error response from daemon: cannot stop container", false},
	}
	for _, tc := range cases {
		t.Run(tc.out, func(t *testing.T) {
			if got := containerNotFound([]byte(tc.out)); got != tc.want {
				t.Fatalf("containerNotFound(%q) = %v，期望 %v", tc.out, got, tc.want)
			}
		})
	}
}

func TestContainerStartUnrecoverable(t *testing.T) {
	cases := []struct {
		out  string
		want bool
	}{
		{"Error response from daemon: network runner-net not found", true},
		{"could not find network runner-net", true},
		{"could not attach to network runner-net", true},
		{"failed to create endpoint x on network y", true},
		{"failed to get network during CreateEndpoint", true},
		{"No such object: whatever", false},
		{"", false},
		// 端口占用是可恢复的，删容器重建救不了，别误判
		{"Error starting userland proxy: address already in use", false},
	}
	for _, tc := range cases {
		t.Run(tc.out, func(t *testing.T) {
			if got := containerStartUnrecoverable([]byte(tc.out)); got != tc.want {
				t.Fatalf("containerStartUnrecoverable(%q) = %v，期望 %v", tc.out, got, tc.want)
			}
		})
	}
}

func TestManagerDockerHostIsDind(t *testing.T) {
	cases := []struct {
		env  string
		want bool
	}{
		{"", false},
		{"unix:///var/run/docker.sock", false},
		{"tcp://runner-dind:2375", true},
		{"  tcp://runner-dind:2375  ", true}, // .env 里常见的多余空格
	}
	for _, tc := range cases {
		t.Run(tc.env, func(t *testing.T) {
			t.Setenv("DOCKER_HOST", tc.env)
			if got := ManagerDockerHostIsDind(); got != tc.want {
				t.Fatalf("DOCKER_HOST=%q 得到 %v，期望 %v", tc.env, got, tc.want)
			}
		})
	}
}
