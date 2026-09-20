// Runner Agent：运行在 Runner 容器内，职责仅为 Runner 进程控制（启动/停止）与健康/状态上报（/status、/health），供 Manager 通过 HTTP 调用。
// 环境变量：RUNNER_INSTALL_DIR（默认 /runner）、AGENT_PORT（默认 8081）
package main

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"

	"github.com/lab-dev/github-actions-runner-manager/internal/runnerproc"
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
	// 不看 pid 文件：actions/runner 不写这种文件（见 internal/runnerproc 的包注释），
	// 照着找必然找不到，于是每个 runner 都报「未运行」，Manager 每 5 分钟就再拉起一次。
	return "installed", runnerproc.Running(installDir)
}

// startMu 串行化 /start 的「查-起」两步，见 handleStart
var startMu sync.Mutex

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

// stopRunner 向该安装目录下的所有 Runner 进程发 SIGTERM。
// runnerproc.Find 把 run.sh 排在 Runner.Listener 之前，先停监护脚本，
// 它的 while 循环才不会在监听器退出后又拉起一个新的。
func stopRunner(installDir string) error {
	pids := runnerproc.Find(installDir)
	if len(pids) == 0 {
		return fmt.Errorf("未找到 %s 下正在运行的 Runner 进程", installDir)
	}
	var firstErr error
	for _, pid := range pids {
		process, err := os.FindProcess(pid)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		// 扫描与发信号之间进程可能自己退了，这不算失败
		if err := process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
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
	// 「检查是否在跑」与「拉起」必须是一个原子段：Manager 的定时巡检、注册完成后的
	// 启动和界面点击都可能同时打到这里，各自都看到「没在跑」就会各拉起一个 run.sh。
	startMu.Lock()
	defer startMu.Unlock()
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
	// 优先环境变量：Manager 创建容器时注入，不依赖挂载文件的 UID 可读性
	if t := strings.TrimSpace(os.Getenv("AGENT_TOKEN")); t != "" {
		return t
	}
	path := filepath.Join(installDir(), agentTokenFile)
	b, err := os.ReadFile(path)
	if err != nil {
		// 文件不存在 = 本特性之前创建的容器，属于预期的兼容路径；
		// 存在却读不了（多为 UID 不匹配）则必须说出来，否则会静默地不鉴权
		if !os.IsNotExist(err) {
			logTokenUnreadable(path, err)
		}
		return ""
	}
	return strings.TrimSpace(string(b))
}

// tokenWarnOnce 保证权限告警只打一次，避免被状态轮询刷屏
var tokenWarnOnce sync.Once

func logTokenUnreadable(path string, err error) {
	tokenWarnOnce.Do(func() {
		log.Printf("警告: 令牌文件 %s 存在但无法读取（%v），接口将不启用鉴权。"+
			"多为 Manager 与 Agent 的 UID 不一致所致，重建该 Runner 容器即可改用环境变量注入令牌", path, err)
	})
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
