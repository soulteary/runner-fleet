package config

import (
	"os"
	"path/filepath"
	"sync"

	"gopkg.in/yaml.v3"
)

// mu 保护配置文件的读写，避免并发写导致覆盖
var mu sync.Mutex

// Config 应用配置
type Config struct {
	Server  ServerConfig  `yaml:"server"`
	GitHub  GitHubConfig  `yaml:"github"`
	Runners RunnersConfig `yaml:"runners"`
}

// ServerConfig HTTP 服务配置
type ServerConfig struct {
	Port int    `yaml:"port"`
	Addr string `yaml:"addr"`
}

// GitHubConfig GitHub 相关配置（可选，用于 API 获取 token）
type GitHubConfig struct {
	Token string `yaml:"token"`
}

// RunnersConfig Runner 根配置
type RunnersConfig struct {
	BasePath string       `yaml:"base_path"` // 所有 runner 安装的根目录
	Items    []RunnerItem `yaml:"items"`
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
	if c.Server.Port == 0 {
		c.Server.Port = 8080
	}
	if c.Runners.BasePath == "" {
		c.Runners.BasePath = "./runners"
	}
	return &c, nil
}

// Save 将配置写回文件（调用方需自行加锁，写操作请使用 LoadAndSave）
func (c *Config) Save(path string) error {
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
	return cfg.Save(path)
}
