package docsconsistency

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// tr(c, "api.x") / trf(c, "api.x", …) 里的键。
var apiKeyRe = regexp.MustCompile(`\btrf?\(\s*\w+\s*,\s*"(api\.[a-z0-9_.]+)"`)

// TestAPIKeys_EveryKeyTheHandlerUsesExistsInAllLanguages
// handler 里用到的每个 api.* 键，六份 i18n JSON 都要有。
//
// 服务端消息此前是中文硬编码：界面切到 English，外壳是英文，每一个 toast 是中文。
// 现在它们走 tr/trf 查表，而查不到时回落成**键本身**——界面上出现 "api.start_failed"
// 一眼能看出是缺陷，但前提是有人看见。新加一处 tr 调用却忘了补键，靠肉眼是看不见的：
// 那条路径可能几个月都没人走到。
//
// 反向也查：JSON 里有而代码里没人用的 api.* 键，是删代码时留下的，留着只会让
// 下一个人以为还有人在用。
func TestAPIKeys_EveryKeyTheHandlerUsesExistsInAllLanguages(t *testing.T) {
	repo := repoRoot(t)
	conf := loadDocsConf(t, repo)

	used := map[string]string{} // key -> 第一次出现的位置
	err := filepath.WalkDir(filepath.Join(repo, "internal"), func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(d.Name(), ".go") {
			return err
		}
		if strings.HasSuffix(d.Name(), "_test.go") {
			return nil
		}
		b, errRead := os.ReadFile(path)
		if errRead != nil {
			return errRead
		}
		rel, _ := filepath.Rel(repo, path)
		for _, m := range apiKeyRe.FindAllStringSubmatch(string(b), -1) {
			if _, seen := used[m[1]]; !seen {
				used[m[1]] = filepath.ToSlash(rel)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(used) < 20 {
		// 正则改坏时会安静地什么都扫不到，那样这条用例永远绿。
		t.Fatalf("只扫到 %d 个 api.* 键，apiKeyRe 可能已经匹配不上了", len(used))
	}

	langs := append([]string{"en"}, conf.langs...)
	inJSON := map[string]map[string]bool{}
	for _, lang := range langs {
		p := filepath.Join(repo, "cmd", "runner-manager", "i18n", lang+".json")
		b, errRead := os.ReadFile(p)
		if errRead != nil {
			t.Fatal(errRead)
		}
		var m map[string]string
		if errJSON := json.Unmarshal(b, &m); errJSON != nil {
			t.Fatalf("%s.json 解析失败: %v", lang, errJSON)
		}
		have := map[string]bool{}
		for k, v := range m {
			if strings.HasPrefix(k, "api.") {
				have[k] = true
				if strings.TrimSpace(v) == "" {
					t.Errorf("%s.json 的 %s 是空串；空值会让 tr 回落，等于没译", lang, k)
				}
			}
		}
		inJSON[lang] = have
	}

	keys := make([]string, 0, len(used))
	for k := range used {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, lang := range langs {
		for _, k := range keys {
			if !inJSON[lang][k] {
				t.Errorf("%s.json 缺 %s（%s 在用）", lang, k, used[k])
			}
		}
		for k := range inJSON[lang] {
			if _, ok := used[k]; !ok {
				t.Errorf("%s.json 的 %s 已经没有代码在用了，删掉它", lang, k)
			}
		}
	}
}
