package runnerproc

import "os/exec"

// Windows runners are launched by the Service Control Manager, not a Unix shell.
func ConfigureCommand(cmd *exec.Cmd) {}
