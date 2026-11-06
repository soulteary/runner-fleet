//go:build !windows

package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/soulteary/runner-fleet/internal/config"
)

func TestSuggestedNamesRemainUniqueAfterSequentialCandidatesAreTaken(t *testing.T) {
	cfg := behaviorConfig(t)
	for i := 2; i <= 20; i++ {
		cfg.Runners.Items = append(cfg.Runners.Items, config.RunnerItem{Name: fmt.Sprintf("demo-%d", i), TargetType: "repo", Target: "o/r"})
	}
	suggested := suggestRunnerName(cfg, "demo", "", nil, false)
	if !strings.HasPrefix(suggested, "demo-") || len(suggested) != len("demo-")+shortRandomSuffixLen || hasErrorConflict(collectRunnerConflicts(cfg, suggested, "", nil, false)) {
		t.Fatalf("invalid random fallback: %q", suggested)
	}
	if got := collectRunnerConflicts(nil, "demo", "", nil, false); len(got) != 0 {
		t.Fatal(got)
	}
	if got := collectRunnerConflicts(cfg, "", "", nil, false); len(got) != 0 {
		t.Fatal(got)
	}
	if os.Geteuid() != 0 {
		dir := t.TempDir()
		if err := os.Chmod(dir, 0000); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(dir, 0700) })
		if got := inspectInstallDir(dir); got != "" {
			t.Fatalf("unreadable directory classified %q", got)
		}
	}
}

func TestPrecheckDockerFailuresAndConflictsAreExplicit(t *testing.T) {
	cfg := behaviorConfig(t)
	cfg.Runners.ContainerMode = true
	cfg.Runners.JobDockerBackend = "none"
	if err := cfg.Save(ConfigPath); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	docker := filepath.Join(bin, "docker")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, script := range []string{"#!/bin/sh\necho 'No such object' >&2\nexit 1\n", "#!/bin/sh\necho 'permission denied' >&2\nexit 1\n", "#!/bin/sh\necho running\n"} {
		if err := os.WriteFile(docker, []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
		lookup := dockerContainerLookup(context.Background())
		_, _, _ = lookup("github-runner-unknown")
		for _, name := range []string{"unique", "demo"} {
			e := echo.New()
			rec := httptest.NewRecorder()
			c := e.NewContext(httptest.NewRequest(http.MethodGet, "/?name="+name, nil), rec)
			if err := PrecheckRunner(c); err != nil {
				t.Fatal(err)
			}
			var response PrecheckResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if rec.Code != 200 || response.ContainerName == "" {
				t.Fatalf("missing container diagnostics: %s", rec.Body.String())
			}
			if name == "demo" && response.Available {
				t.Fatal("taken name reported available")
			}
		}
	}
	for _, query := range []string{"", "?name=demo&path=.."} {
		e := echo.New()
		rec := httptest.NewRecorder()
		c := e.NewContext(httptest.NewRequest(http.MethodGet, "/"+query, nil), rec)
		err := PrecheckRunner(c)
		if err == nil {
			t.Fatal("invalid precheck accepted")
		}
		he, ok := err.(*echo.HTTPError)
		if !ok || he.Code != 400 {
			t.Fatal(err)
		}
	}
	rec := behaviorCall(AddRunner, "", `{"name":"demo","path":"fixed","target_type":"repo","target":"o/r","auto_rename":true}`)
	if rec.Code != 409 {
		t.Fatalf("fixed-path rename should fail: %d", rec.Code)
	}
	rec = behaviorCall(AddRunner, "", `{"name":"demo","target_type":"repo","target":"o/r"}`)
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), "container_name") {
		t.Fatalf("missing container conflict detail: %d %s", rec.Code, rec.Body.String())
	}
	if err := os.WriteFile(ConfigPath, []byte("invalid: ["), 0600); err != nil {
		t.Fatal(err)
	}
	e := echo.New()
	c := e.NewContext(httptest.NewRequest(http.MethodGet, "/?name=demo", nil), httptest.NewRecorder())
	if err := PrecheckRunner(c); err == nil {
		t.Fatal("invalid configuration accepted")
	}
}
