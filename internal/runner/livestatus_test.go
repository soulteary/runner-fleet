package runner

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/lab-dev/github-actions-runner-manager/internal/config"
)

// fakeAgent 起一个本地 Agent，并把 agentBaseURL 指过去。
// 容器名在测试进程里解析不了，不换基址就只能验到「连不上」那一条路径。
func fakeAgent(t *testing.T, status string, running bool) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(AgentStatus{Status: status, Running: running})
	}))
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	orig := agentBaseURL
	agentBaseURL = func(string, int) string { return "http://" + u.Host }
	t.Cleanup(func() { agentBaseURL = orig })
}

// liveStatusConfig 造一份容器模式配置，并把 runner 目录布置成「已注册」
func liveStatusConfig(t *testing.T) *config.Config {
	t.Helper()
	base := t.TempDir()
	installDir := filepath.Join(base, "droiddesk-2")
	if err := os.MkdirAll(installDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(installDir, ".runner"), []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}
	return &config.Config{Runners: config.RunnersConfig{
		BasePath:         base,
		ContainerMode:    true,
		ContainerImage:   "img:tag",
		ContainerNetwork: "runner-net",
		JobDockerBackend: "host-socket",
		AgentPort:        8081,
		Items:            []config.RunnerItem{{Name: "droiddesk-2", Path: "droiddesk-2"}},
	}}
}

