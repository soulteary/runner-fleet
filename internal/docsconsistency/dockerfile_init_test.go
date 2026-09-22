package docsconsistency

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// initBinary 是两个镜像里 PID 1 该是的那个程序。
// 路径来自 noble/universe 的 tini 包（tini_0.19.0-1，装 /usr/bin/tini 与 /usr/bin/tini-static）。
const initBinary = "/usr/bin/tini"

// TestDockerfiles_EntrypointRunsUnderInit 两个镜像的 ENTRYPOINT 都必须以 init 开头。
//
// 为什么值得一条测试守着：这是个「删掉也照样能构建、照样能跑」的改动。
// 镜像少了 tini，Agent 与 Manager 就又变回容器里的 PID 1，于是
//   - Job 留下的孤儿进程没人回收，僵尸跨 Job 累积，还占着
//     runners.resources.pids_limit 的名额，到顶之后 fork 失败，
//     表现为 Job 随机报 Resource temporarily unavailable；
//   - SIGTERM 不再被转发……这一半现在由 Agent / Manager 自己订阅信号兜住，
//     所以少了 tini 也不会有哪条现有用例变红——回收孤儿这一半则没有别的兜底。
//
// 任何一次「顺手改回 ENTRYPOINT」都得先让这条变红。
func TestDockerfiles_EntrypointRunsUnderInit(t *testing.T) {
	repo := repoRoot(t)

	for _, name := range []string{"Dockerfile", "Dockerfile.runner"} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(repo, name)
			argv := entrypointArgv(t, path)
			if len(argv) == 0 {
				t.Fatalf("%s 里没找到 exec 形式（JSON 数组）的 ENTRYPOINT", name)
			}
			if argv[0] != initBinary {
				t.Fatalf("%s 的 ENTRYPOINT 第一项应为 %s，实际 %q（完整 argv: %v）",
					name, initBinary, argv[0], argv)
			}
			// tini 与被它带起来的程序之间要有 --，否则后者的参数会被 tini 自己吃掉
			if len(argv) < 3 || argv[1] != "--" {
				t.Fatalf("%s 的 ENTRYPOINT 应为 [%q, \"--\", <程序>]，实际 %v", name, initBinary, argv)
			}
		})
	}
}

// entrypointArgv 取出 Dockerfile 里最后一条 exec 形式 ENTRYPOINT 的 argv。
//
// 只认 JSON 数组形式：shell 形式（ENTRYPOINT foo bar）会被 /bin/sh -c 包一层，
// sh 才是 PID 1，而它既不转发信号也不回收孤儿——那正是本测试要防的情形，
// 所以解析不出数组时让调用方失败，而不是在这里放过。
func entrypointArgv(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var argv []string
	// 续行（\ 结尾）先拼起来，免得 ENTRYPOINT 被拆在两行时看不见
	lines := strings.Split(strings.ReplaceAll(string(b), "\\\n", " "), "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "ENTRYPOINT") {
			continue
		}
		rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "ENTRYPOINT"))
		var parsed []string
		if err := json.Unmarshal([]byte(rest), &parsed); err != nil {
			t.Fatalf("%s 的 ENTRYPOINT 不是 exec 形式（JSON 数组）: %s", filepath.Base(path), rest)
		}
		argv = parsed
	}
	return argv
}
