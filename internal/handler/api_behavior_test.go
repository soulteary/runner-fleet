package handler

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/soulteary/runner-fleet/internal/config"
	"github.com/soulteary/runner-fleet/internal/runner"
)

func behaviorConfig(t *testing.T) *config.Config {
	t.Helper()
	old := ConfigPath
	ConfigPath = filepath.Join(t.TempDir(), "config.yaml")
	t.Cleanup(func() { ConfigPath = old })
	cfg := &config.Config{Server: config.ServerConfig{Port: 8080}, Runners: config.RunnersConfig{BasePath: t.TempDir(), Items: []config.RunnerItem{{Name: "demo", TargetType: "repo", Target: "owner/repo"}}}}
	if err := cfg.Save(ConfigPath); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func behaviorCall(fn echo.HandlerFunc, name, body string) *httptest.ResponseRecorder {
	e := echo.New()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	c := e.NewContext(req, rec)
	c.SetParamNames("name")
	c.SetParamValues(name)
	if err := fn(c); err != nil {
		e.HTTPErrorHandler(err, c)
	}
	return rec
}

func TestAPIRejectsInvalidIdentityAndUnavailableConfiguration(t *testing.T) {
	behaviorConfig(t)
	funcs := []echo.HandlerFunc{GetRunner, StartRunner, StopRunner, RecreateRunner, RemoveRunnerByName, UpdateRunner}
	for i, fn := range funcs {
		for _, name := range []string{"", ".."} {
			rec := behaviorCall(fn, name, `{"target_type":"repo","target":"owner/repo"}`)
			if rec.Code != 400 {
				t.Fatalf("handler %d name %q: %d %s", i, name, rec.Code, rec.Body.String())
			}
		}
	}
	for _, fn := range []echo.HandlerFunc{GetRunner, StartRunner, StopRunner, RemoveRunnerByName, UpdateRunner} {
		rec := behaviorCall(fn, "absent", `{"target_type":"repo","target":"owner/repo"}`)
		if rec.Code != 404 {
			t.Fatalf("missing runner: %d %s", rec.Code, rec.Body.String())
		}
	}
	if err := os.WriteFile(ConfigPath, []byte("invalid: ["), 0600); err != nil {
		t.Fatal(err)
	}
	for _, fn := range []echo.HandlerFunc{ListRunners, GetRunner, StartRunner, StopRunner, RecreateRunner, AddRunner, RemoveRunnerByName, Index, RunnerRows} {
		rec := behaviorCall(fn, "demo", `{}`)
		if rec.Code != 500 {
			t.Fatalf("broken configuration: %d %s", rec.Code, rec.Body.String())
		}
	}
}

type behaviorRenderer struct {
	name string
	data map[string]any
}

func (r *behaviorRenderer) Render(w io.Writer, name string, data interface{}, _ echo.Context) error {
	r.name = name
	r.data = data.(map[string]any)
	_, err := io.WriteString(w, "rendered")
	return err
}

func TestViewsUseCurrentRunnerStateAndTranslationFallback(t *testing.T) {
	cfg := behaviorConfig(t)
	dir := cfg.Runners.Items[0].InstallPath(cfg.Runners.BasePath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".runner"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	old := I18nLoader
	t.Cleanup(func() { I18nLoader = old; ResetI18nCache() })
	for _, fallback := range []bool{false, true} {
		I18nLoader = func(lang string) (map[string]string, error) {
			if fallback && lang == "en" {
				return map[string]string{"title": "<safe>"}, nil
			}
			return nil, errors.New("unavailable language")
		}
		ResetI18nCache()
		for _, tc := range []struct {
			fn   echo.HandlerFunc
			name string
		}{{Index, "index.html"}, {RunnerRows, "runnerRows"}} {
			r := &behaviorRenderer{}
			e := echo.New()
			e.Renderer = r
			rec := httptest.NewRecorder()
			c := e.NewContext(httptest.NewRequest("GET", "/?lang=fr", nil), rec)
			if err := tc.fn(c); err != nil {
				t.Fatal(err)
			}
			list := r.data["Runners"].([]runner.RunnerInfo)
			if rec.Code != 200 || r.name != tc.name || len(list) != 1 || list[0].Status != runner.StatusInstalled {
				t.Fatalf("rendered invalid view: %s %#v", r.name, r.data)
			}
			translations := r.data["T"].(map[string]string)
			if fallback && translations["title"] != "<safe>" {
				t.Fatal(translations)
			}
			if !fallback && len(translations) != 0 {
				t.Fatal(translations)
			}
		}
	}
}

func TestAddRegistrationQueuePreservesAcceptedState(t *testing.T) {
	for _, script := range []bool{false, true} {
		for _, full := range []bool{false, true} {
			t.Run(strings.Join([]string{map[bool]string{true: "existing-script", false: "install-first"}[script], map[bool]string{true: "full", false: "available"}[full]}, "/"), func(t *testing.T) {
				cfg := behaviorConfig(t)
				cfg.Runners.Items = nil
				if err := cfg.Save(ConfigPath); err != nil {
					t.Fatal(err)
				}
				oldQueue, oldScript := registrationQueue, installRunnerScriptPath
				t.Cleanup(func() { registrationQueue = oldQueue; installRunnerScriptPath = oldScript })
				registrationQueue = make(chan registrationJob, 1)
				if full {
					registrationQueue <- registrationJob{RunnerName: "occupied"}
				}
				installRunnerScriptPath = filepath.Join(t.TempDir(), "install.sh")
				if err := os.WriteFile(installRunnerScriptPath, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
					t.Fatal(err)
				}
				dir := filepath.Join(cfg.Runners.BasePath, "added")
				if script {
					if err := os.MkdirAll(dir, 0700); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(dir, runner.ConfigScriptName()), nil, 0700); err != nil {
						t.Fatal(err)
					}
				}
				body := `{"name":"added","target_type":"repo","target":"owner/repo","registration_token":"fixture-token","labels":[" linux "]}`
				rec := behaviorCall(AddRunner, "", body)
				want := 200
				if full {
					want = 503
				}
				if rec.Code != want {
					t.Fatalf("%d %s", rec.Code, rec.Body.String())
				}
				persisted, err := config.Load(ConfigPath)
				if err != nil {
					t.Fatal(err)
				}
				if len(persisted.Runners.Items) != 1 || persisted.Runners.Items[0].Name != "added" {
					t.Fatalf("lost accepted config: %#v", persisted)
				}
				job := <-registrationQueue
				if full {
					if job.RunnerName != "occupied" {
						t.Fatal("replaced queued job")
					}
				} else {
					if job.RunnerName != "added" || job.Token != "fixture-token" || job.URL != "https://github.com/owner/repo" || len(job.Labels) != 1 || job.Labels[0] != "linux" {
						t.Fatalf("incorrect job: %#v", job)
					}
					var response map[string]any
					if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
						t.Fatal(err)
					}
					if response["queued"] != true {
						t.Fatal(response)
					}
				}
				// A lost acknowledgement must not silently create a second runner.
				retry := behaviorCall(AddRunner, "", body)
				if retry.Code != 409 {
					t.Fatalf("retry: %d %s", retry.Code, retry.Body.String())
				}
			})
		}
	}
}

