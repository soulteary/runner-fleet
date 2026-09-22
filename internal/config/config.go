package config

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/soulteary/cli-kit/env"
	"github.com/soulteary/cli-kit/validator"
	"github.com/soulteary/runner-fleet/internal/atomicfile"
	// go.yaml.in/yaml/v3 是 gopkg.in/yaml.v3 的延续：同一份代码、同一套 API、同样的
	// yaml 包名与 struct tag，而后者停在 2022 年的最后一版不再发布。换过来之后
	// Marshal 的输出逐字节未变（满配、空配、config.yaml.example 往返三种都比过），
	// 所以线上已有的 config.yaml 不需要动一个字节。i18n-kit 的 yamlloader 同期做的
	// 是同一件事。
	"go.yaml.in/yaml/v3"
)

// mu 保护配置文件的读写，避免并发写导致覆盖。
//
// 只串行化写者，Load 刻意不加锁：Save 经 atomicfile 以 rename 发布，目标这个名字
// 要么是旧内容要么是新内容，读者不会撞见半截文件，加读锁挡不住的崩溃场景它也一并解决了。
var mu sync.Mutex
var runnerContainerNameSanitizeRe = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)

// DefaultRunnerImageRepo 默认 Runner 镜像仓库名，与 Manager 同仓库
const DefaultRunnerImageRepo = "ghcr.io/soulteary/runner-fleet"

// DefaultJobDockerBackend 未配置 job_docker_backend 时的默认后端
const DefaultJobDockerBackend = "dind"

// JobDockerBackends 是 job_docker_backend 的全部合法取值。
// 提成包级变量而不是散在 Validate 里的局部 map：全局配置与 items[] 两处都要校验，
// 错误信息也由它拼出来，改动一处即可，不会出现「多了一个取值但报错还是老三样」。
var JobDockerBackends = []string{"dind", "host-socket", "none"}

// DefaultRunnerContainerImage 返回默认 Runner 容器镜像（未配置 container_image 时使用）。
// Tag 取自环境变量 FLEET_IMAGE_TAG，未设置时为 v1.9.0；镜像名为 {repo}:{tag}-runner。
func DefaultRunnerContainerImage() string {
	// 这里刻意不改写成 env.GetTrimmed("FLEET_IMAGE_TAG", "v1.9.0")：
	// 版本号一致性检查（ci-recipes runner-fleet check-version-consistency，正则写在
	// scripts/ci-recipes.conf 的 version_baseline_regex）用 `tag = "vX.Y.Z"` 从本文件里
	// 取全仓库的基准版本号，换成函数调用后那条正则匹配不到，检查会直接以
	// 「无法解析默认镜像 tag」失败。保持这个字面形状。
	tag := strings.TrimSpace(os.Getenv("FLEET_IMAGE_TAG"))
	if tag == "" {
		tag = "v1.9.0"
	}
	return DefaultRunnerImageRepo + ":" + tag + "-runner"
}

// containerImageFromManagerImage 从 MANAGER_IMAGE 推导 Runner 镜像（image:tag -> image:tag-runner）。
// 无 tag 时返回 image:latest-runner；空或解析失败时返回空字符串。
func containerImageFromManagerImage() string {
	raw := env.GetTrimmed("MANAGER_IMAGE", "")
	if raw == "" {
		return ""
	}
	lastColon := strings.LastIndex(raw, ":")
	if lastColon == -1 {
		return raw + ":latest-runner"
	}
	image, tag := raw[:lastColon], raw[lastColon+1:]
	if tag == "" {
		return image + ":latest-runner"
	}
	return image + ":" + tag + "-runner"
}

