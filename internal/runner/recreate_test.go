package runner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lab-dev/github-actions-runner-manager/internal/config"
)

// fakeDocker 在 PATH 前面放一个假的 docker：记录每次调用，并对 inspect 返回指定 JSON。
// 返回日志文件路径，供断言「到底执行了哪些 docker 命令」。
func fakeDocker(t *testing.T, inspectJSON string) string {
	t.Helper()
	dir := t.TempDir()
	logPath := filepath.Join(dir, "calls.log")
	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$*\" >> " + logPath + "\n" +
		"case \"$1\" in\n" +
		"  inspect)\n" +
		"    cat <<'JSON'\n" + inspectJSON + "\nJSON\n" +
		"    ;;\n" +
		"  image)\n" +
		"    echo 'sha256:aaa'\n" +
		"    ;;\n" +
		"esac\n" +
		"exit 0\n"
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	// 只把假 docker 放到最前面，不能整个替换 PATH：脚本里的 cat 等外部命令还得找得到
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	// 等待 Agent 就绪的 sleep 在这里没有意义，调成 0 免得每个用例白等 5 秒
	origCreate, origStart := agentReadyDelayAfterCreate, agentReadyDelayAfterStart
	agentReadyDelayAfterCreate, agentReadyDelayAfterStart = 0, 0
	t.Cleanup(func() {
		agentReadyDelayAfterCreate, agentReadyDelayAfterStart = origCreate, origStart
	})
	return logPath
}

func dockerCalls(t *testing.T, logPath string) string {
	t.Helper()
	b, err := os.ReadFile(logPath)
	if err != nil {
		return ""
	}
	return string(b)
}

func driftTestConfig(t *testing.T, backend string) (*config.Config, string) {
	t.Helper()
	base := t.TempDir()
	installDir := filepath.Join(base, "a")
	if err := os.MkdirAll(installDir, 0755); err != nil {
		t.Fatal(err)
	}
	return &config.Config{Runners: config.RunnersConfig{
		BasePath:         base,
		ContainerMode:    true,
		ContainerImage:   "img:tag",
		ContainerNetwork: "runner-net",
		JobDockerBackend: backend,
		AgentPort:        8081,
	}}, installDir
}

// inspectJSON 造一个「已停止、按 host-socket 建出来」的容器
func stoppedHostSocketInspect(mountSrc string) string {
	return `[{
	  "Image": "sha256:aaa",
	  "State": {"Status": "exited", "Running": false},
	  "Config": {
	    "Image": "img:tag",
	    "Env": ["DOCKER_HOST=unix:///var/run/docker.sock", "AGENT_TOKEN=already-injected"],
	    "Labels": {
      "io.runner-fleet.job-docker-backend": "host-socket",
      "io.runner-fleet.network": "runner-net"
    }
	  },
	  "HostConfig": {
	    "Binds": ["` + mountSrc + `:/runner", "/var/run/docker.sock:/var/run/docker.sock"],
	    "GroupAdd": ["999"],
	    "NetworkMode": "runner-net"
	  },
	  "NetworkSettings": {"Networks": {"runner-net": {}}}
	}]`
}

// TestStartRunnerContainer_RecreatesStoppedContainerOnDrift
// 容器按 host-socket 建出来，配置已改成 dind：启动时必须删掉重建，
// 否则它会带着宿主机 socket 一起回来，等于切换从未发生。
func TestStartRunnerContainer_RecreatesStoppedContainerOnDrift(t *testing.T) {
	cfg, installDir := driftTestConfig(t, "dind")
	logPath := fakeDocker(t, stoppedHostSocketInspect(installDir))

	// Agent 调用必然失败（容器名解析不了），这里只关心执行了哪些 docker 命令
	_ = StartRunnerContainer(context.Background(), cfg, "a", installDir)

	calls := dockerCalls(t, logPath)
	if !strings.Contains(calls, "rm -f github-runner-a") {
		t.Fatalf("漂移时应先删除旧容器，实际调用:\n%s", calls)
	}
	if !strings.Contains(calls, "create --name github-runner-a") {
		t.Fatalf("漂移时应重新创建容器，实际调用:\n%s", calls)
	}
	if !strings.Contains(calls, "DOCKER_HOST=tcp://runner-dind:2375") {
		t.Fatalf("重建时应按新后端注入 DOCKER_HOST，实际调用:\n%s", calls)
	}
}

// TestStartRunnerContainer_StartsStoppedContainerWhenInSync 配置没变就老实 start，不能白白重建
func TestStartRunnerContainer_StartsStoppedContainerWhenInSync(t *testing.T) {
	cfg, installDir := driftTestConfig(t, "host-socket")
	cfg.Runners.DockerGID = 999
	logPath := fakeDocker(t, stoppedHostSocketInspect(installDir))

	_ = StartRunnerContainer(context.Background(), cfg, "a", installDir)

	calls := dockerCalls(t, logPath)
	if !strings.Contains(calls, "start github-runner-a") {
		t.Fatalf("配置一致时应直接启动，实际调用:\n%s", calls)
	}
	if strings.Contains(calls, "rm -f") || strings.Contains(calls, "create --name") {
		t.Fatalf("配置一致时不该重建容器，实际调用:\n%s", calls)
	}
}

