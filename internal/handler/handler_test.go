package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/lab-dev/github-actions-runner-manager/internal/config"
	"github.com/labstack/echo/v4"
)

func init() {
	dir := os.TempDir()
	ConfigPath = filepath.Join(dir, "handler-test-config.yaml")
	cfg := &config.Config{
		Server:  config.ServerConfig{Port: 8080},
		Runners: config.RunnersConfig{BasePath: dir, Items: []config.RunnerItem{}},
	}
	_ = cfg.Save(ConfigPath)
}

func TestHealth(t *testing.T) {
	e := echo.New()
	e.GET("/health", Health)
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d", rec.Code)
	}
	if rec.Body.String() != `{"status":"ok"}` && rec.Body.String() != "{\"status\":\"ok\"}\n" {
		t.Errorf("body = %q", rec.Body.String())
	}
}

func TestVersionInfo(t *testing.T) {
	Version = "test-1.0"
	defer func() { Version = "" }()
	e := echo.New()
	e.GET("/version", VersionInfo)
	req := httptest.NewRequest(http.MethodGet, "/version", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d", rec.Code)
	}
	var m map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&m); err != nil {
		t.Fatal(err)
	}
	if m["version"] != "test-1.0" {
		t.Errorf("version = %q", m["version"])
	}
}

func TestVersionInfo_EmptyDefaultsToDev(t *testing.T) {
	Version = ""
	defer func() { Version = "" }()
	e := echo.New()
	e.GET("/version", VersionInfo)
	req := httptest.NewRequest(http.MethodGet, "/version", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d", rec.Code)
	}
	var m map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&m); err != nil {
		t.Fatal(err)
	}
	if m["version"] != "dev" {
		t.Errorf("version = %q, want dev", m["version"])
	}
}

func TestAddRunner_InvalidName(t *testing.T) {
	e := echo.New()
	e.POST("/api/runners", AddRunner)
	body := map[string]string{
		"name":        "bad..name",
		"target_type": "org",
		"target":      "myorg",
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/runners", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid name, got %d", rec.Code)
	}
}

func TestUpdateRunner_NameImmutable(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	cfg := &config.Config{
		Runners: config.RunnersConfig{
			BasePath: dir,
			Items:    []config.RunnerItem{{Name: "r1", TargetType: "org", Target: "o1"}},
		},
	}
	_ = cfg.Save(cfgPath)
	ConfigPath = cfgPath
	defer func() { ConfigPath = filepath.Join(os.TempDir(), "handler-test-config.yaml") }()

	e := echo.New()
	e.PUT("/api/runners/:name", UpdateRunner)
	body := map[string]any{"name": "r2", "target_type": "org", "target": "o1"}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPut, "/api/runners/r1", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 when body name differs from URL, got %d", rec.Code)
	}
}

func TestAddRunner_WhitespaceName(t *testing.T) {
	e := echo.New()
	e.POST("/api/runners", AddRunner)
	body := map[string]string{
		"name":        "   ",
		"target_type": "org",
		"target":      "myorg",
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/runners", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for whitespace name, got %d", rec.Code)
	}
}

func TestUpdateRunner_TrimmedBodyNameAccepted(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	cfg := &config.Config{
		Runners: config.RunnersConfig{
			BasePath: dir,
			Items:    []config.RunnerItem{{Name: "r1", TargetType: "org", Target: "o1"}},
		},
	}
	_ = cfg.Save(cfgPath)
	ConfigPath = cfgPath
	defer func() { ConfigPath = filepath.Join(os.TempDir(), "handler-test-config.yaml") }()

	e := echo.New()
	e.PUT("/api/runners/:name", UpdateRunner)
	body := map[string]any{"name": "  r1  ", "target_type": "org", "target": "o1"}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPut, "/api/runners/r1", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 when body name trims to URL name, got %d body=%s", rec.Code, rec.Body.String())
	}
}
