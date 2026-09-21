package runner

import (
	"errors"
	"strings"
)

// ProbeErrorType 用于标识容器状态探测失败类型，便于 API/UI 分类展示。
type ProbeErrorType string

const (
	ProbeErrorTypeUnknown      ProbeErrorType = "unknown"
	ProbeErrorTypeDockerAccess ProbeErrorType = "docker-access"
	ProbeErrorTypeAgentHTTP    ProbeErrorType = "agent-http"
	ProbeErrorTypeAgentConnect ProbeErrorType = "agent-connect"
)

// ProbeError 包装底层错误并携带可机器识别的失败类型。
type ProbeError struct {
	Type ProbeErrorType
	Err  error
}

func (e *ProbeError) Error() string {
	if e == nil || e.Err == nil {
		return ""
	}
	return e.Err.Error()
}

func (e *ProbeError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func newProbeError(t ProbeErrorType, err error) error {
	if err == nil {
		return nil
	}
	return &ProbeError{Type: t, Err: err}
}

// DetectProbeErrorType 提取探测错误类型，供 handler/API/UI 统一使用。
func DetectProbeErrorType(err error) ProbeErrorType {
	if err == nil {
		return ProbeErrorTypeUnknown
	}
	var pe *ProbeError
	if errors.As(err, &pe) && pe != nil && pe.Type != "" {
		return pe.Type
	}
	// 兼容历史错误字符串（避免旧路径没有包装导致丢分类）
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "docker"), strings.Contains(msg, "daemon"), strings.Contains(msg, "socket"):
		return ProbeErrorTypeDockerAccess
	case strings.Contains(msg, "agent returned"):
		return ProbeErrorTypeAgentHTTP
	case strings.Contains(msg, "connect"), strings.Contains(msg, "connection refused"), strings.Contains(msg, "no such host"):
		return ProbeErrorTypeAgentConnect
	default:
		return ProbeErrorTypeUnknown
	}
}

// ProbeSuggestion 返回给调用方的简短排障建议。
func ProbeSuggestion(t ProbeErrorType) string {
	switch t {
	case ProbeErrorTypeDockerAccess:
		return "check the docker.sock mount and its permissions (DOCKER_GID/group_add/user), and that the Docker daemon is reachable"
	case ProbeErrorTypeAgentConnect:
		return "check the runner container network, DNS, and that the Agent port is reachable"
	case ProbeErrorTypeAgentHTTP:
		return "read the runner container logs and check the Agent and the scripts under /runner"
	default:
		return "try a stop/start to self-heal first, then read the manager and runner container logs"
	}
}

// ProbeCheckCommand 返回只读检查命令（无副作用）。
func ProbeCheckCommand(t ProbeErrorType) string {
	switch t {
	case ProbeErrorTypeDockerAccess:
		return "ls -l /var/run/docker.sock && id && docker info"
	case ProbeErrorTypeAgentConnect:
		return "docker network inspect runner-net && docker ps --format \"table {{.Names}}\\t{{.Status}}\\t{{.Networks}}\""
	case ProbeErrorTypeAgentHTTP:
		return "docker ps -a | rg \"github-runner-\" && docker logs --tail=200 <runner_container_name>"
	default:
		return "docker compose ps && docker logs --tail=200 runner-manager"
	}
}

// ProbeFixCommand 返回可能有副作用的修复命令。
func ProbeFixCommand(t ProbeErrorType) string {
	switch t {
	case ProbeErrorTypeDockerAccess:
		return "DOCKER_GID=$(stat -c \"%g\" /var/run/docker.sock 2>/dev/null || stat -f \"%g\" /var/run/docker.sock) && echo \"DOCKER_GID=$DOCKER_GID\" > .env && docker compose up -d"
	case ProbeErrorTypeAgentConnect:
		return "docker compose up -d && docker restart runner-manager"
	case ProbeErrorTypeAgentHTTP:
		return "docker restart <runner_container_name>"
	default:
		return "docker compose up -d --force-recreate"
	}
}
