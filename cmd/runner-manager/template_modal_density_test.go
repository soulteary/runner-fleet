package main

import (
	"regexp"
	"strings"
	"testing"
)

// PROBE_ROW_IDS 里写错一个 id 不会有任何报错，只是那一行在健康的 Runner 上
// 继续显示「—」——正是这次要去掉的东西。这里把 JS 里的名单和模板里的
// id 对起来。
func TestTemplate_ProbeRowIDsMatchMarkup(t *testing.T) {
	b, err := templateFS.ReadFile("templates/index.html")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)

	listRe := regexp.MustCompile(`const PROBE_ROW_IDS = \[([^\]]+)\]`)
	m := listRe.FindStringSubmatch(src)
	if m == nil {
		t.Fatal("找不到 PROBE_ROW_IDS")
	}
	ids := regexp.MustCompile(`'([^']+)'`).FindAllStringSubmatch(m[1], -1)
	if len(ids) != 5 {
		t.Fatalf("PROBE_ROW_IDS 应有 5 项，实际 %d 项", len(ids))
	}
	for _, id := range ids {
		if !strings.Contains(src, `id="`+id[1]+`"`) {
			t.Errorf("PROBE_ROW_IDS 里的 %s 在模板里没有对应的行", id[1])
		}
	}

	// 反过来：模板里带 vProbe 前缀的 ...Row 都该在名单里，
	// 以后新增一行探测字段时不会被悄悄漏掉
	rowRe := regexp.MustCompile(`id="(vProbe\w*Row)"`)
	inList := map[string]bool{}
	for _, id := range ids {
		inList[id[1]] = true
	}
	for _, r := range rowRe.FindAllStringSubmatch(src, -1) {
		if !inList[r[1]] {
			t.Errorf("模板里的 %s 不在 PROBE_ROW_IDS 中，无探测错误时它会继续显示「—」", r[1])
		}
	}
}

// 两处时间戳都必须走 renderTimestamp，漏掉哪个那一栏就退回裸 RFC3339
func TestTemplate_TimestampsUseRelativeRenderer(t *testing.T) {
	b, err := templateFS.ReadFile("templates/index.html")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	for _, id := range []string{"vRegistrationCheckedAt", "vGitHubCheckAt"} {
		if !strings.Contains(src, "renderTimestamp(document.getElementById('"+id+"')") {
			t.Errorf("%s 没有走 renderTimestamp", id)
		}
	}
	// 相对时间交给 Intl，不另铺一套六语言文案；拿不到时要能回落
	if !strings.Contains(src, "Intl.RelativeTimeFormat") {
		t.Error("相对时间应使用 Intl.RelativeTimeFormat")
	}
	if !strings.Contains(src, "if (!rel) { el.textContent = iso; return; }") {
		t.Error("Intl 不可用或时间解析失败时应原样显示，不能把信息弄丢")
	}
}
