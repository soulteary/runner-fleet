package main

import (
	"strings"
	"testing"

	"github.com/soulteary/runner-fleet/internal/config"
	"github.com/soulteary/runner-fleet/internal/runner"
)

func rowsData(runners []runner.RunnerInfo, containerMode bool) map[string]any {
	cfg := &config.Config{}
	cfg.Runners.ContainerMode = containerMode
	return map[string]any{
		"Runners": runners,
		"Config":  cfg,
		"T":       map[string]string{},
		"Lang":    "en",
		"TJSON":   "{}",
	}
}

// 自动刷新拿的就是这个片段，它必须能脱离 index.html 单独渲染——
// 否则界面每 15 秒会把 <tbody> 换成一段错误页或空白。
func TestRender_RunnerRowsRendersStandalone(t *testing.T) {
	html, err := renderTo(t, "runnerRows", rowsData([]runner.RunnerInfo{
		{Name: "alpha", Status: runner.StatusInstalled, Target: "acme", TargetType: "org"},
	}, false))
	if err != nil {
		t.Fatalf("片段渲染失败: %v", err)
	}
	if !strings.Contains(html, "alpha") {
		t.Fatalf("片段里没有 runner 名: %s", html)
	}
	// 片段只是 <tbody> 的内容，不该把整页也带出来
	if strings.Contains(html, "<html") || strings.Contains(html, "<table") {
		t.Fatalf("片段不该包含页面骨架: %s", html)
	}
}

// 抽成 {{define}} 之后首屏最容易出的问题是「页面还在，行没了」：
// 模板名写错、数据没往下传，渲染都不报错，只是少一段。
func TestRender_IndexStillEmbedsRunnerRows(t *testing.T) {
	data := rowsData([]runner.RunnerInfo{
		{Name: "beta", Status: runner.StatusInstalled, Target: "acme/app", TargetType: "repo", InstallDir: "/runners/beta"},
	}, false)
	page, err := renderTo(t, "index.html", data)
	if err != nil {
		t.Fatalf("首页渲染失败: %v", err)
	}
	frag, err := renderTo(t, "runnerRows", data)
	if err != nil {
		t.Fatalf("片段渲染失败: %v", err)
	}
	if strings.TrimSpace(frag) == "" {
		t.Fatal("片段渲染为空")
	}
	// 首屏与轮询必须是同一份行渲染逻辑，否则刷新一次界面就会变样
	if !strings.Contains(page, strings.TrimSpace(frag)) {
		t.Fatal("首页里的行与 runnerRows 片段不一致")
	}
	if !strings.Contains(page, `id="runnerRowsBody"`) {
		t.Fatal("首页缺少 runnerRowsBody 容器，JS 无从替换")
	}
}

// 容器模式多一列，空列表那行的 colspan 也跟着变；片段单独渲染时
// Config 要能一路传到底，传丢了这里会退回 6 列。
func TestRender_RunnerRowsHonorsContainerMode(t *testing.T) {
	html, err := renderTo(t, "runnerRows", rowsData(nil, true))
	if err != nil {
		t.Fatalf("片段渲染失败: %v", err)
	}
	if !strings.Contains(html, `colspan="7"`) {
		t.Fatalf("容器模式下空列表应跨 7 列: %s", html)
	}
}
