package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"github.com/soulteary/runner-fleet/internal/atomicfile"
	"github.com/soulteary/runner-fleet/internal/childenv"
	"github.com/soulteary/runner-fleet/internal/config"
	"github.com/soulteary/runner-fleet/internal/runnerproc"
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
	Name                  string     `json:"name"`
	Path                  string     `json:"path"`
	TargetType            string     `json:"target_type"`
	Target                string     `json:"target"`
	Labels                []string   `json:"labels"`
	Status                Status     `json:"status"`
	InstallDir            string     `json:"install_dir"`
	Running               bool       `json:"running"`                      // 进程是否在跑
	Probe                 *ProbeInfo `json:"probe,omitempty"`              // 结构化探测信息（error/type/suggestion/check_command/fix_command）
	JobDockerBackend      string     `json:"job_docker_backend"`           // 容器模式下 Job 内 Docker 后端：dind / host-socket / none
	ContainerDrift        string     `json:"container_drift,omitempty"`    // 容器创建参数与当前配置的差异，非空表示容器是按旧配置建的
	RegistrationMessage   string     `json:"registration_message"`         // 最近一次注册结果信息（成功或失败原因）
	RegistrationCheckedAt string     `json:"registration_checked_at"`      // 注册结果时间
	RegisteredOnGitHub    *bool      `json:"registered_on_github"`         // cron 通过 GitHub API 检查是否在 GitHub 显示，nil 表示未检查或查不到答案
	GitHubCheckAt         string     `json:"github_check_at"`              // 最近一次 GitHub 检查时间
	GitHubCheckError      string     `json:"github_check_error,omitempty"` // 查不到答案时的原因（令牌过期、限流、网络不通等）
	GitHubBusy            *bool      `json:"github_busy,omitempty"`        // GitHub 侧该 Runner 是否正在跑 Job，nil 表示不知道（没查、没查到、或没配 PAT）
	GitHubPublic          *bool      `json:"github_public,omitempty"`      // 目标仓库是否公开，nil 表示不知道（组织目标、查不到、或还没查过）
	GitHubURL             string     `json:"github_url,omitempty"`         // 该目标在 GitHub 上的 Actions Runners 设置页，target 非法时为空
}

// GitHubYes / GitHubNo / GitHubUnknown 供模板判断三态。
//
// 模板里不能直接写 {{if .RegisteredOnGitHub}}：html/template 对指针只看是否为 nil，
// 指向 false 的指针同样为真，于是「GitHub 上没有这个 Runner」会被渲染成「GitHub ✓」。
func (r RunnerInfo) GitHubYes() bool { return r.RegisteredOnGitHub != nil && *r.RegisteredOnGitHub }

// GitHubNo 表示查到了答案且答案是「没有」
func (r RunnerInfo) GitHubNo() bool { return r.RegisteredOnGitHub != nil && !*r.RegisteredOnGitHub }

// GitHubUnknown 表示这次检查没能得出答案（与「从未检查」由 GitHubCheckAt 区分）
func (r RunnerInfo) GitHubUnknown() bool { return r.RegisteredOnGitHub == nil }

// GitHubBusyYes 表示上次查询时 GitHub 说它正在跑 Job。
// 和上面三个同理：GitHubBusy 是 *bool，模板里写 {{if .GitHubBusy}} 会把
// 指向 false 的指针也当成真，于是「空闲」显示成「忙碌中」。
func (r RunnerInfo) GitHubBusyYes() bool { return r.GitHubBusy != nil && *r.GitHubBusy }

// GitHubPublicYes 表示上次查询时 GitHub 说这个目标仓库是公开的。
// 同样不能在模板里写 {{if .GitHubPublic}}：指向 false 的指针也为真，
// 于是每个私有仓库都会挂上「公开仓库」徽标——恰好是最不该误报的那一个。
func (r RunnerInfo) GitHubPublicYes() bool { return r.GitHubPublic != nil && *r.GitHubPublic }

// ProbeInfo 为容器探测失败的结构化信息。
type ProbeInfo struct {
	Error        string `json:"error"`
	Type         string `json:"type"`
	Suggestion   string `json:"suggestion"`
	CheckCommand string `json:"check_command"`
	FixCommand   string `json:"fix_command"`
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
		info.applyGitHubStatus(installDir)
		info.GitHubURL = GitHubSettingsURL(item.TargetType, item.Target)
		return info
	}
	return nil
}