// applyEnvOverrides 用环境变量覆盖配置字段（仅当 env 非空时覆盖）。
// 应在 Load 中默认值处理之后、Validate 之前调用。
func applyEnvOverrides(c *Config) {
	// env.GetTrimmed 的语义与这里要的完全一致：变量未设置、或 trim 后为空时返回传入的原值。
	// 于是「只在环境变量非空时覆盖」不必再逐个写成 if 块，读起来就是一张覆盖表。
	//
	// 同一字段出现两个变量名时，顺序即优先级——后一行覆盖前一行。
	c.Server.Addr = env.GetTrimmed("SERVER_ADDR", c.Server.Addr)
	c.Runners.BasePath = env.GetTrimmed("RUNNERS_BASE_PATH", c.Runners.BasePath)
	c.Runners.ContainerImage = env.GetTrimmed("RUNNER_IMAGE", c.Runners.ContainerImage)
	c.Runners.ContainerImage = env.GetTrimmed("CONTAINER_IMAGE", c.Runners.ContainerImage)
	c.Runners.ContainerNetwork = env.GetTrimmed("CONTAINER_NETWORK", c.Runners.ContainerNetwork)
	c.Runners.VolumeHostPath = env.GetTrimmed("VOLUME_HOST_PATH", c.Runners.VolumeHostPath)
	c.Runners.VolumeHostPath = env.GetTrimmed("RUNNERS_VOLUME_HOST_PATH", c.Runners.VolumeHostPath)

	// 端口刻意不用 env.GetInt：它不做 trim，而 .env 与 compose 的 environment 里
	// 带一个尾随空格是常事，那样 MANAGER_PORT=9090 会被静默丢弃、继续听 8080。
	// 这里仍按原样「trim 后再解析」，范围校验交给 Validate——在这里悄悄忽略一个
	// 越界端口，比让它一路走到 Validate 报出明确错误更难排查。
	for _, key := range []string{"MANAGER_PORT", "SERVER_PORT"} {
		if v := env.GetTrimmed(key, ""); v != "" {
			if port, err := strconv.Atoi(v); err == nil && port > 0 {
				c.Server.Port = port
			}
		}
	}
	if v := env.GetTrimmed("DOCKER_GID", ""); v != "" {
		if gid, err := strconv.Atoi(v); err == nil && gid >= 0 {
			c.Runners.DockerGID = gid
		}
	}

	// 同样不用 env.GetBool：它走 strconv.ParseBool，既不 trim，也会让
	// CONTAINER_MODE=false 反过来关掉配置文件里已开启的容器模式。
	// 这个变量从来只能开、不能关，保持原样，免得一次部署改动静默换掉运行形态。
	if v := strings.ToLower(env.GetTrimmed("CONTAINER_MODE", "")); v == "true" || v == "1" {
		c.Runners.ContainerMode = true
	}
	if v := normalizeJobDockerBackend(env.GetTrimmed("JOB_DOCKER_BACKEND", "")); v != "" {
		c.Runners.JobDockerBackend = v
	}

	// 容器模式且未设 container_image 时，优先从 MANAGER_IMAGE 推导
	if c.Runners.ContainerMode && strings.TrimSpace(c.Runners.ContainerImage) == "" {
		if derived := containerImageFromManagerImage(); derived != "" {
			c.Runners.ContainerImage = derived
		} else {
			c.Runners.ContainerImage = DefaultRunnerContainerImage()
		}
	}
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
	ContainerImage   string `yaml:"container_image"`   // Runner 容器镜像，未填时由 DefaultRunnerContainerImage() 决定（FLEET_IMAGE_TAG 或 v1.9.0）
	ContainerNetwork string `yaml:"container_network"` // 容器所在网络，与 Manager 同网以便访问 Agent，默认 runner-net
	AgentPort        int    `yaml:"agent_port"`        // 容器内 Agent 端口，默认 8081
	// Job Docker 后端：Runner 容器内 Job 执行 docker 命令时的后端。dind=DinD 服务；host-socket=挂载宿主机 socket；none=不提供 Docker
	JobDockerBackend string `yaml:"job_docker_backend"` // dind | host-socket | none，默认 dind
	DindHost         string `yaml:"dind_host"`          // 仅 job_docker_backend=dind 时有效，DinD 主机名，默认 runner-dind
	VolumeHostPath   string `yaml:"volume_host_path"`   // 容器模式下宿主机上 runners 根路径，供 docker create -v 使用；Manager 自身在容器内时必填（如 /data/runners）
	// Resources 容器模式下创建 Runner 容器时施加的资源上限。
	// 不限制时单个失控 Job（如 Gradle daemon）可耗尽整机内存，把 Manager 自身一并拖垮。
	Resources ResourceLimits `yaml:"resources,omitempty"`
	// DockerGID 仅 job_docker_backend=host-socket 时有效：创建 Runner 容器时追加的 docker 组 GID（--group-add），
	// 使容器内 app(UID 1001) 可访问挂载进来的 docker.sock；0 表示自动探测 docker.sock 所属组
	DockerGID int `yaml:"docker_gid"`
}

