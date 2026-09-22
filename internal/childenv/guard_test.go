package childenv

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
)

// 这个文件里的两条用例都是护栏：它们不验证 Sanitize 做得对，而是防止下一次有人
// 绕过它。四个调用点改完就绿了，但真正让这件事不复发的是「新增一个 os.Environ()
// 调用点」和「新增一个凭据类环境变量」这两种改动会当场变红。

// repoRoot 从本包目录向上找 go.mod。
// go test 把工作目录设成被测包的源码目录，所以这里的起点是 internal/childenv。
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	start := dir
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("从 %s 一路向上都没找到 go.mod", start)
		}
		dir = parent
	}
}

// scanDirs 是本仓库全部 Go 代码所在的两棵树。
var scanDirs = []string{"cmd", "internal"}

// exemptPkgDir 是唯一允许出现 os.Environ() 的地方——过滤总得有人先把原始环境拿到手。
const exemptPkgDir = "internal/childenv"

// TestNoRawEnvironForChildProcesses 非测试代码里不得再出现 os.Environ()。
//
// 用 go/parser 而不是 grep：注释和字符串里提到这个名字是正常的（本包的包注释就提了好几次），
// 按文本扫会把它们一起报出来，于是这条用例很快会被加一堆例外，最后没人看得懂它到底在管什么。
// AST 只看调用表达式。
//
// 变异验证：把任意一个调用点改回 os.Environ()，这条用例报出该文件与行号。
func TestNoRawEnvironForChildProcesses(t *testing.T) {
	repo := repoRoot(t)
	fset := token.NewFileSet()

	scanned := 0
	for _, root := range scanDirs {
		err := filepath.WalkDir(filepath.Join(repo, root), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(d.Name(), ".go") || strings.HasSuffix(d.Name(), "_test.go") {
				return nil
			}
			rel, err := filepath.Rel(repo, path)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			if filepath.ToSlash(filepath.Dir(rel)) == exemptPkgDir {
				return nil
			}
			scanned++
			for _, line := range osEnvironCallLines(t, fset, path) {
				t.Errorf("%s:%d 调用了 os.Environ()；子进程的环境改用 childenv.Environ()，"+
					"它会去掉 Manager / Agent 自己的凭据", rel, line)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	// 走错了根目录或者匹配器被改坏时，这条用例会安静地什么都扫不到、永远绿。
	if scanned < 20 {
		t.Fatalf("只扫到 %d 个非测试 .go 文件，扫描范围可能不对", scanned)
	}

	// 匹配器自检：本包自己那一处 os.Environ() 必须还认得出来。
	// 少了它，上面的遍历即使一个调用点都发现不了也照样是绿的。
	self := filepath.Join(repo, exemptPkgDir, "childenv.go")
	if lines := osEnvironCallLines(t, fset, self); len(lines) != 1 {
		t.Fatalf("在 %s 里找到 %d 处 os.Environ()（期望 1 处）；"+
			"要么 Environ 的实现变了，要么 osEnvironCallLines 已经匹配不上了", exemptPkgDir+"/childenv.go", len(lines))
	}
}

// osEnvironCallLines 返回文件里每一处 os.Environ() 调用的行号。
//
// 只认包名字面量 os：本仓库没有给 os 起别名，也没有叫 os 的局部变量，
// 为这两种不存在的情况引入类型检查器只会让这条用例更难懂。
func osEnvironCallLines(t *testing.T, fset *token.FileSet, path string) []int {
	t.Helper()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("%s 解析失败: %v", path, err)
	}
	var lines []int
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Environ" {
			return true
		}
		if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "os" {
			lines = append(lines, fset.Position(call.Lparen).Line)
		}
		return true
	})
	return lines
}

// envCallRe 与 internal/docsconsistency/envvars_test.go 里的是同一条，刻意照抄一份：
// 那是另一个包的测试文件，符号跨不过来，而把它提成非测试代码只为共享一条正则并不值得。
// 两边一起改的场面不会出现——它认的是 os.Getenv("X") 这种调用形状，不是某个具体名字。
var envCallRe = regexp.MustCompile(`(?:os\.Getenv|env\.Get\w*)\(\s*"([A-Z][A-Z0-9_]*)"`)

// credentialNameRe 认的是"名字本身就说明它是凭据"的那一类。
// 命名约定不是强制的，所以这条模式给不出完备性；它要挡的是最常见的那种疏忽——
// 加一个 *_TOKEN 就忘了它也会随 environ 传进 Job。漏网的只能靠人往 Denied 里加，
// BASIC_AUTH_USER 就是这么进去的。
var credentialNameRe = regexp.MustCompile(`PASSWORD|TOKEN|SECRET|_KEY$`)

// TestDenied_CoversCredentialEnvVars 代码读的环境变量里，名字像凭据的都必须在 Denied 里。
//
// 当前应当命中 BASIC_AUTH_PASSWORD 与 AGENT_TOKEN 两个。Denied 里的第三项
// BASIC_AUTH_USER 不会被这条模式扫到——它不是漏网，是刻意加进去的：用户名不是秘密，
// 但它同样只属于 Manager，一起去掉，泄露出去的就不是"半个凭据"。
//
// 变异验证：从 Denied 里删掉 AGENT_TOKEN，这条用例报出它和读它的文件。
func TestDenied_CoversCredentialEnvVars(t *testing.T) {
	repo := repoRoot(t)

	readers := map[string]string{} // 变量名 -> 读它的文件，仅用于报错
	for _, root := range scanDirs {
		err := filepath.WalkDir(filepath.Join(repo, root), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(d.Name(), ".go") || strings.HasSuffix(d.Name(), "_test.go") {
				return nil
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(repo, path)
			for _, m := range envCallRe.FindAllStringSubmatch(string(b), -1) {
				if _, seen := readers[m[1]]; !seen {
					readers[m[1]] = filepath.ToSlash(rel)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	// 正则被改坏时它同样是安静地什么都扫不到。
	if len(readers) < 10 {
		t.Fatalf("只扫到 %d 个环境变量，envCallRe 可能已经匹配不上了: %v", len(readers), sortedKeys(readers))
	}

	hits := 0
	for _, name := range sortedKeys(readers) {
		if !credentialNameRe.MatchString(name) {
			continue
		}
		hits++
		if !slices.Contains(Denied, name) {
			t.Errorf("%s（%s 读它）名字像凭据，却不在 childenv.Denied 里；"+
				"它会随 environ 传进 run.sh 跑的 Job", name, readers[name])
		}
	}
	if hits == 0 {
		t.Fatal("一个凭据类变量都没扫到，credentialNameRe 可能已经匹配不上了")
	}
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
