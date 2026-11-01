package secrets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/soulteary/runner-fleet/internal/config"
)

func TestMigrationRetryAfterDestinationCommitAndSourceRemovalFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("requires filesystem permission enforcement")
	}
	for _, existing := range []string{"", "legacy-pat", "different-current-pat"} {
		t.Run(map[string]string{"": "destination-absent", "legacy-pat": "same-token", "different-current-pat": "different-token"}[existing], func(t *testing.T) {
			dir := t.TempDir()
			store := NewStore(filepath.Join(t.TempDir(), "config.yaml"))
			writeLegacy(t, dir, "legacy-pat")
			if existing != "" {
				writeStore(t, store, "demo", existing)
			}
			if err := os.Chmod(dir, 0500); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(dir, 0700) })
			moved, err := store.Migrate("demo", dir)
			if err == nil || moved {
				t.Fatalf("source deletion failure hidden: moved=%v err=%v", moved, err)
			}
			want := existing
			if want == "" {
				want = "legacy-pat"
			}
			if got := store.GitHubToken("demo"); got != want {
				t.Fatal("destination was lost or replaced")
			}
			data, err := os.ReadFile(legacyPath(dir))
			if err != nil || strings.TrimSpace(string(data)) != "legacy-pat" {
				t.Fatal("legacy record lost despite failed removal")
			}
			if err := os.Chmod(dir, 0700); err != nil {
				t.Fatal(err)
			}
			moved, err = store.Migrate("demo", dir)
			if err != nil || moved {
				t.Fatalf("retry should finish existing publication: moved=%v err=%v", moved, err)
			}
			if got := store.GitHubToken("demo"); got != want {
				t.Fatal("retry changed destination identity")
			}
			if _, err := os.Stat(legacyPath(dir)); !os.IsNotExist(err) {
				t.Fatalf("legacy credential still exposed: %v", err)
			}
		})
	}
}

func TestStoreFilesystemFailuresPreserveRecords(t *testing.T) {
	root := t.TempDir()
	store := &Store{Dir: filepath.Join(root, "tokens")}
	if err := os.WriteFile(store.Dir, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := store.write(filepath.Join(store.Dir, "demo"), "pat"); err == nil {
		t.Fatal("file accepted as token directory")
	}
	store.Dir = filepath.Join(root, "valid")
	if err := os.Mkdir(store.Dir, 0700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(store.Dir, "demo")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	if err := store.write(target, "pat"); err == nil {
		t.Fatal("publication over directory accepted")
	}
	files, err := filepath.Glob(filepath.Join(store.Dir, "*.tmp-*"))
	if err != nil || len(files) != 0 {
		t.Fatalf("temporary credentials left behind: %v %v", files, err)
	}
	dir := t.TempDir()
	if err := os.Mkdir(legacyPath(dir), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Migrate("demo", dir); err == nil {
		t.Fatal("legacy read failure hidden")
	}
	if err := os.Remove(legacyPath(dir)); err != nil {
		t.Fatal(err)
	}
	writeLegacy(t, dir, "pat")
	if _, err := store.Migrate("../unsafe", dir); err == nil {
		t.Fatal("invalid name accepted during migration")
	}
	cfg := &config.Config{Runners: config.RunnersConfig{BasePath: filepath.Dir(dir), Items: []config.RunnerItem{{Name: "../unsafe", Path: filepath.Base(dir)}}}}
	if n := MigrateAll(cfg, store); n != 0 {
		t.Fatal("failed migration counted as moved")
	}
	if err := store.Remove("../unsafe"); err == nil {
		t.Fatal("invalid removal accepted")
	}
}

func TestStoreReadOnlyDirectoryRejectsWritesAndRemoval(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("requires filesystem permission enforcement")
	}
	store := NewStore(filepath.Join(t.TempDir(), "config.yaml"))
	writeStore(t, store, "demo", "original")
	if err := os.Chmod(store.Dir, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(store.Dir, 0700) })
	if err := store.write(filepath.Join(store.Dir, "demo"), "replacement"); err == nil {
		t.Fatal("read-only directory accepted publication")
	}
	if err := store.Remove("demo"); err == nil {
		t.Fatal("removal failure hidden")
	}
	if got := store.GitHubToken("demo"); got != "original" {
		t.Fatal("original token changed")
	}
}