// TestStartRunnerContainer_RunningContainerIsNotRecreated
// 正在运行的容器即便漂移也不能动：上面很可能正跑着 Job，删掉就是拦腰截断。
func TestStartRunnerContainer_RunningContainerIsNotRecreated(t *testing.T) {
	cfg, installDir := driftTestConfig(t, "dind")
	running := strings.Replace(stoppedHostSocketInspect(installDir),
		`"State": {"Status": "exited", "Running": false}`,
		`"State": {"Status": "running", "Running": true}`, 1)
	logPath := fakeDocker(t, running)

	_ = StartRunnerContainer(context.Background(), cfg, "a", installDir)

	calls := dockerCalls(t, logPath)
	if strings.Contains(calls, "rm -f") || strings.Contains(calls, "create --name") {
		t.Fatalf("运行中的容器不该被自动重建，实际调用:\n%s", calls)
	}
}

// TestRecreateRunnerContainer_ForcesRebuildOfRunningContainer
// 显式点「重建」时才允许中断：运行中也删掉重建。
func TestRecreateRunnerContainer_ForcesRebuildOfRunningContainer(t *testing.T) {
	cfg, installDir := driftTestConfig(t, "host-socket")
	cfg.Runners.DockerGID = 999
	running := strings.Replace(stoppedHostSocketInspect(installDir),
		`"State": {"Status": "exited", "Running": false}`,
		`"State": {"Status": "running", "Running": true}`, 1)
	logPath := fakeDocker(t, running)

	// 配置与容器完全一致，强制重建仍应执行
	_ = RecreateRunnerContainer(context.Background(), cfg, "a", installDir)

	calls := dockerCalls(t, logPath)
	if !strings.Contains(calls, "rm -f github-runner-a") {
		t.Fatalf("强制重建应删除旧容器，实际调用:\n%s", calls)
	}
	if !strings.Contains(calls, "create --name github-runner-a") {
		t.Fatalf("强制重建应创建新容器，实际调用:\n%s", calls)
	}
}

// TestContainerRunnerStatus_ReportsDrift 状态查询顺带带出漂移说明，界面据此提示
func TestContainerRunnerStatus_ReportsDrift(t *testing.T) {
	cfg, installDir := driftTestConfig(t, "dind")
	fakeDocker(t, stoppedHostSocketInspect(installDir))

	running, status, drift, err := ContainerRunnerStatus(context.Background(), cfg, "a", installDir)
	if err != nil {
		t.Fatal(err)
	}
	if running {
		t.Fatal("容器未运行时 running 应为 false")
	}
	if status != StatusInstalled {
		t.Fatalf("status = %v, want installed", status)
	}
	if !strings.Contains(drift, "dind") {
		t.Fatalf("drift = %q, 应指出后端已改为 dind", drift)
	}
}

// TestLockRunnerOps_SerializesSameRunner 同一个 Runner 的启停必须串行：
// 重建会先 docker rm 再 create，两路交叉执行就可能删掉对方刚建好的容器。
func TestLockRunnerOps_SerializesSameRunner(t *testing.T) {
	unlock := lockRunnerOps("github-runner-a")

	acquired := make(chan struct{})
	go func() {
		defer lockRunnerOps("github-runner-a")()
		close(acquired)
	}()

	select {
	case <-acquired:
		t.Fatal("同名 Runner 的第二次操作不该在持锁期间拿到锁")
	case <-time.After(50 * time.Millisecond):
	}

	unlock()
	select {
	case <-acquired:
	case <-time.After(2 * time.Second):
		t.Fatal("释放后第二次操作仍未拿到锁")
	}
}

// TestLockRunnerOps_DifferentRunnersRunInParallel 不同 Runner 之间不该互相排队
func TestLockRunnerOps_DifferentRunnersRunInParallel(t *testing.T) {
	defer lockRunnerOps("github-runner-a")()

	done := make(chan struct{})
	go func() {
		defer lockRunnerOps("github-runner-b")()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("不同 Runner 被无谓地串行了")
	}
}

// TestImageIDCache_StatusPathCachesStartPathDoesNot
// 列表页每个 Runner 都要比一次镜像，缓存省掉重复的 docker image inspect；
// 但启停路径必须读实时值——刚 build 完就点「启动」，读到旧 ID 就不会重建了。
func TestImageIDCache_StatusPathCachesStartPathDoesNot(t *testing.T) {
	logPath := fakeDocker(t, `[]`)
	imageIDCache.Delete("img:cache-test")
	t.Cleanup(func() { imageIDCache.Delete("img:cache-test") })

	countInspects := func() int {
		return strings.Count(dockerCalls(t, logPath), "image inspect -f {{.Id}} img:cache-test")
	}

	cachedImageID(context.Background(), "img:cache-test")
	cachedImageID(context.Background(), "img:cache-test")
	if got := countInspects(); got != 1 {
		t.Fatalf("带缓存的查询执行了 %d 次 docker image inspect，应为 1 次", got)
	}

	resolveImageID(context.Background(), "img:cache-test")
	resolveImageID(context.Background(), "img:cache-test")
	if got := countInspects(); got != 3 {
		t.Fatalf("实时查询应每次都执行，累计应为 3 次，实际 %d 次", got)
	}
}

