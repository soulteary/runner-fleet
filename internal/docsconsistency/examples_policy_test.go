package docsconsistency

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode"
)

// docLang 一份文档的语言策略。
type docLang int

const (
	// langZhOnly 只有中文，且刻意不做译文。
	langZhOnly docLang = iota
	// langTranslated 英文为原文，docs_languages 里每种语言都要有对应译文。
	langTranslated
)

// examplesPolicy examples/ 下每份 README 的语言策略。
//
// 这张表是「被决定过」的凭据，不是清单的副本。docs/ 下的三份文档有 ci-recipes 的
// 结构检查和本包的内容检查盯着，examples/ 一直没有任何东西盯——而它那两份 README
// 恰恰是全仓库排障密度最高的内容（15 条带根因，英文 guide.md 的排障只有 9 条），
// 却只有中文，且根 README 的英文入口直接往那儿送人。
//
// 现状是中文单语，这是取舍不是遗漏：见 docs/docs-improvement-plan.md 第 7 节，
// 三个选项与建议都在那里，等作者拍板。在那之前，这张表至少保证**新增**一份
// examples README 时必须在这里补一行，也就是必须先想一下它要不要译文。
var examplesPolicy = map[string]docLang{
	"examples/deploy/README.md":        langZhOnly,
	"examples/runner-images/README.md": langZhOnly,
}

// TestExamplesPolicy_EveryReadmeHasADecision examples/ 下的每份 README 都要在策略表里有一行。
func TestExamplesPolicy_EveryReadmeHasADecision(t *testing.T) {
	repo := repoRoot(t)

	found := map[string]bool{}
	err := filepath.WalkDir(filepath.Join(repo, "examples"), func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.EqualFold(d.Name(), "README.md") {
			return err
		}
		rel, _ := filepath.Rel(repo, path)
		rel = filepath.ToSlash(rel)
		found[rel] = true
		if _, ok := examplesPolicy[rel]; !ok {
			t.Errorf("%s 没有语言策略。在 examplesPolicy 里补一行，"+
				"顺便决定它要不要译文（docs/docs-improvement-plan.md 第 7 节）", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	for rel := range examplesPolicy {
		if !found[rel] {
			t.Errorf("examplesPolicy 里的 %s 已经不存在了，删掉这一行", rel)
		}
	}
}

// TestExamplesPolicy_DeclaredPolicyMatchesReality 声明的策略要和磁盘上的事实一致。
//
// 光有一张表只是备忘录。这条让它变成检查：声称中文单语的，得确实是中文、且确实没有
// 译文副本；声称要翻译的，五种语言一份都不能少。改了策略却没动文件，或者动了文件
// 却没改策略，两个方向都会红。
func TestExamplesPolicy_DeclaredPolicyMatchesReality(t *testing.T) {
	repo := repoRoot(t)
	conf := loadDocsConf(t, repo)

	for rel, policy := range examplesPolicy {
		b, err := os.ReadFile(filepath.Join(repo, rel))
		if err != nil {
			continue // 上一条用例已经报过它不存在
		}
		dir := filepath.Dir(rel)

		switch policy {
		case langZhOnly:
			if !hasHan(string(b)) {
				t.Errorf("%s 声明为中文单语，内容里却没有汉字", rel)
			}
			for _, lang := range conf.langs {
				sib := filepath.Join(repo, dir, lang, "README.md")
				if _, err := os.Stat(sib); err == nil {
					t.Errorf("%s 声明为中文单语，却存在 %s/%s/README.md；"+
						"要么改策略为 langTranslated，要么删掉那份译文", rel, dir, lang)
				}
			}
		case langTranslated:
			if hasHan(string(b)) {
				t.Errorf("%s 声明为英文原文 + 译文，正文里却有汉字", rel)
			}
			for _, lang := range conf.langs {
				sib := filepath.Join(repo, dir, lang, "README.md")
				if _, err := os.Stat(sib); err != nil {
					t.Errorf("%s 声明为英文原文 + 译文，缺 %s/%s/README.md", rel, dir, lang)
				}
			}
		}
	}
}

func hasHan(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}
