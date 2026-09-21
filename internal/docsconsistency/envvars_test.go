package docsconsistency

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// 直接取环境变量的调用点：os.Getenv("X")、env.GetTrimmed("X", …) 等。
var envCallRe = regexp.MustCompile(`(?:os\.Getenv|env\.Get\w*)\(\s*"([A-Z][A-Z0-9_]*)"`)

// loopReadEnvVars 是少数几个不在调用点上出现名字的变量。
//
// config.go 里端口是遍历一个切片读的（`for _, key := range []string{…}`），
// 目的是让「两个名字、后一个生效」这件事只写一遍。代价是上面那条正则看不见它们，
// 所以在这里点名。下面的用例会反过来确认它们确实还在那个文件里——
// 哪天循环没了，这里会提醒你把这两行删掉，而不是继续守着两个不存在的变量。
var loopReadEnvVars = map[string]string{
	"MANAGER_PORT": "internal/config/config.go",
	"SERVER_PORT":  "internal/config/config.go",
}

// scriptEnvVars 是 scripts/install-runner.sh 读的变量。
// 它不是 Go 代码，上面的正则扫不到，但对运维来说和其它变量没有区别。
var scriptEnvVars = map[string]string{
	"RUNNER_VERSION":         "scripts/install-runner.sh",
	"RUNNER_SHA256":          "scripts/install-runner.sh",
	"RUNNER_FORCE_REINSTALL": "scripts/install-runner.sh",
}

// TestEnvVars_EveryVariableTheCodeReadsIsDocumented 代码读的每个环境变量都要在使用指南里查得到。
//
// 审计时这一项是「文档缺口」里最大的一处：guide.md 只写了一句「部分字段可通过环境变量覆盖……
// 详见 .env.example」，而那份文件只有中文，本身还漏了三个变量，另有两个（RUNNER_IMAGE 的别名
// CONTAINER_IMAGE、VOLUME_HOST_PATH 的别名 RUNNERS_VOLUME_HOST_PATH）只有读源码才知道存在。
//
// 补一张表只解决当时那一次。这条用例解决的是下一次：新加一个 env 读取点而不写文档，
// 测试当场红，报出的是变量名和它该去的地方。
func TestEnvVars_EveryVariableTheCodeReadsIsDocumented(t *testing.T) {
	repo := repoRoot(t)
	conf := loadDocsConf(t, repo)

	vars := map[string]string{} // 变量名 -> 读它的文件，仅用于报错
	err := filepath.WalkDir(repo, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".go") || strings.HasSuffix(d.Name(), "_test.go") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(repo, path)
		for _, m := range envCallRe.FindAllStringSubmatch(string(b), -1) {
			if _, seen := vars[m[1]]; !seen {
				vars[m[1]] = filepath.ToSlash(rel)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(vars) < 10 {
		// 正则被改坏时它会安静地什么都扫不到，那样这条用例永远绿。
		t.Fatalf("只扫到 %d 个环境变量，envCallRe 可能已经匹配不上了: %v", len(vars), keysOf(vars))
	}
	for name, where := range loopReadEnvVars {
		src, errRead := os.ReadFile(filepath.Join(repo, where))
		if errRead != nil {
			t.Fatalf("%s 读不到: %v", where, errRead)
		}
		if !strings.Contains(string(src), `"`+name+`"`) {
			t.Errorf("%s 已经不在 %s 里了，把它从 loopReadEnvVars 删掉", name, where)
			continue
		}
		vars[name] = where
	}
	for name, where := range scriptEnvVars {
		vars[name] = where
	}

	// 英文原文与五份译文都要提到——变量名是标识符，不该被翻译，
	// 所以「某一份译文里没有」就是那一份漏了一行。
	files := []string{filepath.Join(conf.root, "guide.md")}
	for _, lang := range conf.langs {
		files = append(files, filepath.Join(conf.root, lang, "guide.md"))
	}
	for _, f := range files {
		b, errRead := os.ReadFile(filepath.Join(repo, f))
		if errRead != nil {
			t.Fatal(errRead)
		}
		text := string(b)
		for _, name := range keysOf(vars) {
			if !mentionsEnvVar(text, name) {
				t.Errorf("%s 没有提到 %s（%s 读它）；环境变量一节缺这一行",
					filepath.ToSlash(f), name, vars[name])
			}
		}
	}
}

// mentionsEnvVar 判断文档里是否提到了这个变量，按标识符边界匹配。
//
// 不能用 strings.Contains：`FLEET_IMAGE_TAG` 是 `FLEET_IMAGE_TAGG` 的子串，
// 于是一个写错成更长名字的变量照样「提到过」。这正是写这条用例时自己踩到的——
// 第一版用 Contains，把译文里的名字改长一个字母，测试纹丝不动。
func mentionsEnvVar(text, name string) bool {
	isNameChar := func(b byte) bool {
		return b == '_' || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
	}
	for i := 0; ; {
		j := strings.Index(text[i:], name)
		if j < 0 {
			return false
		}
		s := i + j
		e := s + len(name)
		beforeOK := s == 0 || !isNameChar(text[s-1])
		afterOK := e == len(text) || !isNameChar(text[e])
		if beforeOK && afterOK {
			return true
		}
		i = s + 1
	}
}

func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestEnvVars_ConfigOverridesAreInTheEnvTemplate config.go 读的每个变量都要出现在 .env.example 里。
//
// 那个文件是「全容器部署只改 .env」这条路径的全部界面，而它此前漏了三个变量，其中两个
// （CONTAINER_IMAGE、RUNNERS_VOLUME_HOST_PATH）是别名，只有读源码才知道存在。
//
// 范围刻意只取 internal/config/config.go：它读的正好是「能覆盖配置文件的那一组」，
// 也就是一份 .env 需要覆盖的全集。Agent 在容器内读的 AGENT_PORT 之类不在其中——
// 那些由 Manager 创建容器时注入，写进模板只会是噪音。
func TestEnvVars_ConfigOverridesAreInTheEnvTemplate(t *testing.T) {
	repo := repoRoot(t)

	src, err := os.ReadFile(filepath.Join(repo, "internal", "config", "config.go"))
	if err != nil {
		t.Fatal(err)
	}
	vars := map[string]bool{}
	for _, m := range envCallRe.FindAllStringSubmatch(string(src), -1) {
		vars[m[1]] = true
	}
	for name, where := range loopReadEnvVars {
		if where == "internal/config/config.go" {
			vars[name] = true
		}
	}
	if len(vars) < 8 {
		t.Fatalf("只在 config.go 里扫到 %d 个变量，envCallRe 可能已经匹配不上了", len(vars))
	}

	tmpl, err := os.ReadFile(filepath.Join(repo, ".env.example"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range keysOf(boolKeysAsMap(vars)) {
		if !mentionsEnvVar(string(tmpl), name) {
			t.Errorf(".env.example 里没有 %s；全容器部署只改 .env 的人会找不到它", name)
		}
	}
}

func boolKeysAsMap(in map[string]bool) map[string]string {
	out := make(map[string]string, len(in))
	for k := range in {
		out[k] = ""
	}
	return out
}
