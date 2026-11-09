//go:build !windows

package runnerproc

import (
	"context"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

func TestConfigureCommandIsolatesRunnerProcessGroup(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "-c", "read line")
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = input.Close() }()
	ConfigureCommand(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	group, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	if group != cmd.Process.Pid || group == syscall.Getpgrp() {
		t.Fatalf("runner process group = %d, runner PID = %d, parent group = %d", group, cmd.Process.Pid, syscall.Getpgrp())
	}
}
