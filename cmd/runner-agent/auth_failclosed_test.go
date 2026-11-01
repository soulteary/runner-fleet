package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestRequireTokenFailsClosed(t *testing.T) {
	for _, state := range []string{"missing", "empty", "whitespace", "unreadable"} {
		t.Run(state, func(t *testing.T) {
			dir := agentDir(t, "")
			path := filepath.Join(dir, agentTokenFile)
			switch state {
			case "empty", "whitespace":
				data := ""
				if state == "whitespace" {
					data = " \n\t"
				}
				if err := os.WriteFile(path, []byte(data), 0600); err != nil {
					t.Fatal(err)
				}
			case "unreadable":
				// Reading a directory fails even when tests run as root.
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			}
			for _, route := range []string{"/status", "/start", "/stop"} {
				called := false
				rec := httptest.NewRecorder()
				requireToken(func(http.ResponseWriter, *http.Request) { called = true })(rec, httptest.NewRequest(http.MethodPost, route, nil))
				if rec.Code != http.StatusServiceUnavailable || called {
					t.Fatalf("%s: status=%d handlerCalled=%v; want 503 and no dispatch", route, rec.Code, called)
				}
			}
		})
	}
}

func TestAgentMuxRejectsAnonymousRequestsAndKeepsHealthOpen(t *testing.T) {
	agentDir(t, "")
	server := httptest.NewServer(newAgentMux())
	defer server.Close()
	for _, path := range []string{"/status", "/start", "/stop", "/health"} {
		resp, err := server.Client().Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		want := http.StatusServiceUnavailable
		if path == "/health" {
			want = http.StatusOK
		}
		if resp.StatusCode != want {
			t.Fatalf("%s: status %d want %d", path, resp.StatusCode, want)
		}
	}
}

func TestRequireTokenRejectsCredentialsLostAfterSuccessfulRequest(t *testing.T) {
	dir := agentDir(t, "original-token")
	if rec := callGuarded(t, "Bearer original-token"); rec.Code != http.StatusOK {
		t.Fatal(rec.Code)
	}
	if err := os.WriteFile(filepath.Join(dir, agentTokenFile), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if rec := callGuarded(t, "Bearer original-token"); rec.Code != http.StatusServiceUnavailable {
		t.Fatal(rec.Code)
	}
	if err := os.WriteFile(filepath.Join(dir, agentTokenFile), []byte("original-token"), 0600); err != nil {
		t.Fatal(err)
	}
	if rec := callGuarded(t, "Bearer original-token"); rec.Code != http.StatusOK {
		t.Fatal(rec.Code)
	}
}

func TestControlMethodsAndScriptExecutionFailures(t *testing.T) {
	dir := agentDir(t, "token")
	for _, fn := range []http.HandlerFunc{handleStart, handleStop} {
		rec := httptest.NewRecorder()
		fn(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		if rec.Code != 405 {
			t.Fatal(rec.Code)
		}
	}
	rec := httptest.NewRecorder()
	handleStart(rec, httptest.NewRequest(http.MethodPost, "/start", nil))
	if rec.Code != 500 {
		t.Fatalf("missing script: %d", rec.Code)
	}
	if err := os.WriteFile(filepath.Join(dir, runScriptName()), []byte("not executable"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := startRunner(dir); err == nil {
		t.Fatal("non-executable script started")
	}
	if got, running := getStatus(""); got != "missing" || running {
		t.Fatalf("empty install path: %s %v", got, running)
	}
}