func TestMutationValidationAndRealStorageErrors(t *testing.T) {
	cfg := behaviorConfig(t)
	for _, body := range []string{`{`, `{}`, `{"target_type":"repo","target":"bad"}`, `{"target_type":"repo","target":"o/r","path":"../escape"}`, `{"name":"different","target_type":"repo","target":"o/r"}`} {
		rec := behaviorCall(UpdateRunner, "demo", body)
		if rec.Code != 400 {
			t.Fatalf("update %s: %d", body, rec.Code)
		}
	}
	rec := behaviorCall(AddRunner, "", `{`)
	if rec.Code != 400 {
		t.Fatal(rec.Code)
	}
	rec = behaviorCall(AddRunner, "", `{"name":"added","path":"../escape","target_type":"repo","target":"o/r"}`)
	if rec.Code != 400 {
		t.Fatal(rec.Code)
	}
	if err := os.WriteFile(filepath.Join(cfg.Runners.BasePath, "blocker"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	cfg.Runners.BasePath = filepath.Join(cfg.Runners.BasePath, "blocker")
	if err := cfg.Save(ConfigPath); err != nil {
		t.Fatal(err)
	}
	rec = behaviorCall(AddRunner, "", `{"name":"added","target_type":"repo","target":"o/r"}`)
	if rec.Code != 500 {
		t.Fatalf("unwritable base: %d", rec.Code)
	}
	if err := os.WriteFile(ConfigPath, []byte("invalid: ["), 0600); err != nil {
		t.Fatal(err)
	}
	rec = behaviorCall(UpdateRunner, "demo", `{"target_type":"repo","target":"o/r"}`)
	if rec.Code != 500 {
		t.Fatalf("invalid config update: %d", rec.Code)
	}
}

func TestUpdateReportsActualAutostartOutcome(t *testing.T) {
	cfg := behaviorConfig(t)
	dir := filepath.Join(cfg.Runners.BasePath, "demo")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".runner"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, success := range []bool{false, true} {
		if success {
			if err := os.WriteFile(filepath.Join(dir, runner.RunScriptName()), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
				t.Fatal(err)
			}
		}
		rec := behaviorCall(UpdateRunner, "demo", `{"target_type":"repo","target":"o/r"}`)
		var response struct {
			Started bool `json:"started"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if rec.Code != 200 || response.Started != success {
			t.Fatalf("success=%v got %d %s", success, rec.Code, rec.Body.String())
		}
		if !success {
			rec := behaviorCall(StartRunner, "demo", `{}`)
			if rec.Code != 500 {
				t.Fatal(rec.Code)
			}
		}
	}
}

func TestRegistrationWithoutInstallerReportsManualRecovery(t *testing.T) {
	behaviorConfig(t)
	old := installRunnerScriptPath
	installRunnerScriptPath = filepath.Join(t.TempDir(), "absent-installer")
	t.Cleanup(func() { installRunnerScriptPath = old })
	rec := behaviorCall(AddRunner, "", `{"name":"manual","target_type":"repo","target":"o/r","registration_token":"fixture"}`)
	if rec.Code != 200 || strings.Contains(rec.Body.String(), `"queued":true`) {
		t.Fatalf("missing installer incorrectly queued: %d %s", rec.Code, rec.Body.String())
	}
}

func TestMutationSaveFailuresKeepPreviousIdentity(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("requires filesystem permission enforcement")
	}
	cfg := behaviorConfig(t)
	original, err := os.ReadFile(ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(ConfigPath, 0400); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Dir(ConfigPath)
	if err := os.Chmod(parent, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0700); _ = os.Chmod(ConfigPath, 0600) })
	for _, tc := range []struct {
		fn         echo.HandlerFunc
		name, body string
	}{
		{AddRunner, "", `{"name":"added","target_type":"repo","target":"o/r"}`},
		{UpdateRunner, "demo", `{"target_type":"repo","target":"o/changed"}`},
		{RemoveRunnerByName, "demo", ``},
	} {
		rec := behaviorCall(tc.fn, tc.name, tc.body)
		if rec.Code != 500 {
			t.Fatalf("failed persistence reported success: %d %s", rec.Code, rec.Body.String())
		}
		current, err := os.ReadFile(ConfigPath)
		if err != nil || string(current) != string(original) {
			t.Fatal("failed persistence changed config")
		}
	}
	if cfg.Runners.Items[0].Name != "demo" {
		t.Fatal("original identity changed")
	}
}

func TestEnglishDiagnosticFallbackUsesArgumentsWithoutFormattingNoise(t *testing.T) {
	old := I18nLoader
	I18nLoader = func(lang string) (map[string]string, error) {
		return map[string]string{"test.formatted": "failure: %s"}, nil
	}
	ResetI18nCache()
	t.Cleanup(func() { I18nLoader = old; ResetI18nCache() })
	if got := trEnf("test.formatted", "reason"); got != "failure: reason" {
		t.Fatal(got)
	}
	if got := trEnf("test.missing", "reason", 7); got != "test.missing: reason: 7" {
		t.Fatal(got)
	}
	c := echo.New().NewContext(httptest.NewRequest("GET", "/", nil), httptest.NewRecorder())
	if got := Trf(c, "test.formatted", "reason"); got != "failure: reason" {
		t.Fatal(got)
	}
}

func TestMissingLocaleKeyUsesEnglishTranslation(t *testing.T) {
	old := I18nLoader
	I18nLoader = func(lang string) (map[string]string, error) {
		if lang == "en" {
			return map[string]string{"fixture.key": "English %s"}, nil
		}
		return map[string]string{}, nil
	}
	ResetI18nCache()
	t.Cleanup(func() { I18nLoader = old; ResetI18nCache() })
	c := echo.New().NewContext(httptest.NewRequest("GET", "/?lang=fr", nil), httptest.NewRecorder())
	if got := Trf(c, "fixture.key", "fallback"); got != "English fallback" {
		t.Fatal(got)
	}
}
