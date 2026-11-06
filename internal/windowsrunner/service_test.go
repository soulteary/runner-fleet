//go:build windows || windowsrunner_portable

package windowsrunner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeService struct {
	binary        string
	state         State
	enabled       bool
	starts, stops int
	deleted       bool
	fail          map[string]error
	pending       bool
	states        []State
}

func (s *fakeService) BinaryPath() (string, error) { return s.binary, s.fail["config"] }
func (s *fakeService) State() (State, error) {
	if len(s.states) > 0 {
		s.state = s.states[0]
		s.states = s.states[1:]
	}
	return s.state, s.fail["query"]
}
func (s *fakeService) SetEnabled(v bool) error {
	if err := s.fail["enable"]; err != nil {
		return err
	}
	s.enabled = v
	return nil
}
func (s *fakeService) Start() error {
	if err := s.fail["start"]; err != nil {
		return err
	}
	s.starts++
	if !s.pending {
		s.state = Running
	}
	return nil
}
func (s *fakeService) Stop() error {
	if err := s.fail["stop"]; err != nil {
		return err
	}
	s.stops++
	if !s.pending {
		s.state = Stopped
	}
	return nil
}
func (s *fakeService) Delete() error {
	if err := s.fail["delete"]; err != nil {
		return err
	}
	s.deleted = true
	return nil
}
func (s *fakeService) Close() error { return nil }

type fakeManager struct {
	services map[string]*fakeService
	creates  int
	fail     error
}

func (m *fakeManager) OpenService(name string) (Service, error) {
	if m.fail != nil {
		return nil, m.fail
	}
	s, ok := m.services[name]
	if !ok || s.deleted {
		return nil, ErrServiceMissing
	}
	return s, nil
}
func (m *fakeManager) Names() ([]string, error) {
	var names []string
	for n := range m.services {
		names = append(names, n)
	}
	return names, m.fail
}
func (m *fakeManager) CreateRunnerService(name, binary string) (Service, error) {
	if m.fail != nil {
		return nil, m.fail
	}
	m.creates++
	s := &fakeService{binary: "\"" + binary + "\" " + name, state: Stopped, fail: map[string]error{}}
	m.services[name] = s
	return s, nil
}
func (m *fakeManager) Close() error { return nil }

func serviceFixture(t *testing.T) (Controller, *fakeManager, *fakeService, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "构建 space")
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{".runner", "config.cmd", "bin/Runner.Listener.exe", "bin/RunnerService.exe"} {
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(f)), []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	name := "actions.runner.owner.repo.demo"
	if err := os.WriteFile(filepath.Join(dir, ".service"), []byte(name), 0600); err != nil {
		t.Fatal(err)
	}
	s := &fakeService{binary: "\"" + serviceBinary(dir) + "\" " + name, state: Stopped, fail: map[string]error{}}
	m := &fakeManager{services: map[string]*fakeService{name: s}}
	c := Controller{Open: func() (Manager, error) { return m, nil }, Protect: func(string) error { return nil }, Cleanup: func(string) error { return nil }}
	return c, m, s, dir
}

func TestMatchesBinaryOfficialAndNamedCommands(t *testing.T) {
	name := "actions.runner.owner.repo.demo"
	// No filesystem access is needed here; keep this path free of whitespace
	// even when the test user's temporary directory contains spaces.
	plainDir := filepath.Join(string(filepath.Separator), "RunnerFleet", "runners", "demo")
	for _, suffix := range []string{"", " " + name, " \"" + name + "\""} {
		if !matchesBinary(serviceBinary(plainDir)+suffix, plainDir, name) {
			t.Fatalf("unquoted executable rejected with suffix %q", suffix)
		}
	}
	for _, path := range []string{"runner", "构建 space"} {
		dir := filepath.Join(t.TempDir(), path)
		binary := serviceBinary(dir)
		quoted := "\"" + binary + "\""
		for _, tt := range []struct {
			name, command string
			want          bool
		}{
			{"official", quoted, true},
			{"official whitespace", " \t" + quoted + "\t ", true},
			{"named", quoted + " " + name, true},
			{"quoted name", quoted + " \"" + name + "\"", true},
			{"case insensitive path", "\"" + strings.ToUpper(binary) + "\"", true},
			{"foreign directory", "\"" + serviceBinary(dir+"-other") + "\"", false},
			{"foreign executable", "\"" + filepath.Join(dir, "bin", "Runner.Listener.exe") + "\"", false},
			{"wrong service", quoted + " actions.runner.other", false},
			{"extra argument", quoted + " " + name + " extra", false},
			{"quoted extra argument", quoted + " \"" + name + "\" extra", false},
			{"empty argument", quoted + " \"\"", false},
			{"unterminated quote", "\"" + binary, false},
		} {
			t.Run(path+"/"+tt.name, func(t *testing.T) {
				if got := matchesBinary(tt.command, dir, name); got != tt.want {
					t.Fatalf("matchesBinary(%q) = %v, want %v", tt.command, got, tt.want)
				}
			})
		}
	}
}

