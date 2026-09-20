// Package githubcheck 通过 GitHub API 查询某个 Runner 是否已在 GitHub 上登记，
// 并在从本工具删除 Runner 时把它从 GitHub 一并注销。
//
// 凭据只来自各 Runner 目录下的 .github_check_token（可选 PAT），没有就跳过——
// 本工具不保管也不要求这个凭据。
package githubcheck

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/lab-dev/github-actions-runner-manager/internal/config"
	"github.com/lab-dev/github-actions-runner-manager/internal/runner"
)

const (
	apiTimeout = 30 * time.Second
	apiPerPage = 100 // 单页数量，GitHub 允许的上限
	// maxPages 兜底，防止 API 行为异常时无限翻页；100*50 = 5000 个 Runner，远超实际
	maxPages = 50
	// RunnerTokenFile 为各 runner 目录下可选的 PAT 文件，用于 List / Delete runners API
	RunnerTokenFile = ".github_check_token"
)

// apiBase 做成变量仅为测试可指向 httptest
var apiBase = "https://api.github.com"

// ghRunner 与 GitHub API 返回结构一致
type ghRunner struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	OS     string `json:"os"`
	Status string `json:"status"`
}

type ghRunnersResponse struct {
	TotalCount int        `json:"total_count"`
	Runners    []ghRunner `json:"runners"`
}

// Run 对每个配置了 PAT 的 runner 查询它是否已在 GitHub 登记，结果写入 .github_status.json。
//
// 查不到答案（令牌过期、限流、网络不通）与「确实没登记」是两回事，分别写入：
// 前者记为未知并附上原因，后者才记为 false。把前者当成后者会在界面上给出
// 斩钉截铁的「未显示」，让人去排查一个并不存在的注册问题。
func Run(cfg *config.Config) {
	if cfg == nil {
		return
	}
	client := &http.Client{Timeout: apiTimeout}
	for _, item := range cfg.Runners.Items {
		installDir := item.InstallPath(cfg.Runners.BasePath)
		token := TokenForRunner(installDir)
		if token == "" {
			continue
		}
		registered, reason := checkOne(client, token, item.TargetType, item.Target, item.Name)
		_ = runner.WriteGitHubStatus(installDir, registered, reason)
	}
}

