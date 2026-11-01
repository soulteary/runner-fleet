package runner

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/soulteary/runner-fleet/internal/config"
)

func TestContainerStartStopsAtDockerFailureBoundaries(t *testing.T) {
	t.Setenv("DOCKER_HOST", "")
	for _, stage := range []string{"inspect", "forced-remove", "drift-remove", "start", "create", "new-start", "invalid-backend", "manager-mount"} {
		t.Run(stage, func(t *testing.T) {
			cfg, dir := driftTestConfig(t, "host-socket")
			cfg.Runners.DockerGID = 999
			if err := os.WriteFile(filepath.Join(dir, AgentTokenFile), []byte("fixture-token"), 0600); err != nil {
				t.Fatal(err)
			}
			facts := stoppedHostSocketInspect(dir)
			facts = strings.ReplaceAll(facts, "already-injected", "fixture-token")
			if stage == "drift-remove" {
				cfg.Runners.ContainerImage = "changed:image"
			}
			missing := stage == "create" || stage == "new-start" || stage == "invalid-backend" || stage == "manager-mount"
			body := "case \"$1\" in\ninspect) "
			switch {
			case stage == "inspect":
				body += "echo 'permission denied' >&2; exit 1"
			case missing:
				body += "echo 'No such object' >&2; exit 1"
			default:
				body += "cat <<'JSON'\n" + facts + "\nJSON\n"
			}
			body += ";;\nimage) echo sha256:aaa;;\n"
			switch stage {
			case "forced-remove", "drift-remove":
				body += "rm) echo 'removal failed' >&2; exit 1;;\n"
			case "start", "new-start":
				body += "start) echo 'startup failed' >&2; exit 1;;\n"
			case "create":
				body += "create) echo 'creation failed' >&2; exit 1;;\n"
			}
			body += "esac\nexit 0"
			calls := fakeDockerProgram(t, body)
			if stage == "invalid-backend" {
				cfg.Runners.JobDockerBackend = "invalid"
			}
			if stage == "manager-mount" {
				cfg.Runners.BasePath = "/app/runners"
			}
			err := startRunnerContainer(context.Background(), cfg, "a", dir, stage == "forced-remove")
			if err == nil {
				t.Fatal("failed Docker operation reported success")
			}
			got := readCalls(t, calls)
			if (stage == "inspect" || stage == "forced-remove" || stage == "drift-remove" || stage == "invalid-backend" || stage == "manager-mount") && strings.Contains(got, "create --") {
				t.Fatalf("created after failed prerequisite: %s", got)
			}
			if got := ReadAgentToken(dir); got != "fixture-token" {
				t.Fatal("failed operation changed identity")
			}
		})
	}
	cfg, dir := driftTestConfig(t, "none")
	t.Setenv("DOCKER_HOST", "tcp://runner-dind:2375")
	if err := StartRunnerContainer(context.Background(), cfg, "a", dir); err == nil || !strings.Contains(err.Error(), "host Docker") {
		t.Fatalf("DinD manager incorrectly accepted: %v", err)
	}
}

func TestStoppedContainerStartAndResourceUpdateFailure(t *testing.T) {
	cfg, dir := driftTestConfig(t, "host-socket")
	cfg.Runners.DockerGID = 999
	cfg.Runners.Resources = config.ResourceLimits{CPUs: "1"}
	if err := os.WriteFile(filepath.Join(dir, AgentTokenFile), []byte("fixture-token"), 0600); err != nil {
		t.Fatal(err)
	}
	facts := strings.ReplaceAll(stoppedHostSocketInspect(dir), "already-injected", "fixture-token")
	fakeDocker(t, facts)
	// The optional docker update failure is logged, while the required Agent
	// acknowledgement remains observable to the caller.
	for _, output := range []string{"", "update refused"} {
		body := "case \"$1\" in\ninspect) cat <<'JSON'\n" + facts + "\nJSON\n;;\nimage) echo sha256:aaa;;\nupdate) printf '%s' '" + output + "'; exit 1;;\nesac\nexit 0"
		calls := fakeDockerProgram(t, body)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
		original := agentBaseURL
		agentBaseURL = func(string, int) string { return server.URL }
		err := StartRunnerContainer(context.Background(), cfg, "a", dir)
		agentBaseURL = original
		server.Close()
		if err == nil || !strings.Contains(err.Error(), "503") {
			t.Fatalf("lost Agent failure after start: %v", err)
		}
		if !strings.Contains(readCalls(t, calls), "update --cpus 1") {
			t.Fatal("existing limits were not applied")
		}
	}
}

func TestContainerStatusDistinguishesAgentDiskStatesAndTransportFailures(t *testing.T) {
	cfg, dir := driftTestConfig(t, "none")
	facts := strings.ReplaceAll(stoppedHostSocketInspect(dir), `"Running": false`, `"Running": true`)
	fakeDocker(t, facts)
	original := agentBaseURL
	t.Cleanup(func() { agentBaseURL = original })
	for _, diskState := range []string{"new", "missing", "unexpected"} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"status":"` + diskState + `","running":false}`))
		}))
		agentBaseURL = func(string, int) string { return server.URL }
		running, status, _, err := ContainerRunnerStatus(context.Background(), cfg, "a", dir)
		server.Close()
		if err != nil || running {
			t.Fatalf("invalid Agent status: %v %v", running, err)
		}
		want := StatusMissing
		if diskState == "new" {
			want = StatusNew
		}
		if diskState == "missing" {
			want = StatusMissing
		}
		if status != want {
			t.Fatalf("%s became %s", diskState, status)
		}
	}
	agentBaseURL = func(string, int) string { return "http://127.0.0.1:1" }
	_, _, _, err := ContainerRunnerStatus(context.Background(), cfg, "a", dir)
	var probe *ProbeError
	if !errors.As(err, &probe) || probe.Type != ProbeErrorTypeAgentConnect {
		t.Fatalf("connection failure misclassified: %v", err)
	}
	fakeDockerProgram(t, "echo 'permission denied' >&2; exit 1")
	_, _, _, err = ContainerRunnerStatus(context.Background(), cfg, "a", dir)
	if !errors.As(err, &probe) || probe.Type != ProbeErrorTypeDockerAccess {
		t.Fatalf("Docker failure misclassified: %v", err)
	}
}
