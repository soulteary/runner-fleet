package main

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// 原生 alert/confirm 会阻塞整个页面，且可能被浏览器的「阻止此页面再次弹窗」
// 一勾永久禁掉——那之后删除、重建这类需要确认的操作会静默地什么都不做。
// 界面统一走 showToast / confirmDialog，这里守住不被改回去。
func TestTemplate_NoNativeDialogs(t *testing.T) {
	b, err := templateFS.ReadFile("templates/index.html")
	if err != nil {
		t.Fatal(err)
	}
	// confirmDialog( 不会命中：confirm 后面跟的是 D 而不是左括号
	re := regexp.MustCompile(`\b(alert|confirm|prompt)\s*\(`)
	var hits []string
	for i, line := range strings.Split(string(b), "\n") {
		if m := re.FindString(line); m != "" {
			hits = append(hits, strconv.Itoa(i+1)+": "+strings.TrimSpace(line))
		}
	}
	if len(hits) > 0 {
		t.Fatalf("模板里出现原生弹窗，请改用 showToast / confirmDialog:\n%s", strings.Join(hits, "\n"))
	}
}

// 请求在途时按钮必须置灰，否则启动一台 Runner 那几十秒里用户会连点，
// 同一个动作被发两次。这里只能守住代码里确实调了 setRowBusy。
func TestTemplate_ActionsMarkButtonsBusy(t *testing.T) {
	b, err := templateFS.ReadFile("templates/index.html")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	for _, want := range []string{
		"function setRowBusy(",
		"async function runnerAction(name, action, btn)",
		"setRowBusy(btn, true)",
		"setRowBusy(btn, false)",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("模板里找不到 %q，按钮忙碌态可能被改掉了", want)
		}
	}
}
