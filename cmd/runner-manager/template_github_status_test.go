package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/lab-dev/github-actions-runner-manager/internal/config"
	"github.com/lab-dev/github-actions-runner-manager/internal/runner"
	"github.com/labstack/echo/v4"
)

// renderIndex 用真实的 index.html 渲染一行 runner，返回 HTML。
// 直接跑模板而不是只测 RunnerInfo 上的辅助方法：这个 bug 的根在模板写法上，
// 有人把 {{if .GitHubYes}} 改回 {{if .RegisteredOnGitHub}} 就会原样复发，
// 只测辅助方法的用例对此一无所知。
func renderIndex(t *testing.T, info runner.RunnerInfo) string {
	t.Helper()
	T := map[string]string{}
	b, err := i18nFS.ReadFile("i18n/zh.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &T); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	r := newTemplateRenderer()
	data := map[string]any{
		"Runners": []runner.RunnerInfo{info},
		"Config":  &config.Config{},
		"T":       T,
		"Lang":    "zh",
		"TJSON":   "{}",
		"Version": "test",
	}
	if err := r.Render(&buf, "index.html", data, echo.New().NewContext(nil, nil)); err != nil {
		t.Fatalf("渲染失败: %v", err)
	}
	return buf.String()
}

func boolPtr(b bool) *bool { return &b }

// 这是本次修复的核心回归用例。
//
// {{if .RegisteredOnGitHub}} 作用在 *bool 上时，html/template 只看指针是否为 nil——
// 指向 false 的指针同样为真。于是「GitHub 上查不到这个 Runner」被渲染成「GitHub ✓」，
// 列表列结构上就不可能显示「未显示」。
func TestIndexTemplate_GitHubStatusThreeStates(t *testing.T) {
	T := map[string]string{}
	b, _ := i18nFS.ReadFile("i18n/zh.json")
	_ = json.Unmarshal(b, &T)
	yes, no, failed, pending := T["github.yes"], T["github.no"], T["github.failed"], T["github.pending"]

	cases := []struct {
		name    string
		info    runner.RunnerInfo
		want    string
		notWant []string
	}{
		{
			name: "已登记",
			info: runner.RunnerInfo{Name: "a", Status: runner.StatusInstalled,
				RegistrationMessage: "ok", GitHubCheckAt: "2026-09-20T09:00:00Z",
				RegisteredOnGitHub: boolPtr(true)},
			want:    yes,
			notWant: []string{no, failed},
		},
		{
			name: "确实没登记",
			info: runner.RunnerInfo{Name: "a", Status: runner.StatusInstalled,
				RegistrationMessage: "ok", GitHubCheckAt: "2026-09-20T09:00:00Z",
				RegisteredOnGitHub: boolPtr(false)},
			want:    no,
			notWant: []string{yes, failed},
		},
		{
			name: "查过但没查出来",
			info: runner.RunnerInfo{Name: "a", Status: runner.StatusInstalled,
				RegistrationMessage: "ok", GitHubCheckAt: "2026-09-20T09:00:00Z",
				RegisteredOnGitHub: nil, GitHubCheckError: "GitHub 返回 401：令牌无效或已过期"},
			want:    failed,
			notWant: []string{yes, no},
		},
		{
			name: "从未检查",
			info: runner.RunnerInfo{Name: "a", Status: runner.StatusInstalled,
				RegistrationMessage: "ok"},
			want:    pending,
			notWant: []string{yes, no, failed},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			html := renderIndex(t, tc.info)
			if !strings.Contains(html, tc.want) {
				t.Fatalf("页面里应出现 %q", tc.want)
			}
			for _, nw := range tc.notWant {
				if nw != "" && strings.Contains(html, nw) {
					t.Fatalf("页面里不应出现 %q（实际渲染成了它）", nw)
				}
			}
		})
	}
}

// 查询失败的原因要能在界面上看到，否则「查询失败」四个字没法照着做
func TestIndexTemplate_ShowsCheckErrorAsTooltip(t *testing.T) {
	html := renderIndex(t, runner.RunnerInfo{
		Name: "a", Status: runner.StatusInstalled, RegistrationMessage: "ok",
		GitHubCheckAt:    "2026-09-20T09:00:00Z",
		GitHubCheckError: "GitHub 返回 401：令牌无效或已过期",
	})
	if !strings.Contains(html, "令牌无效或已过期") {
		t.Fatal("失败原因应出现在页面上（title 提示）")
	}
}

// Runner 名字里的尖括号必须被转义，服务端渲染这一侧不能出洞
func TestIndexTemplate_EscapesRunnerName(t *testing.T) {
	html := renderIndex(t, runner.RunnerInfo{
		Name: `<img src=x onerror=alert(1)>`, Status: runner.StatusNew,
	})
	if strings.Contains(html, "<img src=x onerror=alert(1)>") {
		t.Fatal("Runner 名未转义，列表页存在 XSS")
	}
	if !strings.Contains(html, "&lt;img src=x onerror=alert(1)&gt;") {
		t.Fatal("应以转义形式出现")
	}
}
