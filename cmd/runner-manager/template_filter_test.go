package main

import (
	"regexp"
	"strings"
	"testing"

	"github.com/soulteary/runner-fleet/internal/config"
	"github.com/soulteary/runner-fleet/internal/runner"
)

func filterPageData(runners []runner.RunnerInfo) map[string]any {
	return map[string]any{
		"Runners": runners,
		"Config":  &config.Config{},
		"T":       map[string]string{},
		"Lang":    "en",
		"TJSON":   "{}",
	}
}

// 筛选与计数读的是行上的 data-* 属性，而不是单元格里的可见文本——
// 那些文本按语言翻译过，换一种语言筛选就会失灵。属性少一个，
// 对应的筹码会静默地永远计 0，不会有任何报错。
func TestTemplate_RowsCarryFilterAttributes(t *testing.T) {
	drift := "image: a -> b"
	html, err := renderTo(t, "index.html", filterPageData([]runner.RunnerInfo{{
		Name:           "alpha",
		Status:         runner.StatusInstalled,
		TargetType:     "repo",
		Target:         "acme/app",
		Running:        true,
		ContainerDrift: drift,
		Probe:          &runner.ProbeInfo{Error: "boom"},
	}}))
	if err != nil {
		t.Fatalf("渲染失败: %v", err)
	}
	for _, want := range []string{
		`data-name="alpha"`,
		`data-target="repo: acme/app"`,
		`data-status="installed"`,
		`data-running="1"`,
		`data-drift="1"`,
		`data-probe="1"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("行上缺少筛选属性 %s", want)
		}
	}
}

// 三个布尔属性都必须落成 "0" 而不是空串：JS 判的是 === '1'，
// 但属性整个消失和值为 0 在模板里是两种写法，容易只改对一半。
func TestTemplate_RowBooleanAttributesAreZeroWhenFalse(t *testing.T) {
	html, err := renderTo(t, "index.html", filterPageData([]runner.RunnerInfo{{
		Name:       "beta",
		Status:     runner.StatusNew,
		TargetType: "org",
		Target:     "acme",
	}}))
	if err != nil {
		t.Fatalf("渲染失败: %v", err)
	}
	for _, want := range []string{`data-running="0"`, `data-drift="0"`, `data-probe="0"`} {
		if !strings.Contains(html, want) {
			t.Errorf("布尔属性未落成 0: %s", want)
		}
	}
}

// 空筛选结果那行的列数要和表头一致，否则筛没了的时候表格会错位
func TestTemplate_NoMatchRowSpansAllColumns(t *testing.T) {
	data := filterPageData(nil)
	cfg := &config.Config{}
	cfg.Runners.ContainerMode = true
	data["Config"] = cfg
	html, err := renderTo(t, "index.html", data)
	if err != nil {
		t.Fatalf("渲染失败: %v", err)
	}
	idx := strings.Index(html, `id="noMatchRow"`)
	if idx < 0 {
		t.Fatal("找不到 noMatchRow")
	}
	if !strings.Contains(html[idx:idx+200], `colspan="7"`) {
		t.Errorf("容器模式下空筛选行应跨 7 列: %s", html[idx:idx+200])
	}
}

// btn-view / btn-del 这些类名不只是样式，同时是行内操作的行为选择器：
// 界面靠它们给按钮绑启停、查看、删除。纯粹为了取样式而套上其中一个，
// 就会被当成行操作绑上监听——筛选栏的「清除」按钮正是这么误开过查看弹窗的
// （它没有 data-name，于是去请求 /api/runners/null，弹窗显示「加载失败」）。
// 只取样式的小按钮请用 .btn-neutral。
func TestTemplate_RowActionClassesAlwaysCarryName(t *testing.T) {
	b, err := templateFS.ReadFile("templates/index.html")
	if err != nil {
		t.Fatal(err)
	}
	rowAction := map[string]bool{
		"btn-view": true, "btn-edit": true, "btn-del": true,
		"btn-start": true, "btn-stop": true, "btn-recreate": true,
	}
	// 弹窗页脚那两个按钮的 data-name 是打开弹窗时用 JS 设上去的
	allowed := map[string]bool{"modalStartBtnFooter": true, "modalStopBtnFooter": true}
	classRe := regexp.MustCompile(`class="([^"]*)"`)
	idRe := regexp.MustCompile(`id="([^"]+)"`)
	for i, line := range strings.Split(string(b), "\n") {
		if !strings.Contains(line, "<button") {
			continue
		}
		m := classRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		// 按完整 token 比对：CSS 类选择器就是这么匹配的，
		// btn-edit-primary 不会被 .btn-edit 选中，不该误判
		hit := ""
		for _, cls := range strings.Fields(m[1]) {
			if rowAction[cls] {
				hit = cls
				break
			}
		}
		if hit == "" || strings.Contains(line, "data-name=") {
			continue
		}
		if id := idRe.FindStringSubmatch(line); id != nil && allowed[id[1]] {
			continue
		}
		t.Errorf("第 %d 行的按钮带着行操作类名 %s 却没有 data-name，只取样式请改用 .btn-neutral:\n  %s",
			i+1, hit, strings.TrimSpace(line))
	}
}
