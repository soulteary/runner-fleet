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
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestAgentEntrypointHelper(t *testing.T) {
	if os.Getenv("RUN_AGENT_ENTRYPOINT") != "1" {
		return
	}
	main()
}

func TestAgentEntrypointLifecycle(t *testing.T) {
	for _, token := range []string{"", "entrypoint-test-token"} {
		t.Run(token, func(t *testing.T) {
			dir := t.TempDir()
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			port := strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
			_ = ln.Close()
			args := []string{"-test.run=^TestAgentEntrypointHelper$"}
			if cover := flag.Lookup("test.gocoverdir"); cover != nil && cover.Value.String() != "" {
				args = append(args, "-test.gocoverdir="+cover.Value.String())
			}
			cmd := exec.Command(os.Args[0], args...)
			cmd.Env = append(os.Environ(), "RUN_AGENT_ENTRYPOINT=1", "RUNNER_INSTALL_DIR="+dir, "AGENT_PORT="+port, "AGENT_TOKEN="+token)
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
			client := &http.Client{Timeout: time.Second}
			base := "http://127.0.0.1:" + port
			deadline := time.Now().Add(10 * time.Second)
			for {
				resp, err := client.Get(base + "/health")
				if err == nil {
					_ = resp.Body.Close()
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("Agent did not listen")
				}
				time.Sleep(20 * time.Millisecond)
			}
			req, _ := http.NewRequest(http.MethodGet, base+"/status", nil)
			if token != "" {
				req.Header.Set("Authorization", "Bearer "+token)
			}
			resp, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if err != nil {
				t.Fatal(err)
			}
			want := 200
			if token == "" {
				want = 503
			}
			if resp.StatusCode != want {
				t.Fatalf("status=%d body=%s", resp.StatusCode, body)
			}
			if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				exited = true
				if err != nil {
					t.Fatalf("exit: %v %s", err, output.String())
				}
			case <-time.After(10 * time.Second):
				t.Fatal("Agent did not shut down")
			}
			if !strings.Contains(output.String(), "runner stopped") {
				t.Fatal(output.String())
			}
		})
	}
}
