// Runner Agent：运行在 Runner 容器内，职责仅为 Runner 进程控制（启动/停止）与健康/状态上报（/status、/health），供 Manager 通过 HTTP 调用。
// 环境变量：RUNNER_INSTALL_DIR（默认 /runner）、AGENT_PORT（默认 8081）
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

const defaultInstallDir = "/runner"
const defaultPort = "8081"

func installDir() string {
	if d := os.Getenv("RUNNER_INSTALL_DIR"); d != "" {
		return d
	}
	return defaultInstallDir
}

func runScriptName() string {
	if runtime.GOOS == "windows" {
		return "run.cmd"
	}
	return "run.sh"
}

func readRunnerPid(installDir string) (int, error) {
	for _, name := range []string{"Runner.Listener.pid", ".path"} {
		b, err := os.ReadFile(filepath.Join(installDir, name))
		if err != nil {
			continue
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
		if err != nil || pid <= 0 {
			continue
		}
		return pid, nil
	}
	return 0, os.ErrNotExist
}

func processExists(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	if runtime.GOOS != "windows" {
		err := process.Signal(syscall.Signal(0))
		return err == nil
	}
	return true
}

func getStatus(installDir string) (status string, running bool) {
	if installDir == "" {
		return "missing", false
	}
	fi, err := os.Stat(installDir)
	if err != nil || !fi.IsDir() {
		return "missing", false
	}
	runnerFile := filepath.Join(installDir, ".runner")
	if _, err := os.Stat(runnerFile); err != nil {
		return "new", false
	}
	pid, err := readRunnerPid(installDir)
	if err != nil || pid <= 0 {
		return "installed", false
	}
	if runtime.GOOS == "windows" {
		return "installed", true
	}
	return "installed", processExists(pid)
}

func startRunner(installDir string) error {
	script := filepath.Join(installDir, runScriptName())
	if _, err := os.Stat(script); err != nil {
		return fmt.Errorf("未找到 %s: %w", script, err)
	}
	cmd := exec.Command(script)
	cmd.Dir = installDir
	cmd.Env = os.Environ()
	if runtime.GOOS != "windows" {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

func stopRunner(installDir string) error {
	pid, err := readRunnerPid(installDir)
	if err != nil || pid <= 0 {
		return fmt.Errorf("未找到 runner pid: %w", err)
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		return process.Kill()
	}
	return process.Signal(syscall.SIGTERM)
}

type statusResponse struct {
	Status  string `json:"status"`
	Running bool   `json:"running"`
}

func handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	dir := installDir()
	status, running := getStatus(dir)
	_ = json.NewEncoder(w).Encode(statusResponse{Status: status, Running: running})
}

func handleStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	dir := installDir()
	if _, run := getStatus(dir); run {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"message":"already running"}`))
		return
	}
	if err := startRunner(dir); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"message":"started"}`))
}

func handleStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	dir := installDir()
	if err := stopRunner(dir); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"message":"stop signal sent"}`))
}

func main() {
	port := os.Getenv("AGENT_PORT")
	if port == "" {
		port = defaultPort
	}
	http.HandleFunc("/status", handleStatus)
	http.HandleFunc("/start", handleStart)
	http.HandleFunc("/stop", handleStop)
	http.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	log.Printf("Runner Agent 监听 :%s，RUNNER_INSTALL_DIR=%s", port, installDir())
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatal(err)
	}
}
