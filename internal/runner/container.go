// 容器模式：通过 Docker CLI 与 Runner 容器内 Agent 实现 C/S 控制与状态查询
package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/lab-dev/github-actions-runner-manager/internal/config"
)

// ContainerName 将 runner 名称转为合法容器名（仅保留字母数字横线，并加前缀）
func ContainerName(name string) string {
	re := regexp.MustCompile(`[^a-zA-Z0-9_-]+`)
	safe := re.ReplaceAllString(name, "-")
	safe = strings.Trim(safe, "-")
	if safe == "" {
		safe = "runner"
	}
	return "github-runner-" + safe
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
		return nil, fmt.Errorf("agent 返回 %d", resp.StatusCode)
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
		return fmt.Errorf("agent /start 返回 %d", resp.StatusCode)
	}
	return nil
}

func dockerCmd(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "docker", args...)
	return cmd.CombinedOutput()
}

// ContainerRunning 判断容器是否在运行
func ContainerRunning(ctx context.Context, containerName string) (bool, error) {
	out, err := dockerCmd(ctx, "inspect", "-f", "{{.State.Running}}", containerName)
	if err != nil {
		if strings.Contains(string(out), "No such container") {
			return false, nil
		}
		return false, fmt.Errorf("docker inspect: %w", err)
	}
	return strings.TrimSpace(string(out)) == "true", nil
}

// StartRunnerContainer 若容器不存在则创建并启动，若存在则 start；创建时挂载 installDir 到 /runner
func StartRunnerContainer(ctx context.Context, cfg *config.Config, runnerName, installDir string) error {
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
		return fmt.Errorf("docker ps: %w", err)
	}
	if len(strings.TrimSpace(string(out))) > 0 {
		_, err := dockerCmd(ctx, "start", cn)
		if err != nil {
			return fmt.Errorf("docker start: %w", err)
		}
		time.Sleep(2 * time.Second)
		return CallAgentStart(ctx, cn, cfg.Runners.AgentPort)
	}
	// 创建新容器
	img := cfg.Runners.ContainerImage
	if img == "" {
		img = "ghcr.io/soulteary/runner-fleet-runner:main"
	}
	network := cfg.Runners.ContainerNetwork
	if network == "" {
		network = "runner-net"
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
	// 容器内 DOCKER_HOST 指向 DinD，供 Job 内 docker build 等使用
	createArgs := []string{
		"create",
		"--name", cn,
		"-v", mountSrc + ":/runner",
		"--network", network,
		"-e", "DOCKER_HOST=tcp://" + dindHost + ":2375",
		img,
	}
	out, err = dockerCmd(ctx, createArgs...)
	if err != nil {
		return fmt.Errorf("docker create: %s: %w", string(out), err)
	}
	_, err = dockerCmd(ctx, "start", cn)
	if err != nil {
		return fmt.Errorf("docker start: %w", err)
	}
	// 等待 agent 就绪后调 /start
	time.Sleep(3 * time.Second)
	return CallAgentStart(ctx, cn, cfg.Runners.AgentPort)
}

// StopRunnerContainer 停止容器（不删除，便于下次 start）
func StopRunnerContainer(ctx context.Context, runnerName string) error {
	cn := ContainerName(runnerName)
	_, err := dockerCmd(ctx, "stop", "-t", "30", cn)
	if err != nil {
		out, _ := dockerCmd(ctx, "inspect", "-f", "{{.State.Running}}", cn)
		if strings.Contains(string(out), "No such container") {
			return nil
		}
		return fmt.Errorf("docker stop: %w", err)
	}
	return nil
}

// RemoveRunnerContainer 停止并删除 Runner 容器（移除 runner 时调用）
func RemoveRunnerContainer(ctx context.Context, runnerName string) error {
	cn := ContainerName(runnerName)
	_, _ = dockerCmd(ctx, "stop", "-t", "30", cn)
	out, err := dockerCmd(ctx, "rm", "-f", cn)
	if err != nil {
		if strings.Contains(string(out), "No such container") {
			return nil
		}
		return fmt.Errorf("docker rm: %w", err)
	}
	return nil
}

// ContainerRunnerStatus 在容器模式下获取某 runner 的状态：先看容器是否运行，再问 Agent
// 容器未运行时仍返回 StatusInstalled（与磁盘一致），仅 Running=false，便于界面显示「已注册未运行」
func ContainerRunnerStatus(ctx context.Context, cfg *config.Config, runnerName, installDir string) (running bool, status Status, err error) {
	cn := ContainerName(runnerName)
	ok, err := ContainerRunning(ctx, cn)
	if err != nil || !ok {
		return false, StatusInstalled, nil // 容器未跑时保留「已注册」状态，不覆盖为 unknown
	}
	agent, err := GetAgentStatus(ctx, cn, cfg.Runners.AgentPort)
	if err != nil {
		return true, StatusInstalled, nil // 容器在跑但 agent 未响应，保守视为 installed
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