// List 根据配置与磁盘状态列出所有 runner。
//
// 注意：容器模式下每项的 Running 不可信——Runner 进程在各自的容器里，
// 本进程扫 /proc 看不到它们。需要真实运行状态的调用方请用 ListWithLiveStatus。
func List(cfg *config.Config) []RunnerInfo {
	if cfg == nil {
		return nil
	}
	base := cfg.Runners.BasePath
	list := make([]RunnerInfo, 0, len(cfg.Runners.Items))
	dirs := make([]string, 0, len(cfg.Runners.Items))
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
		info.Status = diskStatus(installDir)
		info.RegistrationMessage, info.RegistrationCheckedAt = readRegistrationResult(installDir)
		info.applyGitHubStatus(installDir)
		info.GitHubURL = GitHubSettingsURL(item.TargetType, item.Target)
		list = append(list, info)
		dirs = append(dirs, installDir)
	}
	// 一次扫描认领所有安装目录：逐个判定等于把整张进程表读 len(list) 遍
	procs := runnerproc.FindMany(dirs)
	for i := range list {
		list[i].Running = list[i].Status == StatusInstalled && len(procs[list[i].InstallDir]) > 0
	}
	return list
}

// ListWithLiveStatus 在 List 之上补齐真实运行状态：容器模式下逐个问容器内的 Agent，
// 默认模式下 List 已经是本机进程的真实状态，直接返回。
//
// 后台的「已注册未运行则拉起」两个循环必须用这个，不能用 List：容器模式下
// List 的 Running 恒为 false（Manager 与 Runner 不在同一个 PID namespace），
// 于是每一轮巡检都会把每个 runner 再拉起一遍。
//
// 探测失败时把该项置为 StatusUnknown 而不是保留 installed：拉起的前提是
// 「确知它没在跑」，Docker 不可达时并不确知，此时什么都不做比反复重启稳妥。
func ListWithLiveStatus(ctx context.Context, cfg *config.Config) []RunnerInfo {
	if cfg == nil {
		return nil
	}
	list := List(cfg)
	if !cfg.Runners.ContainerMode {
		return list
	}
	for i := range list {
		running, status, _, err := ContainerRunnerStatus(ctx, cfg, list[i].Name, list[i].InstallDir)
		if err != nil {
			list[i].Status = StatusUnknown
			list[i].Running = false
			continue
		}
		list[i].Running = running
		list[i].Status = status
	}
	return list
}

// diskStatus 只看目录本身：不存在为 missing，有 .runner 为 installed，否则 new。
// 与进程探测分开，好让 List 用一次 /proc 扫描判定一整批 runner。
func diskStatus(installDir string) Status {
	if installDir == "" {
		return StatusMissing
	}
	fi, err := os.Stat(installDir)
	if err != nil || !fi.IsDir() {
		return StatusMissing
	}
	// 已注册的 runner 会有 .runner 文件
	if _, err := os.Stat(filepath.Join(installDir, ".runner")); err == nil {
		return StatusInstalled
	}
	return StatusNew
}

