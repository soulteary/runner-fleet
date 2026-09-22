package secrets

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

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

// secretsPkgDir 允许出现旧路径的唯一位置
const secretsPkgDir = "internal/secrets"

// patFileName PAT 的旧文件名。这里刻意写成字面量而不是引用 LegacyRunnerTokenFile：
// 本测试要查的就是「这个字面量还出现在哪儿」，用常量去查常量等于自己放自己过关。
const patFileName = ".github_check_token"

// legacyConstName 旧路径常量名
const legacyConstName = "LegacyRunnerTokenFile"

// goFilesUnderRepo 遍历仓库里的非测试 Go 文件，返回相对路径
func goFilesUnderRepo(t *testing.T) []string {
	t.Helper()
	root := repoRoot(t)
	var out []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
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
		name := d.Name()
		// 只查产品代码。测试里出现旧路径是正当的——迁移本身要造出旧文件才测得了。
		if filepath.Ext(name) != ".go" || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		out = append(out, rel)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) == 0 {
		t.Fatal("一个 Go 文件都没扫到，遍历本身出了问题")
	}
	return out
}

func inSecretsPkg(rel string) bool {
	return strings.HasPrefix(filepath.ToSlash(rel), secretsPkgDir+"/")
}

// TestNoManagerSecretsUnderRunnerDirs 护栏：Manager 的 PAT 不许再从 Runner 目录里读。
//
// 这件事修好之后很容易被无声地改回来——`filepath.Join(installDir, ".github_check_token")`
// 是一行就能写出来的东西，而它读到的文件在容器模式下被整个挂进 Runner 容器，
// Job 与它同为 UID 1001，权限挡不住。所以这里把「只有 internal/secrets 的迁移代码
// 能提到旧路径」钉死。
//
// 用 AST 而不是正则：正则会被注释、字符串拼接和换行绕过，而这里要判的恰恰是
// 「有没有真的去拼那个路径」。
func TestNoManagerSecretsUnderRunnerDirs(t *testing.T) {
	root := repoRoot(t)
	fset := token.NewFileSet()

	for _, rel := range goFilesUnderRepo(t) {
		f, err := parser.ParseFile(fset, filepath.Join(root, rel), nil, parser.ParseComments)
		if err != nil {
			t.Fatalf("解析 %s 失败: %v", rel, err)
		}

		ast.Inspect(f, func(n ast.Node) bool {
			switch v := n.(type) {
			case *ast.BasicLit:
				// 旧文件名的字面量只能出现在 internal/secrets 里
				if v.Kind != token.STRING {
					return true
				}
				s, err := strconv.Unquote(v.Value)
				if err != nil {
					return true
				}
				if strings.Contains(s, patFileName) && !inSecretsPkg(rel) {
					t.Errorf("%s:%d 出现了 PAT 的旧文件名 %q。"+
						"PAT 现在只经由 secrets.Store 读写（"+PathDescription+"），"+
						"Runner 目录在容器模式下会被挂进 Runner 容器",
						rel, fset.Position(v.Pos()).Line, s)
				}
			case *ast.Ident:
				// 旧路径常量名同理
				if v.Name == legacyConstName && !inSecretsPkg(rel) {
					t.Errorf("%s:%d 引用了 %s。它只保留给 %s 里的迁移代码",
						rel, fset.Position(v.Pos()).Line, legacyConstName, secretsPkgDir)
				}
			case *ast.CallExpr:
				// filepath.Join(<任意>, <提到 token 的东西>) 不许出现在 secrets 之外。
				// 这是把凭据拼回 Runner 目录的那一行长什么样。
				if inSecretsPkg(rel) || !isFilepathJoin(v) {
					return true
				}
				for _, arg := range v.Args {
					if desc, ok := mentionsPATToken(arg); ok {
						t.Errorf("%s:%d filepath.Join 的参数 %s 看起来在拼 PAT 的路径。"+
							"Manager 的凭据不放在 Runner 目录下，改用 secrets.Store",
							rel, fset.Position(v.Pos()).Line, desc)
					}
				}
			}
			return true
		})
	}
}

// isFilepathJoin 判断是不是 filepath.Join(...)
func isFilepathJoin(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Join" {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "filepath"
}

// mentionsPATToken 判断某个参数是否在指 PAT 这个文件。
//
// 只认 PAT：.agent_token 正当地住在 Runner 目录里（Agent 就在那个容器里跑，
// 令牌另外还经环境变量注入），本护栏不管它——见 PR 里「考虑过但没采用」。
func mentionsPATToken(arg ast.Expr) (string, bool) {
	switch v := arg.(type) {
	case *ast.BasicLit:
		if v.Kind != token.STRING {
			return "", false
		}
		s, err := strconv.Unquote(v.Value)
		if err != nil {
			return "", false
		}
		if strings.Contains(s, patFileName) {
			return strconv.Quote(s), true
		}
	case *ast.Ident:
		if isPATTokenName(v.Name) {
			return v.Name, true
		}
	case *ast.SelectorExpr:
		if isPATTokenName(v.Sel.Name) {
			return v.Sel.Name, true
		}
	}
	return "", false
}

// isPATTokenName 名字里同时带 github 与 token 的，就当它是 PAT
func isPATTokenName(name string) bool {
	l := strings.ToLower(name)
	return strings.Contains(l, "token") && strings.Contains(l, "github")
}

// 护栏本身要能抓到东西，否则它只是一段永远通过的代码。
// 这里用一份写在临时文件里的「违规代码」验它真的会报。
func TestGuardrailCatchesTheViolation(t *testing.T) {
	const bad = `package x

import "path/filepath"

const RunnerTokenFile = ".github_check_token"

func read(installDir string) string {
	_ = filepath.Join(installDir, RunnerTokenFile)
	return filepath.Join(installDir, ".github_check_token")
}
`
	dir := t.TempDir()
	p := filepath.Join(dir, "bad.go")
	if err := os.WriteFile(p, []byte(bad), 0600); err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, p, nil, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}

	var hits int
	ast.Inspect(f, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.BasicLit:
			if v.Kind == token.STRING {
				if s, err := strconv.Unquote(v.Value); err == nil && strings.Contains(s, patFileName) {
					hits++
				}
			}
		case *ast.CallExpr:
			if !isFilepathJoin(v) {
				return true
			}
			for _, arg := range v.Args {
				if _, ok := mentionsPATToken(arg); ok {
					hits++
				}
			}
		}
		return true
	})
	// 两处字面量 + 两处 Join 参数（常量名 RunnerTokenFile 不含 github，
	// 但它的值是字面量，已被第一类抓到；.github_check_token 那次 Join 也被抓到）
	if hits < 3 {
		t.Fatalf("护栏应抓到这段违规代码，只命中 %d 处", hits)
	}
}
