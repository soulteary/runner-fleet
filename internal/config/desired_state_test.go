package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDesiredStateRoundTripAndDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	c := &Config{Runners: RunnersConfig{BasePath: t.TempDir(), Items: []RunnerItem{{Name: "build", TargetType: "repo", Target: "owner/repo"}}}}
	if !c.Runners.Items[0].WantsRunning() {
		t.Fatal("old configuration should default to running")
	}
	if err := c.Save(path); err != nil {
		t.Fatal(err)
	}
	if err := LoadAndSave(path, func(c *Config) error { c.Runners.Items[0].DesiredState = "stopped"; return nil }); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Runners.Items[0].WantsRunning() {
		t.Fatal("manual stop did not survive reload")
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := LoadAndSave(path, func(c *Config) error { c.Runners.Items[0].DesiredState = "typo"; return nil }); err == nil {
		t.Fatal("invalid desired state accepted")
	}
	after, err := os.ReadFile(path)
	if err != nil || string(before) != string(after) {
		t.Fatal("failed validation overwrote configuration")
	}
}
