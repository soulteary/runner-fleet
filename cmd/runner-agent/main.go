// Runner Agent：运行在 Runner 容器内，职责仅为 Runner 进程控制（启动/停止）与健康/状态上报（/status、/health），供 Manager 通过 HTTP 调用。
// 环境变量：RUNNER_INSTALL_DIR（默认 /runner）、AGENT_PORT（默认 8081）
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/soulteary/runner-fleet/internal/childenv"
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
	// run.sh 跑的是用户的 Job：不能把注入给 Agent 的 AGENT_TOKEN 传下去，见 internal/childenv
	cmd.Env = childenv.Environ()
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

// newAgentMux 挂载 Agent 的全部路由。
//
// 用独立的 mux 而不是 http.DefaultServeMux：DefaultServeMux 是包级全局变量，
// 同一进程里注册两次同一路径会 panic，测试因此只能起一个 server；换成独立 mux
// 之后 newAgentServer 可以被反复构造，超时字段也才测得出来。
func newAgentMux() *http.ServeMux {
	mux := http.NewServeMux()
	// 控制类接口需要令牌；/health 保持开放，供容器 HEALTHCHECK 使用
	mux.HandleFunc("/status", requireToken(handleStatus))
	mux.HandleFunc("/start", requireToken(handleStart))
	mux.HandleFunc("/stop", requireToken(handleStop))
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	return mux
}

// newAgentServer 构造带超时的 HTTP 服务。
//
// 不设超时的 http.Server 会无限期地等一个不再说话的对端：一个半开的 TCP 连接
// 就能占住一个 goroutine 和一个 fd 直到进程结束，而 Runner 容器是长期存活的。
// Agent 的处理函数全部即时返回（查进程表、发信号），所以这里可以给得很紧。
func newAgentServer(port string, mux *http.ServeMux) *http.Server {
	return &http.Server{
		Addr:    ":" + port,
		Handler: mux,
		// 只有 Manager 会连过来，请求体最大也就是个空 POST
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
}

// shutdown 让正在跑的 Job 有机会收尾，然后关掉 HTTP 服务。
//
// 这是 docker stop -t <宽限期> 真正落到 Runner 身上的那一环。Agent 是容器里的
// PID 1（经 tini 转发），SIGTERM 先到它这里；它要是像以前那样直接退出，内核就会
// 对 PID namespace 里剩下的进程一律 SIGKILL——run.sh 与 Runner.Listener 连
// SIGTERM 都收不到，Job 不会被优雅取消，GitHub 那边只能等「失去通信」超时。
//
// 返回 true 表示 Runner 在 grace 内退干净了。
func shutdown(ctx context.Context, dir string, grace time.Duration) bool {
	if !runnerproc.Running(dir) {
		return true
	}
	// stopRunner 按「监护脚本先于监听器」的顺序发 SIGTERM，这个顺序必须保留：
	// 反过来先停监听器，run.sh 的 while 循环会立刻再拉起一个新的。
	if err := stopRunner(dir); err != nil {
		log.Printf("sending SIGTERM to the runner failed: %v", err)
	}
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		if !runnerproc.Running(dir) {
			return true
		}
		select {
		case <-ctx.Done():
			return !runnerproc.Running(dir)
		case <-time.After(runnerPollInterval):
		}
	}
	return !runnerproc.Running(dir)
}

// runnerPollInterval 是等 Runner 退出时的轮询间隔。
// 每次轮询都要扫一遍 procfs，200ms 已经足够快（相对几十秒的宽限期），
// 又不至于在一个正忙着收尾的容器里跟 Job 抢 CPU。
const runnerPollInterval = 200 * time.Millisecond

func main() {
	port := os.Getenv("AGENT_PORT")
	if port == "" {
		port = defaultPort
	}
	srv := newAgentServer(port, newAgentMux())
	authState := "enabled"
	if expectedToken() == "" {
		authState = "disabled (no token file; kept for containers created by older versions)"
	}
	log.Printf("Runner Agent listening on :%s, RUNNER_INSTALL_DIR=%s, endpoint auth %s", port, installDir(), authState)

	// 先订阅再监听：容器刚起就被 docker stop 时，信号也不会落到「未订阅 = 直接退出」
	// 的默认行为上。Go 运行时对未 Notify 的 SIGTERM 就是立刻终止进程。
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGTERM, os.Interrupt)

	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	}()

	<-quit
	grace := runnerproc.StopGracePeriod - runnerproc.ShutdownReserve
	log.Printf("received SIGTERM, stopping the runner (up to %s)", grace)
	if shutdown(context.Background(), installDir(), grace) {
		log.Print("runner stopped")
	} else {
		log.Printf("runner still running after %s, exiting anyway", grace)
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), runnerproc.ShutdownReserve)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutting down the HTTP server failed: %v", err)
	}
	os.Exit(0)
}
