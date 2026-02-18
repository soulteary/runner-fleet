// 容器模式：通过 Docker CLI 与 Runner 容器内 Agent 实现 C/S 控制与状态查询。
// 本包负责 Runner 容器的创建/启停/删除及与 Agent 的 HTTP 通信；Manager 仅编排，不承载 Runner 进程。
package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/lab-dev/github-actions-runner-manager/internal/config"
)

// ContainerName 将 runner 名称转为合法容器名，与 config 包规则一致
func ContainerName(name string) string {
	return config.NormalizedContainerName(name)
}

// AgentStatus 容器内 Agent /status 返回结构
type AgentStatus struct {
	Status  string `json:"status"`
	Running bool   `json:"running"`
}

// GetAgentStatus 请求 Runner 容器内 Agent 的 /status，超时 5 秒
func GetAgentStatus(ctx context.Context, containerName string, port int) (*AgentStatus, error) {
	if port <= 0 {
		port = 8081
	}
	url := fmt.Sprintf("http://%s:%d/status", containerName, port)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		msg := strings.TrimSpace(string(body))
		if msg == "" {
			return nil, fmt.Errorf("agent 返回 %d", resp.StatusCode)
		}
		return nil, fmt.Errorf("agent 返回 %d: %s", resp.StatusCode, msg)
	}
	var out AgentStatus
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CallAgentStart 请求 Runner 容器内 Agent 的 POST /start
func CallAgentStart(ctx context.Context, containerName string, port int) error {
	if port <= 0 {
		port = 8081
	}
	url := fmt.Sprintf("http://%s:%d/start", containerName, port)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		msg := strings.TrimSpace(string(body))
		if msg == "" {
			return fmt.Errorf("agent /start 返回 %d", resp.StatusCode)
		}
		return fmt.Errorf("agent /start 返回 %d: %s", resp.StatusCode, msg)
	}
	return nil
}

func dockerCmd(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "docker", args...)
	return cmd.CombinedOutput()
}

// containerNotFound 判断 docker 输出是否表示「容器不存在」（含英文/中文等）
func containerNotFound(out []byte) bool {
	s := string(out)
	lower := strings.ToLower(s)
	if strings.Contains(lower, "no such container") || strings.Contains(lower, "no such object") {
		return true
	}
	// Docker 中文环境或其它 locale 的常见提示
	if strings.Contains(s, "没有此容器") || strings.Contains(s, "没有找到容器") || strings.Contains(s, "未找到容器") {
		return true
	}
	return false
}

// containerStartUnrecoverable 判断 docker start 失败是否因网络已删除等导致无法恢复，需删容器后重建
func containerStartUnrecoverable(out []byte) bool {
	s := string(out)
	lower := strings.ToLower(s)
	if strings.Contains(lower, "network") && (strings.Contains(lower, "not found") || strings.Contains(lower, "no such")) {
		return true
	}
	if strings.Contains(lower, "could not find network") || strings.Contains(lower, "could not attach to network") {
		return true
	}
	if strings.Contains(lower, "failed to create endpoint") || strings.Contains(lower, "failed to get network") {
		return true
	}
	return false
}

// dockerPermissionDenied 判断是否为访问 Docker 权限/连接错误（宿主机 socket 需对 Manager 容器可访问）
func dockerPermissionDenied(out []byte) bool {
	s := string(out)
	lower := strings.ToLower(s)
	if strings.Contains(lower, "permission denied") || strings.Contains(lower, "permissions have not been granted") {
		return true
	}
	if strings.Contains(lower, "cannot connect to the docker daemon") || strings.Contains(lower, "is the docker daemon running") {
		return true
	}
	if strings.Contains(lower, "connection refused") {
		return true
	}
	return false
}

func dockerCmdError(op string, out []byte, err error) error {
	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" {
		trimmed = "(无输出)"
	}
	if dockerPermissionDenied(out) {
		return fmt.Errorf("%s 失败（权限不足或无法连接 daemon）。%s。输出: %s: %w", op, dockerAccessHint, trimmed, err)
	}
	return fmt.Errorf("%s 失败。输出: %s: %w", op, trimmed, err)
}

const dockerAccessHint = "若 Manager 在容器内，请为 runner-manager 配置 group_add 使用宿主机 docker 组 GID（.env 中 DOCKER_GID=$(getent group docker | cut -d: -f3)），或使用 user: \"0:0\" 以 root 访问 socket"

