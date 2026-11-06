package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/soulteary/runner-fleet/internal/config"
	"github.com/soulteary/runner-fleet/internal/windowsrunner"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

const managerServiceName = "RunnerFleet"

func windowsDataDir() string {
	base := os.Getenv("ProgramData")
	if base == "" {
		base = `C:\ProgramData`
	}
	return filepath.Join(base, "RunnerFleet")
}

func platformServiceMain() (bool, error) {
	if len(os.Args) > 1 && os.Args[1] == "service" {
		if len(os.Args) < 3 {
			return true, fmt.Errorf("usage: runner-manager.exe service install|start|stop|uninstall [-config path]")
		}
		flags := flag.NewFlagSet("service", flag.ContinueOnError)
		configPath := flags.String("config", defaultConfigPath(), "absolute config file path")
		if err := flags.Parse(os.Args[3:]); err != nil {
			return true, err
		}
		if flags.NArg() != 0 {
			return true, fmt.Errorf("unexpected service arguments")
		}
		return true, manageService(os.Args[2], *configPath)
	}
	isService, err := svc.IsWindowsService()
	if err != nil {
		return true, err
	}
	if !isService {
		// Do not depend on the directory from which an interactive console was opened.
		if len(os.Args) == 1 {
			os.Args = append(os.Args, "-config", defaultConfigPath())
		}
		return false, nil
	}
	return true, svc.Run(managerServiceName, managerService{})
}

type managerService struct{}

func (managerService) Execute(_ []string, requests <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	status <- svc.Status{State: svc.StartPending}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); runManager(ctx) }()
	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for {
		select {
		case <-done:
			return false, 0
		case req := <-requests:
			switch req.Cmd {
			case svc.Interrogate:
				status <- req.CurrentStatus
			case svc.Stop, svc.Shutdown:
				cancel()
				for checkpoint := uint32(1); ; checkpoint++ {
					status <- svc.Status{State: svc.StopPending, CheckPoint: checkpoint, WaitHint: 15000}
					select {
					case <-done:
						return false, 0
					case <-time.After(time.Second):
					}
				}
			}
		}
	}
}

func manageService(action, configPath string) error {
	if action != "install" && action != "start" && action != "stop" && action != "uninstall" {
		return fmt.Errorf("unknown service action %q", action)
	}
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to Windows service manager (use an administrator terminal): %w", err)
	}
	defer func() { _ = m.Disconnect() }()
	if action == "install" {
		if existing, err := m.OpenService(managerServiceName); err == nil {
			_ = existing.Close()
			return fmt.Errorf("RunnerFleet service is already installed")
		} else if !errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
			return err
		}
		configPath, err = filepath.Abs(configPath)
		if err != nil {
			return err
		}
		if err := windowsrunner.ProtectManager(filepath.Dir(configPath)); err != nil {
			return err
		}
		cfg, err := config.Load(configPath)
		if err != nil {
			return err
		}

		if err := cfg.Save(configPath); err != nil {
			return err
		}
		if err := windowsrunner.ProtectManager(cfg.Runners.BasePath); err != nil {
			return err
		}
		if err := windowsrunner.ProtectManager(filepath.Join(filepath.Dir(configPath), "tokens")); err != nil {
			return err
		}
		programFiles := os.Getenv("ProgramFiles")
		if programFiles == "" {
			return fmt.Errorf("ProgramFiles is unavailable")
		}
		installDir := filepath.Join(programFiles, "RunnerFleet")
		if err := windowsrunner.ProtectManager(installDir); err != nil {
			return err
		}
		src, err := os.Executable()
		if err != nil {
			return err
		}
		exe := filepath.Join(installDir, "runner-manager.exe")
		if !strings.EqualFold(src, exe) {
			if err := copyManagerExecutable(src, exe); err != nil {
				return err
			}
		}
		s, err := m.CreateService(managerServiceName, exe, mgr.Config{DisplayName: "Runner Fleet Manager", StartType: mgr.StartAutomatic, ServiceStartName: "LocalSystem"}, "-config", configPath)
		if err != nil {
			return err
		}
		defer func() { _ = s.Close() }()
		if err := s.SetRecoveryActions([]mgr.RecoveryAction{{Type: mgr.ServiceRestart, Delay: 5 * time.Second}}, 86400); err != nil {
			return err
		}
		fmt.Printf("Installed RunnerFleet. Start with: runner-manager.exe service start\nConfig: %s\n", configPath)
		return nil
	}
	s, err := m.OpenService(managerServiceName)
	if err != nil {
		return err
	}
	defer func() { _ = s.Close() }()
	switch action {
	case "start":
		if err := s.Start(); err != nil && !errors.Is(err, windows.ERROR_SERVICE_ALREADY_RUNNING) {
			return err
		}
		return waitManagerService(s, svc.Running)
	case "stop", "uninstall":
		if _, err := s.Control(svc.Stop); err != nil && !errors.Is(err, windows.ERROR_SERVICE_NOT_ACTIVE) {
			return err
		}
		if err := waitManagerService(s, svc.Stopped); err != nil {
			return err
		}
		if action == "uninstall" {
			return s.Delete()
		}
		return nil
	default:
		return fmt.Errorf("unknown service action %q", action)
	}
}

func waitManagerService(s *mgr.Service, target svc.State) error {
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		state, err := s.Query()
		if err != nil {
			return err
		}
		if state.State == target {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("timed out waiting for Manager service state %d", target)
}

func copyManagerExecutable(src, dst string) error {
	r, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = r.Close() }()
	w, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = io.Copy(w, r)
	closeErr := w.Close()
	if err != nil {
		return err
	}
	return closeErr
}

// Log files are available even when SCM launches the Manager without a console.
func platformLogOutput() io.Writer {
	dir := filepath.Join(windowsDataDir(), "logs")
	if err := windowsrunner.ProtectManager(dir); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return os.Stderr
	}
	f, err := os.OpenFile(filepath.Join(dir, "manager-"+time.Now().Format("2006-01-02")+".log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return os.Stderr
	}
	return io.MultiWriter(os.Stderr, f)
}

func defaultConfigPath() string { return filepath.Join(windowsDataDir(), "config.yaml") }
