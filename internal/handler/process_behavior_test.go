package handler

import (
	"context"
	"github.com/labstack/echo/v4"
	health "github.com/soulteary/health-kit/v4"
	"net/http/httptest"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/soulteary/runner-fleet/internal/runner"
	"github.com/soulteary/runner-fleet/internal/runnerproc"
)

func TestProcessAPIReusesRunningRunnerAndDeliversStop(t *testing.T) {
	cfg := behaviorConfig(t)
	dir := filepath.Join(cfg.Runners.BasePath, "demo")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".runner"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	fifo := filepath.Join(dir, "wait")
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	ready := filepath.Join(dir, "ready")
	script := "#!/bin/bash\ntrap 'exit 0' TERM\nexec 3<>\"" + fifo + "\"\n: > \"" + ready + "\"\nread -r -t 60 -u 3\n"
	if err := os.WriteFile(filepath.Join(dir, runner.RunScriptName()), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	if err := runner.Start(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, pid := range runnerproc.Find(dir) {
			_ = syscall.Kill(-pid, syscall.SIGKILL)
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, err := os.Stat(ready)
		if err == nil && runnerproc.Running(dir) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("runner did not install trap")
		}
		time.Sleep(10 * time.Millisecond)
	}
	before := runnerproc.Find(dir)
	rec := behaviorCall(StartRunner, "demo", "")
	if rec.Code != 200 {
		t.Fatal(rec.Code)
	}
	after := runnerproc.Find(dir)
	if len(after) != len(before) || len(after) != 1 || after[0] != before[0] {
		t.Fatalf("start duplicated live runner: %v -> %v", before, after)
	}
	rec = behaviorCall(StopRunner, "demo", "")
	if rec.Code != 200 {
		t.Fatalf("stop: %d %s", rec.Code, rec.Body.String())
	}
	deadline = time.Now().Add(5 * time.Second)
	for runnerproc.Running(dir) {
		if time.Now().After(deadline) {
			t.Fatal("stop did not reach process")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestReadinessRejectsInvalidConfigAndNonDirectoryStorage(t *testing.T) {
	cfg := behaviorConfig(t)
	if err := os.WriteFile(ConfigPath, []byte("invalid: ["), 0600); err != nil {
		t.Fatal(err)
	}
	if err := checkConfigLoads(context.Background()); err == nil {
		t.Fatal("invalid configuration ready")
	}
	if err := checkBasePathUsable(context.Background()); err == nil {
		t.Fatal("invalid configuration yielded usable storage")
	}
	file := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(file, nil, 0600); err != nil {
		t.Fatal(err)
	}
	cfg.Runners.BasePath = file
	if err := cfg.Save(ConfigPath); err != nil {
		t.Fatal(err)
	}
	if err := checkBasePathUsable(context.Background()); err == nil {
		t.Fatal("regular file accepted as runner storage")
	}
}

func TestProbeWhitelistRejectsRequestsWithoutExposingDetails(t *testing.T) {
	agg := health.NewAggregator(health.DefaultConfig().WithIPWhitelist([]string{"127.0.0.1"}))
	req := httptest.NewRequest("GET", "/ready", nil)
	req.RemoteAddr = "192.0.2.1:1234"
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(req, rec)
	err := writeHealth(c, agg)
	he, ok := err.(*echo.HTTPError)
	if !ok || he.Code != 403 || he.Message != "Forbidden" {
		t.Fatalf("restricted probe accepted or disclosed details: %v", err)
	}
}
