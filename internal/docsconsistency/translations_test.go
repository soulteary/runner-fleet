package docsconsistency

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// docsConf 是 scripts/ci-recipes.conf 里与文档有关的三项。
//
// 从同一个文件读，而不是在这里再写一遍语言清单：ci-recipes 的标题检查读它，
// 这里的内容检查也读它，加第七种语言仍然只改那一个文件。两边各钉一份的下场，
// Makefile 顶上的注释已经写过了——「改一处忘另一处，就是本地与 CI 判得不一样」。
type docsConf struct {
	root  string
	langs []string
	files []string
}

func loadDocsConf(t *testing.T, repo string) docsConf {
	t.Helper()
	f, err := os.Open(filepath.Join(repo, "scripts", "ci-recipes.conf"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	conf := docsConf{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if i := strings.Index(line, "#"); i >= 0 {
			line = line[:i]
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key, val = strings.TrimSpace(key), strings.TrimSpace(val)
		switch key {
		case "docs_root":
			conf.root = val
		case "docs_languages":
			conf.langs = append(conf.langs, strings.Fields(val)...)
		case "docs_files":
			conf.files = append(conf.files, strings.Fields(val)...)
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	if conf.root == "" || len(conf.langs) == 0 || len(conf.files) == 0 {
		t.Fatalf("ci-recipes.conf 里没读到 docs_root/docs_languages/docs_files: %+v", conf)
	}
	return conf
}

// TestTranslations_SectionContentMatchesEnglish 比对英文原文与五份译文在每一节里的内容量。
//
// 现有的 ci-recipes check-docs-structure 比的是标题层级序列，标题之下少了什么它看不见。
// 这不是假设出来的风险，是已经发生过的事：075b612 给英文 development.md 的 HTTP API 表
// 加了 DELETE /api/runners/:name 一行，译文一份没动；797959a 作为补译把
// 「删除 Runner 与 GitHub」那一节补了回去——因为它是标题，结构检查会红——
// 而表格里的那一行不是标题，于是在五份译文里一起缺了下来，检查照样报「全部一致」。
//
// 比的是量不是文本：文本本来就该被翻译。四个量分别对应四类实际发生过的漂移——
// 表格少一行、代码块少一段、代码块里少一条命令、列表少一条。
func TestTranslations_SectionContentMatchesEnglish(t *testing.T) {
	repo := repoRoot(t)
	conf := loadDocsConf(t, repo)

	for _, file := range conf.files {
		enPath := filepath.Join(repo, conf.root, file)
		en := parseSections(t, enPath)

		for _, lang := range conf.langs {
			trPath := filepath.Join(repo, conf.root, lang, file)
			tr := parseSections(t, trPath)

			if len(en) != len(tr) {
				// 标题数对不上是 check-docs-structure 的职责，这里只是说明为什么不往下比。
				t.Errorf("%s/%s: 节数为 %d，英文为 %d；先让 check-docs-structure 过",
					lang, file, len(tr), len(en))
				continue
			}

			for i := range en {
				where := fmt.Sprintf("%s/%s 第 %d 节（英文 %s:%d）",
					lang, file, i, filepath.Join(conf.root, file), en[i].line)

				if en[i].tableRow != tr[i].tableRow {
					t.Errorf("%s: 表格 %d 行，英文 %d 行", where, tr[i].tableRow, en[i].tableRow)
				}
				if en[i].bullets != tr[i].bullets {
					t.Errorf("%s: 列表项 %d 条，英文 %d 条", where, tr[i].bullets, en[i].bullets)
				}
				if len(en[i].blocks) != len(tr[i].blocks) {
					t.Errorf("%s: 代码块 %d 段，英文 %d 段", where, len(tr[i].blocks), len(en[i].blocks))
					continue
				}
				for b := range en[i].blocks {
					if en[i].blocks[b].lines != tr[i].blocks[b].lines {
						t.Errorf("%s: 第 %d 段代码块有 %d 行命令，英文 %d 行",
							where, b+1, tr[i].blocks[b].lines, en[i].blocks[b].lines)
					}
					// 语言标记不该被翻译，对不上多半是某一份漏译了代码块、
					// 后面的块整体错位一格，而只比行数时两边恰好都对得上。
					if en[i].blocks[b].info != tr[i].blocks[b].info {
						t.Errorf("%s: 第 %d 段代码块标着 ```%s，英文是 ```%s",
							where, b+1, tr[i].blocks[b].info, en[i].blocks[b].info)
					}
				}

				enLinks := normalizeLinks(repo, conf, en[i].links)
				trLinks := normalizeLinks(repo, conf, tr[i].links)
				if strings.Join(enLinks, " ") != strings.Join(trLinks, " ") {
					t.Errorf("%s: 链接目标为 %v，英文为 %v", where, trLinks, enLinks)
				}
			}
		}
	}
}

// normalizeLinks 把链接目标折算成可跨语言比较的形式。
//
// 两处必须归一，否则全是假阳性：路径深度（../examples 与 ../../examples 指同一处，
// filepath.Join 时已经解决），以及各语言指向自己那份文档的链接——
// docs/zh/development.md 的页脚指 docs/zh/README.md，英文指 docs/README.md，
// 这是对的，不是漂移。去掉语言目录段后两者相等。
func normalizeLinks(repo string, conf docsConf, links []string) []string {
	out := make([]string, 0, len(links))
	for _, l := range links {
		rel, err := filepath.Rel(repo, l)
		if err != nil {
			rel = l
		}
		rel = filepath.ToSlash(rel)
		for _, lang := range conf.langs {
			prefix := conf.root + "/" + lang
			if rel == prefix || strings.HasPrefix(rel, prefix+"/") {
				rel = conf.root + strings.TrimPrefix(rel, prefix)
				break
			}
		}
		out = append(out, strings.TrimSuffix(rel, "/"))
	}
	sort.Strings(out)
	return out
}
