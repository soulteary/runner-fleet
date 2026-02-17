package runner

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

func TestGetAgentStatus_ErrorBodyIncluded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/status" {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "agent unhealthy", http.StatusBadGateway)
	}))
	defer srv.Close()

	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	host := u.Hostname()
	port := 80
	if u.Port() != "" {
		// httptest 必有端口，这里仅做健壮处理
		if p, convErr := strconv.Atoi(u.Port()); convErr == nil && p > 0 {
			port = p
		}
	}

	_, err = GetAgentStatus(context.Background(), host, port)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "agent unhealthy") {
		t.Fatalf("expected response body in error, got: %v", err)
	}
}

func TestCallAgentStart_ErrorBodyIncluded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/start" {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "start failed in agent", http.StatusConflict)
	}))
	defer srv.Close()

	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	host := u.Hostname()
	port := 80
	if u.Port() != "" {
		if p, convErr := strconv.Atoi(u.Port()); convErr == nil && p > 0 {
			port = p
		}
	}

	err = CallAgentStart(context.Background(), host, port)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "start failed in agent") {
		t.Fatalf("expected response body in error, got: %v", err)
	}
}

func TestGetAgentStatus_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/status" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(AgentStatus{Status: "installed", Running: true})
	}))
	defer srv.Close()

	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	host := u.Hostname()
	port := 80
	if u.Port() != "" {
		if p, convErr := strconv.Atoi(u.Port()); convErr == nil && p > 0 {
			port = p
		}
	}

	st, err := GetAgentStatus(context.Background(), host, port)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if st.Status != "installed" || !st.Running {
		t.Fatalf("unexpected status: %+v", st)
	}
}
