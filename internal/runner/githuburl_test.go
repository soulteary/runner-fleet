package runner

import (
	"net/url"
	"strings"
	"testing"

	"github.com/lab-dev/github-actions-runner-manager/internal/config"
)

func TestGitHubSettingsURL_TargetShapes(t *testing.T) {
	cases := []struct {
		name       string
		targetType string
		target     string
		want       string
	}{
		{"仓库", "repo", "soulteary/runner-fleet",
			"https://github.com/soulteary/runner-fleet/settings/actions/runners"},
		{"组织", "org", "my-org",
			"https://github.com/organizations/my-org/settings/actions/runners"},
		// 组织与仓库的设置页路径不同，弄反了就是 404
		{"大小写与空格不影响", "  REPO  ", "  soulteary/runner-fleet  ",
			"https://github.com/soulteary/runner-fleet/settings/actions/runners"},

		// 拼不出可信地址时宁可不给链接：点了 404 的链接比没有链接更难排查
		{"空目标", "repo", "", ""},
		{"仓库缺少 owner", "repo", "runner-fleet", ""},
		{"仓库 owner 为空", "repo", "/runner-fleet", ""},
		{"组织名里有斜杠", "org", "a/b", ""},
		{"未知目标类型", "user", "someone", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := GitHubSettingsURL(tc.targetType, tc.target); got != tc.want {
				t.Fatalf("GitHubSettingsURL(%q, %q) = %q，期望 %q",
					tc.targetType, tc.target, got, tc.want)
			}
		})
	}
}

// 这个地址要放进 href。target 是用户填的，所以必须证明它改不了协议，
// 也拼不出第二个属性——否则列表页就多了一个注入点。
//
// 真正兜底的是写死的 https://github.com 前缀：无论 target 是什么，
// 它都只能出现在路径段里，且逐段转义。
func TestGitHubSettingsURL_TargetCannotEscapeTheURL(t *testing.T) {
	hostile := []struct {
		targetType string
		target     string
	}{
		{"org", `javascript:alert(1)`},
		{"org", `my-org" onmouseover="alert(1)`},
		{"org", `my-org?x=1`},
		{"org", `my-org#frag`},
		{"org", `my-org\..\..\etc`},
		{"repo", `o/r?x=1`},
		{"repo", `o/r" onclick="alert(1)`},
		{"repo", `o/r#x`},
	}
	for _, h := range hostile {
		got := GitHubSettingsURL(h.targetType, h.target)
		if got == "" {
			continue // 被判为非法而不生成链接，同样是安全的结果
		}
		if !strings.HasPrefix(got, "https://github.com/") {
			t.Fatalf("target %q 生成了非 github.com 地址: %q", h.target, got)
		}
		// 引号、尖括号会让它从 href 属性里逃出去；? 与 # 会改变路径含义
		for _, bad := range []string{`"`, `'`, `<`, `>`, ` `, `?`, `#`} {
			if strings.Contains(strings.TrimPrefix(got, "https://github.com/"), bad) {
				t.Fatalf("target %q 生成的地址里残留了未转义的 %q: %q", h.target, bad, got)
			}
		}
		u, err := url.Parse(got)
		if err != nil {
			t.Fatalf("target %q 生成的地址无法解析: %v", h.target, err)
		}
		if u.Scheme != "https" || u.Host != "github.com" {
			t.Fatalf("target %q 生成了 %s://%s", h.target, u.Scheme, u.Host)
		}
		if !strings.HasSuffix(u.EscapedPath(), "/settings/actions/runners") {
			t.Fatalf("target %q 生成的路径不是 Runners 设置页: %q", h.target, u.EscapedPath())
		}
	}
}

// List/GetByName 都要把地址填上，否则界面上只有一半的行能跳转
func TestGitHubSettingsURL_IsSetOnListedRunners(t *testing.T) {
	cfg := &config.Config{Runners: config.RunnersConfig{
		BasePath: t.TempDir(),
		Items:    []config.RunnerItem{{Name: "alpha", TargetType: "repo", Target: "o/r"}},
	}}
	list := List(cfg)
	if len(list) != 1 {
		t.Fatalf("应列出 1 个 runner，得到 %d", len(list))
	}
	want := "https://github.com/o/r/settings/actions/runners"
	if list[0].GitHubURL != want {
		t.Fatalf("List 里的 GitHubURL = %q，期望 %q", list[0].GitHubURL, want)
	}
	got := GetByName(cfg, "alpha")
	if got == nil {
		t.Fatal("GetByName 应找到 alpha")
	}
	if got.GitHubURL != want {
		t.Fatalf("GetByName 里的 GitHubURL = %q，期望 %q", got.GitHubURL, want)
	}
}
