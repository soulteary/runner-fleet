package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// mu 保护配置文件的读写，避免并发写导致覆盖
var mu sync.Mutex
var runnerContainerNameSanitizeRe = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)

// DefaultRunnerImageRepo 默认 Runner 镜像仓库名，与 Manager 同仓库
const DefaultRunnerImageRepo = "ghcr.io/soulteary/runner-fleet"

// DefaultRunnerContainerImage 返回默认 Runner 容器镜像（未配置 container_image 时使用）。
// Tag 取自环境变量 FLEET_IMAGE_TAG，未设置时为 v1.0.0；镜像名为 {repo}:{tag}-runner。
func DefaultRunnerContainerImage() string {
	tag := strings.TrimSpace(os.Getenv("FLEET_IMAGE_TAG"))
	if tag == "" {
		tag = "v1.0.0"
	}
	return DefaultRunnerImageRepo + ":" + tag + "-runner"
}

// Config 应用配置
type Config struct {
	Server  ServerConfig  `yaml:"server"`
	Runners RunnersConfig `yaml:"runners"`
}

// ServerConfig HTTP 服务配置
type ServerConfig struct {
	Port int    `yaml:"port"`
	Addr string `yaml:"addr"`
}

// RunnersConfig Runner 根配置
type RunnersConfig struct {
	BasePath string       `yaml:"base_path"` // 所有 runner 安装的根目录
	Items    []RunnerItem `yaml:"items"`

	// 容器模式：Runner 运行在独立容器中，Manager 通过 Docker API 启停并透过 Agent 获取状态
	ContainerMode    bool   `yaml:"container_mode"`    // 为 true 时启停与状态均走容器
	ContainerImage   string `yaml:"container_image"`   // Runner 容器镜像，未填时由 DefaultRunnerContainerImage() 决定（FLEET_IMAGE_TAG 或 v1.0.0）
	ContainerNetwork string `yaml:"container_network"` // 容器所在网络，与 Manager 同网以便访问 Agent，默认 runner-net
	AgentPort        int    `yaml:"agent_port"`        // 容器内 Agent 端口，默认 8081
	// Job Docker 后端：Runner 容器内 Job 执行 docker 命令时的后端。dind=DinD 服务；host-socket=挂载宿主机 socket；none=不提供 Docker
	JobDockerBackend string `yaml:"job_docker_backend"` // dind | host-socket | none，默认 dind
	DindHost         string `yaml:"dind_host"`          // 仅 job_docker_backend=dind 时有效，DinD 主机名，默认 runner-dind
	VolumeHostPath   string `yaml:"volume_host_path"`   // 容器模式下宿主机上 runners 根路径，供 docker create -v 使用；Manager 自身在容器内时必填（如 /data/runners）
}

// RunnerItem 单个 Runner 配置
type RunnerItem struct {
	Name       string   `yaml:"name"`        // 显示名称，也用作目录名
	Path       string   `yaml:"path"`        // 相对 base_path 的目录，空则用 name
	TargetType string   `yaml:"target_type"` // org | repo
	Target     string   `yaml:"target"`      // org 名或 owner/repo
	Labels     []string `yaml:"labels"`      // 自定义标签
}

// InstallPath 返回该 runner 的完整安装路径
func (r RunnerItem) InstallPath(basePath string) string {
	dir := r.Path
	if dir == "" {
		dir = r.Name
	}
	return filepath.Join(basePath, filepath.Clean(dir))
}

// Load 从文件加载配置
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	// 默认值
	c.Server.Addr = strings.TrimSpace(c.Server.Addr)
	c.Runners.BasePath = strings.TrimSpace(c.Runners.BasePath)
	if c.Server.Port == 0 {
		c.Server.Port = 8080
	}
	if c.Runners.BasePath == "" {
		c.Runners.BasePath = "./runners"
	}
	c.Runners.ContainerImage = strings.TrimSpace(c.Runners.ContainerImage)
	c.Runners.ContainerNetwork = strings.TrimSpace(c.Runners.ContainerNetwork)
	c.Runners.DindHost = strings.TrimSpace(c.Runners.DindHost)
	c.Runners.VolumeHostPath = strings.TrimSpace(c.Runners.VolumeHostPath)
	if c.Runners.ContainerMode && c.Runners.ContainerImage == "" {
		c.Runners.ContainerImage = DefaultRunnerContainerImage()
	}
	if c.Runners.ContainerNetwork == "" {
		c.Runners.ContainerNetwork = "runner-net"
	}
	if c.Runners.AgentPort <= 0 {
		c.Runners.AgentPort = 8081
	}
	jobBackend := strings.ToLower(strings.TrimSpace(c.Runners.JobDockerBackend))
	if jobBackend == "" {
		jobBackend = "dind"
	}
	c.Runners.JobDockerBackend = jobBackend
	if c.Runners.JobDockerBackend == "dind" && c.Runners.DindHost == "" {
		c.Runners.DindHost = "runner-dind"
	}
	if err := Validate(&c); err != nil {
		return nil, err
	}
	return &c, nil
}

