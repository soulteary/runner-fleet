package runner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/soulteary/runner-fleet/internal/config"
)

func TestRemovedWorkingDirectoryRejectsRelativeLifecyclePaths(t *testing.T) {
	original, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Chdir(original); err != nil {
			t.Error(err)
		}
	}()
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	if err := Start("."); err == nil {
		t.Fatal("relative start accepted without a resolvable cwd")
	}
	if err := Stop("."); err == nil {
		t.Fatal("relative stop accepted without a resolvable cwd")
	}
	if _, err := EnsureRunnerDir(&config.Config{Runners: config.RunnersConfig{BasePath: "."}}, "demo", ""); err == nil {
		t.Fatal("created runner under a removed cwd")
	}
	if got := hostMountSource(&config.Config{}, "relative-runner"); got != "relative-runner" {
		t.Fatalf("lost unresolved path fallback: %q", got)
	}
}

func TestContainerDefaultsAndInvalidBackendRemainExplicit(t *testing.T) {
	cfg := &config.Config{}
	s := desiredContainerSpec(cfg, "demo", t.TempDir(), "token")
	args, err := s.createArgs()
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--network runner-net") || !strings.Contains(joined, "DOCKER_HOST=tcp://runner-dind:2375") || !strings.Contains(joined, "AGENT_TOKEN=token") {
		t.Fatalf("incomplete default isolation: %s", joined)
	}
	cfg.Runners.ContainerMode = true
	cfg.Runners.JobDockerBackend = "invalid"
	facts := &containerFacts{}
	if got := driftFromFacts(context.Background(), cfg, "demo", filepath.Join(t.TempDir(), "demo"), "token", facts, func(context.Context, string) string { t.Fatal("resolved image for invalid config"); return "" }); got != "" {
		t.Fatalf("invalid backend reported as actionable drift: %s", got)
	}
	s = containerSpec{Network: "expected", JobBackend: "host-socket", DockerGID: 123, MountSrc: "/fixture"}
	facts = &containerFacts{Env: []string{"DOCKER_HOST=unix://" + HostDockerSocket}, Binds: []string{HostDockerSocket + ":" + HostDockerSocket}, Labels: map[string]string{labelNetwork: "expected", labelJobBackend: "host-socket"}}
	if got := s.driftReason(facts, ""); !strings.Contains(got, "docker_gid") {
		t.Fatalf("missing supplementary group ignored: %s", got)
	}
	s.DockerGID = unknownDockerGID
	facts.Labels = nil
	facts.Networks = []string{"other"}
	if got := s.driftReason(facts, ""); !strings.Contains(got, "container_network") {
		t.Fatalf("legacy network mismatch ignored: %s", got)
	}
}

func TestRunnerDirectoryAndTokenStorageFailuresStayClosed(t *testing.T) {
	cfg := &config.Config{Runners: config.RunnersConfig{BasePath: t.TempDir()}}
	if _, err := EnsureRunnerDir(cfg, "../outside", ""); !errors.Is(err, os.ErrInvalid) {
		t.Fatalf("unsafe fallback name escaped base: %v", err)
	}
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0600); err != nil {
		t.Fatal(err)
	}
	cfg.Runners.BasePath = file
	if _, err := EnsureRunnerDir(cfg, "demo", ""); err == nil {
		t.Fatal("file accepted as runner base")
	}
	if os.Geteuid() != 0 {
		dir := t.TempDir()
		if err := os.Chmod(dir, 0500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(dir, 0700) })
		if token, err := EnsureAgentToken(dir); err == nil || token != "" {
			t.Fatal("read-only storage produced usable credentials")
		}
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) != 0 {
			t.Fatalf("partial token files left behind: %v %v", entries, err)
		}
	}
}