// ContainerRunning 判断容器是否在运行
func ContainerRunning(ctx context.Context, containerName string) (bool, error) {
	out, err := dockerCmd(ctx, "inspect", "-f", "{{.State.Running}}", containerName)
	if err != nil {
		if containerNotFound(out) {
			return false, nil
		}
		return false, dockerCmdError("docker inspect", out, err)
	}
	return strings.TrimSpace(string(out)) == "true", nil
}

// managerMustUseHostDocker 提示：容器模式下 Manager 必须用宿主机 Docker 创建 Runner 容器，不能把 DOCKER_HOST 设为 DinD
const errContainerModeNeedHostDocker = "容器模式下 Manager 必须使用宿主机 Docker（unix socket）创建/启停 Runner 容器，不能使用 DinD。请在 .env 中移除或注释 DOCKER_HOST=tcp://runner-dind:2375，使 Manager 使用默认 unix:///var/run/docker.sock；DinD 仅供 Runner 容器内 Job 的 docker build 等使用"

func managerDockerHostIsDind() bool {
	h := os.Getenv("DOCKER_HOST")
	return strings.HasPrefix(strings.TrimSpace(h), "tcp://")
}

// ManagerDockerHostIsDind 供启动时检查：若为 true 且开启容器模式，Manager 无法创建 Runner 容器
func ManagerDockerHostIsDind() bool {
	return managerDockerHostIsDind()
}

// StartRunnerContainer 若容器不存在则创建并启动，若存在则 start；创建时挂载 installDir 到 /runner
func StartRunnerContainer(ctx context.Context, cfg *config.Config, runnerName, installDir string) error {
	if cfg.Runners.ContainerMode && managerDockerHostIsDind() {
		return fmt.Errorf("%s", errContainerModeNeedHostDocker)
	}
	cn := ContainerName(runnerName)
	running, err := ContainerRunning(ctx, cn)
	if err != nil {
		return err
	}
	if running {
		// 容器已在跑，可选：调 Agent /start 确保 listener 启动（若容器刚启动 agent 可能尚未起 run.sh）
		_ = CallAgentStart(ctx, cn, cfg.Runners.AgentPort)
		return nil
	}
	// 检查是否存在但已停止
	out, err := dockerCmd(ctx, "ps", "-a", "-q", "-f", "name=^"+cn+"$")
	if err != nil {
		return dockerCmdError("docker ps", out, err)
	}
	if len(strings.TrimSpace(string(out))) > 0 {
		startOut, startErr := dockerCmd(ctx, "start", cn)
		if startErr == nil {
			time.Sleep(2 * time.Second)
			return CallAgentStart(ctx, cn, cfg.Runners.AgentPort)
		}
		// start 失败且为网络已删除等不可恢复原因时，删除旧容器并走下方「创建新容器」流程（如 compose down 后网络被删）
		if containerStartUnrecoverable(startOut) {
			_, _ = dockerCmd(ctx, "rm", "-f", cn)
			// fall through to create new container
		} else {
			return dockerCmdError("docker start", startOut, startErr)
		}
	}
	// 创建新容器
	// 容器模式下若 Manager 在容器内（base_path 通常为 /app/runners），未设置 volume_host_path 会导致 docker create -v 使用容器内路径，宿主机上无效
	if cfg.Runners.ContainerMode && strings.TrimSpace(cfg.Runners.VolumeHostPath) == "" {
		baseClean := filepath.Clean(cfg.Runners.BasePath)
		if strings.HasPrefix(baseClean, "/app") || strings.HasPrefix(filepath.Clean(installDir), "/app") {
			return fmt.Errorf("容器模式下 Manager 若在容器内运行，必须在 config.yaml 中设置 runners.volume_host_path 为宿主机上 runners 根目录的绝对路径（当前 base_path 为 %s）", cfg.Runners.BasePath)
		}
	}
	img := cfg.Runners.ContainerImage
	if img == "" {
		img = "ghcr.io/soulteary/runner-fleet:main-runner"
	}
	network := cfg.Runners.ContainerNetwork
	if network == "" {
		network = "runner-net"
	}
	jobBackend := strings.ToLower(strings.TrimSpace(cfg.Runners.JobDockerBackend))
	if jobBackend == "" {
		jobBackend = "dind"
	}
	dindHost := cfg.Runners.DindHost
	if dindHost == "" {
		dindHost = "runner-dind"
	}
	mountSrc := installDir
	if cfg.Runners.VolumeHostPath != "" {
		// Manager 在容器内时，installDir 为容器内路径；Docker 需宿主机路径，用 volume_host_path + 相对路径
		rel, err := filepath.Rel(cfg.Runners.BasePath, installDir)
		if err != nil {
			rel = filepath.Base(installDir)
		}
		mountSrc = filepath.Join(cfg.Runners.VolumeHostPath, rel)
	} else {
		// Manager 在宿主机时，传绝对路径给 docker create，避免 cwd 影响
		if abs, err := filepath.Abs(installDir); err == nil {
			mountSrc = abs
		}
	}
	createArgs := []string{
		"create",
		"--name", cn,
		"-v", mountSrc + ":/runner",
		"--network", network,
	}
	switch jobBackend {
	case "dind":
		createArgs = append(createArgs, "-e", "DOCKER_HOST=tcp://"+dindHost+":2375")
	case "host-socket":
		createArgs = append(createArgs, "-v", "/var/run/docker.sock:/var/run/docker.sock", "-e", "DOCKER_HOST=unix:///var/run/docker.sock")
	case "none":
		// Job 内不提供 Docker，不注入环境与挂载
	default:
		return fmt.Errorf("不支持的 runners.job_docker_backend=%q（仅支持 dind/host-socket/none）", cfg.Runners.JobDockerBackend)
	}
	createArgs = append(createArgs, img)
	out, err = dockerCmd(ctx, createArgs...)
	if err != nil {
		return dockerCmdError("docker create", out, err)
	}
	out, err = dockerCmd(ctx, "start", cn)
	if err != nil {
		return dockerCmdError("docker start", out, err)
	}
	// 等待 agent 就绪后调 /start
	time.Sleep(3 * time.Second)
	return CallAgentStart(ctx, cn, cfg.Runners.AgentPort)
}

