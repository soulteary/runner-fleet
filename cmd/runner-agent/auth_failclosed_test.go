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
