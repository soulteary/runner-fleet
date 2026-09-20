package main

import (
	"bytes"
	"html/template"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/lab-dev/github-actions-runner-manager/internal/config"
	"github.com/lab-dev/github-actions-runner-manager/internal/runner"
)

func TestNewTemplateRenderer_ParsesEmbeddedTemplates(t *testing.T) {
	r := newTemplateRenderer()
	if r == nil || r.templates == nil {
		t.Fatal("渲染器或模板集为空")
	}
	// 两种命名都要能查到：ParseFS 在不同 Go 版本里给出的名字不一样，
	// Render 的回退就是为这件事写的
	if r.templates.Lookup("index.html") == nil && r.templates.Lookup("templates/index.html") == nil {
		t.Fatal("index.html 两种命名都查不到")
	}
}

func renderTo(t *testing.T, name string, data any) (string, error) {
	t.Helper()
	var buf bytes.Buffer
	err := newTemplateRenderer().Render(&buf, name, data, echo.New().NewContext(nil, nil))
	return buf.String(), err
}

// Render 的契约是：无论 ParseFS 把模板登记成 index.html 还是 templates/index.html，
// 用 basename 调都能渲染出来（handler 传的就是 basename）。
// 反过来不成立——当前 Go 版本只登记 basename，用带路径的名字调本就该报错。
func TestRender_IndexRendersByBaseName(t *testing.T) {
	data := map[string]any{
		"Runners": []runner.RunnerInfo{{Name: "alpha", Status: runner.StatusInstalled}},
		"Config":  &config.Config{},
		"T":       map[string]string{},
		"Lang":    "zh",
		"TJSON":   "{}",
		"Version": "test",
	}
	html, err := renderTo(t, "index.html", data)
	if err != nil {
		t.Fatalf("渲染失败: %v", err)
	}
	if !strings.Contains(html, "alpha") {
		t.Fatal("渲染的页面里没有 runner 名")
	}

	// 回退逻辑本身：把模板换个名字登记，basename 依然要能渲染
	tpl := template.Must(template.New("templates/x.html").Parse(`hello {{.}}`))
	r := &templateRenderer{templates: tpl}
	var buf bytes.Buffer
	if err := r.Render(&buf, "x.html", "world", echo.New().NewContext(nil, nil)); err != nil {
		t.Fatalf("模板登记为带路径的名字时，用 basename 调应能渲染: %v", err)
	}
	if buf.String() != "hello world" {
		t.Fatalf("渲染结果为 %q", buf.String())
	}
}

// 未知模板名要报错，而不是悄悄渲染出一个空页面
func TestRender_UnknownTemplateReturnsError(t *testing.T) {
	if _, err := renderTo(t, "does-not-exist.html", nil); err == nil {
		t.Fatal("未知模板名应返回错误")
	}
}

// 没有 runner 时页面照样渲染得出来
func TestRender_EmptyRunnerListStillRenders(t *testing.T) {
	html, err := renderTo(t, "index.html", map[string]any{
		"Runners": []runner.RunnerInfo{},
		"Config":  &config.Config{},
		"T":       map[string]string{},
		"Lang":    "en",
		"TJSON":   "{}",
		"Version": "test",
	})
	if err != nil {
		t.Fatalf("空列表渲染失败: %v", err)
	}
	if !strings.Contains(html, "<html") {
		t.Fatal("渲染结果不像一个 HTML 页面")
	}
}
