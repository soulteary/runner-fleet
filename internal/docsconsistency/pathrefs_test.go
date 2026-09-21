package docsconsistency

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// 只查这几个前缀开头的引用。仓库里出现的其它斜杠串不是本仓库的路径：
// runners/<name>/.agent_token 是运行期产物，config/config.yaml 被 gitignore，
// src/Misc/layoutbin/runsvc.sh 是 actions/runner 的路径。
var refPrefixes = []string{"docs/", "examples/", "scripts/", "cmd/", "internal/", ".github/"}

// 扫描这些文件。Markdown 之间的相对链接已经被渲染器和人眼盯着，
// 真正没人看的是配置模板与 compose 文件注释里的引用——本仓库历史上的两处失效引用
// 都在这里（docker-compose.yml 与 config.yaml.example 顶部各有一句「详见 …」），
// 而且指向的文件在仓库历史中从未存在过。
func scannedForRefs(name string) bool {
	switch filepath.Ext(name) {
	case ".yml", ".yaml", ".example", ".sh", ".conf", ".go":
		return true
	}
	return name == "Makefile" || strings.Contains(name, "Dockerfile")
}

var urlRe = regexp.MustCompile(`https?://\S+`)

// 结尾可能粘上的 ASCII 标点。中文标点由 cutAtNonPath 一并截掉。
const trailingPunct = ".,;:!?)]}'\"`"

// cutAtNonPath 把 token 截到第一个不可能出现在路径里的字符。
//
// 中文注释里路径后面常常直接跟标点或汉字、中间没有空格——
// 「见 scripts/ci-recipes.conf（基准路径……」按空白切出来是一整个 token。
// 不截断就会拿着 "scripts/ci-recipes.conf（基准路径" 去 Stat，对一条完全正确的
// 注释报红。这里按「什么能出现在路径里」来切，比逐个枚举中文标点可靠。
func cutAtNonPath(tok string) string {
	for i, r := range tok {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case strings.ContainsRune("_./-+@~*?$<>", r):
		default:
			return tok[:i]
		}
	}
	return tok
}

// TestPathRefs_NonMarkdownFilesPointAtRealFiles 非 Markdown 文件里引用的仓库路径必须存在。
//
// Markdown 之间的相对链接目前零断链，断掉的两处全在 .yml 与 .example 的注释里，
// 没有任何检查覆盖那一侧——而 config.yaml.example 恰恰是每个用户第一步就会复制走、
// 并照着改的文件，它顶上那句「详细字段说明见 …」曾把人指向一份从未存在过的文件。
//
// 附带的约束：注释里也不能再写失效路径。这是有意的——一条指不到东西的路径，
// 写在注释里和写在文档里一样会浪费读者的时间。
func TestPathRefs_NonMarkdownFilesPointAtRealFiles(t *testing.T) {
	repo := repoRoot(t)

	err := filepath.WalkDir(repo, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "runners", "config":
				return fs.SkipDir
			}
			return nil
		}
		if !scannedForRefs(d.Name()) {
			return nil
		}
		rel, _ := filepath.Rel(repo, path)
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(b), "\n") {
			for _, ref := range pathRefs(line) {
				if _, err := os.Stat(filepath.Join(repo, ref)); err != nil {
					t.Errorf("%s:%d 引用了不存在的 %s", filepath.ToSlash(rel), i+1, ref)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// pathRefs 取出一行里指向本仓库的路径引用。
//
// 按 token 切而不是在整行上跑正则，是为了让 github.com/soulteary/ci-recipes/cmd/ci-recipes
// 这类外部模块路径整体落选：正则在整行上匹配，会从这一串的中间截出一段本仓库没有的
// cmd 目录，于是检查会因为一条完全正确的 go install 命令而报红。
func pathRefs(line string) []string {
	line = urlRe.ReplaceAllString(line, " ")
	line = strings.Map(func(r rune) rune {
		switch r {
		case '"', '\'', '`', '(', ')', '[', ']', '{', '}', '<', '>', ',', ';', '=', '|':
			return ' '
		}
		return r
	}, line)

	var out []string
	for _, tok := range strings.Fields(line) {
		tok = strings.TrimPrefix(tok, "./")
		tok = cutAtNonPath(tok)
		tok = strings.TrimRight(tok, trailingPunct)
		// 通配与模板占位不是具体路径：scripts/*.sh、docs/<lang>/guide.md、$(DIR)/x
		if strings.ContainsAny(tok, "*?$<>") {
			continue
		}
		for _, p := range refPrefixes {
			if strings.HasPrefix(tok, p) && len(tok) > len(p) {
				out = append(out, tok)
				break
			}
		}
	}
	return out
}
