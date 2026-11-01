package handler

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestRemovedWorkingDirectoryFailsClosedForRelativePaths(t *testing.T) {
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
	if isUnderBasePath(".", "/tmp/runner") || isUnderBasePath("/tmp", ".") {
		t.Fatal("unresolvable deletion boundary accepted")
	}
	if _, err := runConfigScript(".", "https://github.com/o/r", "fixture", "demo", nil, time.Second); err == nil {
		t.Fatal("unresolvable registration directory accepted")
	}
}

func TestRegistrationScriptsReturnDeadlineOnActualProcessTimeout(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "wait")
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	body := "#!/bin/bash\nexec 3<>\"" + fifo + "\"\nread -r -t 60 -u 3\n"
	script := filepath.Join(dir, "config.sh")
	if err := os.WriteFile(script, []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := runConfigScript(dir, "https://github.com/o/r", "fixture", "demo", nil, 25*time.Millisecond); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("registration timeout: %v", err)
	}
	old := installRunnerScriptPath
	installRunnerScriptPath = script
	t.Cleanup(func() { installRunnerScriptPath = old })
	if _, err := runInstallRunnerScript(dir, "demo", 25*time.Millisecond); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("install timeout: %v", err)
	}
}
