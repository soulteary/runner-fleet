//go:build !windows

package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/soulteary/runner-fleet/internal/runner"
)

type agentRedirectTransport struct {
	target    *url.URL
	transport *http.Transport
}

func (t agentRedirectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	u := *req.URL
	clone.URL = &u
	clone.URL.Scheme = t.target.Scheme
	clone.URL.Host = t.target.Host
	return t.transport.RoundTrip(clone)
}

func TestContainerAPIUsesAgentStatusAndPreservesProbeFailures(t *testing.T) {
	cfg := behaviorConfig(t)
	cfg.Runners.ContainerMode = true
	cfg.Runners.ContainerImage = "fixture:latest"
	cfg.Runners.ContainerNetwork = "fixture-net"
	cfg.Runners.JobDockerBackend = "none"
	if err := cfg.Save(ConfigPath); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(cfg.Runners.BasePath, "demo")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".runner"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, runner.AgentTokenFile), []byte("fixture-token"), 0600); err != nil {
		t.Fatal(err)
	}
	facts := []any{map[string]any{"Image": "sha256:fixture", "State": map[string]any{"Running": true, "Status": "running"}, "Config": map[string]any{"Image": "fixture:latest", "Env": []string{"AGENT_TOKEN=fixture-token"}, "Labels": map[string]string{"io.runner-fleet.job-docker-backend": "none", "io.runner-fleet.network": "fixture-net"}}, "HostConfig": map[string]any{"Binds": []string{dir + ":/runner"}, "NetworkMode": "fixture-net"}}}
	data, err := json.Marshal(facts)
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	script := "#!/bin/sh\ncase \"$1\" in\ninspect) cat <<'JSON'\n" + string(data) + "\nJSON\n;;\nimage) echo sha256:fixture;;\nesac\n"
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("DOCKER_HOST", "")
	var statusCode atomic.Int64
	statusCode.Store(200)
	var running atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture-token" {
			w.WriteHeader(401)
			return
		}
		if r.URL.Path == "/status" {
			w.WriteHeader(int(statusCode.Load()))
			if statusCode.Load() == 200 {
				_ = json.NewEncoder(w).Encode(map[string]any{"status": "installed", "running": running.Load()})
			}
			return
		}
		w.WriteHeader(200)
	}))
	defer server.Close()
	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	original := http.DefaultTransport
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	defer transport.CloseIdleConnections()
	http.DefaultTransport = agentRedirectTransport{target: target, transport: transport}
	t.Cleanup(func() { http.DefaultTransport = original })
	for _, failed := range []bool{false, true} {
		statusCode.Store(200)
		if failed {
			statusCode.Store(503)
		}
		for _, fn := range []func(){
			func() {
				rec := behaviorCall(ListRunners, "", "")
				if rec.Code != 200 {
					t.Fatal(rec.Code)
				}
			},
			func() {
				rec := behaviorCall(GetRunner, "demo", "")
				if rec.Code != 200 {
					t.Fatal(rec.Code)
				}
			},
			func() {
				rec := behaviorCall(StartRunner, "demo", "")
				if rec.Code != 200 {
					t.Fatalf("start: %d %s", rec.Code, rec.Body.String())
				}
			},
		} {
			fn()
		}
		running.Store(true)
		rec := behaviorCall(StopRunner, "demo", "")
		if rec.Code != 200 {
			t.Fatalf("stop: %d %s", rec.Code, rec.Body.String())
		}
		running.Store(false)
	}
	statusCode.Store(200)
	rec := behaviorCall(RecreateRunner, "absent", "")
	if rec.Code != 404 {
		t.Fatal(rec.Code)
	}
	if err := os.Remove(filepath.Join(dir, ".runner")); err != nil {
		t.Fatal(err)
	}
	rec = behaviorCall(RecreateRunner, "demo", "")
	if rec.Code != 400 {
		t.Fatal(rec.Code)
	}
	if err := os.WriteFile(filepath.Join(dir, ".runner"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	rec = behaviorCall(RecreateRunner, "demo", "")
	if rec.Code != 200 {
		t.Fatalf("recreate: %d %s", rec.Code, rec.Body.String())
	}
	// A real storage record error stops recreation before Docker changes.
	if err := os.WriteFile(filepath.Join(dir, runner.AgentTokenFile), nil, 0600); err != nil {
		t.Fatal(err)
	}
	rec = behaviorCall(RecreateRunner, "demo", "")
	if rec.Code != 500 {
		t.Fatalf("recreate credential failure: %d %s", rec.Code, rec.Body.String())
	}
}
