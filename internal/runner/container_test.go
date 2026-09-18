package runner

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/lab-dev/github-actions-runner-manager/internal/config"
)

func TestGetAgentStatus_ErrorBodyIncluded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/status" {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "agent unhealthy", http.StatusBadGateway)
	}))
	defer srv.Close()

	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	host := u.Hostname()
	port := 80
	if u.Port() != "" {
		// httptest 必有端口，这里仅做健壮处理
		if p, convErr := strconv.Atoi(u.Port()); convErr == nil && p > 0 {
			port = p
		}
	}

	_, err = GetAgentStatus(context.Background(), host, port)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "agent unhealthy") {
		t.Fatalf("expected response body in error, got: %v", err)
	}
}

func TestCallAgentStart_ErrorBodyIncluded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/start" {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "start failed in agent", http.StatusConflict)
	}))
	defer srv.Close()

	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	host := u.Hostname()
	port := 80
	if u.Port() != "" {
		if p, convErr := strconv.Atoi(u.Port()); convErr == nil && p > 0 {
			port = p
		}
	}

	err = CallAgentStart(context.Background(), host, port)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "start failed in agent") {
		t.Fatalf("expected response body in error, got: %v", err)
	}
}

func TestGetAgentStatus_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/status" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(AgentStatus{Status: "installed", Running: true})
	}))
	defer srv.Close()

	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	host := u.Hostname()
	port := 80
	if u.Port() != "" {
		if p, convErr := strconv.Atoi(u.Port()); convErr == nil && p > 0 {
			port = p
		}
	}

	st, err := GetAgentStatus(context.Background(), host, port)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if st.Status != "installed" || !st.Running {
		t.Fatalf("unexpected status: %+v", st)
	}
}

// joinArgs 便于断言参数序列中是否出现连续片段
func joinArgs(args []string) string {
	return strings.Join(args, "\x00")
}

func containsSeq(args []string, seq ...string) bool {
	return strings.Contains(joinArgs(args), joinArgs(seq))
}

func TestRunnerCreateArgs_HostSocketAddsDockerGroup(t *testing.T) {
	// host-socket 下容器内 app(UID 1001) 需加入 socket 所属组，否则 Job 中 docker 报 permission denied
	args, err := runnerCreateArgs("github-runner-a", "/host/runners/a", "runner-net", "img:tag", "host-socket", "runner-dind", 999, config.ResourceLimits{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !containsSeq(args, "--group-add", "999") {
		t.Errorf("expected --group-add 999, got %v", args)
	}
	if !containsSeq(args, "-v", HostDockerSocket+":"+HostDockerSocket) {
		t.Errorf("expected docker.sock mount, got %v", args)
	}
	if !containsSeq(args, "-e", "DOCKER_HOST=unix://"+HostDockerSocket) {
		t.Errorf("expected unix DOCKER_HOST, got %v", args)
	}
	if args[len(args)-1] != "img:tag" {
		t.Errorf("image must be the last arg, got %v", args)
	}
}

func TestRunnerCreateArgs_HostSocketUnknownGIDSkipsGroupAdd(t *testing.T) {
	// 探测不到 socket GID 时不追加 --group-add，退回镜像内预置的 docker 组
	args, err := runnerCreateArgs("github-runner-a", "/host/runners/a", "runner-net", "img:tag", "host-socket", "runner-dind", unknownDockerGID, config.ResourceLimits{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, a := range args {
		if a == "--group-add" {
			t.Fatalf("unexpected --group-add with unknown gid: %v", args)
		}
	}
}

func TestRunnerCreateArgs_DindAndNone(t *testing.T) {
	dind, err := runnerCreateArgs("github-runner-a", "/host/runners/a", "runner-net", "img:tag", "dind", "my-dind", 999, config.ResourceLimits{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !containsSeq(dind, "-e", "DOCKER_HOST=tcp://my-dind:2375") {
		t.Errorf("expected dind DOCKER_HOST, got %v", dind)
	}
	if strings.Contains(joinArgs(dind), "--group-add") || strings.Contains(joinArgs(dind), HostDockerSocket) {
		t.Errorf("dind must not mount socket nor add docker group, got %v", dind)
	}

	none, err := runnerCreateArgs("github-runner-a", "/host/runners/a", "runner-net", "img:tag", "none", "runner-dind", 999, config.ResourceLimits{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(joinArgs(none), "DOCKER_HOST") || strings.Contains(joinArgs(none), "--group-add") {
		t.Errorf("none backend must not inject docker env/group, got %v", none)
	}
}

func TestRunnerCreateArgs_UnsupportedBackend(t *testing.T) {
	if _, err := runnerCreateArgs("github-runner-a", "/host/runners/a", "runner-net", "img:tag", "podman", "runner-dind", 999, config.ResourceLimits{}); err == nil {
		t.Fatal("expected error for unsupported backend")
	} else if !strings.Contains(err.Error(), "job_docker_backend") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestSocketGID(t *testing.T) {
	// 存在的文件返回其所属组；不存在时返回 unknownDockerGID
	f := filepath.Join(t.TempDir(), "docker.sock")
	if err := os.WriteFile(f, []byte(""), 0660); err != nil {
		t.Fatal(err)
	}
	if got, want := socketGID(f), os.Getgid(); got != want {
		t.Errorf("socketGID = %d, want %d", got, want)
	}
	if got := socketGID(filepath.Join(t.TempDir(), "missing.sock")); got != unknownDockerGID {
		t.Errorf("socketGID(missing) = %d, want %d", got, unknownDockerGID)
	}
}

func TestRunnerDockerGID_ConfigWinsOverDetection(t *testing.T) {
	cfg := &config.Config{}
	cfg.Runners.DockerGID = 1234
	if got := runnerDockerGID(cfg); got != 1234 {
		t.Errorf("runnerDockerGID = %d, want 1234 from config", got)
	}
	// 未配置时退回探测 docker.sock（宿主机上可能不存在，此时为 unknownDockerGID）
	cfg.Runners.DockerGID = 0
	if got, want := runnerDockerGID(cfg), socketGID(HostDockerSocket); got != want {
		t.Errorf("runnerDockerGID = %d, want detected %d", got, want)
	}
}

func TestRunnerCreateArgs_ResourceLimits(t *testing.T) {
	limits := config.ResourceLimits{CPUs: "2", Memory: "4g", MemorySwap: "4g", PidsLimit: 512}
	args, err := runnerCreateArgs("github-runner-a", "/host/runners/a", "runner-net", "img:tag", "none", "runner-dind", unknownDockerGID, limits)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range [][]string{
		{"--cpus", "2"}, {"--memory", "4g"}, {"--memory-swap", "4g"}, {"--pids-limit", "512"},
	} {
		if !containsSeq(args, want...) {
			t.Errorf("缺少参数 %v，实际: %v", want, args)
		}
	}
	if args[len(args)-1] != "img:tag" {
		t.Errorf("镜像必须在参数末尾: %v", args)
	}
}

func TestRunnerCreateArgs_NoLimitsKeepsOldBehaviour(t *testing.T) {
	// 未配置资源上限时不应产生任何限制参数，保持与旧版本一致
	args, err := runnerCreateArgs("github-runner-a", "/host/runners/a", "runner-net", "img:tag", "dind", "runner-dind", unknownDockerGID, config.ResourceLimits{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, flag := range []string{"--cpus", "--memory", "--memory-swap", "--pids-limit"} {
		if strings.Contains(joinArgs(args), flag) {
			t.Errorf("不应出现 %s: %v", flag, args)
		}
	}
}
