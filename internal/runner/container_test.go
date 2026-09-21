package runner

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/soulteary/runner-fleet/internal/config"
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

	_, err = GetAgentStatus(context.Background(), host, port, "")
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

	err = CallAgentStart(context.Background(), host, port, "")
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

	st, err := GetAgentStatus(context.Background(), host, port, "")
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
	args, err := runnerCreateArgs("github-runner-a", "/host/runners/a", "runner-net", "img:tag", "host-socket", "runner-dind", 999, config.ResourceLimits{}, "")
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
	args, err := runnerCreateArgs("github-runner-a", "/host/runners/a", "runner-net", "img:tag", "host-socket", "runner-dind", unknownDockerGID, config.ResourceLimits{}, "")
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
	dind, err := runnerCreateArgs("github-runner-a", "/host/runners/a", "runner-net", "img:tag", "dind", "my-dind", 999, config.ResourceLimits{}, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !containsSeq(dind, "-e", "DOCKER_HOST=tcp://my-dind:2375") {
		t.Errorf("expected dind DOCKER_HOST, got %v", dind)
	}
	if strings.Contains(joinArgs(dind), "--group-add") || strings.Contains(joinArgs(dind), HostDockerSocket) {
		t.Errorf("dind must not mount socket nor add docker group, got %v", dind)
	}

	none, err := runnerCreateArgs("github-runner-a", "/host/runners/a", "runner-net", "img:tag", "none", "runner-dind", 999, config.ResourceLimits{}, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(joinArgs(none), "DOCKER_HOST") || strings.Contains(joinArgs(none), "--group-add") {
		t.Errorf("none backend must not inject docker env/group, got %v", none)
	}
}

func TestRunnerCreateArgs_UnsupportedBackend(t *testing.T) {
	if _, err := runnerCreateArgs("github-runner-a", "/host/runners/a", "runner-net", "img:tag", "podman", "runner-dind", 999, config.ResourceLimits{}, ""); err == nil {
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
	args, err := runnerCreateArgs("github-runner-a", "/host/runners/a", "runner-net", "img:tag", "none", "runner-dind", unknownDockerGID, limits, "")
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
	args, err := runnerCreateArgs("github-runner-a", "/host/runners/a", "runner-net", "img:tag", "dind", "runner-dind", unknownDockerGID, config.ResourceLimits{}, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, flag := range []string{"--cpus", "--memory", "--memory-swap", "--pids-limit"} {
		if strings.Contains(joinArgs(args), flag) {
			t.Errorf("不应出现 %s: %v", flag, args)
		}
	}
}

func TestResourceUpdateArgs(t *testing.T) {
	// 未配置上限时不应执行 docker update，保持旧行为
	if got := resourceUpdateArgs("github-runner-a", config.ResourceLimits{}); got != nil {
		t.Errorf("未配置上限时应返回 nil，实际: %v", got)
	}
	got := resourceUpdateArgs("github-runner-a", config.ResourceLimits{CPUs: "2", Memory: "4g", MemorySwap: "4g", PidsLimit: 512})
	want := "update --cpus 2 --memory 4g --memory-swap 4g --pids-limit 512 github-runner-a"
	if strings.Join(got, " ") != want {
		t.Errorf("resourceUpdateArgs = %q\nwant %q", strings.Join(got, " "), want)
	}
	if got[len(got)-1] != "github-runner-a" {
		t.Errorf("容器名必须在参数末尾: %v", got)
	}
}

func TestEnsureAgentToken_GeneratesAndReuses(t *testing.T) {
	dir := t.TempDir()
	first, err := EnsureAgentToken(dir)
	if err != nil {
		t.Fatalf("生成令牌失败: %v", err)
	}
	if len(first) != agentTokenBytes*2 {
		t.Errorf("令牌长度应为 %d 个 hex 字符，实际 %d: %q", agentTokenBytes*2, len(first), first)
	}
	// 权限必须是 0600：该文件与 Job 共享同一挂载目录
	info, err := os.Stat(filepath.Join(dir, AgentTokenFile))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("令牌文件权限应为 0600，实际 %v", perm)
	}
	// 再次调用必须复用同一令牌，否则 Manager 重启后会和运行中的 Agent 对不上
	second, err := EnsureAgentToken(dir)
	if err != nil {
		t.Fatal(err)
	}
	if second != first {
		t.Errorf("重复调用应复用令牌: %q vs %q", first, second)
	}
	if got := ReadAgentToken(dir); got != first {
		t.Errorf("ReadAgentToken = %q, want %q", got, first)
	}
}

func TestEnsureAgentToken_DistinctPerRunner(t *testing.T) {
	a, err := EnsureAgentToken(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	b, err := EnsureAgentToken(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Error("不同 Runner 应有不同令牌，否则一个 Job 泄露的令牌能控制其它 Runner")
	}
}

func TestReadAgentToken_MissingIsEmpty(t *testing.T) {
	// 旧容器没有令牌文件，必须返回空串以降级为不鉴权，而不是报错
	if got := ReadAgentToken(t.TempDir()); got != "" {
		t.Errorf("无令牌文件时应返回空串，实际 %q", got)
	}
}

func TestSetAgentAuth(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "http://example/status", nil)
	if err != nil {
		t.Fatal(err)
	}
	setAgentAuth(req, "")
	if _, ok := req.Header["Authorization"]; ok {
		t.Error("令牌为空时不应设置 Authorization 头")
	}
	setAgentAuth(req, "abc123")
	if got := req.Header.Get("Authorization"); got != "Bearer abc123" {
		t.Errorf("Authorization = %q", got)
	}
}

func TestAgentCalls_SendBearerToken(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"installed","running":true}`))
	}))
	defer srv.Close()
	host, portStr, err := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := GetAgentStatus(context.Background(), host, port, "tok-status"); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer tok-status" {
		t.Errorf("/status 未带令牌: %q", gotAuth)
	}
	if err := CallAgentStart(context.Background(), host, port, "tok-start"); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer tok-start" {
		t.Errorf("/start 未带令牌: %q", gotAuth)
	}
}

func TestRunnerCreateArgs_InjectsAgentToken(t *testing.T) {
	// 令牌必须通过环境变量注入：只靠挂载的 0600 文件时，Manager 与 Agent 的 UID
	// 不一致就会读不到，Agent 静默降级为不鉴权
	args, err := runnerCreateArgs("github-runner-a", "/host/runners/a", "runner-net", "img:tag", "none", "runner-dind", unknownDockerGID, config.ResourceLimits{}, "tok-abc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !containsSeq(args, "-e", "AGENT_TOKEN=tok-abc") {
		t.Errorf("未注入 AGENT_TOKEN: %v", args)
	}
	if args[len(args)-1] != "img:tag" {
		t.Errorf("镜像必须在参数末尾: %v", args)
	}
}

func TestRunnerCreateArgs_NoTokenNoEnv(t *testing.T) {
	// 拿不到令牌时不应注入空值，避免 Agent 把空串当成有效令牌
	args, err := runnerCreateArgs("github-runner-a", "/host/runners/a", "runner-net", "img:tag", "none", "runner-dind", unknownDockerGID, config.ResourceLimits{}, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(joinArgs(args), "AGENT_TOKEN") {
		t.Errorf("令牌为空时不应注入环境变量: %v", args)
	}
}
