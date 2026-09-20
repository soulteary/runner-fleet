package main

import (
	"regexp"
	"strings"
	"testing"
)

// dataRef 匹配对接口返回值的引用，如 data.message、data.install_dir
var dataRef = regexp.MustCompile(`data\.[A-Za-z_][A-Za-z0-9_]*`)

// escapedRef 匹配已经过转义的引用：escapeHtml(data.x ...
var escapedRef = regexp.MustCompile(`escapeHtml\(\s*data\.[A-Za-z_][A-Za-z0-9_]*`)

// boolRef 匹配只作真假判断、并不拼进字符串的引用：data.x ? / data.x &&
var boolRef = regexp.MustCompile(`data\.[A-Za-z_][A-Za-z0-9_]*\s*(\?|&&|\|\||\))`)

// 接口返回值里嵌着用户填的 Runner 名，而名称校验只拦 .. / \，
// `<img src=x onerror=...>` 这样的名字是收得进来的（会被原样建成目录、写进配置、
// 再由接口回显）。任何拼进 innerHTML 的地方都必须转义。
//
// 用扫描而不是只断言某一行：这个 bug 就是「旁边一行老实转义了、这一行漏了」，
// 钉死某一行挡不住下一处新增的漏网。
func TestIndexTemplate_NoUnescapedDataInInnerHTML(t *testing.T) {
	b, err := templateFS.ReadFile("templates/index.html")
	if err != nil {
		t.Fatal(err)
	}
	var bad []string
	for _, line := range strings.Split(string(b), "\n") {
		if !strings.Contains(line, "innerHTML") || !strings.Contains(line, "data.") {
			continue
		}
		stripped := escapedRef.ReplaceAllString(line, "")
		stripped = boolRef.ReplaceAllString(stripped, "")
		if m := dataRef.FindAllString(stripped, -1); len(m) > 0 {
			bad = append(bad, strings.TrimSpace(line)+"  ← "+strings.Join(m, ", "))
		}
	}
	if len(bad) > 0 {
		t.Fatalf("以下 innerHTML 赋值里有未经 escapeHtml 的接口字段，Runner 名可借此注入脚本:\n  %s",
			strings.Join(bad, "\n  "))
	}
}

// 守住上面那三个正则本身：它们若写错就成了永远通过的空检查
func TestIndexTemplate_EscapingScannerCatchesUnescaped(t *testing.T) {
	cases := []struct {
		line string
		want bool // true = 应被判为「有未转义的字段」
	}{
		{`msgEl.innerHTML = data.message + '<br>';`, true},
		{`el.innerHTML = '<span>' + data.install_dir + '</span>';`, true},
		{`msgEl.innerHTML = escapeHtml(data.message || '');`, false},
		{`el.innerHTML = data.running ? '<b>x</b>' : '';`, false},
		{`el.innerHTML = '<s>' + escapeHtml(data.status || '') + '</s>';`, false},
	}
	for _, tc := range cases {
		stripped := escapedRef.ReplaceAllString(tc.line, "")
		stripped = boolRef.ReplaceAllString(stripped, "")
		got := len(dataRef.FindAllString(stripped, -1)) > 0
		if got != tc.want {
			t.Errorf("扫描器对 %q 判为 %v，期望 %v", tc.line, got, tc.want)
		}
	}
}
