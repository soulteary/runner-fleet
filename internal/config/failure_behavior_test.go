package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLoadFailuresAndInvalidMutationPreserveConfiguration(t *testing.T) {
	t.Setenv("MANAGER_IMAGE", "")
	t.Setenv("CONTAINER_MODE", "true")
	t.Setenv("RUNNER_IMAGE", "")
	t.Setenv("CONTAINER_IMAGE", "")
	path := filepath.Join(t.TempDir(), "config.yaml")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Runners.ContainerImage != DefaultRunnerContainerImage() {
		t.Fatalf("wrong default runner image %q", cfg.Runners.ContainerImage)
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := LoadAndSave(path, func(cfg *Config) error { cfg.Server.Port = 70000; return nil }); err == nil {
		t.Fatal("invalid mutation committed")
	}
	current, err := os.ReadFile(path)
	if err != nil || !reflect.DeepEqual(original, current) {
		t.Fatal("invalid mutation changed stored config")
	}
	if _, err := Load(filepath.Dir(path)); err == nil {
		t.Fatal("directory read accepted as configuration")
	}
	t.Setenv("SERVER_PORT", "70000")
	if _, err := Load(filepath.Join(t.TempDir(), "new.yaml")); err == nil {
		t.Fatal("invalid environment accepted on initial load")
	}
	t.Setenv("SERVER_PORT", "")
	if os.Geteuid() != 0 {
		parent := t.TempDir()
		if err := os.Chmod(parent, 0500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(parent, 0700) })
		if cfg, err := Load(filepath.Join(parent, "missing", "config.yaml")); err != nil || cfg == nil {
			t.Fatalf("in-memory fallback missing: %v", err)
		}
		if cfg, err := Load(filepath.Join(parent, "config.yaml")); err != nil || cfg == nil {
			t.Fatalf("save fallback missing: %v", err)
		}
	}
}

func TestResourceLimitSwapAndImageFallbackSemantics(t *testing.T) {
	r := ResourceLimits{Memory: " 512m ", MemorySwap: " -1 "}
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	if got := r.Args(); !reflect.DeepEqual(got, []string{"--memory", "512m", "--memory-swap", "-1"}) {
		t.Fatal(got)
	}
	if err := (ResourceLimits{Memory: "1.2.3g", MemorySwap: "2g"}).Validate(); err == nil {
		t.Fatal("malformed memory accepted")
	}
	for _, raw := range []string{"", "registry/image:"} {
		t.Setenv("MANAGER_IMAGE", raw)
		want := ""
		if raw != "" {
			want = "registry/image:latest-runner"
		}
		if got := containerImageFromManagerImage(); got != want {
			t.Fatalf("%q -> %q", raw, got)
		}
	}
}

func TestResourceSizeOverflowDoesNotBecomeAByteCount(t *testing.T) {
	value := strings.Repeat("9", 400) + "g"
	if err := (ResourceLimits{Memory: "1g", MemorySwap: "invalid"}).Validate(); err == nil {
		t.Fatal("invalid swap notation accepted")
	}
	if _, err := parseDockerSize(value); err == nil {
		t.Fatal("overflowed byte count accepted")
	}
}
