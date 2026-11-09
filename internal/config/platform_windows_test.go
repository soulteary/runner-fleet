package config

import (
	"path/filepath"
	"testing"
)

func TestWindowsPathsNamesAndUnsupportedModes(t *testing.T) {
	for _, name := range []string{"CON", "nul.txt", "LPT1", "bad:name", "bad?name", "trailing.", "trailing ", "bad\x01name"} {
		if IsSafeRunnerNameOrPath(name) {
			t.Errorf("accepted Windows reserved name %q", name)
		}
	}
	if !IsSafeRunnerNameOrPath("构建 runner") {
		t.Fatal("valid Unicode/spaced name rejected")
	}
	path := filepath.Join(t.TempDir(), "data", "config.yaml")
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Server.Addr != "127.0.0.1" || c.Runners.BasePath != filepath.Join(filepath.Dir(path), "runners") {
		t.Fatal(c)
	}
	c.Runners.ContainerMode = true
	if err := Validate(c); err == nil {
		t.Fatal("accepted Windows containers")
	}
	c.Runners.ContainerMode = false
	c.Runners.Items = []RunnerItem{{Name: "Build", TargetType: "repo", Target: "o/r"}, {Name: "build", TargetType: "repo", Target: "o/r"}}
	if err := Validate(c); err == nil {
		t.Fatal("accepted case-insensitive duplicate")
	}
}
