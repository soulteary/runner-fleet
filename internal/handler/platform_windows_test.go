package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/soulteary/runner-fleet/internal/config"
	"github.com/soulteary/runner-fleet/internal/runner"
	"github.com/soulteary/runner-fleet/internal/windowsrunner"
)

// Reuse the test executable as a deterministic Runner.Listener.exe. This
// exercises Windows process arguments and working directories without GitHub.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "configure" {
		var token, account string
		unattended, service := false, false
		for i, arg := range os.Args {
			if arg == "--token" && i+1 < len(os.Args) {
				token = os.Args[i+1]
			}
			if arg == "--windowslogonaccount" && i+1 < len(os.Args) {
				account = os.Args[i+1]
			}
			if arg == "--unattended" {
				unattended = true
			}
			if arg == "--runasservice" {
				service = true
			}
		}
		wantAccount, accountErr := windowsrunner.RunnerLogonAccount()
		if accountErr != nil || account != wantAccount || !unattended || !service || os.Getenv("BASIC_AUTH_PASSWORD") != "" {
			os.Exit(3)
		}
		f, err := os.OpenFile(".configure-attempts", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			os.Exit(4)
		}
		_, _ = f.WriteString("configure\n")
		_ = f.Close()
		if token == "EXPIRED" {
			fmt.Fprintln(os.Stderr, "registration token EXPIRED is expired")
			os.Exit(1)
		}
		if err := os.WriteFile(".runner", []byte(`{"agentId":42}`), 0600); err != nil {
			os.Exit(5)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

type httpWindowsService struct {
	binary  string
	state   windowsrunner.State
	enabled bool
	starts  int
	fail    error
	deleted bool
}

func (s *httpWindowsService) BinaryPath() (string, error)         { return s.binary, nil }
func (s *httpWindowsService) State() (windowsrunner.State, error) { return s.state, s.fail }
func (s *httpWindowsService) SetEnabled(v bool) error             { s.enabled = v; return nil }
func (s *httpWindowsService) Start() error                        { s.starts++; s.state = windowsrunner.Running; return nil }
func (s *httpWindowsService) Stop() error                         { s.state = windowsrunner.Stopped; return nil }
func (s *httpWindowsService) Delete() error                       { s.deleted = true; return nil }
func (s *httpWindowsService) Close() error                        { return nil }

type httpWindowsManager struct{ service *httpWindowsService }

func (m httpWindowsManager) OpenService(string) (windowsrunner.Service, error) {
	if m.service.deleted {
		return nil, windowsrunner.ErrServiceMissing
	}
	return m.service, nil
}
func (m httpWindowsManager) Names() ([]string, error) { return []string{"actions.runner.demo"}, nil }
func (m httpWindowsManager) CreateRunnerService(string, string) (windowsrunner.Service, error) {
	return nil, errors.New("unexpected service creation")
}
func (m httpWindowsManager) Close() error { return nil }

func windowsAPIConfig(t *testing.T) (*config.Config, *httpWindowsService, string) {
	t.Helper()
	oldPath, oldController, oldSecrets := ConfigPath, windowsrunner.Default, Secrets
	t.Cleanup(func() { ConfigPath = oldPath; windowsrunner.Default = oldController; Secrets = oldSecrets })
	root := t.TempDir()
	ConfigPath = filepath.Join(root, "config.yaml")
	Secrets = nil
	cfg := &config.Config{Runners: config.RunnersConfig{BasePath: filepath.Join(root, "runners space 中文"), Items: []config.RunnerItem{{Name: "demo", TargetType: "repo", Target: "owner/repo"}}}}
	if err := cfg.Save(ConfigPath); err != nil {
		t.Fatal(err)
	}
	dir := cfg.Runners.Items[0].InstallPath(cfg.Runners.BasePath)
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{".runner", "config.cmd", "bin/Runner.Listener.exe", "bin/RunnerService.exe"} {
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(name)), []byte("registered fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, ".service"), []byte("actions.runner.demo"), 0600); err != nil {
		t.Fatal(err)
	}
	s := &httpWindowsService{binary: "\"" + filepath.Join(dir, "bin", "RunnerService.exe") + "\" actions.runner.demo", state: windowsrunner.Running}
	windowsrunner.Default = windowsrunner.Controller{Open: func() (windowsrunner.Manager, error) { return httpWindowsManager{s}, nil }, Protect: func(string) error { return nil }, Cleanup: func(string) error { return nil }}
	return cfg, s, dir
}

func windowsAPICall(fn echo.HandlerFunc, body string) *httptest.ResponseRecorder {
	e := echo.New()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	c := e.NewContext(req, rec)
	c.SetParamNames("name")
	c.SetParamValues("demo")
	if err := fn(c); err != nil {
		e.HTTPErrorHandler(err, c)
	}
	return rec
}

func TestWindowsStopPersistsAcrossReloadReconcileAndEdit(t *testing.T) {
	_, s, _ := windowsAPIConfig(t)
	if rec := windowsAPICall(StopRunner, ""); rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	fresh, err := config.Load(ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Runners.Items[0].WantsRunning() || s.enabled || s.state != windowsrunner.Stopped {
		t.Fatal("stop intent not applied")
	}
	runner.ReconcileWindows(context.Background(), ConfigPath)
	if s.starts != 0 {
		t.Fatal("reconciler restarted intentionally stopped runner")
	}
	if rec := windowsAPICall(UpdateRunner, `{"target_type":"repo","target":"owner/repo","labels":["edited"]}`); rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	fresh, err = config.Load(ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Runners.Items[0].WantsRunning() || s.starts != 0 {
		t.Fatal("edit lost manual stop")
	}
	if rec := windowsAPICall(StartRunner, ""); rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	if rec := windowsAPICall(StartRunner, ""); rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	if s.starts != 1 || !s.enabled {
		t.Fatal("repeated start duplicated runner")
	}
}

func TestWindowsRegistrationRetryPreservesGitHubIdentity(t *testing.T) {
	cfg, s, dir := windowsAPIConfig(t)
	s.state = windowsrunner.Stopped
	j := registrationJob{BasePath: cfg.Runners.BasePath, InstallDir: dir, RunnerName: "demo", URL: "https://github.com/owner/repo", Token: "SECRET"}
	for range 2 {
		runWindowsRegistrationJob(j)
	}
	if s.starts != 1 {
		t.Fatal("registration retry duplicated runner")
	}
	data, err := os.ReadFile(filepath.Join(dir, ".runner"))
	if err != nil || string(data) != "registered fixture" {
		t.Fatal("registration was overwritten")
	}
	s.fail = errors.New("SCM denied SECRET")
	runWindowsRegistrationJob(j)
	data, err = os.ReadFile(filepath.Join(dir, runner.RegistrationResultFile))
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Success bool
		Message string
	}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	if result.Success || strings.Contains(result.Message, "SECRET") {
		t.Fatal("failure hidden or token exposed", result)
	}
	if info := runner.GetByName(cfg, "demo"); info.Status != runner.StatusUnknown {
		t.Fatal("unknown service reported as stopped")
	}
}

func TestWindowsOfficialServiceRegistrationRetryReusesIdentity(t *testing.T) {
	cfg, s, dir := windowsAPIConfig(t)
	s.binary = "\"" + filepath.Join(dir, "bin", "RunnerService.exe") + "\""
	writeRegistrationResult(dir, false, "registered; Windows service setup failed; click Start to retry")
	j := registrationJob{BasePath: cfg.Runners.BasePath, InstallDir: dir, RunnerName: "demo", Token: "unused"}
	for range 2 {
		runWindowsRegistrationJob(j)
	}
	info := runner.GetByName(cfg, "demo")
	if !info.Running || info.Probe != nil || info.RegistrationMessage != "registered; Windows service configured" {
		t.Fatalf("official service not recognized: %+v", info)
	}
	if data, err := os.ReadFile(filepath.Join(dir, ".runner")); err != nil || string(data) != "registered fixture" {
		t.Fatal("registration was overwritten", err)
	}
	if s.starts != 0 || s.deleted {
		t.Fatal("running official service was restarted or deleted")
	}
}

func TestWindowsStartUpdatesHistoricalResultOnlyAfterConfirmedSuccess(t *testing.T) {
	for _, failure := range []string{"", "path", "query"} {
		t.Run("failure="+failure, func(t *testing.T) {
			cfg, s, dir := windowsAPIConfig(t)
			s.binary = "\"" + filepath.Join(dir, "bin", "RunnerService.exe") + "\""
			if failure == "path" {
				s.binary = "\"" + filepath.Join(dir, "bin", "Other.exe") + "\""
			}
			if failure == "query" {
				s.fail = errors.New("SCM query denied")
			}
			const previous = "registered; Windows service setup failed; click Start to retry"
			writeRegistrationResult(dir, false, previous)
			before, err := os.ReadFile(filepath.Join(dir, runner.RegistrationResultFile))
			if err != nil {
				t.Fatal(err)
			}
			// A status probe alone must not rewrite historical errors.
			if info := runner.GetByName(cfg, "demo"); info.RegistrationMessage != previous {
				t.Fatal("probe hid historical error", info)
			}
			rec := windowsAPICall(StartRunner, "")
			after, err := os.ReadFile(filepath.Join(dir, runner.RegistrationResultFile))
			if err != nil {
				t.Fatal(err)
			}
			if failure != "" {
				if rec.Code != 500 || string(after) != string(before) {
					t.Fatalf("failed repair changed historical result: %d %s", rec.Code, after)
				}
			} else {
				var result struct {
					Success bool
					Message string
				}
				if err := json.Unmarshal(after, &result); err != nil {
					t.Fatal(err)
				}
				if rec.Code != 200 || !result.Success || result.Message != "registered; Windows service configured" {
					t.Fatalf("successful repair left historical error: %d %s", rec.Code, after)
				}
			}
			if s.starts != 0 || s.deleted {
				t.Fatal("running service was restarted or deleted")
			}
		})
	}
}

func TestWindowsDeleteCleansServiceBeforeDirectory(t *testing.T) {
	_, s, dir := windowsAPIConfig(t)
	if rec := windowsAPICall(RemoveRunnerByName, ""); rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	if !s.deleted {
		t.Fatal("service was left installed")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("runner directory was left behind")
	}
	cfg, err := config.Load(ConfigPath)
	if err != nil || len(cfg.Runners.Items) != 0 {
		t.Fatal("runner configuration was left behind", err)
	}
}

func TestWindowsExpiredTokenThenSuccessfulRegistrationInUnicodeDirectory(t *testing.T) {
	cfg, s, dir := windowsAPIConfig(t)
	s.state = windowsrunner.Stopped
	if err := os.Remove(filepath.Join(dir, ".runner")); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bin", "Runner.Listener.exe"), data, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BASIC_AUTH_PASSWORD", "must-not-leak")
	j := registrationJob{BasePath: cfg.Runners.BasePath, InstallDir: dir, RunnerName: "demo", URL: "https://github.com/owner/repo", Token: "EXPIRED"}
	runWindowsRegistrationJob(j)
	result, err := os.ReadFile(filepath.Join(dir, runner.RegistrationResultFile))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(result), "EXPIRED") || !strings.Contains(string(result), "fresh GitHub registration token") {
		t.Fatal("expired token diagnostic is missing or token leaked", string(result))
	}
	if _, err := os.Stat(filepath.Join(dir, ".runner")); !os.IsNotExist(err) {
		t.Fatal("failed registration created identity")
	}
	if s.starts != 0 {
		t.Fatal("started runner after failed registration")
	}
	j.Token = "VALID"
	runWindowsRegistrationJob(j)
	runWindowsRegistrationJob(j)
	if s.starts != 1 {
		t.Fatal("successful retry did not start exactly one runner", s.starts)
	}
	attempts, err := os.ReadFile(filepath.Join(dir, ".configure-attempts"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(attempts), "configure") != 2 {
		t.Fatal("reconfigured existing GitHub identity", string(attempts))
	}
}