func getStatus(installDir string) (Status, bool) {
	status := diskStatus(installDir)
	if status != StatusInstalled {
		return status, false
	}
	return status, isProcessRunning(installDir)
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

// GitHubStatus 是 .github_status.json 的内容：上一次 GitHub 查询留下的结论。
//
// 三个 *bool 都是三态，nil 一律是「不知道」而不是 false。把「没查出来」写成 false，
// 界面上就会对一个根本没查到的东西打包票——这正是本文件里那几条注释反复在说的事。
type GitHubStatus struct {
	// Registered 该 Runner 是否已在 GitHub 上登记
	Registered *bool `json:"registered"`
	// Busy GitHub 是否说它正在跑 Job
	Busy *bool `json:"busy,omitempty"`
	// Public 目标仓库是否公开。组织目标、查不到、从未查过都是 nil。
	Public *bool `json:"public,omitempty"`
	// LastCheck 最近一次「是否已登记」查询的时间（RFC3339）。
	//
	// 由调用方填，不在写入时顺手盖上当前时间：界面靠它是否为空把「从未检查」
	// 与「查过但失败」分开说，而没有 PAT 时这个查询根本不会发生——
	// 可见性那一半却仍然会写文件（匿名也能查）。盖上时间就等于对着一个
	// 从未查过的 Runner 说「查过了，失败」。
	LastCheck string `json:"last_check,omitempty"`
	// Error 查不到答案时的原因（令牌过期、限流、网络不通等）
	Error string `json:"error,omitempty"`
	// VisibilityCheckedAt 最近一次问过仓库可见性的时间（RFC3339），空表示从未问过。
	//
	// 可见性几乎不变，而匿名请求每小时只有 60 次，靠它把查询压到每个仓库一天一次。
	VisibilityCheckedAt string `json:"visibility_checked_at,omitempty"`
}

// ReadGitHubStatus 读取上一次 GitHub 查询的结论。
//
// 文件不存在、或解析不了，一律读成零值——那是「从未查过」，所有字段都为空。
// 老版本写下的文件里 registered 是普通 bool，反序列化成非 nil 指针，语义不变；
// 没有 public / visibility_checked_at 的老文件读出 nil 与空串，也就是「还没查过可见性」。
func ReadGitHubStatus(installDir string) GitHubStatus {
	var v GitHubStatus
	b, err := os.ReadFile(filepath.Join(installDir, GitHubStatusFile))
	if err != nil {
		return GitHubStatus{}
	}
	if json.Unmarshal(b, &v) != nil {
		return GitHubStatus{}
	}
	return v
}

// WriteGitHubStatus 记录一次 GitHub 查询的结论。
//
// 整个文件一次写完，所以调用方要把这次没重查的字段原样带上——
// 典型做法是先 ReadGitHubStatus 拿到上一次的结论再改其中几项，
// 否则每五分钟一次的登记检查会把一天只查一次的可见性结论抹掉。
func WriteGitHubStatus(installDir string, st GitHubStatus) error {
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	// 原子替换：列表渲染随时可能在读这个文件，就地截断重写会让它读到半截 JSON，
	// 那会被当成「这个 Runner 没有 GitHub 状态」显示出来。
	return atomicfile.WriteFile(filepath.Join(installDir, GitHubStatusFile), b, 0644)
}

// applyGitHubStatus 把磁盘上那份结论填进 info。
func (r *RunnerInfo) applyGitHubStatus(installDir string) {
	gh := ReadGitHubStatus(installDir)
	r.RegisteredOnGitHub, r.GitHubBusy, r.GitHubPublic = gh.Registered, gh.Busy, gh.Public
	r.GitHubCheckAt, r.GitHubCheckError = gh.LastCheck, gh.Error
}

// isProcessRunning 检测本机是否有属于该安装目录的 Runner 进程存活。
//
// 只对「Runner 进程与 Manager 同在一个 PID namespace」的默认模式有意义。
// 容器模式下 Runner 跑在各自的容器里，Manager 扫自己的 /proc 必然找不到，
// 判定要走 ContainerRunnerStatus 问容器内的 Agent——见 ListWithLiveStatus。
func isProcessRunning(installDir string) bool {
	return runnerproc.Running(installDir)
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
	// base_path 本身沿用 0755 创建，只把 Runner 自己的安装目录收到 0700。
	//
	// config.sh 会往这个目录里写 .credentials_rsaparams——Runner 向 GitHub 表明身份用的
	// RSA 私钥。actions/runner 不给这些文件设权限（ConfigurationStore 只在 Windows 上打
	// Hidden 属性，Unix 侧完全跟 umask 走，通常是 0644），所以目录若可被他人进入，
	// 宿主机上任何本地用户都能读走它，进而冒充这个 Runner 领 Job、看到传给 Job 的 secrets。
	//
	// MkdirAll 对已存在的目录不改权限：老部署里的目录仍是 0755，由启动自检点名并给出
	// chmod 命令，而不是在这里替用户改——UID 不匹配的部署下收紧权限会把本来能跑的弄坏。
	if err := os.MkdirAll(baseAbs, 0755); err != nil {
		return "", err
	}
	return abs, os.MkdirAll(abs, RunnerDirMode)
}

// RunnerDirMode 是 Runner 安装目录的权限：只有属主可进入。
// 目录里有 GitHub 凭据，见 EnsureRunnerDir 的说明。
const RunnerDirMode = 0o700

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

var execCommand = exec.Command

// Start 在 installDir 下后台启动 runner（执行 run.sh/run.cmd）
// 将 installDir 转为绝对路径，避免相对路径在 exec 时随进程 CWD 解析导致找不到 run.sh
func Start(installDir string) error {
	absDir, err := filepath.Abs(installDir)
	if err != nil {
		return fmt.Errorf("cannot resolve the runner path: %w", err)
	}
	installDir = absDir
	script := filepath.Join(installDir, RunScriptName())
	if _, err := os.Stat(script); err != nil {
		return fmt.Errorf("run script %s not found: %w", script, err)
	}
	cmd := execCommand(script)
	cmd.Dir = installDir
	// run.sh 跑的是用户的 Job：不能把 Manager 自己的凭据传下去，见 internal/childenv
	cmd.Env = childenv.Environ()
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

// StartIfInstalled 若已注册则启动：容器模式调 StartRunnerContainer，否则调 Start。供 main 与 handler 统一“已注册未运行则启动”逻辑
func StartIfInstalled(ctx context.Context, cfg *config.Config, name, installDir string) error {
	if cfg == nil {
		return fmt.Errorf("the configuration is empty")
	}
	if cfg.Runners.ContainerMode {
		return StartRunnerContainer(ctx, cfg, name, installDir)
	}
	return Start(installDir)
}

// Stop 向该安装目录下的 Runner 进程发送 SIGTERM。
//
// runnerproc.Find 把监护脚本排在监听器之前，这里按序发信号：先让 run.sh 退出，
// 它的 while 循环才不会在监听器被终止后又拉起一个新的。
// 一个都没找到时返回错误——调用方（界面上的「停止」）需要知道没停成。
func Stop(installDir string) error {
	absDir, err := filepath.Abs(installDir)
	if err != nil {
		return fmt.Errorf("cannot resolve the runner path: %w", err)
	}
	installDir = absDir
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
		// 进程可能在扫描与发信号之间自己退了，ESRCH 不算失败
		if err := process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}
