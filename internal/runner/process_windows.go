package runner

import (
	"context"
	"time"

	"github.com/soulteary/runner-fleet/internal/config"
	"github.com/soulteary/runner-fleet/internal/windowsrunner"
)

func ConfigScriptName() string { return "config.cmd" }
func RunScriptName() string    { return "run.cmd" }
func startLocal(ctx context.Context, dir string) error {
	return windowsrunner.Default.SetRunning(ctx, dir, true)
}
func Start(dir string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	return startLocal(ctx, dir)
}
func Stop(dir string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	return windowsrunner.Default.SetRunning(ctx, dir, false)
}
func applyWindowsServiceStatus(info *RunnerInfo) {
	if !windowsrunner.Enabled || info.Status != StatusInstalled {
		return
	}
	state, err := windowsrunner.Default.Status(info.InstallDir)
	if err != nil || state == windowsrunner.Pending {
		info.Status = StatusUnknown
		message := "Windows service is starting or stopping"
		if err != nil {
			message = err.Error()
		}
		info.Probe = &ProbeInfo{Type: "windows-service", Error: message, Suggestion: "Inspect the Windows service and runner _diag logs; click Start to repair a missing runner service"}
		return
	}
	info.Running = state == windowsrunner.Running
}

func desiredState(item config.RunnerItem) string {
	if item.WantsRunning() {
		return "running"
	}
	return "stopped"
}
func localRunning(string) bool { return false }
func applyLocalStatuses(list []RunnerInfo, _ []string) {
	for i := range list {
		applyWindowsServiceStatus(&list[i])
	}
}