// ResourceLimits Runner 容器的资源上限，字段留空表示不限制。
// 取值直接透传给 docker create，语义与 docker 官方一致。
type ResourceLimits struct {
	CPUs       string `yaml:"cpus,omitempty"`        // --cpus，如 "2" 或 "1.5"
	Memory     string `yaml:"memory,omitempty"`      // --memory，如 "4g"、"512m"
	MemorySwap string `yaml:"memory_swap,omitempty"` // --memory-swap，如 "4g"；设为 "-1" 表示不限 swap
	PidsLimit  int    `yaml:"pids_limit,omitempty"`  // --pids-limit，正数生效，-1 表示不限
}

// dockerCPUsRe 匹配 docker --cpus 接受的正数（如 2、1.5、0.25）
var dockerCPUsRe = regexp.MustCompile(`^[0-9]+(\.[0-9]+)?$`)

// dockerSizeRe 匹配 docker 的内存大小写法：纯字节数，或带 b/k/m/g 单位
var dockerSizeRe = regexp.MustCompile(`^[0-9]+(\.[0-9]+)?[bkmgBKMG]?$`)

// Args 将资源上限转为 docker create 参数，未设置的字段不产生参数
func (r ResourceLimits) Args() []string {
	var args []string
	if v := strings.TrimSpace(r.CPUs); v != "" {
		args = append(args, "--cpus", v)
	}
	if v := strings.TrimSpace(r.Memory); v != "" {
		args = append(args, "--memory", v)
	}
	if v := strings.TrimSpace(r.MemorySwap); v != "" {
		args = append(args, "--memory-swap", v)
	}
	if r.PidsLimit != 0 {
		args = append(args, "--pids-limit", strconv.Itoa(r.PidsLimit))
	}
	return args
}

// Validate 校验取值格式，避免把非法值传给 docker 后才在创建容器时报错
func (r ResourceLimits) Validate() error {
	if v := strings.TrimSpace(r.CPUs); v != "" {
		// 正则挡掉 1e3/NaN 等 ParseFloat 能接受但 docker 不接受的写法，再要求数值为正
		f, err := strconv.ParseFloat(v, 64)
		if !dockerCPUsRe.MatchString(v) || err != nil || f <= 0 {
			return fmt.Errorf("runners.resources.cpus must be a positive number (for example \"2\" or \"1.5\"), got %q", r.CPUs)
		}
	}
	if v := strings.TrimSpace(r.Memory); v != "" && !dockerSizeRe.MatchString(v) {
		return fmt.Errorf("runners.resources.memory must use docker memory notation (for example \"512m\" or \"4g\"), got %q", r.Memory)
	}
	if v := strings.TrimSpace(r.MemorySwap); v != "" && v != "-1" && !dockerSizeRe.MatchString(v) {
		return fmt.Errorf("runners.resources.memory_swap must use docker memory notation or be \"-1\", got %q", r.MemorySwap)
	}
	if r.PidsLimit < -1 {
		return fmt.Errorf("runners.resources.pids_limit must be a positive number, or -1 for unlimited, got %d", r.PidsLimit)
	}
	memory := strings.TrimSpace(r.Memory)
	swap := strings.TrimSpace(r.MemorySwap)
	if swap != "" && memory == "" {
		return fmt.Errorf("runners.resources.memory_swap needs memory set as well, or docker refuses to create the container")
	}
	// docker 要求 memory-swap（内存+swap 总量）不小于 memory，否则创建容器时才报错
	if swap != "" && swap != "-1" && memory != "" {
		memBytes, memErr := parseDockerSize(memory)
		swapBytes, swapErr := parseDockerSize(swap)
		if memErr == nil && swapErr == nil && swapBytes < memBytes {
			return fmt.Errorf("runners.resources.memory_swap(%s) cannot be smaller than memory(%s): it is the total of memory plus swap, and docker refuses to create the container", r.MemorySwap, r.Memory)
		}
	}
	return nil
}

