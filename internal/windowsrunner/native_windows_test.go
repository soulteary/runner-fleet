package windowsrunner

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
)

// The test executable doubles as a harmless service host for real SCM tests.
// It never downloads a runner or contacts GitHub.
func TestMain(m *testing.M) {
	if len(os.Args) == 2 && os.Args[1] == "init" {
		os.Exit(0)
	}
	if len(os.Args) == 2 && strings.HasPrefix(os.Args[1], "actions.runner.fleet.test-") {
		if err := svc.Run(os.Args[1], testServiceHost{}); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

type testServiceHost struct{}

func (testServiceHost) Execute(_ []string, requests <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for req := range requests {
		if req.Cmd == svc.Stop || req.Cmd == svc.Shutdown {
			status <- svc.Status{State: svc.StopPending}
			return false, 0
		}
		if req.Cmd == svc.Interrogate {
			status <- req.CurrentStatus
		}
	}
	return false, 0
}

func TestWindowsNativeSCMTwoIndependentServices(t *testing.T) {
	if os.Getenv("RUNNER_FLEET_SCM_TESTS") != "1" {
		t.Skip("set RUNNER_FLEET_SCM_TESTS=1 in an administrator session")
	}
	if !windows.GetCurrentProcessToken().IsElevated() {
		t.Fatal("SCM acceptance requires an elevated administrator session")
	}
	source, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dirs := []string{filepath.Join(t.TempDir(), "runner 一"), filepath.Join(t.TempDir(), "runner 二")}
	for i, dir := range dirs {
		if err := ProtectRunner(dir); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(dir, "bin"), 0700); err != nil {
			t.Fatal(err)
		}
		src, err := os.Open(source)
		if err != nil {
			t.Fatal(err)
		}
		dst, err := os.Create(serviceBinary(dir))
		if err != nil {
			_ = src.Close()
			t.Fatal(err)
		}
		_, copyErr := io.Copy(dst, src)
		_ = src.Close()
		closeErr := dst.Close()
		if copyErr != nil || closeErr != nil {
			t.Fatal(copyErr, closeErr)
		}
		name := fmt.Sprintf("actions.runner.fleet.test-%d-%d", os.Getpid(), i)
		m, err := openNative()
		if err != nil {
			t.Fatal(err)
		}
		s, err := m.CreateRunnerService(name, serviceBinary(dir))
		_ = m.Close()
		if err != nil {
			t.Fatal(err)
		}
		_ = s.Close()
		// Always remove the service, even if the marker write or assertions fail.
		t.Cleanup(func() {
			m, e := openNative()
			if e == nil {
				defer func() { _ = m.Close() }()
				s, e := m.OpenService(name)
				if e == nil {
					_ = s.SetEnabled(false)
					_ = s.Stop()
					_ = s.Delete()
					_ = s.Close()
				}
			}
		})
		if err := os.WriteFile(filepath.Join(dir, ".service"), []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		err = Default.SetRunning(ctx, dir, true)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := Default.SetRunning(ctx, dirs[0], false); err != nil {
		t.Fatal(err)
	}
	if state, err := Default.Status(dirs[1]); err != nil || state != Running {
		t.Fatalf("other runner interrupted: %v %v", state, err)
	}
	m, err := openNative()
	if err != nil {
		t.Fatal(err)
	}
	s, err := Default.lookup(m, dirs[0])
	if err != nil {
		t.Fatal(err)
	}
	// A disabled service cannot be restarted by SCM after a reboot.
	if err := s.Start(); err == nil {
		t.Error("manually stopped service was not disabled")
	}
	_ = s.Close()
	_ = m.Close()
	for _, dir := range dirs {
		if err := Default.Remove(ctx, dir); err != nil {
			t.Fatal(err)
		}
	}
}
