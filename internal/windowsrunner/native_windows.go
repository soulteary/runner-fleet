package windowsrunner

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unsafe"

	"github.com/soulteary/runner-fleet/internal/childenv"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

type nativeManager struct{ *mgr.Mgr }
type nativeService struct{ *mgr.Service }

func openNative() (Manager, error) {
	m, err := mgr.Connect()
	if err != nil {
		return nil, err
	}
	return &nativeManager{m}, nil
}
func (m *nativeManager) Close() error             { return m.Disconnect() }
func (m *nativeManager) Names() ([]string, error) { return m.ListServices() }
func (m *nativeManager) OpenService(name string) (Service, error) {
	s, err := m.Mgr.OpenService(name)
	if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
		return nil, ErrServiceMissing
	}
	if errors.Is(err, windows.ERROR_SERVICE_MARKED_FOR_DELETE) {
		return nil, ErrServiceDeleting
	}
	if err != nil {
		return nil, err
	}
	return &nativeService{s}, nil
}
func (m *nativeManager) CreateRunnerService(name, binary string) (Service, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	initCmd := exec.CommandContext(ctx, binary, "init")
	initCmd.Env = childenv.Environ()
	if out, err := initCmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("initialize official runner event source: %w: %s", err, out)
	}
	s, err := m.CreateService(name, binary, mgr.Config{
		DisplayName: "GitHub Actions Runner (Runner Fleet)", StartType: mgr.StartAutomatic,
		ServiceStartName: `NT AUTHORITY\NETWORK SERVICE`,
	}, name)
	if err != nil {
		return nil, err
	}
	return &nativeService{s}, nil
}
func (s *nativeService) BinaryPath() (string, error) {
	c, err := s.Config()
	return c.BinaryPathName, err
}
func (s *nativeService) State() (State, error) {
	st, err := s.Query()
	if err != nil {
		return "", err
	}
	switch st.State {
	case svc.Running:
		return Running, nil
	case svc.Stopped:
		return Stopped, nil
	case svc.StartPending, svc.StopPending, svc.ContinuePending, svc.PausePending:
		return Pending, nil
	default:
		return "", fmt.Errorf("unsupported runner service state %d", st.State)
	}
}
func (s *nativeService) SetEnabled(enable bool) error {
	c, err := s.Config()
	if err != nil {
		return err
	}
	c.StartType = mgr.StartDisabled
	if enable {
		c.StartType = mgr.StartAutomatic
	}
	if err := s.UpdateConfig(c); err != nil {
		return err
	}
	if !enable {
		if err := s.ResetRecoveryActions(); err != nil {
			return err
		}
		return s.SetRecoveryActionsOnNonCrashFailures(false)
	}
	if err := s.SetRecoveryActions([]mgr.RecoveryAction{{Type: mgr.ServiceRestart, Delay: 5 * time.Second}}, 86400); err != nil {
		return err
	}
	return s.SetRecoveryActionsOnNonCrashFailures(true)
}
func (s *nativeService) Start() error {
	err := s.Service.Start()
	if errors.Is(err, windows.ERROR_SERVICE_ALREADY_RUNNING) {
		return nil
	}
	return err
}
func (s *nativeService) Stop() error {
	_, err := s.Control(svc.Stop)
	if errors.Is(err, windows.ERROR_SERVICE_NOT_ACTIVE) {
		return nil
	}
	return err
}

// Protected DACLs prevent Runner jobs (NetworkService) from reading Manager
// configuration/PATs or modifying the executable that LocalSystem runs.
func ProtectManager(dir string) error { return protect(dir, "D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)") }
func ProtectRunner(dir string) error {
	return protect(dir, "D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;FA;;;NS)")
}
func protect(dir, sddl string) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
}

func removeRunnerGroup(dir string) error {
	// Matches NativeWindowsServiceHelper.GetUniqueRunnerGroupName in actions/runner.
	abs, err := filepath.Abs(filepath.Join(dir, "bin"))
	if err != nil {
		return err
	}
	hash := sha256.Sum256([]byte(strings.ToLower(abs)))
	name, err := windows.UTF16PtrFromString(fmt.Sprintf("GITHUB_ActionsRunner_G%x", hash[:])[:len("GITHUB_ActionsRunner_G")+5])
	if err != nil {
		return err
	}
	proc := windows.NewLazySystemDLL("netapi32.dll").NewProc("NetLocalGroupDel")
	r, _, _ := proc.Call(0, uintptr(unsafe.Pointer(name)))
	if r == 0 || r == 2220 {
		return nil
	} // NERR_GroupNotFound
	return fmt.Errorf("remove runner local group: Windows error %d", r)
}
