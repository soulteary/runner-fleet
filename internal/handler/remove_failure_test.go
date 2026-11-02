package handler

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/soulteary/runner-fleet/internal/config"
	"github.com/soulteary/runner-fleet/internal/githubcheck"
	"github.com/soulteary/runner-fleet/internal/secrets"
)

func TestRemoveRunnerRetainsIdentityWhenContainerRemovalUnconfirmed(t *testing.T) {
	_, installDir := removeTestSetup(t, tokenInStore)
	cfg, err := config.Load(ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Runners.ContainerMode = true
	if err := cfg.Save(ConfigPath); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte("#!/bin/sh\necho 'daemon unavailable' >&2\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	calls := 0
	original := deregisterFromGitHub
	deregisterFromGitHub = func(context.Context, *secrets.Store, string, string, string, string) githubcheck.DeregisterResult {
		calls++
		return githubcheck.DeregisterResult{Done: true}
	}
	t.Cleanup(func() { deregisterFromGitHub = original })
	code, _ := doRemove(t)
	if code != 500 {
		t.Errorf("status=%d, want 500 for unconfirmed container removal", code)
	}
	if calls != 0 {
		t.Errorf("deregistration calls=%d before container removal confirmed", calls)
	}
	if _, err := os.Stat(installDir); err != nil {
		t.Errorf("runner directory lost: %v", err)
	}
	if got := storedToken(t); got != "pat" {
		t.Errorf("stored identity lost: %q", got)
	}
	after, err := config.Load(ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := after.FindItem("alpha"); !ok {
		t.Error("runner configuration lost")
	}
}

func TestRemoveRunnerRetriesAfterLostContainerRemovalAcknowledgment(t *testing.T) {
	_, installDir := removeTestSetup(t, tokenInStore)
	cfg, err := config.Load(ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Runners.ContainerMode = true
	if err := cfg.Save(ConfigPath); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	marker := filepath.Join(t.TempDir(), "removed")
	// First rm commits its simulated remote action, then loses acknowledgment.
	// Retry observes that same container as absent; no identity is replaced.
	script := "#!/bin/sh\ncase \"$1\" in\nstop) exit 0;;\nrm) if [ -f '" + marker + "' ]; then echo 'No such container'; exit 1; fi\ntouch '" + marker + "'\necho 'connection reset' >&2\nexit 1;;\nesac\n"
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	calls := 0
	stubDereg(t, func(context.Context, *secrets.Store, string, string, string, string) githubcheck.DeregisterResult {
		calls++
		return githubcheck.DeregisterResult{Done: true, MessageKey: "github_deregister.done"}
	})
	code, _ := doRemove(t)
	if code != 500 || calls != 0 || storedToken(t) != "pat" {
		t.Fatalf("first status=%d calls=%d; identity must remain", code, calls)
	}
	if _, err := os.Stat(installDir); err != nil {
		t.Fatal(err)
	}
	code, _ = doRemove(t)
	if code != 200 || calls != 1 {
		t.Fatalf("retry status=%d calls=%d", code, calls)
	}
	if _, err := os.Stat(installDir); !os.IsNotExist(err) {
		t.Errorf("directory remains: %v", err)
	}
	if storedToken(t) != "" {
		t.Error("PAT remains after confirmed removal")
	}
	after, err := config.Load(ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := after.FindItem("alpha"); ok {
		t.Error("configuration remains after confirmed removal")
	}
}

func TestRemoveRunnerPreservesTrackingWhenLocalCleanupFails(t *testing.T) {
	for _, failure := range []string{"installation", "credential"} {
		t.Run(failure, func(t *testing.T) {
			_, dir := removeTestSetup(t, tokenInStore)
			if failure == "installation" {
				if err := os.Chmod(dir, 0500); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Chmod(dir, 0700) })
			} else {
				tokenPath := filepath.Join(filepath.Dir(ConfigPath), "tokens", "alpha")
				if err := os.Remove(tokenPath); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(tokenPath, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(tokenPath, "occupied"), []byte("preserve"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			stubDereg(t, func(context.Context, *secrets.Store, string, string, string, string) githubcheck.DeregisterResult {
				return githubcheck.DeregisterResult{Done: true}
			})
			code, _ := doRemove(t)
			if code != 500 {
				t.Errorf("cleanup failure returned %d, want 500", code)
			}
			after, err := config.Load(ConfigPath)
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := after.FindItem("alpha"); !ok {
				t.Error("tracking record lost after cleanup failure")
			}
			if failure == "installation" && storedToken(t) != "pat" {
				t.Error("credential lost before installation cleanup confirmed")
			}
		})
	}
}

func TestRemoveRunnerReportsConfigCommitFailureAfterCleanup(t *testing.T) {
	_, dir := removeTestSetup(t, tokenInStore)
	cfgDir := filepath.Dir(ConfigPath)
	t.Cleanup(func() { _ = os.Chmod(cfgDir, 0700); _ = os.Chmod(ConfigPath, 0600) })
	calls := 0
	stubDereg(t, func(context.Context, *secrets.Store, string, string, string, string) githubcheck.DeregisterResult {
		calls++
		if calls > 1 {
			return githubcheck.DeregisterResult{Done: true}
		}
		if err := os.Chmod(ConfigPath, 0400); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(cfgDir, 0500); err != nil {
			t.Fatal(err)
		}
		return githubcheck.DeregisterResult{Done: true}
	})
	// Leave the tokens directory writable so cleanup commits before config save fails.
	code, _ := doRemove(t)
	if code != 500 || calls != 1 {
		t.Fatalf("status=%d calls=%d", code, calls)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("cleanup did not commit: %v", err)
	}
	after, err := config.Load(ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := after.FindItem("alpha"); !ok {
		t.Error("failed completion removed tracking record")
	}

	if err := os.Chmod(cfgDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(ConfigPath, 0600); err != nil {
		t.Fatal(err)
	}
	code, _ = doRemove(t)
	if code != 200 || calls != 2 {
		t.Fatalf("retry status=%d calls=%d", code, calls)
	}
	after, err = config.Load(ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := after.FindItem("alpha"); ok {
		t.Error("tracking record remains after successful retry")
	}
}
