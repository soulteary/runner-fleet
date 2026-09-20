package runner

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// TestEnsureAgentToken_SingleWinnerUnderConcurrency
// 启停路径（持 runnerOps 锁）与状态路径（不持锁，它每次列表都要跑，不能被一次几秒的
// docker create 挡住）会同时走到 EnsureAgentToken。「先读后写」不是原子的：两边各生成一个、
// 后写的覆盖先写的，而注入进容器的是先写的那个，于是 Agent 认 env 里的 A、Manager 从盘上
// 读到 B，从此永远 401——而且容器 env 非空，agent_token 漂移检查也修不回来，只能人工介入。
// 所以这里要求「单一胜者」：无论多少路并发，所有人拿到的必须是同一个、且与磁盘一致。
func TestEnsureAgentToken_SingleWinnerUnderConcurrency(t *testing.T) {
	dir := t.TempDir()
	const n = 16
	tokens := make([]string, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start // 尽量让它们同时进入
			tok, err := EnsureAgentToken(dir)
			if err != nil {
				t.Errorf("第 %d 路 EnsureAgentToken 失败: %v", i, err)
				return
			}
			tokens[i] = tok
		}(i)
	}
	close(start)
	wg.Wait()

	onDisk := ReadAgentToken(dir)
	if onDisk == "" {
		t.Fatal("并发结束后磁盘上没有令牌")
	}
	for i, tok := range tokens {
		if tok != onDisk {
			t.Fatalf("第 %d 路拿到 %q，磁盘上却是 %q —— 注入容器的与 Manager 之后读到的会不一致，Agent 将永远返回 401",
				i, tok, onDisk)
		}
	}
}

// TestEnsureAgentToken_ReusesExistingToken 已有令牌时必须原样返回，不能重新生成：
// 重新生成就意味着已经在跑的容器（env 里是旧令牌）从此对不上。
func TestEnsureAgentToken_ReusesExistingToken(t *testing.T) {
	dir := t.TempDir()
	first, err := EnsureAgentToken(dir)
	if err != nil {
		t.Fatal(err)
	}
	second, err := EnsureAgentToken(dir)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("第二次调用换了令牌: %q → %q", first, second)
	}
	if got := ReadAgentToken(dir); got != first {
		t.Fatalf("磁盘上的令牌与返回值不一致: %q vs %q", got, first)
	}
}

// TestEnsureAgentToken_RecoversFromEmptyLeftover
// O_EXCL 是先建文件再写内容。若 Manager 恰在这两步之间被杀，会留下一个零字节的令牌文件。
// 它会永远挡住后续的 O_EXCL，于是谁也拿不到令牌——而拿不到令牌就不谈 agent_token 漂移，
// 鉴权就此永久静默失效，正是本 PR 要消灭的那种状态。所以必须能自愈。
func TestEnsureAgentToken_RecoversFromEmptyLeftover(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, AgentTokenFile)
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}

	token, err := EnsureAgentToken(dir)
	if err != nil {
		t.Fatalf("零字节残留应当能自愈，却失败了: %v", err)
	}
	if token == "" {
		t.Fatal("自愈后仍未拿到令牌")
	}
	if got := ReadAgentToken(dir); got != token {
		t.Fatalf("磁盘上的令牌与返回值不一致: %q vs %q", got, token)
	}
}

// TestEnsureAgentToken_KeepsUnreadableFileWithContent
// 反面：ReadAgentToken 读不动文件时也返回空串（权限被改过等）。那种情况下文件里是有内容的，
// 删掉就等于把正在运行的容器的令牌作废、换一个它不认识的。只有确认是零字节才允许清理。
func TestEnsureAgentToken_KeepsUnreadableFileWithContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, AgentTokenFile)
	// 只有空白字符：ReadAgentToken 会 TrimSpace 成空串，但文件是有内容的
	if err := os.WriteFile(path, []byte("   \n"), 0600); err != nil {
		t.Fatal(err)
	}

	if _, err := EnsureAgentToken(dir); err == nil {
		t.Fatal("读不到内容的非空文件不该被当作残留处理")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("有内容的令牌文件不该被删除: %v", err)
	}
}
