// Agent 调用令牌：Runner 容器内的 Agent 暴露 /start、/stop 等控制接口，
// 同一 docker 网络内的任何容器都能访问。令牌用于确认调用方确实是 Manager。
//
// 令牌存放在 Runner 安装目录下，该目录既被 Manager 访问（宿主机侧路径），
// 也挂载进 Runner 容器（/runner），因此无需通过环境变量传递，
// Manager 重启后也能重新读到同一个令牌。
package runner

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// AgentTokenFile Runner 安装目录下保存 Agent 令牌的文件名
const AgentTokenFile = ".agent_token"

// agentTokenBytes 令牌随机字节数，hex 编码后为 64 个字符
const agentTokenBytes = 32

// EnsureAgentToken 返回该 Runner 的 Agent 令牌：已存在则读出，否则生成并以 0600 写入。
// 生成或写入失败时返回错误，调用方可选择降级为不带令牌（保持旧行为）。
func EnsureAgentToken(installDir string) (string, error) {
	if token := ReadAgentToken(installDir); token != "" {
		return token, nil
	}
	buf := make([]byte, agentTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("生成 Agent 令牌失败: %w", err)
	}
	token := hex.EncodeToString(buf)
	path := filepath.Join(installDir, AgentTokenFile)
	if err := os.WriteFile(path, []byte(token), 0600); err != nil {
		return "", fmt.Errorf("写入 Agent 令牌 %s 失败: %w", path, err)
	}
	return token, nil
}

// ReadAgentToken 读取令牌；文件不存在或内容为空时返回空字符串。
// 空字符串表示「该 Runner 没有令牌」，此时 Manager 不带鉴权头、Agent 也不校验，
// 以兼容本特性之前创建的容器。
func ReadAgentToken(installDir string) string {
	b, err := os.ReadFile(filepath.Join(installDir, AgentTokenFile))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}
