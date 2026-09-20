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
	"time"
)

// AgentTokenFile Runner 安装目录下保存 Agent 令牌的文件名
const AgentTokenFile = ".agent_token"

// agentTokenBytes 令牌随机字节数，hex 编码后为 64 个字符
const agentTokenBytes = 32

// 抢输的一方等待胜者写完令牌的上限（tokenWaitAttempts × tokenWaitInterval）。
// 等的只是一次 64 字节的写，给到 100ms 已经很宽裕；超时就按「拿不到令牌」降级，
// 与写入失败时的行为一致，不会卡住调用方。
const (
	tokenWaitAttempts = 50
	tokenWaitInterval = 2 * time.Millisecond
)

// EnsureAgentToken 返回该 Runner 的 Agent 令牌：已存在则读出，否则生成并以 0600 写入。
// 生成或写入失败时返回错误，调用方可选择降级为不带令牌（保持旧行为）。
//
// 「先读后写」不是原子的，因此用 O_EXCL 保证只有一个写入方能把文件建出来，
// 其余的回头读它写下的那个。启停路径（持 runnerOps 锁）与状态路径（不持锁，
// 因为它每次列表都要跑，不能被一次几秒的 docker create 挡住）会同时走到这里：
// 各生成一个的话，后写的会覆盖先写的，而注入容器的是先写的那个——
// Agent 认自己 env 里的 A，Manager 之后从盘上读到 B，从此永远 401，
// 而且容器 env 非空，agent_token 漂移检查也修不回来。
func EnsureAgentToken(installDir string) (string, error) {
	path := filepath.Join(installDir, AgentTokenFile)
	// 两轮：第二轮留给「清掉零字节残留」之后的重试
	for attempt := 0; attempt < 2; attempt++ {
		if token := ReadAgentToken(installDir); token != "" {
			return token, nil
		}
		buf := make([]byte, agentTokenBytes)
		if _, err := rand.Read(buf); err != nil {
			return "", fmt.Errorf("生成 Agent 令牌失败: %w", err)
		}
		token := hex.EncodeToString(buf)

		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err == nil {
			if _, err := f.Write([]byte(token)); err != nil {
				// 半截文件留着会让后续的 O_EXCL 一直撞上一个空文件，谁也写不进去。
				// 这两步是清理，失败了也只能照样把原始错误报上去
				_ = f.Close()
				_ = os.Remove(path)
				return "", fmt.Errorf("写入 Agent 令牌 %s 失败: %w", path, err)
			}
			if err := f.Close(); err != nil {
				_ = os.Remove(path)
				return "", fmt.Errorf("写入 Agent 令牌 %s 失败: %w", path, err)
			}
			return token, nil
		}
		if !os.IsExist(err) {
			return "", fmt.Errorf("写入 Agent 令牌 %s 失败: %w", path, err)
		}

		// 已经有人抢先建好了：用他的，别用自己刚生成的。
		// O_EXCL 先建文件再写内容，中间有个极短的窗口能读到空文件，
		// 这时不能当成「没有令牌」，稍等一下胜者就写完了。
		for i := 0; i < tokenWaitAttempts; i++ {
			if existing := ReadAgentToken(installDir); existing != "" {
				return existing, nil
			}
			time.Sleep(tokenWaitInterval)
		}

		// 等满了还是空。一次 64 字节的写不可能拖过 100ms，所以这多半是上一次在
		// 「建好文件、还没写入」之间被杀留下的零字节文件。不清掉的话它会永远挡住
		// O_EXCL，此后谁也拿不到令牌——而拿不到令牌就不谈 agent_token 漂移，
		// 于是鉴权永久静默失效，正是本项要消灭的那种状态。
		//
		// 只清零字节的：ReadAgentToken 读不动文件时也返回空串（例如权限被改过），
		// 那种情况下文件里是有内容的，删掉就等于把正在运行的容器的令牌作废了。
		fi, serr := os.Stat(path)
		if serr != nil || fi.Size() != 0 {
			return "", fmt.Errorf("令牌文件 %s 已存在但读不到内容，不敢删除（可能是权限问题）", path)
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return "", fmt.Errorf("清理空的 Agent 令牌文件 %s 失败: %w", path, err)
		}
	}
	return "", fmt.Errorf("令牌文件 %s 反复为空，无法建立", path)
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
