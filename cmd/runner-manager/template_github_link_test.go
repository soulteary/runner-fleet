package main

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/lab-dev/github-actions-runner-manager/internal/runner"
)

func zhText(t *testing.T, key string) string {
	t.Helper()
	T := map[string]string{}
	b, err := i18nFS.ReadFile("i18n/zh.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &T); err != nil {
		t.Fatal(err)
	}
	if T[key] == "" {
		t.Fatalf("zh.json 里没有 %q", key)
	}
	return T[key]
}

// 已登记时「已注册」和「GitHub ✓」都要能点到 GitHub 的 Runners 设置页
func TestIndexTemplate_GitHubIndicatorsLinkToSettingsPage(t *testing.T) {
	html := renderIndex(t, runner.RunnerInfo{
		Name: "alpha", Status: runner.StatusInstalled,
		TargetType: "repo", Target: "o/r",
		RegistrationMessage: "ok", GitHubCheckAt: "2026-09-20T09:00:00Z",
		RegisteredOnGitHub: boolPtr(true),
		GitHubURL:          runner.GitHubSettingsURL("repo", "o/r"),
	})
	href := `href="https://github.com/o/r/settings/actions/runners"`
	if n := strings.Count(html, href); n != 2 {
		t.Fatalf("「已注册」与「GitHub ✓」都应链接到设置页，找到 %d 处 %s", n, href)
	}
	// 新标签页打开，并且带 noopener——否则新页面能通过 window.opener 操纵本页
	if strings.Count(html, `target="_blank"`) < 2 || strings.Count(html, `rel="noopener noreferrer"`) < 2 {
		t.Fatal("跳转 GitHub 的链接应带 target=_blank 与 rel=noopener noreferrer")
	}
}

// 「已注册」变成链接之后，原来挂在它上面的注册结果原文不能丢——
// 那一格真正的诊断信息就是它，注册失败时尤其要看得到。
func TestIndexTemplate_RegisteredLinkKeepsRegistrationMessageTooltip(t *testing.T) {
	msg := "注册失败：A runner exists with the same name，请先删除同名 Runner"
	html := renderIndex(t, runner.RunnerInfo{
		Name: "alpha", Status: runner.StatusInstalled,
		TargetType: "repo", Target: "o/r",
		RegistrationMessage: msg,
		RegisteredOnGitHub:  boolPtr(true), GitHubCheckAt: "2026-09-20T09:00:00Z",
		GitHubURL: runner.GitHubSettingsURL("repo", "o/r"),
	})
	if !strings.Contains(html, `title="`+msg+`"`) {
		t.Fatal("「已注册」链接应保留注册结果原文作为 title")
	}
}

// 目标拼不出可信地址时宁可不给链接：点了 404 的链接比纯文本更难排查
func TestIndexTemplate_NoLinkWithoutURL(t *testing.T) {
	html := renderIndex(t, runner.RunnerInfo{
		Name: "alpha", Status: runner.StatusInstalled,
		RegistrationMessage: "ok", GitHubCheckAt: "2026-09-20T09:00:00Z",
		RegisteredOnGitHub: boolPtr(true),
		GitHubURL:          "", // target 非法，Go 侧已拒绝生成
	})
	if strings.Contains(html, "/settings/actions/runners") {
		t.Fatal("没有可信地址时不应渲染链接")
	}
	// 文案本身还得在，只是不可点
	if !strings.Contains(html, zhText(t, "github.yes")) {
		t.Fatal("即便没有链接，「GitHub ✓」文案仍应显示")
	}
}

// hrefRe 取出渲染结果里所有 href 的值
var hrefRe = regexp.MustCompile(`href="([^"]*)"`)

