package runner

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/lab-dev/github-actions-runner-manager/internal/config"
)

// 与 handler 写入的文件名一致，供 cron 与 API 读取
const (
	RegistrationResultFile = ".registration_result.json"
	GitHubStatusFile       = ".github_status.json"
)

// Status 表示 runner 目录状态
type Status string

const (
	StatusUnknown   Status = "unknown"
	StatusInstalled Status = "installed" // 已配置（存在 .runner 等）
	StatusNew       Status = "new"       // 仅目录存在，未注册
	StatusMissing   Status = "missing"   // 目录不存在
)

// RunnerInfo 供前端展示的 runner 信息
type RunnerInfo struct {
	Name                  string   `json:"name"`
	Path                  string   `json:"path"`
	TargetType            string   `json:"target_type"`
	Target                string   `json:"target"`
	Labels                []string `json:"labels"`
	Status                Status   `json:"status"`
	InstallDir            string   `json:"install_dir"`
	Running               bool     `json:"running"`                 // 进程是否在跑
	ProbeError            string   `json:"probe_error"`             // 容器模式下状态探测失败原因（如 Docker 权限、Agent 不可达）
	JobDockerBackend      string   `json:"job_docker_backend"`      // 容器模式下 Job 内 Docker 后端：dind / host-socket / none
	RegistrationMessage   string   `json:"registration_message"`    // 最近一次注册结果信息（成功或失败原因）
	RegistrationCheckedAt string   `json:"registration_checked_at"` // 注册结果时间
	RegisteredOnGitHub    *bool    `json:"registered_on_github"`    // cron 通过 GitHub API 检查是否在 GitHub 显示，nil 表示未检查
	GitHubCheckAt         string   `json:"github_check_at"`         // 最近一次 GitHub 检查时间
}

// GetByName 根据名称获取单个 runner 信息，不存在返回 nil；cfg 为 nil 时安全返回 nil
func GetByName(cfg *config.Config, name string) *RunnerInfo {
	if cfg == nil {
		return nil
	}
	for _, item := range cfg.Runners.Items {
		if item.Name != name {
			continue
		}
		installDir := item.InstallPath(cfg.Runners.BasePath)
		info := &RunnerInfo{
			Name:       item.Name,
			Path:       item.Path,
			TargetType: item.TargetType,
			Target:     item.Target,
			Labels:     append([]string(nil), item.Labels...),
			InstallDir: installDir,
		}
		if cfg.Runners.ContainerMode {
			info.JobDockerBackend = cfg.Runners.JobDockerBackend
		}
		if item.Path == "" {
			info.Path = item.Name
		}
		info.Status, info.Running = getStatus(installDir)
		info.RegistrationMessage, info.RegistrationCheckedAt = readRegistrationResult(installDir)
		info.RegisteredOnGitHub, info.GitHubCheckAt = readGitHubStatus(installDir)
		return info
	}
	return nil
}

// List 根据配置与磁盘状态列出所有 runner
func List(cfg *config.Config) []RunnerInfo {
	base := cfg.Runners.BasePath
	list := make([]RunnerInfo, 0, len(cfg.Runners.Items))
	for _, item := range cfg.Runners.Items {
		installDir := item.InstallPath(base)
		info := RunnerInfo{
			Name:       item.Name,
			Path:       item.Path,
			TargetType: item.TargetType,
			Target:     item.Target,
			Labels:     append([]string(nil), item.Labels...),
			InstallDir: installDir,
		}
		if cfg.Runners.ContainerMode {
			info.JobDockerBackend = cfg.Runners.JobDockerBackend
		}
		if item.Path == "" {
			info.Path = item.Name
		}
		info.Status, info.Running = getStatus(installDir)
		info.RegistrationMessage, info.RegistrationCheckedAt = readRegistrationResult(installDir)
		info.RegisteredOnGitHub, info.GitHubCheckAt = readGitHubStatus(installDir)
		list = append(list, info)
	}
	return list
}

func getStatus(installDir string) (Status, bool) {
	if installDir == "" {
		return StatusMissing, false
	}
	fi, err := os.Stat(installDir)
	if err != nil || !fi.IsDir() {
		return StatusMissing, false
	}
	// 已注册的 runner 会有 .runner 文件
	runnerFile := filepath.Join(installDir, ".runner")
	if _, err := os.Stat(runnerFile); err == nil {
		running := isProcessRunning(installDir)
		return StatusInstalled, running
	}
	return StatusNew, false
}

// readRegistrationResult 读取 handler 写入的注册结果，返回 message 与 at
func readRegistrationResult(installDir string) (message, at string) {
	b, err := os.ReadFile(filepath.Join(installDir, RegistrationResultFile))
	if err != nil {
		return "", ""
	}
	var v struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
		At      string `json:"at"`
	}
	if json.Unmarshal(b, &v) != nil {
		return "", ""
	}
	return v.Message, v.At
}