// parseDockerSize 将 docker 的内存写法解析为字节数，供跨单位比较使用。
// 接受纯字节数或带 b/k/m/g 后缀（大小写均可），与 dockerSizeRe 保持一致。
func parseDockerSize(v string) (int64, error) {
	v = strings.TrimSpace(v)
	if !dockerSizeRe.MatchString(v) {
		return 0, fmt.Errorf("not a valid docker memory value: %q", v)
	}
	mult := int64(1)
	switch last := v[len(v)-1]; last {
	case 'b', 'B':
		v = v[:len(v)-1]
	case 'k', 'K':
		mult, v = 1024, v[:len(v)-1]
	case 'm', 'M':
		mult, v = 1024*1024, v[:len(v)-1]
	case 'g', 'G':
		mult, v = 1024*1024*1024, v[:len(v)-1]
	}
	n, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0, fmt.Errorf("not a valid docker memory value: %q", v)
	}
	return int64(n * float64(mult)), nil
}

// RunnerItem 单个 Runner 配置
type RunnerItem struct {
	Name       string   `yaml:"name"`        // 显示名称，也用作目录名
	Path       string   `yaml:"path"`        // 相对 base_path 的目录，空则用 name
	TargetType string   `yaml:"target_type"` // org | repo
	Target     string   `yaml:"target"`      // org 名或 owner/repo
	Labels     []string `yaml:"labels"`      // 自定义标签

	// 以下仅容器模式有效，留空则回落到 runners 下的全局同名配置。
	// 一台机器上不同项目往往需要不同工具链（如 Flutter+Android 与 Node），
	// 全局单一镜像无法覆盖，故允许按 Runner 覆盖。
	ContainerImage   string `yaml:"container_image,omitempty"`    // 该 Runner 使用的容器镜像
	JobDockerBackend string `yaml:"job_docker_backend,omitempty"` // dind | host-socket | none
}

// FindItem 按名称查找 Runner 配置项
func (c *Config) FindItem(name string) (RunnerItem, bool) {
	for _, item := range c.Runners.Items {
		if item.Name == name {
			return item, true
		}
	}
	return RunnerItem{}, false
}

// ContainerImageFor 返回该 Runner 实际使用的容器镜像，优先级：
// items[].container_image > runners.container_image > DefaultRunnerContainerImage()
func (c *Config) ContainerImageFor(runnerName string) string {
	if item, ok := c.FindItem(runnerName); ok {
		if img := strings.TrimSpace(item.ContainerImage); img != "" {
			return img
		}
	}
	if img := strings.TrimSpace(c.Runners.ContainerImage); img != "" {
		return img
	}
	return DefaultRunnerContainerImage()
}

// JobDockerBackendFor 返回该 Runner 实际使用的 Job Docker 后端，优先级：
// items[].job_docker_backend > runners.job_docker_backend > dind
func (c *Config) JobDockerBackendFor(runnerName string) string {
	if item, ok := c.FindItem(runnerName); ok {
		if b := normalizeJobDockerBackend(item.JobDockerBackend); b != "" {
			return b
		}
	}
	if b := normalizeJobDockerBackend(c.Runners.JobDockerBackend); b != "" {
		return b
	}
	return DefaultJobDockerBackend
}

// normalizeJobDockerBackend 去空白并转小写，空值返回空字符串供调用方回落
func normalizeJobDockerBackend(v string) string {
	return strings.ToLower(strings.TrimSpace(v))
}

