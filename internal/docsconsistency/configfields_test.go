package docsconsistency

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

var yamlTagRe = regexp.MustCompile(`yaml:"([a-z][a-z0-9_]*)`)

// TestConfigFields_EveryYAMLFieldIsInTheConfigTable config.go 里每个 yaml 字段都要在配置表里。
//
// 审计时这张表漏了六个字段：runners.docker_gid，以及 items[] 的 name / path /
// target_type / target / labels。结果是想手写 config.yaml 的人只能去读
// config.yaml.example（只有中文）或者 config.go 的 struct tag——而那正是「有文档」
// 与「没文档」的实际差别。
//
// 比的是字段名出现与否，不比描述：描述该被翻译，字段名不该。
func TestConfigFields_EveryYAMLFieldIsInTheConfigTable(t *testing.T) {
	repo := repoRoot(t)
	conf := loadDocsConf(t, repo)

	src, err := os.ReadFile(filepath.Join(repo, "internal", "config", "config.go"))
	if err != nil {
		t.Fatal(err)
	}
	fields := map[string]bool{}
	for _, m := range yamlTagRe.FindAllStringSubmatch(string(src), -1) {
		fields[m[1]] = true
	}
	if len(fields) < 15 {
		// 正则被改坏时会安静地扫不到东西，那样这条用例永远绿。
		t.Fatalf("只扫到 %d 个 yaml 字段，yamlTagRe 可能已经匹配不上了", len(fields))
	}
	// 顶层的两个容器本身不是「字段」，表里也没有单独一行。
	delete(fields, "server")
	delete(fields, "runners")

	names := make([]string, 0, len(fields))
	for f := range fields {
		names = append(names, f)
	}
	sort.Strings(names)

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
		for _, name := range names {
			// 边界匹配的理由与 mentionsEnvVar 相同：target 是 target_type 的前缀，
			// 只看子串的话，表里少了 target 这一行也发现不了。
			if !mentionsConfigField(text, name) {
				t.Errorf("%s 没有提到配置字段 %q；配置表缺这一行", filepath.ToSlash(f), name)
			}
		}
	}
}

func mentionsConfigField(text, name string) bool {
	isNameChar := func(b byte) bool {
		return b == '_' || (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9')
	}
	for i := 0; ; {
		j := strings.Index(text[i:], name)
		if j < 0 {
			return false
		}
		s := i + j
		e := s + len(name)
		if (s == 0 || !isNameChar(text[s-1])) && (e == len(text) || !isNameChar(text[e])) {
			return true
		}
		i = s + 1
	}
}
