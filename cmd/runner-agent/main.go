// Runner Agent：运行在 Runner 容器内，职责仅为 Runner 进程控制（启动/停止）与健康/状态上报（/status、/health），供 Manager 通过 HTTP 调用。
// 环境变量：RUNNER_INSTALL_DIR（默认 /runner）、AGENT_PORT（默认 8081）
package main

import (
	"crypto/subtle"
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

// agentTokenFile 与 Manager 侧 runner.AgentTokenFile 保持一致：
// 令牌由 Manager 写入挂载目录，Agent 从自己的 RUNNER_INSTALL_DIR 读取。
const agentTokenFile = ".agent_token"

// expectedToken 返回本 Agent 要求的令牌：优先环境变量，其次安装目录下的令牌文件。
// 返回空字符串表示未配置令牌，此时不启用鉴权（兼容本特性之前创建的容器）。
func expectedToken() string {
	if t := strings.TrimSpace(os.Getenv("AGENT_TOKEN")); t != "" {
		return t
	}
	b, err := os.ReadFile(filepath.Join(installDir(), agentTokenFile))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// requireToken 包装控制类接口；令牌未配置时直接放行，配置了则要求 Bearer 匹配。
// 每次请求重新读取令牌，便于 Manager 在容器启动后才写入令牌的场景。
func requireToken(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		want := expectedToken()
		if want != "" {
			got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if subtle.ConstantTimeCompare([]byte(strings.TrimSpace(got)), []byte(want)) != 1 {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"message":"unauthorized"}`))
				return
			}
		}
		next(w, r)
	}
}

func main() {
	port := os.Getenv("AGENT_PORT")
	if port == "" {
		port = defaultPort
	}
	// 控制类接口需要令牌；/health 保持开放，供容器 HEALTHCHECK 使用
	http.HandleFunc("/status", requireToken(handleStatus))
	http.HandleFunc("/start", requireToken(handleStart))
	http.HandleFunc("/stop", requireToken(handleStop))
	http.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	authState := "已启用"
	if expectedToken() == "" {
		authState = "未启用（无令牌文件，兼容旧容器）"
	}
	log.Printf("Runner Agent 监听 :%s，RUNNER_INSTALL_DIR=%s，接口鉴权%s", port, installDir(), authState)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatal(err)
	}
}
