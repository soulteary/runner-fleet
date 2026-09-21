package main

import (
	"io/fs"
	"regexp"
	"sort"
	"strings"
	"testing"
)

func i18nLangs(t *testing.T) []string {
	t.Helper()
	entries, err := fs.ReadDir(i18nFS, "i18n")
	if err != nil {
		t.Fatal(err)
	}
	var langs []string
	for _, e := range entries {
		langs = append(langs, strings.TrimSuffix(e.Name(), ".json"))
	}
	sort.Strings(langs)
	if len(langs) < 2 {
		t.Fatalf("内嵌语言文件只有 %v，嵌入声明可能没生效", langs)
	}
	return langs
}

func TestLoadI18n_AllEmbeddedFilesParse(t *testing.T) {
	for _, lang := range i18nLangs(t) {
		m, err := loadI18n(lang)
		if err != nil {
			t.Errorf("%s.json 解析失败: %v", lang, err)
			continue
		}
		if len(m) == 0 {
			t.Errorf("%s.json 里没有任何键", lang)
		}
	}
}

func TestLoadI18n_UnknownLangErrors(t *testing.T) {
	if _, err := loadI18n("does-not-exist"); err == nil {
		t.Fatal("未知语言应报错，由调用方回落到 en")
	}
	// 路径拼接不该被 .. 带出 i18n 目录
	if _, err := loadI18n("../go"); err == nil {
		t.Fatal("带 .. 的语言名不应读到 i18n 之外的文件")
	}
}

// 少一个键在界面上是一处空白，不会报错，所以只能靠用例守。
// 新增文案时漏掉某个语言正是最容易发生的疏忽。
func TestLoadI18n_AllLanguagesHaveSameKeys(t *testing.T) {
	en, err := loadI18n("en")
	if err != nil {
		t.Fatal(err)
	}
	for _, lang := range i18nLangs(t) {
		if lang == "en" {
			continue
		}
		m, err := loadI18n(lang)
		if err != nil {
			t.Fatal(err)
		}
		var missing, extra []string
		for k := range en {
			if _, ok := m[k]; !ok {
				missing = append(missing, k)
			}
		}
		for k := range m {
			if _, ok := en[k]; !ok {
				extra = append(extra, k)
			}
		}
		sort.Strings(missing)
		sort.Strings(extra)
		if len(missing) > 0 {
			t.Errorf("%s.json 相对 en.json 缺少: %v", lang, missing)
		}
		if len(extra) > 0 {
			t.Errorf("%s.json 有 en.json 里没有的键: %v", lang, extra)
		}
	}
}

// 空值和缺失一样是一处空白
func TestLoadI18n_NoBlankValues(t *testing.T) {
	for _, lang := range i18nLangs(t) {
		m, err := loadI18n(lang)
		if err != nil {
			t.Fatal(err)
		}
		for k, v := range m {
			if strings.TrimSpace(v) == "" {
				t.Errorf("%s.json 的 %q 是空值", lang, k)
			}
		}
	}
}

var (
	tplKeyRe = regexp.MustCompile(`\{\{index \$?\.T "([^"]+)"\}\}`)
	// JS 侧的 t('xxx.yyy')：要求带点，免得把 createElement('div') 这类误当成键
	jsKeyRe = regexp.MustCompile(`\bt\('([a-z][a-z_]*\.[a-zA-Z_0-9]+)'\)`)
)

// 模板里引用了却不存在的键，渲染出来就是一段空白，不会有任何报错。
//
// 脚本已从 index.html 拆到 static/app.js，两边都要扫：只扫模板的话，
// app.js 里写错一个键同样不会报错，界面上就是一处空白。
func TestTemplateKeysExistInI18n(t *testing.T) {
	tpl, err := templateFS.ReadFile("templates/index.html")
	if err != nil {
		t.Fatal(err)
	}
	js, err := staticFS.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	en, err := loadI18n("en")
	if err != nil {
		t.Fatal(err)
	}
	used := map[string]bool{}
	for _, m := range tplKeyRe.FindAllStringSubmatch(string(tpl), -1) {
		used[m[1]] = true
	}
	jsUsed := 0
	for _, m := range jsKeyRe.FindAllStringSubmatch(string(js), -1) {
		used[m[1]] = true
		jsUsed++
	}
	// 脚本拆出去之后最容易发生的疏忽是这里只剩模板、忘了跟着扫 app.js
	if jsUsed == 0 {
		t.Fatal("没有从 static/app.js 里提取到任何 i18n 键，扫描范围或正则可能失效了")
	}
	if len(used) == 0 {
		t.Fatal("没有从模板里提取到任何 i18n 键，正则可能失效了")
	}
	var missing []string
	for k := range used {
		if _, ok := en[k]; !ok {
			missing = append(missing, k)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("模板或脚本引用了 en.json 里没有的键: %v", missing)
	}
}
