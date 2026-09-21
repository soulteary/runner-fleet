// Runner Agent：运行在 Runner 容器内，职责仅为 Runner 进程控制（启动/停止）与健康/状态上报（/status、/health），供 Manager 通过 HTTP 调用。
// 环境变量：RUNNER_INSTALL_DIR（默认 /runner）、AGENT_PORT（默认 8081）
package main

import (
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

	"github.com/soulteary/runner-fleet/internal/runnerproc"
	secure "github.com/soulteary/secure-kit/v2"
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
		return fmt.Errorf("%s not found: %w", script, err)
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
		return fmt.Errorf("no running runner process found under %s", installDir)
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
		log.Printf("warning: the token file %s exists but cannot be read (%v), so the endpoints run unauthenticated. "+
			"This usually means the Manager and the Agent run under different UIDs; recreate the runner container "+
			"to have the token injected through the environment instead", path, err)
	})
}

// bearerPrefix Authorization 头里的认证 scheme；按 RFC 7235，scheme 名大小写不敏感
const bearerPrefix = "Bearer "

// bearerToken 取出 `Authorization: Bearer <token>` 里的令牌，不是这个 scheme 时返回空串。
//
// 以前这里用 strings.TrimPrefix，而它在前缀不存在时原样返回整个字符串——于是一个
// 不带 scheme 的裸令牌也能通过。那不构成漏洞（调用方照样得先有那串密钥），
// 但文档六种语言都写着 `Authorization: Bearer`，Manager 发的也一直是 Bearer
// （见 runner.setAgentAuth）：代码比自己的文档更松，没有道理。
func bearerToken(header string) string {
	h := strings.TrimSpace(header)
	if len(h) < len(bearerPrefix) || !strings.EqualFold(h[:len(bearerPrefix)], bearerPrefix) {
		return ""
	}
	return strings.TrimSpace(h[len(bearerPrefix):])
}

// requireToken 包装控制类接口；令牌未配置时直接放行，配置了则要求 Bearer 匹配。
// 每次请求重新读取令牌，便于 Manager 在容器启动后才写入令牌的场景。
func requireToken(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		want := expectedToken()
		if want != "" {
			got := bearerToken(r.Header.Get("Authorization"))
			// 与 Manager 侧同理：subtle.ConstantTimeCompare 在两侧长度不等时立即
			// 返回，耗时会随「猜的长度是否等于真实长度」而变，把令牌长度泄漏出去；
			// secure.ConstantTimeEqual 先把两侧补到同长再比
			if !secure.ConstantTimeEqual(got, want) {
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
	authState := "enabled"
	if expectedToken() == "" {
		authState = "disabled (no token file; kept for containers created by older versions)"
	}
	log.Printf("Runner Agent listening on :%s, RUNNER_INSTALL_DIR=%s, endpoint auth %s", port, installDir(), authState)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatal(err)
	}
}