func runningHostSocketInspect(mountSrc string) string {
	return `[{
	  "Image": "sha256:aaa",
	  "State": {"Status": "running", "Running": true},
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

// 这是「每 5 分钟把所有 runner 重新拉起一遍」的根因回归用例。
//
// 容器模式下 Manager 与 Runner 不在同一个 PID namespace，List 扫自己的 /proc
// 必然找不到 Runner 进程，Running 恒为 false，后台巡检的判据
// （Status == installed && !Running）于是每一轮都成立。
// ListWithLiveStatus 必须去问容器内的 Agent 才能得到真话。
func TestListWithLiveStatus_RunningContainerIsNotReportedIdle(t *testing.T) {
	cfg := liveStatusConfig(t)
	installDir := cfg.Runners.Items[0].InstallPath(cfg.Runners.BasePath)
	fakeDocker(t, runningHostSocketInspect(installDir))
	fakeAgent(t, "installed", true)

	// List 是磁盘视角：容器模式下它看不到别的 namespace 里的进程
	if plain := List(cfg); len(plain) != 1 || plain[0].Running {
		t.Fatalf("前提不成立：容器模式下 List 的 Running 应为 false，得到 %+v", plain)
	}

	live := ListWithLiveStatus(context.Background(), cfg)
	if len(live) != 1 {
		t.Fatalf("应返回 1 项，得到 %d", len(live))
	}
	if !live[0].Running {
		t.Fatal("Agent 报告 running=true，ListWithLiveStatus 仍报未运行 —— 定时巡检会把正在跑的 runner 再拉起一次")
	}
	if live[0].Status != StatusInstalled {
		t.Fatalf("状态应为 %q，得到 %q", StatusInstalled, live[0].Status)
	}
}

// Agent 说没在跑，就该被拉起——修复不能把「该拉起」的情况一并堵死
func TestListWithLiveStatus_StoppedRunnerStaysStartable(t *testing.T) {
	cfg := liveStatusConfig(t)
	installDir := cfg.Runners.Items[0].InstallPath(cfg.Runners.BasePath)
	fakeDocker(t, runningHostSocketInspect(installDir))
	fakeAgent(t, "installed", false)

	live := ListWithLiveStatus(context.Background(), cfg)
	if live[0].Running {
		t.Fatal("Agent 报告 running=false，不应判定为运行中")
	}
	if live[0].Status != StatusInstalled {
		t.Fatalf("状态应为 %q（巡检据此拉起），得到 %q", StatusInstalled, live[0].Status)
	}
}

// 探测失败时必须置为 unknown：拉起的前提是「确知它没在跑」。
// Docker 或 Agent 不可达时并不确知，此时反复重启容器只会把事情弄得更糟。
func TestListWithLiveStatus_ProbeFailureIsNotTreatedAsIdle(t *testing.T) {
	cfg := liveStatusConfig(t)
	installDir := cfg.Runners.Items[0].InstallPath(cfg.Runners.BasePath)
	fakeDocker(t, runningHostSocketInspect(installDir))
	// 不起假 Agent：容器在跑但 Agent 连不上

	live := ListWithLiveStatus(context.Background(), cfg)
	if live[0].Status == StatusInstalled {
		t.Fatalf("探测失败时状态不应为 %q，否则巡检会照样去拉起它", StatusInstalled)
	}
	if live[0].Status != StatusUnknown {
		t.Fatalf("探测失败时状态应为 %q，得到 %q", StatusUnknown, live[0].Status)
	}
}

// 容器根本不存在：该拉起，交给 StartIfInstalled 去建
func TestListWithLiveStatus_MissingContainerStaysStartable(t *testing.T) {
	cfg := liveStatusConfig(t)
	fakeDocker(t, `[]`)

	live := ListWithLiveStatus(context.Background(), cfg)
	if live[0].Running {
		t.Fatal("容器不存在时不应判定为运行中")
	}
	if live[0].Status != StatusInstalled {
		t.Fatalf("容器不存在但目录已注册，状态应为 %q，得到 %q", StatusInstalled, live[0].Status)
	}
}

// 默认模式下 List 本身就是本机进程的真实状态，不该再绕一圈去问 docker
func TestListWithLiveStatus_DefaultModePassesThrough(t *testing.T) {
	cfg := liveStatusConfig(t)
	cfg.Runners.ContainerMode = false
	logPath := fakeDocker(t, `[]`)

	live := ListWithLiveStatus(context.Background(), cfg)
	if len(live) != 1 || live[0].Status != StatusInstalled {
		t.Fatalf("默认模式应原样返回 List 的结果，得到 %+v", live)
	}
	if calls := dockerCalls(t, logPath); calls != "" {
		t.Fatalf("默认模式不应调用 docker，实际调用了:\n%s", calls)
	}
}

// liveStatusConfigUnregistered 与 liveStatusConfig 相同，只是不写 .runner：
// 配置里有这个 runner，但它从没被注册过
func liveStatusConfigUnregistered(t *testing.T) *config.Config {
	t.Helper()
	cfg := liveStatusConfig(t)
	installDir := cfg.Runners.Items[0].InstallPath(cfg.Runners.BasePath)
	if err := os.Remove(filepath.Join(installDir, ".runner")); err != nil {
		t.Fatal(err)
	}
	return cfg
}

// 没注册过的 runner 不能被探测结果说成「已注册未运行」。
// 容器不存在时 ContainerRunnerStatus 一律返回 installed，若拿它覆盖磁盘状态，
// startIdleRunners 就会给一个从没配置过的 runner 建容器并发 /start。
func TestListWithLiveStatus_UnregisteredRunnerStaysNew(t *testing.T) {
	cfg := liveStatusConfigUnregistered(t)
	fakeDocker(t, `[]`) // 容器不存在

	live := ListWithLiveStatus(context.Background(), cfg)
	if live[0].Status != StatusNew {
		t.Fatalf("没有 .runner、容器也不存在时应为 %q，得到 %q —— 巡检会据此给它建容器", StatusNew, live[0].Status)
	}
	if live[0].Running {
		t.Fatal("不应判定为运行中")
	}
}

// 容器存在但已停止，目录仍未注册：同样不能说成 installed
func TestListWithLiveStatus_UnregisteredRunnerWithStoppedContainerStaysNew(t *testing.T) {
	cfg := liveStatusConfigUnregistered(t)
	installDir := cfg.Runners.Items[0].InstallPath(cfg.Runners.BasePath)
	fakeDocker(t, stoppedHostSocketInspect(installDir))

	live := ListWithLiveStatus(context.Background(), cfg)
	if live[0].Status != StatusNew {
		t.Fatalf("目录未注册时应为 %q，得到 %q", StatusNew, live[0].Status)
	}
}

// 安装目录整个不存在时更不能说成 installed：
// 巡检据此去建容器，bind mount 的源路径会被 Docker 以 root 属主创建出来，
// 之后 Manager（UID 1001）就写不进去了
func TestListWithLiveStatus_MissingInstallDirStaysMissing(t *testing.T) {
	cfg := liveStatusConfig(t)
	installDir := cfg.Runners.Items[0].InstallPath(cfg.Runners.BasePath)
	if err := os.RemoveAll(installDir); err != nil {
		t.Fatal(err)
	}
	fakeDocker(t, `[]`)

	live := ListWithLiveStatus(context.Background(), cfg)
	if live[0].Status != StatusMissing {
		t.Fatalf("安装目录不存在时应为 %q，得到 %q", StatusMissing, live[0].Status)
	}
}

// 直接钉住 ContainerRunnerStatus 的契约：没有容器可问时回落到磁盘状态，
// 而不是一律说 installed。界面与后台巡检都建立在这个返回值上。
func TestContainerRunnerStatus_FallsBackToDiskStatus(t *testing.T) {
	cfg := liveStatusConfig(t)
	installDir := cfg.Runners.Items[0].InstallPath(cfg.Runners.BasePath)
	fakeDocker(t, `[]`)

	// 有 .runner：容器不存在也仍是「已注册未运行」
	_, status, _, err := ContainerRunnerStatus(context.Background(), cfg, "droiddesk-2", installDir)
	if err != nil {
		t.Fatal(err)
	}
	if status != StatusInstalled {
		t.Fatalf("已注册的目录应为 %q，得到 %q", StatusInstalled, status)
	}

	// 删掉 .runner：不能再说 installed
	if err := os.Remove(filepath.Join(installDir, ".runner")); err != nil {
		t.Fatal(err)
	}
	_, status, _, err = ContainerRunnerStatus(context.Background(), cfg, "droiddesk-2", installDir)
	if err != nil {
		t.Fatal(err)
	}
	if status != StatusNew {
		t.Fatalf("未注册的目录应为 %q，得到 %q", StatusNew, status)
	}
}
