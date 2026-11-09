//go:build !windows

package runnerproc

import (
	"os/exec"
	"syscall"
)

// ConfigureCommand puts a runner in its own Unix process group.
func ConfigureCommand(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }
