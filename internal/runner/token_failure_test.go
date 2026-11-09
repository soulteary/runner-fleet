//go:build !windows

package runner

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/soulteary/runner-fleet/internal/config"
)

func TestStartContainerTokenFailureHasNoDockerSideEffects(t *testing.T) {
	t.Setenv("DOCKER_HOST", "")
	dir := t.TempDir()
	path := filepath.Join(dir, AgentTokenFile)
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	calls := fakeDockerProgram(t, "exit 0")
	cfg := &config.Config{}
	for _, force := range []bool{false, true} {
		if err := startRunnerContainer(context.Background(), cfg, "demo", dir, force); err == nil {
			t.Fatal("token failure must stop container operations")
		}
	}
	if got := readCalls(t, calls); got != "" {
		t.Fatalf("docker called despite token failure: %s", got)
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) != 0 {
		t.Fatalf("existing record changed: %q, %v", data, err)
	}
}

func TestContainerStatusTokenFailureDoesNotCallAgent(t *testing.T) {
	cfg, dir := driftTestConfig(t, "host-socket")
	if err := os.WriteFile(filepath.Join(dir, AgentTokenFile), nil, 0600); err != nil {
		t.Fatal(err)
	}
	facts := stoppedHostSocketInspect(dir)
	for _, running := range []bool{false, true} {
		t.Run(fmt.Sprint(running), func(t *testing.T) {
			snapshot := facts
			if running {
				snapshot = strings.ReplaceAll(snapshot, `"Running": false`, `"Running": true`)
			}
			fakeDocker(t, snapshot)
			called := false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true; w.WriteHeader(200) }))
			defer server.Close()
			original := agentBaseURL
			agentBaseURL = func(string, int) string { return server.URL }
			defer func() { agentBaseURL = original }()
			gotRunning, status, _, err := ContainerRunnerStatus(context.Background(), cfg, "a", dir)
			if err == nil || status != StatusUnknown || gotRunning != running || called {
				t.Fatalf("running=%v status=%v error=%v agentCalled=%v", gotRunning, status, err, called)
			}
		})
	}
}
