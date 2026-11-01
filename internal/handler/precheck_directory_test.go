package handler

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInspectInstallDirStates(t *testing.T) {
	base := t.TempDir()
	for _, tc := range []struct{ name, marker, want string }{
		{"empty", "", ""},
		{"hidden-file", ".partial-install", "nonempty"},
		{"ordinary-file", "run.sh", "nonempty"},
		{"registered", ".runner", "registered"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(base, tc.name)
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			if tc.marker != "" {
				if err := os.WriteFile(filepath.Join(dir, tc.marker), nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if got := inspectInstallDir(dir); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
			link := dir + "-link"
			if err := os.Symlink(dir, link); err != nil {
				t.Fatal(err)
			}
			if got := inspectInstallDir(link); got != tc.want {
				t.Fatalf("symlink got %q want %q", got, tc.want)
			}
		})
	}
	if got := inspectInstallDir(filepath.Join(base, "missing")); got != "" {
		t.Fatal(got)
	}
	file := filepath.Join(base, "file")
	if err := os.WriteFile(file, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if got := inspectInstallDir(file); got != "" {
		t.Fatal(got)
	}
}
