package main

import (
	"strings"
	"testing"

	"github.com/soulteary/runner-fleet/internal/config"
	"github.com/soulteary/runner-fleet/internal/runner"
)

func a11yPage(t *testing.T, infos ...runner.RunnerInfo) string {
	t.Helper()
	html, err := renderTo(t, "index.html", map[string]any{
		"Runners": infos,
		"Config":  &config.Config{},
		"T":       map[string]string{},
		"Lang":    "en",
		"TJSON":   "{}",
	})
	if err != nil {
		t.Fatalf("渲染失败: %v", err)
	}
	return html
}

// 弹窗此前既没有 role 也没有 aria-modal，读屏器会把背景内容和弹窗混着念；
// 关闭键写的是 &times;，读出来是「times」。
func TestTemplate_ModalIsAnAccessibleDialog(t *testing.T) {
	html := a11yPage(t)
	i := strings.Index(html, `id="runnerModal"`)
	if i < 0 {
		t.Fatal("找不到弹窗")
	}
	head := html[i : i+800]
	for _, want := range []string{`role="dialog"`, `aria-modal="true"`, `aria-labelledby="modalTitle"`} {
		if !strings.Contains(head, want) {
			t.Errorf("弹窗缺少 %s", want)
		}
	}
	j := strings.Index(html, `id="modalClose"`)
	if j < 0 {
		t.Fatal("找不到关闭按钮")
	}
	if !strings.Contains(html[j-160:j+80], "aria-label=") {
		t.Error("&times; 关闭按钮没有可访问名，读屏器会念成 times")
	}
}

// 焦点管理靠这三段 JS：少哪一段都不会报错，只是键盘用户重新迷路
func TestTemplate_ModalManagesFocus(t *testing.T) {
	// 脚本已从 index.html 拆到 static/app.js，断言跟着载体走，意图不变
	b, err := staticFS.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	html := string(b)
	for _, want := range []string{
		"function trapTab(",
		"lastFocused = document.activeElement",
		"document.contains(lastFocused)",
		"modal.addEventListener('keydown', (e) => trapTab(e, modal));",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("焦点管理缺少 %q", want)
		}
	}
}

// 语言选择器此前没有任何可访问名，读屏器只能念出当前选中的语言
func TestTemplate_LangSelectHasAccessibleName(t *testing.T) {
	html := a11yPage(t)
	i := strings.Index(html, `id="langSelect"`)
	if i < 0 {
		t.Fatal("找不到语言选择器")
	}
	if !strings.Contains(html[i:i+200], "aria-label=") {
		t.Error("语言选择器缺少 aria-label")
	}
	if !strings.Contains(html, `for="langSelect"`) {
		t.Error("语言选择器缺少关联的 label")
	}
}

// 漂移差异、探测错误这些是排查要看的信息，此前只挂在 title 上——
// 键盘和触屏都拿不到。改挂 .tip 之后 title 不该再留着，否则两个提示会同时冒出来。
func TestTemplate_DiagnosticsAreKeyboardReachable(t *testing.T) {
	html := a11yPage(t, runner.RunnerInfo{
		Name: "alpha", Status: runner.StatusUnknown,
		TargetType: "repo", Target: "o/r",
		ContainerDrift:      "image: old -> new",
		Probe:               &runner.ProbeInfo{Error: "agent 无响应", Type: "agent_unreachable"},
		RegistrationMessage: "注册失败：token 过期",
		GitHubBusy:          boolPtr(true),
	})
	for _, want := range []string{
		`<span class="tip">image: old -&gt; new</span>`,
		`<span class="tip">agent 无响应</span>`,
		`<span class="tip">注册失败：token 过期</span>`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("诊断信息未挂成可聚焦提示: %s", want)
		}
	}
	// 带提示的元素必须能聚焦，否则键盘永远走不到
	if n := strings.Count(html, `has-tip" tabindex="0"`); n < 3 {
		t.Errorf("可聚焦的提示元素只有 %d 个，少于预期", n)
	}
	for _, gone := range []string{
		`title="image: old -&gt; new"`,
		`title="agent 无响应"`,
		`title="注册失败：token 过期"`,
	} {
		if strings.Contains(html, gone) {
			t.Errorf("改挂 .tip 后 title 应移除，否则会冒出两个提示: %s", gone)
		}
	}
}
