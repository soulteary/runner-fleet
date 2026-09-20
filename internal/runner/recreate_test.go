package runner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	  "Config": {"Image": "img:tag", "Env": ["DOCKER_HOST=unix:///var/run/docker.sock"]},
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