// Validate 校验配置：同名 Runner 冲突等
func Validate(c *Config) error {
	seen := make(map[string]bool)
	seenContainerNames := make(map[string]string)
	seenInstallPaths := make(map[string]string)
	validBackend := map[string]bool{
		"dind":        true,
		"host-socket": true,
		"none":        true,
	}
	jobBackend := strings.ToLower(strings.TrimSpace(c.Runners.JobDockerBackend))
	if jobBackend == "" {
		jobBackend = "dind"
		c.Runners.JobDockerBackend = jobBackend
	}
	if !validBackend[jobBackend] {
		return fmt.Errorf("runners.job_docker_backend 仅支持 dind/host-socket/none，当前为 %q", c.Runners.JobDockerBackend)
	}
	if !c.Runners.ContainerMode {
		if strings.TrimSpace(c.Runners.VolumeHostPath) != "" {
			return fmt.Errorf("runners.volume_host_path 仅在 container_mode=true 时可设置")
		}
		if jobBackend != "dind" {
			return fmt.Errorf("container_mode=false 时 runners.job_docker_backend 必须为 dind（当前为 %q）", jobBackend)
		}
	}
	if c.Runners.ContainerMode {
		if strings.TrimSpace(c.Runners.VolumeHostPath) != "" && !filepath.IsAbs(c.Runners.VolumeHostPath) {
			return fmt.Errorf("runners.volume_host_path 必须为宿主机绝对路径")
		}
		baseClean := filepath.Clean(c.Runners.BasePath)
		if strings.TrimSpace(c.Runners.VolumeHostPath) == "" && strings.HasPrefix(baseClean, "/app") {
			return fmt.Errorf("container_mode=true 且 base_path=%s 时必须设置 runners.volume_host_path（宿主机 runners 根目录绝对路径）", c.Runners.BasePath)
		}
	}
	for i, item := range c.Runners.Items {
		name := strings.TrimSpace(item.Name)
		path := strings.TrimSpace(item.Path)
		targetType := strings.ToLower(strings.TrimSpace(item.TargetType))
		target := strings.TrimSpace(item.Target)
		if name == "" {
			return fmt.Errorf("runners.items[%d].name 不能为空", i)
		}
		if !IsSafeRunnerNameOrPath(name) {
			return fmt.Errorf("runners.items[%d].name 包含非法字符（不允许 .. / \\\\）: %s", i, name)
		}
		if path != "" && !IsSafeRunnerNameOrPath(path) {
			return fmt.Errorf("runners.items[%d].path 包含非法字符（不允许 .. / \\\\）: %s", i, path)
		}
		if err := ValidateTarget(targetType, target); err != nil {
			return fmt.Errorf("runners.items[%d]: %w", i, err)
		}
		if seen[name] {
			return fmt.Errorf("runners.items 中存在同名 Runner: %s", name)
		}
		seen[name] = true
		installPath := item.InstallPath(c.Runners.BasePath)
		installKey := filepath.Clean(installPath)
		if existing, ok := seenInstallPaths[installKey]; ok {
			return fmt.Errorf("runners.items 安装目录冲突: %s 与 %s 均映射到 %s", existing, name, installKey)
		}
		seenInstallPaths[installKey] = name
		if c.Runners.ContainerMode {
			containerName := NormalizedContainerName(name)
			if existing, ok := seenContainerNames[containerName]; ok {
				return fmt.Errorf("runners.items 中 Runner 名称映射后容器名冲突: %s 与 %s 均映射为 %s", existing, name, containerName)
			}
			seenContainerNames[containerName] = name
		}
	}
	return nil
}

// NormalizedContainerName 将 runner 名称转为合法容器名（仅保留字母数字横线，并加前缀），供 config 与 runner 包共用
func NormalizedContainerName(name string) string {
	safe := runnerContainerNameSanitizeRe.ReplaceAllString(name, "-")
	safe = strings.Trim(safe, "-")
	if safe == "" {
		safe = "runner"
	}
	return "github-runner-" + safe
}

// IsSafeRunnerNameOrPath 校验 name/path 不含路径穿越或非法字符（禁止 .. / \）
func IsSafeRunnerNameOrPath(s string) bool {
	if s == "" {
		return false
	}
	return !strings.Contains(s, "..") && !strings.Contains(s, "/") && !strings.Contains(s, "\\")
}

// ValidateTarget 校验 target 格式：org 为组织名（不含 /），repo 为 owner/repo（恰好一个斜杠且两端非空）
func ValidateTarget(targetType, target string) error {
	t := strings.TrimSpace(target)
	if t == "" {
		return fmt.Errorf("target 不能为空")
	}
	tt := strings.ToLower(strings.TrimSpace(targetType))
	switch tt {
	case "org":
		if strings.Contains(t, "/") {
			return fmt.Errorf("目标类型为组织(org)时，target 应为组织名，不能包含 /")
		}
		return nil
	case "repo":
		parts := strings.SplitN(t, "/", 2)
		if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
			return fmt.Errorf("目标类型为仓库(repo)时，target 应为 owner/repo 格式（且 owner 与 repo 均非空）")
		}
		if strings.Contains(parts[1], "/") {
			return fmt.Errorf("target 只能包含一个 /，格式为 owner/repo")
		}
		return nil
	default:
		return fmt.Errorf("target_type 必须为 org 或 repo")
	}
}

// Save 将配置写回文件（调用方需自行加锁，写操作请使用 LoadAndSave）
func (c *Config) Save(path string) error {
	if err := Validate(c); err != nil {
		return err
	}
	data, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// LoadAndSave 在持锁下加载配置、执行 fn、写回；用于所有修改配置的写操作，避免并发覆盖
func LoadAndSave(path string, fn func(*Config) error) error {
	mu.Lock()
	defer mu.Unlock()
	cfg, err := Load(path)
	if err != nil {
		return err
	}
	if err := fn(cfg); err != nil {
		return err
	}
	if err := Validate(cfg); err != nil {
		return err
	}
	return cfg.Save(path)
}
