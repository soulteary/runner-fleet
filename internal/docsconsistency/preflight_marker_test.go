package docsconsistency

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/soulteary/runner-fleet/internal/runner"
)

// 文档里的排障第一步：docker compose logs runner-manager | grep '\[preflight'
var grepLogsRe = regexp.MustCompile(`logs runner-manager \| grep '([^']*)'`)

// TestPreflightMarker_DocsGrepWhatTheCodeActuallyLogs 文档让人 grep 的关键词，
// 必须真的是代码打出来的行首标记。
//
// 六种语言的排障小节开头都是这条命令，它是「哪里不对先看启动自检」的落地形态。
// 关键词与标记之间没有任何编译期关系：改了 runner.PreflightLogMarker 而没动文档，
// 六份文档的第一条排障建议会一起变成「grep 一个不存在的词」——命令照样退出 0，
// 只是一行都不返回，读者得到的结论是「自检没报问题」。
//
// 这条检查本身也是 1.2 那件事的一半：标记从「[自检 …]」换成 ASCII 的
// [preflight，是为了让英/法/德/日/韩的读者也能打出这个词。import 而不是读文件，
// 是为了让常量被删掉时这里直接编译不过。
func TestPreflightMarker_DocsGrepWhatTheCodeActuallyLogs(t *testing.T) {
	repo := repoRoot(t)
	marker := runner.PreflightLogMarker

	checked := 0
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
		if filepath.Ext(path) != ".md" {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(repo, path)
		for _, m := range grepLogsRe.FindAllStringSubmatch(string(b), -1) {
			checked++
			// 文档里写的是 grep 的正则，[ 在那里必须转义；比对前还原成字面量。
			pattern := strings.ReplaceAll(m[1], `\[`, "[")
			if !strings.HasPrefix(marker, pattern) {
				t.Errorf("%s 让人 grep %q，但自检日志的标记是 %q",
					filepath.ToSlash(rel), m[1], marker)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// 一条都没匹配上，多半是命令的写法被改了而这条检查没跟上——
	// 那样它会永远是绿的，也就永远不再检查它被写来检查的东西。
	if checked == 0 {
		t.Fatalf("没有在任何文档里找到 %q 形态的排障命令；"+
			"命令改写过的话，grepLogsRe 也要跟着改", grepLogsRe)
	}
}
