package runner

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"github.com/lab-dev/github-actions-runner-manager/internal/config"
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
	Name       string   `json:"name"`
	Path       string   `json:"path"`
	TargetType string   `json:"target_type"`
	Target     string   `json:"target"`
	Labels     []string `json:"labels"`
	Status     Status   `json:"status"`
	InstallDir string   `json:"install_dir"`
	Running    bool     `json:"running"` // 进程是否在跑（简化：仅看 pid 文件或 run.sh 进程）
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
		if item.Path == "" {
			info.Path = item.Name
		}
		info.Status, info.Running = getStatus(installDir)
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
		if item.Path == "" {
			info.Path = item.Name
		}
		info.Status, info.Running = getStatus(installDir)
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

// readRunnerPid 读取 runner 的 pid 文件（Runner.Listener.pid 或 .path），返回 pid，无效则 0 与 error
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
func Start(installDir string) error {
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
	_ = cmd.Process.Release()
	return nil
}

// Stop 向 runner 进程发送停止信号（读取 pid 后 SIGTERM；Windows 用 taskkill）
func Stop(installDir string) error {
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