// TestStartRunnerContainer_RecreatesContainerWithoutAgentToken
// 本特性之前建的容器没有 AGENT_TOKEN，Agent 不鉴权——同网络里的其它容器就能控制这个 Runner。
// 启动时应当自动重建补上，而不是等人手动 docker rm。
func TestStartRunnerContainer_RecreatesContainerWithoutAgentToken(t *testing.T) {
	cfg, installDir := driftTestConfig(t, "host-socket")
	cfg.Runners.DockerGID = 999
	legacy := strings.Replace(stoppedHostSocketInspect(installDir),
		`"AGENT_TOKEN=already-injected"`, `"LANG=C.UTF-8"`, 1)
	logPath := fakeDocker(t, legacy)

	_ = StartRunnerContainer(context.Background(), cfg, "a", installDir)

	calls := dockerCalls(t, logPath)
	if !strings.Contains(calls, "rm -f github-runner-a") || !strings.Contains(calls, "create --name github-runner-a") {
		t.Fatalf("没有 AGENT_TOKEN 的旧容器应被重建，实际调用:\n%s", calls)
	}
	if !strings.Contains(calls, "-e AGENT_TOKEN=") {
		t.Fatalf("重建时应注入 AGENT_TOKEN，实际调用:\n%s", calls)
	}
}

// TestContainerRunnerStatus_RunningPreTokenContainerReportsDrift
// 升级时最要命的一类容器：本特性之前建的，而且**正在运行**。
// 它的目录里没有 .agent_token，而两处自动拉起都只管没在跑的（info.Running 为假才拉），
// 所以谁也不会去生成令牌。早先状态路径直接 ReadAgentToken，读到空就把整条检查跳过——
// 界面上不出「配置已变更」，人不知道该点「重建容器」，容器就一直不鉴权地跑下去。
// 状态路径必须自己把令牌 Ensure 出来，才谈得上发现这件事。
func TestContainerRunnerStatus_RunningPreTokenContainerReportsDrift(t *testing.T) {
	cfg, installDir := driftTestConfig(t, "host-socket")
	cfg.Runners.DockerGID = 999

	// 前提：目录里没有令牌文件，正如升级前的样子
	tokenPath := filepath.Join(installDir, AgentTokenFile)
	if _, err := os.Stat(tokenPath); !os.IsNotExist(err) {
		t.Fatal("用例前提不成立：不该已有令牌文件")
	}

	running := strings.Replace(stoppedHostSocketInspect(installDir),
		`"State": {"Status": "exited", "Running": false}`,
		`"State": {"Status": "running", "Running": true}`, 1)
	// 旧容器没有被注入过 AGENT_TOKEN
	running = strings.Replace(running, `, "AGENT_TOKEN=already-injected"`, "", 1)
	fakeDocker(t, running)

	// Agent 探测必然失败（容器名解析不了），这里只关心 drift 与令牌文件
	_, _, drift, _ := ContainerRunnerStatus(context.Background(), cfg, "a", installDir)

	if !strings.Contains(drift, "agent_token") {
		t.Fatalf("运行中的旧容器缺少 AGENT_TOKEN 未被检出，drift=%q", drift)
	}
	if _, err := os.Stat(tokenPath); err != nil {
		t.Fatalf("状态查询应顺带把令牌生成出来，否则下次还是读到空: %v", err)
	}
}

// TestContainerRunnerStatus_TokenlessDirStaysQuiet
// 反面：令牌实在生成不出来（这里用一个不存在的目录模拟不可写）时要保持沉默。
// 否则「报漂移 → 重建 → 仍然没有令牌可注入 → 再报漂移」会变成每次启动都删容器重建。
func TestContainerRunnerStatus_TokenlessDirStaysQuiet(t *testing.T) {
	cfg, installDir := driftTestConfig(t, "host-socket")
	cfg.Runners.DockerGID = 999
	missingDir := filepath.Join(installDir, "does-not-exist")

	running := strings.Replace(stoppedHostSocketInspect(missingDir),
		`"State": {"Status": "exited", "Running": false}`,
		`"State": {"Status": "running", "Running": true}`, 1)
	running = strings.Replace(running, `, "AGENT_TOKEN=already-injected"`, "", 1)
	fakeDocker(t, running)

	_, _, drift, _ := ContainerRunnerStatus(context.Background(), cfg, "a", missingDir)

	if strings.Contains(drift, "agent_token") {
		t.Fatalf("生成不出令牌时不该报 agent_token 漂移（会变成反复重建），drift=%q", drift)
	}
}
