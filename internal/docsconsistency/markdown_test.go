package docsconsistency

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// repoRoot 本包在 internal/docsconsistency 下，测试的工作目录就是包目录。
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	// 认准一个只会出现在仓库根的文件，路径算错时立刻失败，而不是静默地什么都没查。
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("没找到仓库根（%s）: %v", root, err)
	}
	return root
}

func readLines(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n")
}

var (
	headingRe = regexp.MustCompile(`^(#{1,6})\s+(.*)$`)
	fenceRe   = regexp.MustCompile("^\\s*```(.*)$")
	bulletRe  = regexp.MustCompile(`^\s*([-*]|\d+\.)\s+`)
	linkRe    = regexp.MustCompile(`\[([^\]]*)\]\(([^)]+)\)`)
	codeRe    = regexp.MustCompile("`[^`]*`")
)

// section 一个标题及其正文的统计量。正文指到下一个标题为止的内容。
type section struct {
	line     int // 标题所在行号，仅用于报错定位
	tableRow int // 表格行数（以 | 开头的行）
	blocks   []codeBlock
	bullets  int
	links    []string // 解析为仓库相对路径后的链接目标，纯锚点链接不计
}

type codeBlock struct {
	info  string // ``` 后面的语言标记
	lines int    // 非注释、非空的行数
}

// parseSections 按标题切分文档并统计每节的内容量。
//
// 围栏内的行不参与任何识别：guide.md 的 bash 块里有 `# 编辑 config/config.yaml…`
// 这样的注释行，当成标题会让节数直接对不上（ci-recipes 的标题检查同样跳过围栏，
// 两边看到的必须是同一份切分）。
func parseSections(t *testing.T, path string) []section {
	t.Helper()
	lines := readLines(t, path)

	// 文件开头到第一个标题之间的内容归入一个哨兵节，这样所有文件的下标都能对齐。
	secs := []section{{line: 0}}
	inFence := false
	var fenceInfo string
	var fenceCount int

	for i, raw := range lines {
		if m := fenceRe.FindStringSubmatch(raw); m != nil {
			if inFence {
				cur := &secs[len(secs)-1]
				cur.blocks = append(cur.blocks, codeBlock{info: fenceInfo, lines: fenceCount})
				inFence = false
			} else {
				inFence = true
				fenceInfo = strings.TrimSpace(m[1])
				fenceCount = 0
			}
			continue
		}
		if inFence {
			s := strings.TrimSpace(raw)
			// 注释行按语言翻译，不参与比对；空行同理。
			if s != "" && !strings.HasPrefix(s, "#") && !strings.HasPrefix(s, "//") {
				fenceCount++
			}
			continue
		}

		if headingRe.MatchString(raw) {
			secs = append(secs, section{line: i + 1})
			continue
		}

		cur := &secs[len(secs)-1]
		if strings.HasPrefix(strings.TrimSpace(raw), "|") {
			cur.tableRow++
		}
		if bulletRe.MatchString(raw) {
			cur.bullets++
		}
		cur.links = append(cur.links, docLinks(path, raw)...)
	}
	return secs
}

// docLinks 取出一行里的 Markdown 链接，解析成仓库相对路径。
//
// 解析而不是直接比字面量，是因为同一个目标在英文与译文里的写法必然不同：
// docs/guide.md 写 ../examples/，docs/zh/guide.md 写 ../../examples/，
// 指的是同一个地方。纯锚点链接（#4-security…）两边本该不同，跳过。
func docLinks(path, raw string) []string {
	// 顶部的语言导航行两边天然不同（当前语言不是链接），不比。
	if strings.HasPrefix(strings.TrimSpace(raw), "**文档") {
		return nil
	}
	// 行内代码里的 [x](y) 是被展示的文本，不是链接。
	line := codeRe.ReplaceAllString(raw, "")

	var out []string
	for _, m := range linkRe.FindAllStringSubmatch(line, -1) {
		href := strings.TrimSpace(m[2])
		if strings.HasPrefix(href, "http://") || strings.HasPrefix(href, "https://") ||
			strings.HasPrefix(href, "mailto:") || strings.HasPrefix(href, "#") {
			continue
		}
		target := href
		if i := strings.Index(target, "#"); i >= 0 {
			target = target[:i]
		}
		if target == "" {
			continue
		}
		out = append(out, filepath.ToSlash(filepath.Clean(filepath.Join(filepath.Dir(path), target))))
	}
	return out
}
