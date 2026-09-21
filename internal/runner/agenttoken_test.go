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

// TestEnsureAgentToken_NeverUnlinksExistingFile
// 回收空文件必然要 unlink，而「等若干毫秒还是空就判定写入者已死」是不成立的：
// 写入者可能只是被调度走了，或者文件系统写入卡了一下。此时 unlink 会把一个还活着的
// 写入者的 inode 摘掉，它照样把 A 写进那个没有名字的 inode 并返回 A，
// 而盘上留下的是回收者发布的 B —— 容器注入 A、Manager 读到 B，从此永远 401。
// 所以这里的硬性要求是：EnsureAgentToken 绝不删除已经占着正式名字的文件。
func TestEnsureAgentToken_NeverUnlinksExistingFile(t *testing.T) {
	cases := map[string]string{
		"零字节（早先版本写入失败的残留）": "",
		"只有空白（读出来也是空串）":    "   \n",
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, AgentTokenFile)
			if err := os.WriteFile(path, []byte(content), 0600); err != nil {
				t.Fatal(err)
			}

			if _, err := EnsureAgentToken(dir); err == nil {
				t.Fatal("占着名字但读不出内容时应当报错，而不是默默接管")
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("原文件不该被删除: %v", err)
			}
			if string(got) != content {
				t.Fatalf("原文件内容被改动了: %q → %q", content, string(got))
			}
		})
	}
}

// TestEnsureAgentToken_LeavesNoTempFileBehind
// 临时文件是实现细节，正常与失败路径都不该把它留在 Runner 目录里。
func TestEnsureAgentToken_LeavesNoTempFileBehind(t *testing.T) {
	dir := t.TempDir()
	if _, err := EnsureAgentToken(dir); err != nil {
		t.Fatal(err)
	}
	// 再走一次「名字已被占」的分支
	if _, err := EnsureAgentToken(dir); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != AgentTokenFile {
			t.Fatalf("目录里残留了临时文件: %s", e.Name())
		}
	}
}

// TestEnsureAgentToken_PublishedFileIsNeverEmpty
// 原子发布的意义就在这里：.agent_token 这个名字要么不存在，要么内容完整。
// 没有「已创建、还没写完」的中间态，也就没有需要回收的空文件。
func TestEnsureAgentToken_PublishedFileIsNeverEmpty(t *testing.T) {
	dir := t.TempDir()
	token, err := EnsureAgentToken(dir)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, AgentTokenFile))
	if err != nil {
		t.Fatal(err)
	}
	if len(b) == 0 || string(b) != token {
		t.Fatalf("发布出来的文件内容不完整: %q（返回值 %q）", string(b), token)
	}
}

// TestEnsureAgentToken_Format 钉住令牌的外形：64 个小写十六进制字符。
//
// 生成实现从 rand.Read(32 字节)+hex.EncodeToString 换成了 secure.RandomHex(32)，
// 两者必须完全等价。令牌既写进 .agent_token，也作为 AGENT_TOKEN 注入容器，
// 而 agent_token 的漂移检查只看「容器里有没有一个非空值」、不比对取值
// （见 drift.go 中的说明）——换一种编码不会触发重建，只会让存量容器
// 从此一直 401，且界面上看不出原因。
func TestEnsureAgentToken_Format(t *testing.T) {
	token, err := EnsureAgentToken(t.TempDir())
	if err != nil {
		t.Fatalf("生成令牌失败: %v", err)
	}
	if len(token) != agentTokenBytes*2 {
		t.Fatalf("令牌长度应为 %d（%d 字节的 hex），得到 %d: %q",
			agentTokenBytes*2, agentTokenBytes, len(token), token)
	}
	isLowerHex := func(r rune) bool {
		return (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')
	}
	for _, r := range token {
		if !isLowerHex(r) {
			t.Fatalf("令牌应为小写十六进制，出现 %q: %s", r, token)
		}
	}
}