// InstallPath 返回该 runner 的完整安装路径
func (r RunnerItem) InstallPath(basePath string) string {
	dir := r.Path
	if dir == "" {
		dir = r.Name
	}
	return filepath.Join(basePath, filepath.Clean(dir))
}

// defaultConfig 返回与 Load 中默认值一致的配置（不读文件、不应用环境变量），用于文件不存在时从 env 生成配置。
func defaultConfig() *Config {
	return &Config{
		Server: ServerConfig{
			Port: 8080,
			Addr: "0.0.0.0",
		},
		Runners: RunnersConfig{
			BasePath:         "./runners",
			Items:            []RunnerItem{},
			ContainerMode:    false,
			ContainerImage:   "",
			ContainerNetwork: "runner-net",
			AgentPort:        8081,
			JobDockerBackend: DefaultJobDockerBackend,
			DindHost:         "runner-dind",
			VolumeHostPath:   "",
			DockerGID:        0, // 0 = 自动探测 docker.sock 所属组
		},
	}
}

// Load 从文件加载配置。若文件不存在，则基于默认配置与环境变量生成配置并写入 path，然后返回。
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			c := defaultConfig()
			applyEnvOverrides(c)
			if err := Validate(c); err != nil {
				return nil, err
			}
			dir := filepath.Dir(path)
			if mkdirErr := os.MkdirAll(dir, 0755); mkdirErr != nil {
				log.Printf("warning: cannot create the config directory %s: %v, the config file will not be written", dir, mkdirErr)
			} else if saveErr := c.Save(path); saveErr != nil {
				log.Printf("warning: cannot write the config to %s: %v, continuing with the in-memory config", path, saveErr)
			}
			return c, nil
		}
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
	jobBackend := normalizeJobDockerBackend(c.Runners.JobDockerBackend)
	if jobBackend == "" {
		jobBackend = DefaultJobDockerBackend
	}
	c.Runners.JobDockerBackend = jobBackend
	if c.Runners.JobDockerBackend == DefaultJobDockerBackend && c.Runners.DindHost == "" {
		c.Runners.DindHost = "runner-dind"
	}
	applyEnvOverrides(&c)
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
	jobBackend := normalizeJobDockerBackend(c.Runners.JobDockerBackend)
	if jobBackend == "" {
		jobBackend = DefaultJobDockerBackend
		c.Runners.JobDockerBackend = jobBackend
	}
	if validator.ValidateEnumCaseInsensitive(jobBackend, JobDockerBackends) != nil {
		return fmt.Errorf("runners.job_docker_backend only supports %s, got %q",
			strings.Join(JobDockerBackends, "/"), c.Runners.JobDockerBackend)
	}
	// Port 为 0 表示「没写」，由 Load 填默认值，这里不管；非 0 才校验范围。
	// 原先各处只要求 > 0，于是 MANAGER_PORT=70000 能一路写进配置，直到
	// ListenAndServe 才以一句不提配置项的 "invalid port" 失败。
	if c.Server.Port != 0 {
		if err := validator.ValidatePort(c.Server.Port); err != nil {
			return fmt.Errorf("server.port must be between 1 and 65535, got %d (it may also come from MANAGER_PORT/SERVER_PORT)", c.Server.Port)
		}
	}
	if err := c.Runners.Resources.Validate(); err != nil {
		return err
	}
	if c.Runners.DockerGID < 0 {
		return fmt.Errorf("runners.docker_gid cannot be negative (got %d); leave it empty or 0 to detect the group owning docker.sock", c.Runners.DockerGID)
	}
	if !c.Runners.ContainerMode {
		if strings.TrimSpace(c.Runners.VolumeHostPath) != "" {
			return fmt.Errorf("runners.volume_host_path can only be set when container_mode=true")
		}
		if jobBackend != "dind" {
			return fmt.Errorf("runners.job_docker_backend must be dind when container_mode=false (got %q)", jobBackend)
		}
	}
	if c.Runners.ContainerMode {
		if strings.TrimSpace(c.Runners.VolumeHostPath) != "" && !filepath.IsAbs(c.Runners.VolumeHostPath) {
			return fmt.Errorf("runners.volume_host_path must be an absolute path on the host")
		}
		baseClean := filepath.Clean(c.Runners.BasePath)
		if strings.TrimSpace(c.Runners.VolumeHostPath) == "" && strings.HasPrefix(baseClean, "/app") {
			return fmt.Errorf("runners.volume_host_path must be set when container_mode=true and base_path=%s (the absolute path of the runners base directory on the host)", c.Runners.BasePath)
		}
	}
	for i, item := range c.Runners.Items {
		name := strings.TrimSpace(item.Name)
		path := strings.TrimSpace(item.Path)
		targetType := strings.ToLower(strings.TrimSpace(item.TargetType))
		target := strings.TrimSpace(item.Target)
		if name == "" {
			return fmt.Errorf("runners.items[%d].name cannot be empty", i)
		}
		if !IsSafeRunnerNameOrPath(name) {
			return fmt.Errorf("runners.items[%d].name contains an illegal character (.. / \\\\ are not allowed): %s", i, name)
		}
		if path != "" && !IsSafeRunnerNameOrPath(path) {
			return fmt.Errorf("runners.items[%d].path contains an illegal character (.. / \\\\ are not allowed): %s", i, path)
		}
		if err := ValidateTarget(targetType, target); err != nil {
			return fmt.Errorf("runners.items[%d]: %w", i, err)
		}
		itemBackend := normalizeJobDockerBackend(item.JobDockerBackend)
		itemImage := strings.TrimSpace(item.ContainerImage)
		if itemBackend != "" && validator.ValidateEnumCaseInsensitive(itemBackend, JobDockerBackends) != nil {
			return fmt.Errorf("runners.items[%d].job_docker_backend only supports %s, got %q",
				i, strings.Join(JobDockerBackends, "/"), item.JobDockerBackend)
		}
		if !c.Runners.ContainerMode {
			// 与 runners.volume_host_path 的处理一致：容器模式专属字段不允许在非容器模式下设置，
			// 避免配置看起来生效、实际被忽略
			if itemImage != "" {
				return fmt.Errorf("runners.items[%d].container_image can only be set when container_mode=true", i)
			}
			if itemBackend != "" {
				return fmt.Errorf("runners.items[%d].job_docker_backend can only be set when container_mode=true", i)
			}
		}
		if seen[name] {
			return fmt.Errorf("runners.items has two runners with the same name: %s", name)
		}
		seen[name] = true
		installPath := item.InstallPath(c.Runners.BasePath)
		installKey := filepath.Clean(installPath)
		if existing, ok := seenInstallPaths[installKey]; ok {
			return fmt.Errorf("runners.items has an install directory conflict: %s and %s both map to %s", existing, name, installKey)
		}
		seenInstallPaths[installKey] = name
		if c.Runners.ContainerMode {
			containerName := NormalizedContainerName(name)
			if existing, ok := seenContainerNames[containerName]; ok {
				return fmt.Errorf("runners.items has a container name conflict after mapping: %s and %s both map to %s", existing, name, containerName)
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
		return fmt.Errorf("target cannot be empty")
	}
	tt := strings.ToLower(strings.TrimSpace(targetType))
	switch tt {
	case "org":
		if strings.Contains(t, "/") {
			return fmt.Errorf("when target_type is org, target must be the organization name and cannot contain /")
		}
		return nil
	case "repo":
		parts := strings.SplitN(t, "/", 2)
		if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
			return fmt.Errorf("when target_type is repo, target must be owner/repo, with both parts non-empty")
		}
		if strings.Contains(parts[1], "/") {
			return fmt.Errorf("target can contain only one /, in the form owner/repo")
		}
		return nil
	default:
		return fmt.Errorf("target_type must be org or repo")
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
	// 原子替换而不是 os.WriteFile：每个 API 请求、前端 15 秒一次的轮询和后台循环
	// 都在读这个文件，就地截断重写会让它们读到空的或半截的 YAML（读空时
	// yaml.Unmarshal 不报错，界面上会短暂出现「一个 Runner 都没有」）。
	return atomicfile.WriteFile(path, data, 0644)
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