// StopRunnerContainer 停止容器（不删除，便于下次 start）
func StopRunnerContainer(ctx context.Context, runnerName string) error {
	cn := ContainerName(runnerName)
	out, err := dockerCmd(ctx, "stop", "-t", "30", cn)
	if err != nil {
		inspectOut, _ := dockerCmd(ctx, "inspect", "-f", "{{.State.Running}}", cn)
		if containerNotFound(inspectOut) {
			return nil
		}
		return dockerCmdError("docker stop", out, err)
	}
	return nil
}

// RemoveRunnerContainer 停止并删除 Runner 容器（移除 runner 时调用）
func RemoveRunnerContainer(ctx context.Context, runnerName string) error {
	cn := ContainerName(runnerName)
	_, _ = dockerCmd(ctx, "stop", "-t", "30", cn)
	out, err := dockerCmd(ctx, "rm", "-f", cn)
	if err != nil {
		if containerNotFound(out) {
			return nil
		}
		return dockerCmdError("docker rm", out, err)
	}
	return nil
}

// ContainerRunnerStatus 在容器模式下获取某 runner 的状态：先看容器是否运行，再问 Agent
// 容器未运行时仍返回 StatusInstalled（与磁盘一致），仅 Running=false，便于界面显示「已注册未运行」
func ContainerRunnerStatus(ctx context.Context, cfg *config.Config, runnerName, installDir string) (running bool, status Status, err error) {
	cn := ContainerName(runnerName)
	ok, err := ContainerRunning(ctx, cn)
	if err != nil {
		return false, StatusUnknown, newProbeError(ProbeErrorTypeDockerAccess, err)
	}
	if !ok {
		return false, StatusInstalled, nil // 容器未跑时保留「已注册」状态，不覆盖为 unknown
	}
	agent, err := GetAgentStatus(ctx, cn, cfg.Runners.AgentPort)
	if err != nil {
		agentErrType := ProbeErrorTypeAgentConnect
		if strings.Contains(err.Error(), "agent 返回") {
			agentErrType = ProbeErrorTypeAgentHTTP
		}
		return true, StatusUnknown, newProbeError(agentErrType, err)
	}
	switch agent.Status {
	case "installed":
		return agent.Running, StatusInstalled, nil
	case "new":
		return false, StatusNew, nil
	default:
		return false, StatusMissing, nil
	}
}
