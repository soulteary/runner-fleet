//go:build !windows

package runner

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/soulteary/runner-fleet/internal/config"
)

func TestProbeErrorPreservesCauseAndRecoveryAdvice(t *testing.T) {
	cause := errors.New("transport failed")
	pe := &ProbeError{Type: ProbeErrorTypeAgentConnect, Err: cause}
	wrapped := fmt.Errorf("status unavailable: %w", pe)
	if !errors.Is(wrapped, cause) || pe.Error() != cause.Error() || DetectProbeErrorType(wrapped) != ProbeErrorTypeAgentConnect {
		t.Fatalf("lost cause: %v", wrapped)
	}
	var missing *ProbeError
	if missing.Error() != "" || missing.Unwrap() != nil || (&ProbeError{}).Error() != "" {
		t.Fatal("nil errors must remain safe")
	}
	if newProbeError(ProbeErrorTypeUnknown, nil) != nil {
		t.Fatal("created error from nil cause")
	}
	for _, tc := range []struct {
		err        error
		want       ProbeErrorType
		check, fix string
	}{
		{nil, ProbeErrorTypeUnknown, "docker compose ps", "docker compose"},
		{errors.New("agent returned 503"), ProbeErrorTypeAgentHTTP, "docker logs", "docker restart"},
		{errors.New("connect: refused"), ProbeErrorTypeAgentConnect, "network inspect", "docker restart runner-manager"},
		{errors.New("unexpected reply"), ProbeErrorTypeUnknown, "docker compose ps", "docker compose"},
		{errors.New("socket unavailable"), ProbeErrorTypeDockerAccess, "docker info", "DOCKER_GID"},
	} {
		got := DetectProbeErrorType(tc.err)
		if got != tc.want || ProbeSuggestion(got) == "" || !strings.Contains(ProbeCheckCommand(got), tc.check) || !strings.Contains(ProbeFixCommand(got), tc.fix) {
			t.Fatalf("wrong recovery classification: %v -> %s", tc.err, got)
		}
	}
}

