package main

import (
	"bytes"
	"errors"
	"flag"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/soulteary/runner-fleet/internal/config"
)

// Run the real entrypoint in an isolated process: signals, flags and background
// workers must not interfere with other unit tests in the parent process.
func TestManagerEntrypointHelper(t *testing.T) {
	if os.Getenv("RUN_MANAGER_ENTRYPOINT") != "1" {
		return
	}
	flag.CommandLine = flag.NewFlagSet("runner-manager", flag.ExitOnError)
	os.Args = []string{"runner-manager", "-config", os.Getenv("ENTRYPOINT_CONFIG")}
	main()
}

func TestManagerEntrypointServesAndShutsDown(t *testing.T) {
	dir := t.TempDir()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.yaml")
	cfg := &config.Config{Server: config.ServerConfig{Addr: "127.0.0.1", Port: port}, Runners: config.RunnersConfig{BasePath: dir}}
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	args := []string{"-test.run=^TestManagerEntrypointHelper$"}
	if cover := flag.Lookup("test.gocoverdir"); cover != nil && cover.Value.String() != "" {
		args = append(args, "-test.gocoverdir="+cover.Value.String())
	}
	cmd := exec.Command(os.Args[0], args...)
	cmd.Env = append(os.Environ(), "RUN_MANAGER_ENTRYPOINT=1", "ENTRYPOINT_CONFIG="+path, "BASIC_AUTH_PASSWORD=", "CSRF_TRUSTED_ORIGINS=http://localhost")
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := false
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() {
		if !exited {
			_ = cmd.Process.Kill()
			var exitError *exec.ExitError
			if waitErr := <-done; waitErr != nil && !errors.As(waitErr, &exitError) {
				t.Errorf("waiting for cleanup: %v", waitErr)
			}
		}
	})
	base := "http://127.0.0.1:" + strconv.Itoa(port)
	client := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(10 * time.Second)
	for {
		resp, err := client.Get(base + "/health")
		if err == nil {
			_ = resp.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("manager did not listen")
		}
		time.Sleep(20 * time.Millisecond)
	}
	for _, endpoint := range []string{"/ready", "/version", "/", "/api/runners", "/static/app.css?v=test", "/static/app.js"} {
		resp, err := client.Get(base + endpoint)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != 200 || len(body) == 0 {
			t.Fatalf("%s: %d %s", endpoint, resp.StatusCode, body)
		}
	}
	// Exercise the actual delayed startup checks without replacing their clocks.
	time.Sleep(31 * time.Second)
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		exited = true
		if err != nil {
			t.Fatalf("exit: %v: %s", err, output.String())
		}
	case <-time.After(15 * time.Second):
		t.Fatal("manager did not shut down")
	}
	if !strings.Contains(output.String(), "stopped") {
		t.Fatalf("missing completed shutdown: %s", output.String())
	}
}