// readGitHubStatus 读取 cron 写入的 GitHub 检查结果
func readGitHubStatus(installDir string) (registered *bool, checkAt string) {
	b, err := os.ReadFile(filepath.Join(installDir, GitHubStatusFile))
	if err != nil {
		return nil, ""
	}
	var v struct {
		Registered bool   `json:"registered"`
		LastCheck  string `json:"last_check"`
	}
	if json.Unmarshal(b, &v) != nil {
		return nil, ""
	}
	reg := v.Registered
	return &reg, v.LastCheck
}

// WriteGitHubStatus 由 cron 调用，写入 GitHub 检查结果到 runner 目录
func WriteGitHubStatus(installDir string, registered bool) error {
	p := filepath.Join(installDir, GitHubStatusFile)
	body := struct {
		Registered bool   `json:"registered"`
		LastCheck  string `json:"last_check"`
	}{Registered: registered, LastCheck: time.Now().Format(time.RFC3339)}
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	return os.WriteFile(p, b, 0644)
}

// isProcessRunning 检测 Runner 进程是否存活：读取 pid 文件并校验进程存在（非 Windows）；
// Windows 上仍为简化实现（仅看 pid 文件是否存在），注释中已说明。
func isProcessRunning(installDir string) bool {
	pid, err := readRunnerPid(installDir)
	if err != nil || pid <= 0 {
		return false
	}
	if runtime.GOOS == "windows" {
		// Windows 上不查进程表，仅依据 pid 文件存在视为可能运行中
		return true
	}
	return processExists(pid)
}

// EnsureRunnerDir 确保 runner 目录存在并返回路径，且必须在 base_path 之下（防路径穿越）
func EnsureRunnerDir(cfg *config.Config, name, subPath string) (string, error) {
	dir := subPath
	if dir == "" {
		dir = name
	}
	dir = filepath.Clean(dir)
	if strings.Contains(dir, "..") || filepath.IsAbs(dir) {
		dir = name
	}
	baseAbs, err := filepath.Abs(cfg.Runners.BasePath)
	if err != nil {
		baseAbs = cfg.Runners.BasePath
	}
	abs := filepath.Join(baseAbs, dir)
	abs, err = filepath.Abs(abs)
	if err != nil {
		abs = filepath.Join(baseAbs, dir)
	}
	// 确保安装目录在 base_path 之下
	rel, err := filepath.Rel(baseAbs, abs)
	if err != nil || strings.HasPrefix(rel, "..") || rel == ".." {
		return "", os.ErrInvalid
	}
	return abs, os.MkdirAll(abs, 0755)
}

// ConfigScriptName 返回当前系统的配置脚本名
func ConfigScriptName() string {
	if runtime.GOOS == "windows" {
		return "config.cmd"
	}
	return "config.sh"
}

// RunScriptName 返回当前系统的运行脚本名
func RunScriptName() string {
	if runtime.GOOS == "windows" {
		return "run.cmd"
	}
	return "run.sh"
}

// readRunnerPid 读取 runner 的 pid 文件，返回 pid，无效则 0 与 error。
// Runner.Listener.pid 为官方 runner 使用；.path 为部分版本或兼容用途。
func readRunnerPid(installDir string) (int, error) {
	for _, name := range []string{"Runner.Listener.pid", ".path"} {
		f := filepath.Join(installDir, name)
		b, err := os.ReadFile(f)
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

// processExists 检查进程是否存在（Unix 下用 Kill(pid, 0)）
func processExists(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// Unix: signal 0 不发送信号，仅检查进程是否存在
	if runtime.GOOS != "windows" {
		err := process.Signal(syscall.Signal(0))
		return err == nil
	}
	// Windows: FindProcess 不保证进程存活，保守返回 true 由上层仅用 pid 文件判断
	return true
}

var execCommand = exec.Command

// Start 在 installDir 下后台启动 runner（执行 run.sh/run.cmd）
// 将 installDir 转为绝对路径，避免相对路径在 exec 时随进程 CWD 解析导致找不到 run.sh
func Start(installDir string) error {
	absDir, err := filepath.Abs(installDir)
	if err != nil {
		return fmt.Errorf("解析 runner 路径失败: %w", err)
	}
	installDir = absDir
	script := filepath.Join(installDir, RunScriptName())
	if _, err := os.Stat(script); err != nil {
		return fmt.Errorf("未找到运行脚本 %s: %w", script, err)
	}
	cmd := execCommand(script)
	cmd.Dir = installDir
	cmd.Env = os.Environ()
	if runtime.GOOS != "windows" {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	// 必须对子进程 Wait，否则在容器内（主进程为 PID 1）退出的 run.sh 会变成僵尸进程。
	// 在后台 goroutine 中 Wait，不阻塞 Start 返回。
	go func() {
		_ = cmd.Wait()
	}()
	return nil
}

// Stop 向 runner 进程发送停止信号（读取 pid 后 SIGTERM；Windows 用 taskkill）
// 将 installDir 转为绝对路径，确保能正确找到该 runner 的 pid 文件
func Stop(installDir string) error {
	absDir, err := filepath.Abs(installDir)
	if err != nil {
		return fmt.Errorf("解析 runner 路径失败: %w", err)
	}
	installDir = absDir
	pid, err := readRunnerPid(installDir)
	if err != nil || pid <= 0 {
		return fmt.Errorf("未找到 runner pid 文件或 pid 无效: %w", err)
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