func TestControllerReusesOfficialService(t *testing.T) {
	for _, marker := range []bool{true, false} {
		t.Run(fmt.Sprintf("marker=%v", marker), func(t *testing.T) {
			c, m, s, dir := serviceFixture(t)
			s.binary = "\"" + serviceBinary(dir) + "\""
			s.state = Running
			if !marker {
				if err := os.Remove(filepath.Join(dir, ".service")); err != nil {
					t.Fatal(err)
				}
			}
			for range 2 {
				if err := c.Ensure(context.Background(), dir); err != nil {
					t.Fatal(err)
				}
				if state, err := c.Status(dir); err != nil || state != Running {
					t.Fatalf("official service not recognized: %s, %v", state, err)
				}
			}
			if m.creates != 0 || s.starts+s.stops != 0 || s.deleted || s.enabled {
				t.Fatal("existing service was modified or recreated")
			}
			if data, err := os.ReadFile(filepath.Join(dir, ".runner")); err != nil || string(data) != "fixture" {
				t.Fatal("registration changed", err)
			}
		})
	}
}

func TestControllerRejectsInvalidMarkersForOfficialService(t *testing.T) {
	for _, marker := range []string{"UnrelatedSystemService", "actions.runner.demo\nextra", "actions.runner.demo/extra", `actions.runner.demo\extra`, "actions.runner.demo\x00"} {
		t.Run(marker, func(t *testing.T) {
			c, m, s, dir := serviceFixture(t)
			s.binary = "\"" + serviceBinary(dir) + "\""
			if err := os.WriteFile(filepath.Join(dir, ".service"), []byte(marker), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := c.Status(dir); err == nil {
				t.Fatal("invalid service marker accepted")
			}
			if err := c.Ensure(context.Background(), dir); err == nil {
				t.Fatal("invalid service marker repaired instead of rejected")
			}
			if m.creates != 0 || s.starts+s.stops != 0 || s.deleted {
				t.Fatal("invalid service marker caused a service mutation")
			}
		})
	}
}

func TestControllerLifecycleIsIdempotentAndIndependent(t *testing.T) {
	c, m, s, dir := serviceFixture(t)
	other := &fakeService{state: Running}
	m.services["actions.runner.other"] = other
	ctx := context.Background()
	for range 2 {
		if err := c.SetRunning(ctx, dir, true); err != nil {
			t.Fatal(err)
		}
	}
	if s.starts != 1 || !s.enabled {
		t.Fatal("duplicated start or failed to enable boot recovery")
	}
	for range 2 {
		if err := c.SetRunning(ctx, dir, false); err != nil {
			t.Fatal(err)
		}
	}
	if s.stops != 1 || s.enabled {
		t.Fatal("stop is not persistent/idempotent")
	}
	if other.state != Running {
		t.Fatal("stopped another instance")
	}
	if err := c.Ensure(ctx, dir); err != nil || m.creates != 0 {
		t.Fatalf("recreated registered service: %v", err)
	}
	if state, err := c.Status(dir); err != nil || state != Stopped {
		t.Fatal(state, err)
	}
	if err := c.Remove(ctx, dir); err != nil {
		t.Fatal(err)
	}
	if !s.deleted {
		t.Fatal("service not deleted")
	}
	if err := c.Remove(ctx, dir); err != nil {
		t.Fatalf("removal retry: %v", err)
	}
}

func TestControllerRepairsServiceWithoutRegisteringAgain(t *testing.T) {
	for _, orphan := range []bool{true, false} {
		c, m, _, dir := serviceFixture(t)
		if err := os.Remove(filepath.Join(dir, ".service")); err != nil {
			t.Fatal(err)
		}
		if !orphan {
			m.services = map[string]*fakeService{}
		}
		if err := c.Ensure(context.Background(), dir); err != nil {
			t.Fatal(err)
		}
		if orphan && m.creates != 0 || !orphan && m.creates != 1 {
			t.Fatalf("incorrect recovery: orphan=%v creates=%d", orphan, m.creates)
		}
		if _, err := c.Status(dir); err != nil {
			t.Fatal(err)
		}
		if data, err := os.ReadFile(filepath.Join(dir, ".runner")); err != nil || string(data) != "fixture" {
			t.Fatal("registration changed during repair")
		}
	}
}

func TestControllerRefusesForeignServiceAndUnknownStatus(t *testing.T) {
	for _, bad := range []string{"foreign executable", "wrong service argument", "bad marker", "config", "query", "enable", "start", "stop", "delete"} {
		t.Run(bad, func(t *testing.T) {
			c, _, s, dir := serviceFixture(t)
			if bad == "foreign executable" {
				s.binary = `"C:\Other\RunnerService.exe" actions.runner.owner.repo.demo`
			}
			if bad == "wrong service argument" {
				s.binary = "\"" + serviceBinary(dir) + "\" actions.runner.other"
			}
			if bad == "bad marker" {
				if err := os.WriteFile(filepath.Join(dir, ".service"), []byte("UnrelatedSystemService"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if bad == "config" || bad == "query" || bad == "enable" || bad == "start" || bad == "stop" || bad == "delete" {
				s.fail[bad] = errors.New("access denied")
			}
			if bad == "stop" {
				s.state = Running
			}
			var err error
			if bad == "delete" {
				err = c.Remove(context.Background(), dir)
			} else {
				err = c.SetRunning(context.Background(), dir, bad != "stop")
			}
			if err == nil {
				t.Fatal("operation falsely succeeded")
			}
			if bad == "foreign executable" || bad == "bad marker" {
				if s.starts+s.stops != 0 || s.deleted {
					t.Fatal("modified unverified service")
				}
			}
		})
	}
}

func TestControllerWaitsWithDeadlineAndSerializesConcurrentStarts(t *testing.T) {
	c, _, s, dir := serviceFixture(t)
	s.state = Pending
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	if err := c.SetRunning(ctx, dir, true); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("pending service did not time out: %v", err)
	}
	s.state = Stopped
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			unlock := Lock(dir)
			defer unlock()
			if err := c.SetRunning(context.Background(), dir, true); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if s.starts != 1 {
		t.Fatalf("concurrent requests started service %d times", s.starts)
	}
}

func TestControllerRepairAndRemovalFailuresPreserveIdentity(t *testing.T) {
	c, m, s, dir := serviceFixture(t)
	m.fail = errors.New("SCM unavailable")
	if _, err := c.Status(dir); err == nil {
		t.Fatal("SCM failure reported stopped")
	}
	if err := c.Ensure(context.Background(), dir); err == nil {
		t.Fatal("SCM failure ignored")
	}
	m.fail = nil
	c.Cleanup = func(string) error { return errors.New("cleanup denied") }
	if err := c.Remove(context.Background(), dir); err == nil {
		t.Fatal("cleanup error ignored")
	}
	if _, err := os.Stat(filepath.Join(dir, ".runner")); err != nil {
		t.Fatal("deleted registration on failure")
	}
	s.deleted = false
	if err := os.Remove(filepath.Join(dir, ".service")); err != nil {
		t.Fatal(err)
	}
	m.services = map[string]*fakeService{}
	c.Protect = func(string) error { return errors.New("ACL denied") }
	if err := c.Ensure(context.Background(), dir); err == nil || !strings.Contains(err.Error(), "ACL denied") {
		t.Fatal(err)
	}
}

func TestControllerPendingTransitionsAndLostMarkerRemoval(t *testing.T) {
	c, _, s, dir := serviceFixture(t)
	s.states = []State{Pending, Stopped, Stopped, Pending, Running}
	if err := c.SetRunning(context.Background(), dir, true); err != nil {
		t.Fatal(err)
	}
	s.states = []State{Pending, Stopped, Stopped}
	if err := c.SetRunning(context.Background(), dir, false); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, ".service")); err != nil {
		t.Fatal(err)
	}
	if err := c.Remove(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	if !s.deleted {
		t.Fatal("orphaned service survived removal")
	}
}

func TestControllerCanceledOperationsDoNotChangeServices(t *testing.T) {
	c, _, s, dir := serviceFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, op := range []func() error{
		func() error { return c.Ensure(ctx, dir) }, func() error { return c.SetRunning(ctx, dir, true) }, func() error { return c.Remove(ctx, dir) },
	} {
		if err := op(); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	}
	if s.starts+s.stops != 0 || s.deleted {
		t.Fatal("canceled operation changed service")
	}
}

func TestControllerRejectsMalformedMarkerAndIncompleteRepair(t *testing.T) {
	c, m, s, dir := serviceFixture(t)
	if err := os.Remove(filepath.Join(dir, ".service")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, ".service"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Status(dir); err == nil {
		t.Fatal("directory accepted as service marker")
	}
	if err := os.Remove(filepath.Join(dir, ".service")); err != nil {
		t.Fatal(err)
	}
	m.services = map[string]*fakeService{}
	if err := os.Remove(filepath.Join(dir, "bin", "RunnerService.exe")); err != nil {
		t.Fatal(err)
	}
	if err := c.Ensure(context.Background(), dir); err == nil {
		t.Fatal("created service with missing executable")
	}
	if err := os.Remove(filepath.Join(dir, ".runner")); err != nil {
		t.Fatal(err)
	}
	if err := c.Ensure(context.Background(), dir); err == nil {
		t.Fatal("repaired an unregistered runner")
	}
	_ = s
}

func TestControllerRejectsInvalidProxyBeforeStartingService(t *testing.T) {
	c, _, s, dir := serviceFixture(t)
	content := proxyBegin + "\nhttps_proxy=http://localhost:7890\n"
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	if err := c.SetRunning(context.Background(), dir, true); err == nil || !strings.Contains(err.Error(), "proxy") {
		t.Fatalf("invalid proxy configuration did not fail startup: %v", err)
	}
	if s.starts != 0 {
		t.Fatal("service started despite invalid proxy configuration")
	}
	b, err := os.ReadFile(filepath.Join(dir, ".env"))
	if err != nil || string(b) != content {
		t.Fatal("invalid proxy configuration was overwritten", err)
	}
}
