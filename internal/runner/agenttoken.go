// Agent 调用令牌：Runner 容器内的 Agent 暴露 /start、/stop 等控制接口，
// 同一 docker 网络内的任何容器都能访问。令牌用于确认调用方确实是 Manager。
//
// 令牌存放在 Runner 安装目录下，该目录既被 Manager 访问（宿主机侧路径），
// 也挂载进 Runner 容器（/runner），因此无需通过环境变量传递，
// Manager 重启后也能重新读到同一个令牌。
package runner

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	secure "github.com/soulteary/secure-kit"
)

// AgentTokenFile Runner 安装目录下保存 Agent 令牌的文件名
const AgentTokenFile = ".agent_token"

// agentTokenBytes 令牌随机字节数，hex 编码后为 64 个字符
const agentTokenBytes = 32

// EnsureAgentToken 返回该 Runner 的 Agent 令牌：已存在则读出，否则生成并以 0600 写入。
// 生成或写入失败时返回错误，调用方可选择降级为不带令牌（保持旧行为）。
//
// 启停路径（持 runnerOps 锁）与状态路径（不持锁，因为它每次列表都要跑，不能被一次几秒的
// docker create 挡住）会同时走到这里，所以创建必须是「单一胜者」：各生成一个的话，
// 后落盘的会覆盖先落盘的，而注入进容器的是先落盘的那个——Agent 认自己 env 里的 A、
// Manager 之后从盘上读到 B，从此永远 401，且容器 env 非空，agent_token 漂移检查也修不回来。
//
// 做法是「先写临时文件，内容齐全后再原子地挂上正式名字」：os.Link 不会覆盖已有的名字，
// 所以既是单一胜者，又保证 .agent_token 这个名字要么不存在、要么内容完整——
// 不存在「已创建、还没写完」的中间态。
//
// 刻意不去回收空文件。回收必然要 unlink，而「等若干毫秒还是空就判定写入者已死」
// 是不成立的：写入者可能只是被调度走了，或者文件系统写入卡了一下（宿主机负载、
// NFS、cgroup IO 限流都会）。此时 unlink 就会把一个还活着的写入者的 inode 摘掉，
// 它照样把 A 写进那个已经没有名字的 inode 并返回 A，而盘上留下的是回收者发布的 B——
// 正是上面那种分裂。本实现既然不产生空文件，也就没有需要回收的东西。
func EnsureAgentToken(installDir string) (string, error) {
	if token := ReadAgentToken(installDir); token != "" {
		return token, nil
	}
	path := filepath.Join(installDir, AgentTokenFile)

	// RandomHex(n) 返回 2n 个十六进制字符，与原先 rand.Read(32 字节)+hex 编码等价
	token, err := secure.RandomHex(agentTokenBytes)
	if err != nil {
		return "", fmt.Errorf("生成 Agent 令牌失败: %w", err)
	}

	// os.CreateTemp 以 0600 创建，与正式文件一致
	f, err := os.CreateTemp(installDir, AgentTokenFile+".tmp-*")
	if err != nil {
		return "", fmt.Errorf("创建临时令牌文件失败: %w", err)
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }()
	if _, err := f.Write([]byte(token)); err != nil {
		_ = f.Close()
		return "", fmt.Errorf("写入临时令牌文件 %s 失败: %w", tmp, err)
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("写入临时令牌文件 %s 失败: %w", tmp, err)
	}

	if err := os.Link(tmp, path); err != nil {
		if !os.IsExist(err) {
			return "", fmt.Errorf("发布 Agent 令牌 %s 失败: %w", path, err)
		}
		// 名字已被占：别人先发布了，用他的。经由 Link 挂上来的内容必然是完整的
		if existing := ReadAgentToken(installDir); existing != "" {
			return existing, nil
		}
		// 占位的是个读不出内容的文件。本实现不会产生这种文件，所以它要么是更早版本
		// 写入失败的残留，要么是权限问题。两种都不该由这里擅自删除——见上面关于
		// unlink 的说明——交给人处理，并把该做什么说清楚。
		return "", fmt.Errorf("令牌文件 %s 已存在但读不到内容（可能是早先版本写入失败的残留，或权限不对）；"+
			"确认无误后删除它，Manager 会重新生成；在此之前该 Runner 的 Agent 不启用鉴权", path)
	}
	return token, nil
}

// tokenWarnOnce 同一个安装目录只告警一次，避免被状态轮询刷屏
var tokenWarnOnce sync.Map

// WarnAgentTokenUnavailable 令牌拿不到时告警。状态路径每次列表都会调用 EnsureAgentToken，
// 不能每次都打；但也不能完全不打——拿不到令牌意味着这个 Runner 的 Agent 不鉴权，
// 而且 agent_token 漂移检查会因为「手头没有令牌」而跳过自己，界面上什么都看不到。
func WarnAgentTokenUnavailable(installDir string, err error) {
	if err == nil {
		return
	}
	v, _ := tokenWarnOnce.LoadOrStore(installDir, &sync.Once{})
	v.(*sync.Once).Do(func() {
		log.Printf("警告: %v", err)
	})
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
