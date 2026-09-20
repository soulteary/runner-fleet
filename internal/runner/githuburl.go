package runner

import (
	"net/url"
	"strings"

	"github.com/lab-dev/github-actions-runner-manager/internal/config"
)

// githubWebBase 做成变量仅为测试；GitHub Enterprise 另说，本工具目前只对 github.com
var githubWebBase = "https://github.com"

// GitHubSettingsURL 拼出该目标在 GitHub 上的 Actions Runners 设置页地址。
//
// 指向列表页而不是 /runners/<id> 那样的单个 Runner 页：Runner 的数字 id 只在
// 查询 GitHub 时才拿得到，没配 PAT 的部署根本没有它，做成「有时能跳、有时跳不了」
// 不如始终跳到那一页——要找的 Runner 就在上面。
//
// 目标非法时返回空串，由调用方决定不渲染链接。这里不能返回一个半成品 URL：
// 界面上一个点了 404 的链接比没有链接更难判断问题出在哪。
//
// 逐段转义，理由与 githubcheck.runnersEndpoint 相同：ValidateTarget 只管 / 与空值，
// 不限制字符集。前缀是写死的 https://github.com，所以协议不可能被 target 改写——
// 这也是模板敢把它放进 href 的前提。
func GitHubSettingsURL(targetType, target string) string {
	tt := strings.ToLower(strings.TrimSpace(targetType))
	raw := strings.TrimSpace(target)
	if config.ValidateTarget(tt, raw) != nil {
		return ""
	}
	if tt == "org" {
		return githubWebBase + "/organizations/" + url.PathEscape(raw) + "/settings/actions/runners"
	}
	owner, repo, ok := strings.Cut(raw, "/")
	if !ok || owner == "" || repo == "" {
		return ""
	}
	return githubWebBase + "/" + url.PathEscape(owner) + "/" + url.PathEscape(repo) + "/settings/actions/runners"
}