// TokenForRunner 读取该 runner 目录下的 PAT，不存在或为空则返回空串
func TokenForRunner(installDir string) string {
	b, err := os.ReadFile(filepath.Join(installDir, RunnerTokenFile))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// checkOne 返回该 runner 是否已在 GitHub 登记；无法判定时返回 nil 与原因说明
func checkOne(client *http.Client, token, targetType, target, runnerName string) (*bool, string) {
	runners, err := listRunners(context.Background(), client, token, targetType, target)
	if err != nil {
		return nil, err.Error()
	}
	found := false
	for _, r := range runners {
		if r.Name == runnerName {
			found = true
			break
		}
	}
	return &found, ""
}

// listRunners 列出目标下的全部 Runner，自动翻页。
//
// 必须翻页：只取第一页时，一个 Runner 数超过 per_page 的组织里，排在后面的
// Runner 会被判成「没登记」——正是本包要避免的那种假否定。
func listRunners(ctx context.Context, client *http.Client, token, targetType, target string) ([]ghRunner, error) {
	tt := strings.ToLower(strings.TrimSpace(targetType))
	if err := config.ValidateTarget(tt, target); err != nil {
		return nil, fmt.Errorf("target 无效: %w", err)
	}
	base, err := runnersEndpoint(tt, target)
	if err != nil {
		return nil, err
	}
	var all []ghRunner
	for page := 1; page <= maxPages; page++ {
		u := fmt.Sprintf("%s?per_page=%d&page=%d", base, apiPerPage, page)
		var data ghRunnersResponse
		if err := doJSON(ctx, client, http.MethodGet, u, token, &data); err != nil {
			return nil, err
		}
		all = append(all, data.Runners...)
		// 最后一页：本页不满，或已取满 total_count
		if len(data.Runners) < apiPerPage || (data.TotalCount > 0 && len(all) >= data.TotalCount) {
			return all, nil
		}
	}
	return all, fmt.Errorf("目标下的 Runner 超过 %d 个，未能全部列出", maxPages*apiPerPage)
}

// runnersEndpoint 拼出 actions/runners 的 API 地址。
// 路径段逐个转义：ValidateTarget 只管 / 与空值，没有限制字符集，
// 未转义地拼进 URL 会让 `myorg?x=1` 这样的 target 打到另一个端点上去。
func runnersEndpoint(targetType, target string) (string, error) {
	raw := strings.TrimSpace(target)
	if targetType == "org" {
		return apiBase + "/orgs/" + url.PathEscape(raw) + "/actions/runners", nil
	}
	owner, repo, ok := strings.Cut(raw, "/")
	if !ok {
		return "", fmt.Errorf("target 应为 owner/repo 格式")
	}
	return apiBase + "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo) + "/actions/runners", nil
}

// doJSON 发一个带鉴权的请求；out 为 nil 时不解析响应体（用于 DELETE）
func doJSON(ctx context.Context, client *http.Client, method, u, token string, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, u, nil)
	if err != nil {
		return fmt.Errorf("构造请求失败: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("请求 GitHub API 失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNoContent {
		return nil
	}
	if resp.StatusCode != http.StatusOK {
		return httpError(resp)
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("解析 GitHub API 响应失败: %w", err)
	}
	return nil
}

// httpError 把状态码翻译成能照着做的说明。
// 只说「HTTP 403」没有用：限流与权限不足的处理方式完全不同。
func httpError(resp *http.Response) error {
	switch resp.StatusCode {
	case http.StatusUnauthorized:
		return fmt.Errorf("GitHub 返回 401：%s 里的令牌无效或已过期", RunnerTokenFile)
	case http.StatusForbidden:
		if resp.Header.Get("X-RateLimit-Remaining") == "0" {
			reset := resp.Header.Get("X-RateLimit-Reset")
			if ts, err := strconv.ParseInt(reset, 10, 64); err == nil {
				return fmt.Errorf("GitHub API 限流，%s 后恢复", time.Until(time.Unix(ts, 0)).Round(time.Second))
			}
			return fmt.Errorf("GitHub API 限流，请稍后再试")
		}
		return fmt.Errorf("GitHub 返回 403：令牌权限不足（组织需 admin:org，仓库需 repo）")
	case http.StatusNotFound:
		return fmt.Errorf("GitHub 返回 404：目标不存在，或令牌看不到它")
	default:
		return fmt.Errorf("GitHub 返回 %d", resp.StatusCode)
	}
}

// DeregisterResult 说明一次注销尝试的结果，供调用方原样展示给用户
type DeregisterResult struct {
	// Done 为 true 表示 GitHub 上已经没有这个 Runner 了（本次删除的，或本来就没有）
	Done bool
	// Message 面向用户：Done 为 false 时说明为什么没删成、该去哪儿手动删
	Message string
}

// Deregister 把某个 Runner 从 GitHub 注销。
//
// 用该 Runner 目录下的 PAT 调 DELETE .../actions/runners/{id}。没有 PAT 就注销不了：
// GitHub 侧的删除需要 PAT 或一个新的 removal token，而 config.sh remove 也要 removal token，
// 二者本工具都拿不到。这种情况下如实说明，让人知道 GitHub 上还留着一个。
//
// 必须在删除安装目录之前调用——令牌就放在那个目录里。
func Deregister(ctx context.Context, installDir, targetType, target, runnerName string) DeregisterResult {
	token := TokenForRunner(installDir)
	if token == "" {
		return DeregisterResult{
			Message: fmt.Sprintf("GitHub 上的同名 Runner 未被删除：该 Runner 目录下没有 %s（可选 PAT），"+
				"本工具无从调用删除接口。请到目标仓库或组织的 Settings → Actions → Runners 手动删除 %q，"+
				"否则之后用同一名称重新添加会因重名而注册失败", RunnerTokenFile, runnerName),
		}
	}
	client := &http.Client{Timeout: apiTimeout}
	runners, err := listRunners(ctx, client, token, targetType, target)
	if err != nil {
		return DeregisterResult{
			Message: fmt.Sprintf("GitHub 上的同名 Runner 未被删除：%v。"+
				"请到 Settings → Actions → Runners 确认并手动删除 %q", err, runnerName),
		}
	}
	var id int64
	for _, r := range runners {
		if r.Name == runnerName {
			id = r.ID
			break
		}
	}
	if id == 0 {
		return DeregisterResult{Done: true, Message: "GitHub 上没有同名 Runner，无需注销"}
	}
	base, err := runnersEndpoint(strings.ToLower(strings.TrimSpace(targetType)), target)
	if err != nil {
		return DeregisterResult{Message: fmt.Sprintf("GitHub 上的同名 Runner 未被删除：%v", err)}
	}
	if err := doJSON(ctx, client, http.MethodDelete, base+"/"+strconv.FormatInt(id, 10), token, nil); err != nil {
		// 404 说明它已经不在了，目的已经达到
		if strings.Contains(err.Error(), "404") {
			return DeregisterResult{Done: true, Message: "GitHub 上没有同名 Runner，无需注销"}
		}
		return DeregisterResult{
			Message: fmt.Sprintf("GitHub 上的同名 Runner 未被删除：%v。"+
				"请到 Settings → Actions → Runners 手动删除 %q", err, runnerName),
		}
	}
	return DeregisterResult{Done: true, Message: "已从 GitHub 注销该 Runner"}
}