func TestPersistedStatusRejectsCorruptRecordsAndKeepsValidDetails(t *testing.T) {
	dir := t.TempDir()
	for _, record := range []string{RegistrationResultFile, GitHubStatusFile} {
		if err := os.WriteFile(filepath.Join(dir, record), []byte("{"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	msg, at := readRegistrationResult(dir)
	if msg != "" || at != "" || ReadGitHubStatus(dir).Registered != nil {
		t.Fatal("corrupt record became a valid status")
	}
	if err := os.WriteFile(filepath.Join(dir, RegistrationResultFile), []byte(`{"success":false,"message":"registration refused","at":"2026-11-02T00:00:00Z"}`), 0600); err != nil {
		t.Fatal(err)
	}
	msg, at = readRegistrationResult(dir)
	if msg != "registration refused" || at != "2026-11-02T00:00:00Z" {
		t.Fatalf("lost result %q %q", msg, at)
	}
	cfg := &config.Config{Runners: config.RunnersConfig{BasePath: dir, ContainerMode: true, JobDockerBackend: "none", Items: []config.RunnerItem{{Name: "demo", TargetType: "repo", Target: "o/r"}}}}
	info := GetByName(cfg, "demo")
	if info == nil || info.JobDockerBackend != "none" {
		t.Fatal(info)
	}
	if err := StartIfInstalled(context.Background(), nil, "demo", dir); err == nil {
		t.Fatal("nil config accepted")
	}
	if err := StartIfInstalled(context.Background(), &config.Config{}, "demo", dir); err == nil {
		t.Fatal("missing script accepted")
	}
	if err := os.WriteFile(filepath.Join(dir, RunScriptName()), []byte("not executable"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Start(dir); err == nil {
		t.Fatal("non-executable script started")
	}
	cfg.Runners.ContainerMode = true
	if err := os.WriteFile(filepath.Join(dir, AgentTokenFile), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := StartIfInstalled(context.Background(), cfg, "demo", dir); err == nil {
		t.Fatal("container start ignored credential failure")
	}
}

func TestAgentResponseFailuresRemainErrors(t *testing.T) {
	original := agentBaseURL
	t.Cleanup(func() { agentBaseURL = original })
	for _, tc := range []struct {
		name, body string
		status     int
	}{{"empty failure", "", 503}, {"invalid JSON", "{", 200}} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			agentBaseURL = func(string, int) string { return server.URL }
			if _, err := GetAgentStatus(context.Background(), "demo", 0, "token"); err == nil {
				t.Fatal("invalid status response accepted")
			}
			if tc.status != 200 {
				if err := CallAgentStart(context.Background(), "demo", 0, "token"); err == nil || !strings.Contains(err.Error(), "503") {
					t.Fatalf("start response: %v", err)
				}
			}
		})
	}
	agentBaseURL = func(string, int) string { return ":bad-url" }
	if _, err := GetAgentStatus(context.Background(), "demo", 0, ""); err == nil {
		t.Fatal("invalid URL accepted")
	}
	if err := CallAgentStart(context.Background(), "demo", 0, ""); err == nil {
		t.Fatal("invalid URL accepted")
	}
}

func TestLegacyDockerBackendDriftRequiresRecreation(t *testing.T) {
	for _, tc := range []struct {
		backend, host string
		socket        bool
		want          string
	}{
		{"none", "unix://" + HostDockerSocket, false, "job_docker_backend"},
		{"none", "", false, ""},
		{"host-socket", "", false, "job_docker_backend"},
		{"dind", "unix://" + HostDockerSocket, true, "job_docker_backend"},
		{"dind", "tcp://expected:2375", false, ""},
		{"dind", "tcp://old:2375", false, "dind_host"},
		{"dind", "", false, "job_docker_backend"},
		{"other", "", false, ""},
	} {
		s := containerSpec{JobBackend: tc.backend, DindHost: "expected"}
		facts := &containerFacts{}
		facts.Env = []string{"DOCKER_HOST=" + tc.host}
		if tc.socket {
			facts.Binds = []string{HostDockerSocket + ":" + HostDockerSocket}
		}
		got := s.legacyBackendDrift(facts)
		if tc.want == "" && got != "" || tc.want != "" && !strings.Contains(got, tc.want) {
			t.Fatalf("%+v -> %q", tc, got)
		}
	}
}

func TestPreflightReportsImageFailureAndLocalSocketBoundary(t *testing.T) {
	fakeDockerProgram(t, "printf 'cannot start image\\nmore detail\\n'; exit 1")
	result := checkRunnerImageTools(context.Background(), "missing:tag")
	if result.Level != CheckWarn {
		t.Fatalf("image failure was not reported: %+v", result)
	}
	if !strings.Contains(result.Message, "cannot start image") {
		t.Fatalf("missing actionable image failure: %+v", result)
	}
	dir := t.TempDir()
	socket := filepath.Join(dir, "docker.sock")
	if err := os.WriteFile(socket, nil, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DOCKER_HOST", "unix://"+socket)
	result = checkDefaultModeDocker(context.Background())
	if result.Level != "ok" {
		t.Fatalf("accessible socket group misclassified: %+v", result)
	}
	if firstLine(nil, errors.New("fallback")) != "fallback" || firstLine([]byte("line one\nline two"), nil) != "line one" {
		t.Fatal("unexpected first-line diagnostics")
	}
}

func TestPreflightChecksRealDinDEndpointAndMissingImageTools(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:2375")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	cfg := &config.Config{Runners: config.RunnersConfig{JobDockerBackend: "dind", DindHost: "127.0.0.1"}}
	if got := checkJobDockerBackend(context.Background(), cfg); got.Level != CheckOK {
		t.Fatalf("reachable DinD rejected: %+v", got)
	}
	cfg.Runners.DindHost = ""
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := checkJobDockerBackend(ctx, cfg); got.Level != CheckWarn {
		t.Fatalf("unreachable default DinD accepted: %+v", got)
	}
	fakeDockerProgram(t, "echo '"+missingToolMarker+"git'")
	if got := checkRunnerImageTools(context.Background(), "fixture"); got.Level != CheckWarn || !strings.Contains(got.Message, "git") {
		t.Fatalf("missing tool not reported: %+v", got)
	}
}

func TestDockerReachabilityDiagnosticsDoNotDependOnHostDaemon(t *testing.T) {
	for _, output := range []string{"", "daemon access refused"} {
		fakeDockerProgram(t, "printf '%s' '"+output+"'; exit 1")
		got := checkDockerReachable(context.Background())
		if got.Level != CheckError || got.Hint != dockerAccessHint || !strings.Contains(got.Message, "cannot reach the Docker daemon") {
			t.Fatalf("unreachable daemon incorrectly classified: %+v", got)
		}
		if output != "" && !strings.Contains(got.Message, output) {
			t.Fatalf("diagnostic lost: %+v", got)
		}
	}
	fakeDockerProgram(t, "echo 29.0.0")
	if got := checkDockerReachable(context.Background()); got.Level != CheckOK || !strings.Contains(got.Message, "29.0.0") {
		t.Fatalf("reachable daemon incorrectly classified: %+v", got)
	}
}
