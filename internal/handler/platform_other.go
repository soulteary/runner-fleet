//go:build !windows

package handler

import (
	"context"
	"github.com/labstack/echo/v4"
	"github.com/soulteary/runner-fleet/internal/config"
	"os/exec"
	"path/filepath"
)

func runPlatformRegistrationJob(registrationJob) bool      { return false }
func windowsRunnerAction(echo.Context, bool) (bool, error) { return false, nil }
func removeWindowsRunner(echo.Context) (bool, error)       { return false, nil }

func lockPlatformUpdate(echo.Context) (func(), error) { return func() {}, nil }

func registrationCommand(ctx context.Context, script string, args []string) *exec.Cmd {
	return exec.CommandContext(ctx, script, args...)
}
func validatePlatformUpdate(*config.Config, config.RunnerItem, string) error { return nil }
func sameInstallPath(a, b string) bool                                       { return filepath.Clean(a) == filepath.Clean(b) }
