package handler

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/soulteary/runner-fleet/internal/config"
	"github.com/soulteary/runner-fleet/internal/runner"
	"github.com/soulteary/runner-fleet/internal/windowsrunner"
)

func runPlatformRegistrationJob(j registrationJob) bool { runWindowsRegistrationJob(j); return true }

func runWindowsRegistrationJob(j registrationJob) {
	unlock := windowsrunner.Lock(j.InstallDir)
	defer unlock()
	cfg, err := config.Load(ConfigPath)
	if err != nil {
		writeRegistrationResult(j.InstallDir, false, err.Error())
		return
	}
	item, ok := cfg.FindItem(j.RunnerName)
	if !ok || item.InstallPath(cfg.Runners.BasePath) != j.InstallDir {
		return
	} // deleted/edited while queued
	fail := func(stage string, err error, output []byte) {
		message := stage + ": " + err.Error()
		if len(output) != 0 {
			message += ": " + strings.TrimSpace(string(output))
		}
		if j.Token != "" {
			message = strings.ReplaceAll(message, j.Token, "[REDACTED]")
		}
		writeRegistrationResult(j.InstallDir, false, message)
		log.Printf("[registration] %s %s", j.RunnerName, message)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if _, err := os.Stat(filepath.Join(j.InstallDir, ".runner")); os.IsNotExist(err) {
		installer := windowsrunner.Installer{Version: os.Getenv("RUNNER_VERSION")}
		if err := installer.Install(ctx, j.InstallDir); err != nil {
			fail("installing Windows runner failed", err, nil)
			return
		}
		out, err := runConfigScript(j.InstallDir, j.URL, j.Token, j.RunnerName, j.Labels, 2*time.Minute)
		if err != nil {
			stage := "registering Windows runner failed (check the name and use a fresh GitHub registration token)"
			if _, statErr := os.Stat(filepath.Join(j.InstallDir, ".runner")); statErr == nil {
				stage = "registered on GitHub but Windows service setup failed; click Start to repair without registering again"
			}
			fail(stage, err, out)
			return
		}
	} else if err != nil {
		fail("reading Windows runner registration failed", err, nil)
		return
	}
	if err := windowsrunner.Default.Ensure(ctx, j.InstallDir); err != nil {
		fail("registered; Windows service setup failed; click Start to retry", err, nil)
		return
	}
	if err := windowsrunner.Default.SetRunning(ctx, j.InstallDir, item.WantsRunning()); err != nil {
		fail("registered; applying Windows service state failed", err, nil)
		return
	}
	writeRegistrationResult(j.InstallDir, true, "registered; Windows service configured")
}

// windowsRunnerAction owns desired-state writes and SCM changes under one lock.
// The periodic reconciler and registration worker use the same lock.
func windowsRunnerAction(c echo.Context, start bool) (bool, error) {
	if !windowsrunner.Enabled {
		return false, nil
	}
	_, info, unlock, err := lockWindowsRunner(c)
	if err != nil {
		return true, err
	}
	defer unlock()
	if _, err := os.Stat(filepath.Join(info.InstallDir, ".runner")); start && err != nil {
		return true, echo.NewHTTPError(http.StatusBadRequest, trf(c, "api.start_requires_registered", "new"))
	}
	state := "stopped"
	if start {
		state = "running"
	}
	if err := config.LoadAndSave(ConfigPath, func(cur *config.Config) error {
		for i := range cur.Runners.Items {
			if cur.Runners.Items[i].Name == info.Name {
				cur.Runners.Items[i].DesiredState = state
				return nil
			}
		}
		return fmt.Errorf("runner no longer exists")
	}); err != nil {
		return true, echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	ctx, cancel := lifecycleContext(c.Request().Context(), 60*time.Second)
	defer cancel()
	if start {
		if err := windowsrunner.Default.Ensure(ctx, info.InstallDir); err != nil {
			return true, echo.NewHTTPError(http.StatusInternalServerError, trf(c, "api.windows_repair_failed", err))
		}
	}
	err = windowsrunner.Default.SetRunning(ctx, info.InstallDir, start)
	if !start && errors.Is(err, windowsrunner.ErrServiceMissing) {
		err = nil
	}
	if err != nil {
		return true, echo.NewHTTPError(http.StatusInternalServerError, trf(c, "api.windows_state_failed", err))
	}
	messageKey := "api.windows_stopped"
	if start {
		messageKey = "api.windows_running"
	}
	return true, c.JSON(http.StatusOK, map[string]any{"message": tr(c, messageKey), "desired_state": state})
}

func lockWindowsRunner(c echo.Context) (*config.Config, *runner.RunnerInfo, func(), error) {
	name := c.Param("name")
	if !config.IsSafeRunnerNameOrPath(name) {
		return nil, nil, nil, echo.NewHTTPError(http.StatusBadRequest, tr(c, "api.name_invalid"))
	}
	cfg, err := getConfig(c)
	if err != nil {
		return nil, nil, nil, err
	}
	item, ok := cfg.FindItem(name)
	if !ok {
		return nil, nil, nil, echo.NewHTTPError(http.StatusNotFound, tr(c, "api.runner_not_found"))
	}
	dir := item.InstallPath(cfg.Runners.BasePath)
	unlock := windowsrunner.Lock(dir)
	cfg, err = getConfig(c)
	if err != nil {
		unlock()
		return nil, nil, nil, err
	}
	item, ok = cfg.FindItem(name)
	if !ok || item.InstallPath(cfg.Runners.BasePath) != dir {
		unlock()
		return nil, nil, nil, echo.NewHTTPError(http.StatusConflict, tr(c, "api.windows_changed"))
	}
	return cfg, runner.GetByName(cfg, name), unlock, nil
}

func removeWindowsRunner(c echo.Context) (bool, error) {
	if !windowsrunner.Enabled {
		return false, nil
	}
	cfg, info, unlock, err := lockWindowsRunner(c)
	if err != nil {
		return true, err
	}
	defer unlock()
	if !isUnderBasePath(cfg.Runners.BasePath, info.InstallDir) {
		return true, echo.NewHTTPError(http.StatusBadRequest, "runner directory is outside runners base path")
	}
	// Save stopped before removal, so a partial failure is safe to retry after reboot.
	if err := config.LoadAndSave(ConfigPath, func(cur *config.Config) error {
		for i := range cur.Runners.Items {
			if cur.Runners.Items[i].Name == info.Name {
				cur.Runners.Items[i].DesiredState = "stopped"
			}
		}
		return nil
	}); err != nil {
		return true, echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	ctx, cancel := lifecycleContext(c.Request().Context(), 60*time.Second)
	defer cancel()
	if err := windowsrunner.Default.Remove(ctx, info.InstallDir); err != nil {
		return true, echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	store := secretStore()
	dereg := deregisterFromGitHub(ctx, store, info.InstallDir, info.TargetType, info.Target, info.Name)
	if err := os.RemoveAll(info.InstallDir); err != nil {
		return true, echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	if err := store.Remove(info.Name); err != nil {
		return true, echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	if err := config.LoadAndSave(ConfigPath, func(cur *config.Config) error { return removeRunnerFromConfig(c, cur, info.Name) }); err != nil {
		return true, err
	}
	return true, c.JSON(http.StatusOK, map[string]any{"message": trf(c, "api.removed", trf(c, dereg.MessageKey, dereg.MessageArgs...)), "github_deregistered": dereg.Done})
}

func lockPlatformUpdate(c echo.Context) (func(), error) {
	_, _, unlock, err := lockWindowsRunner(c)
	return unlock, err
}

func registrationCommand(ctx context.Context, script string, args []string) *exec.Cmd {
	args = append([]string{"configure"}, args...)
	args = append(args, "--runasservice")
	return exec.CommandContext(ctx, filepath.Join(filepath.Dir(script), "bin", "Runner.Listener.exe"), args...)
}
func validatePlatformUpdate(cfg *config.Config, old config.RunnerItem, path string) error {
	if !sameInstallPath(old.InstallPath(cfg.Runners.BasePath), (config.RunnerItem{Name: old.Name, Path: path}).InstallPath(cfg.Runners.BasePath)) {
		if _, err := os.Stat(filepath.Join(old.InstallPath(cfg.Runners.BasePath), ".runner")); err == nil {
			return echo.NewHTTPError(http.StatusBadRequest, "cannot change the installation path of a registered Windows runner")
		}
	}
	return nil
}
func sameInstallPath(a, b string) bool {
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}
