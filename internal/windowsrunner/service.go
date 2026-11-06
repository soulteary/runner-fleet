//go:build windows || windowsrunner_portable

package windowsrunner

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var ErrServiceMissing = errors.New("runner Windows service is missing")
var ErrServiceDeleting = errors.New("runner Windows service deletion is pending")

type State string

const (
	Stopped State = "stopped"
	Running State = "running"
	Pending State = "pending"
)

// Service and Manager keep SCM interactions small and independently testable.
type Service interface {
	BinaryPath() (string, error)
	State() (State, error)
	SetEnabled(bool) error
	Start() error
	Stop() error
	Delete() error
	Close() error
}
type Manager interface {
	OpenService(string) (Service, error)
	Names() ([]string, error)
	CreateRunnerService(name, binary string) (Service, error)
	Close() error
}

type Controller struct {
	Open    func() (Manager, error)
	Protect func(string) error
	Cleanup func(string) error
}

var Default = Controller{Open: openNative}

var locks sync.Map

// Lock serializes registration, desired-state writes, reconciliation and deletion.
func Lock(dir string) func() {
	abs, _ := filepath.Abs(dir)
	key := strings.ToLower(filepath.Clean(abs))
	v, _ := locks.LoadOrStore(key, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

func serviceBinary(dir string) string { return filepath.Join(dir, "bin", "RunnerService.exe") }

func matchesBinary(command, dir, name string) bool {
	command = strings.TrimSpace(command)
	var binary, args string
	if strings.HasPrefix(command, "\"") {
		end := strings.Index(command[1:], "\"")
		if end < 0 {
			return false
		}
		binary, args = command[1:end+1], strings.TrimSpace(command[end+2:])
	} else {
		binary, args, _ = strings.Cut(command, " ")
		args = strings.TrimSpace(args)
	}
	// RunnerService.exe receives its SCM service name as its sole argument.
	if args != name && args != "\""+name+"\"" {
		return false
	}
	want, err := filepath.Abs(serviceBinary(dir))
	if err != nil {
		return false
	}
	got, err := filepath.Abs(binary)
	return err == nil && strings.EqualFold(filepath.Clean(got), filepath.Clean(want))
}

func (c Controller) lookup(m Manager, dir string) (Service, error) {
	marker, err := os.ReadFile(filepath.Join(dir, ".service"))
	if os.IsNotExist(err) {
		return nil, ErrServiceMissing
	}
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(string(marker))
	if !strings.HasPrefix(strings.ToLower(name), "actions.runner.") || strings.ContainsAny(name, "\r\n/\\\x00") {
		return nil, fmt.Errorf("invalid runner service marker")
	}
	s, err := m.OpenService(name)
	if err != nil {
		return nil, err
	}
	path, err := s.BinaryPath()
	if err != nil || !matchesBinary(path, dir, name) {
		_ = s.Close()
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("runner service executable does not match its installation directory")
	}
	return s, nil
}

func (c Controller) Status(dir string) (State, error) {
	m, err := c.Open()
	if err != nil {
		return "", err
	}
	defer func() { _ = m.Close() }()
	s, err := c.lookup(m, dir)
	if err != nil {
		return "", err
	}
	defer func() { _ = s.Close() }()
	return s.State()
}

// discover recovers the marker if configuration was interrupted after SCM
// created the service but before the official .service file was written.
func discover(ctx context.Context, m Manager, dir string) error {
	names, err := m.Names()
	if err != nil {
		return err
	}
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !strings.HasPrefix(strings.ToLower(name), "actions.runner.") {
			continue
		}
		s, err := m.OpenService(name)
		if err != nil {
			return err
		}
		binary, err := s.BinaryPath()
		_ = s.Close()
		if err != nil {
			return err
		}
		if matchesBinary(binary, dir, name) {
			return os.WriteFile(filepath.Join(dir, ".service"), []byte(name), 0600)
		}
	}
	return ErrServiceMissing
}

// Ensure repairs service installation after a successful GitHub registration.
// It never calls configure again and therefore cannot create another GitHub identity.
func (c Controller) Ensure(ctx context.Context, dir string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(dir, ".runner")); err != nil {
		return fmt.Errorf("runner is not registered: %w", err)
	}
	m, err := c.Open()
	if err != nil {
		return err
	}
	defer func() { _ = m.Close() }()
	s, err := c.lookup(m, dir)
	if err == nil {
		return s.Close()
	}
	if !errors.Is(err, ErrServiceMissing) {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := discover(ctx, m, dir); !errors.Is(err, ErrServiceMissing) {
		return err
	}
	if !Complete(dir) {
		return fmt.Errorf("registered runner is missing its Windows service executable")
	}
	protect := c.Protect
	if protect == nil {
		protect = ProtectRunner
	}
	if err := protect(dir); err != nil {
		return err
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	key := sha256.Sum256([]byte(strings.ToLower(abs)))
	name := fmt.Sprintf("actions.runner.fleet.%x", key[:12])
	s, err = m.CreateRunnerService(name, serviceBinary(abs))
	if err != nil {
		return err
	}
	defer func() { _ = s.Close() }()
	// Write before starting: RunnerService.exe reads this official marker.
	return os.WriteFile(filepath.Join(dir, ".service"), []byte(name), 0600)
}

// SetRunning applies the persisted intent to SCM. A stopped runner is disabled
// and has no recovery actions, so neither a reboot nor SCM recovery starts it.
func (c Controller) SetRunning(ctx context.Context, dir string, running bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m, err := c.Open()
	if err != nil {
		return err
	}
	defer func() { _ = m.Close() }()
	s, err := c.lookup(m, dir)
	if err != nil {
		return err
	}
	defer func() { _ = s.Close() }()
	if err := s.SetEnabled(running); err != nil {
		return err
	}
	state, err := s.State()
	if err != nil {
		return err
	}
	target := Stopped
	if running {
		target = Running
	}
	if state == target {
		return nil
	}
	if state == Pending {
		if err := waitSettled(ctx, s); err != nil {
			return err
		}
		state, err = s.State()
		if err != nil {
			return err
		}
		if state == target {
			return nil
		}
	}
	if running {
		if err := SyncRunnerProxy(dir); err != nil {
			return fmt.Errorf("configure runner proxy: %w", err)
		}
		err = s.Start()
	} else {
		err = s.Stop()
	}
	if err != nil {
		return err
	}
	for {
		state, err := s.State()
		if err != nil {
			return err
		}
		if state == target {
			return nil
		}
		if running && state == Stopped {
			return fmt.Errorf("runner service exited before reaching running state; inspect runner _diag logs")
		}
		if err := pause(ctx); err != nil {
			return fmt.Errorf("waiting for runner service to become %s: %w", target, err)
		}
	}
}

func waitSettled(ctx context.Context, s Service) error {
	for {
		state, err := s.State()
		if err != nil {
			return err
		}
		if state != Pending {
			return nil
		}
		if err := pause(ctx); err != nil {
			return err
		}
	}
}

func pause(ctx context.Context) error {
	timer := time.NewTimer(200 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (c Controller) Remove(ctx context.Context, dir string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// Removal must also find orphaned services before deleting their executable.
	m, err := c.Open()
	if err != nil {
		return err
	}
	if _, markerErr := os.Stat(filepath.Join(dir, ".service")); os.IsNotExist(markerErr) {
		if findErr := discover(ctx, m, dir); findErr != nil && !errors.Is(findErr, ErrServiceMissing) {
			_ = m.Close()
			return findErr
		}
	}
	_ = m.Close()
	err = c.SetRunning(ctx, dir, false)
	if err != nil && !errors.Is(err, ErrServiceMissing) && !errors.Is(err, ErrServiceDeleting) {
		return err
	}
	m, err = c.Open()
	if err != nil {
		return err
	}
	defer func() { _ = m.Close() }()
	s, err := c.lookup(m, dir)
	if err != nil && !errors.Is(err, ErrServiceMissing) && !errors.Is(err, ErrServiceDeleting) {
		return err
	}
	if err == nil {
		deleteErr := s.Delete()
		_ = s.Close()
		if deleteErr != nil {
			return deleteErr
		}
	}
	if marker, err := os.ReadFile(filepath.Join(dir, ".service")); err == nil {
		for {
			remaining, err := m.OpenService(strings.TrimSpace(string(marker)))
			if errors.Is(err, ErrServiceMissing) {
				break
			}
			if err != nil && !errors.Is(err, ErrServiceDeleting) {
				return err
			}
			if remaining != nil {
				_ = remaining.Close()
			}
			if err := pause(ctx); err != nil {
				return fmt.Errorf("waiting for runner service deletion: %w", err)
			}
		}
	}
	cleanup := c.Cleanup
	if cleanup == nil {
		cleanup = removeRunnerGroup
	}
	if err := cleanup(dir); err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(dir, ".service")); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