// href 是拼给浏览器的。target 由用户填，必须证明它落进 href 后仍然出不去。
//
// 只断言 href 的值，不在整页里搜关键字：target 同时也会作为文本出现在目标那一列，
// 在那里它已经被转义成 &#34; 之类的惰性文本，整页搜 onmouseover= 会搜到它而误报。
// 真正要证明的是「引号进不了 href」——能逃出属性的只有裸引号。
func TestIndexTemplate_HostileTargetCannotInjectHref(t *testing.T) {
	for _, target := range []string{
		`o/r" onmouseover="alert(1)`,
		`o/r`,
	} {
		html := renderIndex(t, runner.RunnerInfo{
			Name: "alpha", Status: runner.StatusInstalled,
			TargetType: "repo", Target: target,
			RegistrationMessage: "ok", GitHubCheckAt: "2026-09-20T09:00:00Z",
			RegisteredOnGitHub: boolPtr(true),
			GitHubURL:          runner.GitHubSettingsURL("repo", target),
		})
		var checked int
		for _, m := range hrefRe.FindAllStringSubmatch(html, -1) {
			href := m[1]
			if !strings.Contains(href, "/settings/actions/runners") {
				continue
			}
			checked++
			if !strings.HasPrefix(href, "https://github.com/") {
				t.Fatalf("target %q 生成了非 github.com 的 href: %q", target, href)
			}
			// html/template 会把 URL 上下文里危险的协议改写成 #ZgotmplZ
			if strings.Contains(href, "ZgotmplZ") || strings.Contains(href, "javascript:") {
				t.Fatalf("target %q 生成了被模板判危的 href: %q", target, href)
			}
			for _, bad := range []string{`"`, `'`, `<`, `>`, ` `} {
				if strings.Contains(href, bad) {
					t.Fatalf("target %q 的 href 里有未转义的 %q: %q", target, bad, href)
				}
			}
		}
		if checked == 0 {
			t.Fatalf("target %q 没渲染出任何设置页链接，断言落空了", target)
		}
	}
}

// 忙碌徽标只在 GitHub 说它忙的时候出现。
// GitHubBusy 是 *bool：模板里写 {{if .GitHubBusy}} 会让「空闲」也显示成「忙碌中」，
// 和当初 RegisteredOnGitHub 那个 bug 是同一个坑，所以三态都要钉住。
func TestIndexTemplate_BusyBadgeOnlyWhenBusy(t *testing.T) {
	busy := zhText(t, "badge.busy")
	cases := []struct {
		name string
		v    *bool
		want bool
	}{
		{"忙碌", boolPtr(true), true},
		{"空闲", boolPtr(false), false},
		{"未知", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			html := renderIndex(t, runner.RunnerInfo{
				Name: "alpha", Status: runner.StatusInstalled,
				TargetType: "repo", Target: "o/r",
				RegistrationMessage: "ok", GitHubBusy: tc.v,
			})
			if got := strings.Contains(html, ">"+busy+"<"); got != tc.want {
				t.Fatalf("忙碌徽标出现 = %v，期望 %v", got, tc.want)
			}
		})
	}
}

// 查看态要真的是三栏。样式表里少了这条，弹窗就退回一长条竖排，
// 而模板渲染得出来的 HTML 是一样的——只能直接断言样式。
func TestIndexTemplate_ViewIsThreeColumnGrid(t *testing.T) {
	b, err := templateFS.ReadFile("templates/index.html")
	if err != nil {
		t.Fatal(err)
	}
	css := string(b)
	for _, want := range []string{
		"#modalView { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr));",
		"#modalView .row.span-all { grid-column: 1 / -1; }",
	} {
		if !strings.Contains(css, want) {
			t.Fatalf("样式表里应有 %q", want)
		}
	}
	// 行内 display:block 会盖掉 display:grid，三栏当场失效
	if strings.Contains(css, "modalView.style.display = 'block'") {
		t.Fatal("modalView 不能用行内 display:block 显示，会盖掉样式表里的 grid")
	}
	if strings.Contains(css, "modalEditForm.style.display = 'block'") {
		t.Fatal("modalEditForm 不能用行内 display:block 显示，会盖掉样式表里的 grid")
	}
}
