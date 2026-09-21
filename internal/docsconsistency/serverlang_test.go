package docsconsistency

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

// 服务端说什么语言，是文档承诺过的事。
//
// README 与六份指南都写着「界面与消息有六种语言」。产品此前只兑现了外壳：
// 用户点一下按钮之后真正读到的那一句——toast、错误、自检项、日志——全是 handler
// 与 runner 里的中文字面量。把界面切到 English，外壳是英文，每一个 toast 是中文。
//
// 现在两边都收拾过了：走请求的消息查 i18n 表（api.* 键），没有请求可依的地方
// （日志、后台任务写盘的注册结果、config 的校验错误）一律英文。这条用例守的是
// 它不再倒回去——新加一行 log.Printf 或 fmt.Errorf 时顺手写中文，是最省事的写法，
// 而它不会让任何测试变红，也不会有人在 review 里逐行盯着。
//
// 用 AST 而不是 grep：注释里的中文是好事（这个仓库的注释本来就是中文写的），
// 要管的只有字面量。go/parser 天然把两者分开，grep 分不开。

// cjkRe 认 CJK 汉字、日文假名与谚文。
//
// 不止查汉字：ja/ko 的译文同样不该出现在 Go 字面量里，而「只查汉字」会让
// 一句纯假名的日文大摇大摆地过去。全角标点单列一段——历史上漏掉的恰恰是它们
// （app.js 里那个 '：' 分隔符在英文界面上渲染成 "Check failed：..."，
// 汉字一个没有，问题一模一样）。
var cjkRe = regexp.MustCompile(`[\p{Han}\p{Hiragana}\p{Katakana}\p{Hangul}\x{3000}-\x{303F}\x{FF00}-\x{FFEF}]`)

// goRoots 要扫的目录。cmd 不能漏：CSRF 守卫拦在路由之前，代码在 cmd/runner-manager，
// 但它返回的 403 与 handler 的错误走同一条路进到界面。
var goRoots = []string{"internal", "cmd"}

// TestServerLanguage_NoCJKInGoStringLiterals
// 非测试 Go 代码里不许再出现中日韩字面量。
func TestServerLanguage_NoCJKInGoStringLiterals(t *testing.T) {
	repo := repoRoot(t)

	scanned := 0
	for _, root := range goRoots {
		err := filepath.WalkDir(filepath.Join(repo, root), func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(d.Name(), ".go") {
				return err
			}
			if strings.HasSuffix(d.Name(), "_test.go") {
				return nil
			}
			fset := token.NewFileSet()
			f, errParse := parser.ParseFile(fset, path, nil, 0)
			if errParse != nil {
				return errParse
			}
			rel, _ := filepath.Rel(repo, path)
			ast.Inspect(f, func(n ast.Node) bool {
				lit, ok := n.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				scanned++
				v, errUnquote := strconv.Unquote(lit.Value)
				if errUnquote != nil {
					return true
				}
				if cjkRe.MatchString(v) {
					t.Errorf("%s:%d 字面量里有中日韩文字，服务端消息一律英文或走 i18n 键: %s",
						filepath.ToSlash(rel), fset.Position(lit.Pos()).Line, lit.Value)
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	// 走错目录、或改坏了跳过规则，都会让这条用例一个字面量都没看就宣布通过。
	if scanned < 500 {
		t.Fatalf("只扫到 %d 个字符串字面量，遍历大概已经扫不到代码了", scanned)
	}
}

// uiAssetAllowed 是界面资源里允许出现的中日韩文字：语言选择器里各语言的自称。
//
// 「中文」不该被译成 "Chinese"——那一栏的读者恰恰是还没切到自己语言的人，
// 唯一对他有用的写法就是用他自己的文字写的名字。
var uiAssetAllowed = []string{"中文", "日本語", "한국어"}

// commentRes 按出现顺序剥掉四种注释。只删不增，所以最坏情况是漏报，不会误报。
var commentRes = []*regexp.Regexp{
	regexp.MustCompile(`(?s)\{\{/\*.*?\*/\}\}`),  // Go 模板注释
	regexp.MustCompile(`(?s)<!--.*?-->`),         // HTML 注释
	regexp.MustCompile(`(?s)/\*.*?\*/`),          // CSS / JS 块注释
	regexp.MustCompile(`(?m)(^|[\s;{},)])//.*$`), // JS 行注释
}

// TestServerLanguage_NoCJKInUIAssets
// 模板与前端资源里，注释之外不许有中日韩文字。
//
// 界面文案本来就该走 t('key') 查表，写死一个中文标签在六语言界面里是明确的缺陷。
// 这条用例真正逮到过东西：app.js 里 '：' 这个全角冒号被当成分隔符写死，
// 英文界面上渲染成 "Check failed：token expired"——一个汉字都没有，
// 但那是一句中文排版混进了拉丁文句子。
func TestServerLanguage_NoCJKInUIAssets(t *testing.T) {
	repo := repoRoot(t)
	assets := []string{
		"cmd/runner-manager/templates/index.html",
		"cmd/runner-manager/static/app.js",
		"cmd/runner-manager/static/app.css",
	}
	for _, rel := range assets {
		b, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		src := string(b)
		for _, re := range commentRes {
			src = re.ReplaceAllString(src, "$1")
		}
		for i, line := range strings.Split(src, "\n") {
			stripped := line
			for _, ok := range uiAssetAllowed {
				stripped = strings.ReplaceAll(stripped, ok, "")
			}
			if loc := cjkRe.FindStringIndex(stripped); loc != nil {
				t.Errorf("%s:%d 注释之外出现中日韩文字 %q，界面文案要走 t('key'): %s",
					rel, i+1, stripped[loc[0]:loc[1]], strings.TrimSpace(line))
			}
		}
	}
}

// 让 unicode 的导入有用武之地：cjkRe 用的是 \p{Han} 这类类名，
// 这里顺带钉住「全角冒号确实落在我们查的区间里」，免得正则写错了没人发现。
func TestServerLanguage_CJKRegexpCoversFullWidthPunctuation(t *testing.T) {
	for _, r := range []rune{'：', '，', '、', '。', '（', 'あ', 'ア', '한', '中'} {
		if !cjkRe.MatchString(string(r)) {
			t.Errorf("cjkRe 认不出 %q（U+%04X）", r, r)
		}
	}
	for _, r := range []rune{':', 'a', 'é', 'ß', '-'} {
		if cjkRe.MatchString(string(r)) {
			t.Errorf("cjkRe 不该认 %q（U+%04X）", r, r)
		}
		if unicode.Is(unicode.Han, r) {
			t.Errorf("%q 不该是汉字", r)
		}
	}
}
