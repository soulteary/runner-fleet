package docsconsistency

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestQuickStart_ReadmeMatchesTheGuide README 与使用指南里的快速开始命令必须一致。
//
// 这是 README 瘦身之后**刻意留下**的唯一一处技术性重复：30 秒起步得在首页上，
// 完整说明得在指南里，两边都得有。留着重复就得有人盯着，而「有人盯着」在这个仓库
// 已经失败过一次——那段命令在两处是两个形态，其中一处 chown 排在 mkdir 之前、
// 照抄必定报错，而六份译文和 README 一起错着，没有任何东西会因此变红。
//
// 只比命令行，不比注释：注释在指南里是被翻译的，在 README 里是英文。
// CI 的 `Quick start runs` job 负责证明命令真能跑通，这里负责证明两处是同一段。
func TestQuickStart_ReadmeMatchesTheGuide(t *testing.T) {
	repo := repoRoot(t)

	readme := commandsInFirstBashBlock(t,
		filepath.Join(repo, "README.md"), regexp.MustCompile(`(?m)^## .*Quick start`))
	guide := commandsInFirstBashBlock(t,
		filepath.Join(repo, "docs", "guide.md"), regexp.MustCompile(`(?m)^### .*docker-compose`))

	if strings.Join(readme, "\n") != strings.Join(guide, "\n") {
		t.Errorf("README.md 与 docs/guide.md 的快速开始命令不一致。\nREADME:\n  %s\nguide:\n  %s",
			strings.Join(readme, "\n  "), strings.Join(guide, "\n  "))
	}
	// 两边都抽不出东西时上面那个比较会「相等」，于是这条用例永远绿。
	if len(guide) < 3 {
		t.Fatalf("从 docs/guide.md 只抽到 %d 条命令，定位用的正则可能已经对不上了", len(guide))
	}
}

// commandsInFirstBashBlock 取出 heading 之后第一个 ```bash 块里的命令行（去掉注释与空行）。
func commandsInFirstBashBlock(t *testing.T, path string, heading *regexp.Regexp) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(b), "\n")

	i := 0
	for ; i < len(lines); i++ {
		if heading.MatchString(lines[i]) {
			break
		}
	}
	if i == len(lines) {
		t.Fatalf("%s 里没找到匹配 %s 的标题", filepath.Base(path), heading)
	}

	var out []string
	inBlock := false
	for ; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		switch {
		case !inBlock && line == "```bash":
			inBlock = true
		case inBlock && strings.HasPrefix(line, "```"):
			return out
		case inBlock && line != "" && !strings.HasPrefix(line, "#"):
			out = append(out, line)
		}
	}
	t.Fatalf("%s 里那个 ```bash 块没有闭合", filepath.Base(path))
	return nil
}
