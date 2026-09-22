// Package githubcheck 通过 GitHub API 查询某个 Runner 是否已在 GitHub 上登记，
// 并在从本工具删除 Runner 时把它从 GitHub 一并注销。
//
// 凭据只来自 Manager 侧的凭据目录（config/tokens/<runner name>，可选 PAT），
// 没有就跳过——本工具不保管也不要求这个凭据。
//
// 凭据不放在 Runner 目录下：容器模式会把那个目录整个挂进 Runner 容器，Job 读得到。
// 详见 internal/secrets。留在旧位置的 PAT 由 secrets.Store 在每次读取前搬走。
package githubcheck

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/soulteary/runner-fleet/internal/config"
	"github.com/soulteary/runner-fleet/internal/runner"
	"github.com/soulteary/runner-fleet/internal/secrets"
)

const (
	apiTimeout = 30 * time.Second
	apiPerPage = 100 // 单页数量，GitHub 允许的上限
	// maxPages 兜底，防止 API 行为异常时无限翻页；100*50 = 5000 个 Runner，远超实际
	maxPages = 50
)

// tokenRef 一个 PAT 连同它的来源路径。
//
// 路径只用来把错误说清楚：401 的时候用户要知道该去改哪个文件，而这个位置
// 随 -config 而变，不是一个能写死在消息里的常量。
type tokenRef struct {
	value string
	path  string
}

// apiBase 做成变量仅为测试可指向 httptest
var apiBase = "https://api.github.com"

// ghRunner 与 GitHub API 返回结构一致
type ghRunner struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	OS   string `json:"os"`
	// Status 为 online / offline；Busy 为 true 表示 GitHub 正在往它派发的 Job 里跑东西
	Status string `json:"status"`
	Busy   bool   `json:"busy"`
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
func Run(cfg *config.Config, store *secrets.Store) {
	if cfg == nil || store == nil {
		return
	}
	client := &http.Client{Timeout: apiTimeout}
	for _, item := range cfg.Runners.Items {
		installDir := item.InstallPath(cfg.Runners.BasePath)
		tok := tokenFor(store, item.Name, installDir)
		if tok.value == "" {
			continue
		}
		registered, busy, reason := checkOne(client, tok, item.TargetType, item.Target, item.Name)
		_ = runner.WriteGitHubStatus(installDir, registered, busy, reason)
	}
}

// TokenForRunner 读取该 Runner 的 PAT，不存在或为空则返回空串。
//
// 每次读取前先迁移一次，所以用户照旧文档把 PAT 放进 Runner 目录的，最迟一个
// 检查周期（约 5 分钟）后就会被移出那个会挂进容器的目录。
func TokenForRunner(store *secrets.Store, runnerName, installDir string) string {
	return tokenFor(store, runnerName, installDir).value
}

// tokenFor 同 TokenForRunner，另外带回该 PAT 应在的路径供错误消息使用。
// path 一定非空：消息里总要能指出一个位置。
func tokenFor(store *secrets.Store, runnerName, installDir string) tokenRef {
	ref := tokenRef{path: secrets.PathDescription}
	if store == nil {
		return ref
	}
	// 迁移失败时不回落去读旧位置：那正是本次要关掉的暴露。这时可见性检查会
	// 停在「没有凭据」上，所以必须说出来，否则用户只看到检查无声地不再更新。
	if _, err := store.Migrate(runnerName, installDir); err != nil {
		log.Printf("warning: cannot move the PAT for runner %s out of its runner directory: %v", runnerName, err)
	}
	if p, err := store.Path(runnerName); err == nil {
		ref.path = p
	}
	ref.value = store.GitHubToken(runnerName)
	return ref
}

// checkOne 返回该 runner 是否已在 GitHub 登记、以及它是否正在跑 Job；
// 无法判定时 registered 为 nil 并带上原因说明。
//
// busy 只在「确实找到了这个 Runner」时才有值。没找到、或压根没查成，
// busy 都是 nil——那是「不知道」，不是「不忙」。把前者写成 false，
// 界面上就会对一个根本没查到的 Runner 打包票说它空闲。
func checkOne(client *http.Client, tok tokenRef, targetType, target, runnerName string) (registered, busy *bool, reason string) {
	runners, err := listRunners(context.Background(), client, tok, targetType, target)
	if err != nil {
		return nil, nil, err.Error()
	}
	found := false
	for _, r := range runners {
		if r.Name == runnerName {
			found = true
			b := r.Busy
			busy = &b
			break
		}
	}
	return &found, busy, ""
}

// listRunners 列出目标下的全部 Runner，自动翻页。
//
// 必须翻页：只取第一页时，一个 Runner 数超过 per_page 的组织里，排在后面的
// Runner 会被判成「没登记」——正是本包要避免的那种假否定。
func listRunners(ctx context.Context, client *http.Client, tok tokenRef, targetType, target string) ([]ghRunner, error) {
	tt := strings.ToLower(strings.TrimSpace(targetType))
	if err := config.ValidateTarget(tt, target); err != nil {
		return nil, fmt.Errorf("invalid target: %w", err)
	}
	base, err := runnersEndpoint(tt, target)
	if err != nil {
		return nil, err
	}
	var all []ghRunner
	for page := 1; page <= maxPages; page++ {
		u := fmt.Sprintf("%s?per_page=%d&page=%d", base, apiPerPage, page)
		var data ghRunnersResponse
		if err := doJSON(ctx, client, http.MethodGet, u, tok, &data); err != nil {
			return nil, err
		}
		all = append(all, data.Runners...)
		// 最后一页：本页不满，或已取满 total_count
		if len(data.Runners) < apiPerPage || (data.TotalCount > 0 && len(all) >= data.TotalCount) {
			return all, nil
		}
	}
	return all, fmt.Errorf("the target has more than %d runners; the list is incomplete", maxPages*apiPerPage)
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
		return "", fmt.Errorf("target must be in owner/repo form")
	}
	return apiBase + "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo) + "/actions/runners", nil
}

// doJSON 发一个带鉴权的请求；out 为 nil 时不解析响应体（用于 DELETE）
func doJSON(ctx context.Context, client *http.Client, method, u string, tok tokenRef, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, u, nil)
	if err != nil {
		return fmt.Errorf("could not build the request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+tok.value)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("the GitHub API request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNoContent {
		return nil
	}
	if resp.StatusCode != http.StatusOK {
		return httpError(resp, tok)
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("could not parse the GitHub API response: %w", err)
	}
	return nil
}

// httpError 把状态码翻译成能照着做的说明。
// 只说「HTTP 403」没有用：限流与权限不足的处理方式完全不同。
func httpError(resp *http.Response, tok tokenRef) error {
	switch resp.StatusCode {
	case http.StatusUnauthorized:
		return fmt.Errorf("GitHub returned 401: the token in %s is invalid or expired", tok.path)
	case http.StatusForbidden:
		if resp.Header.Get("X-RateLimit-Remaining") == "0" {
			reset := resp.Header.Get("X-RateLimit-Reset")
			if ts, err := strconv.ParseInt(reset, 10, 64); err == nil {
				return fmt.Errorf("GitHub API rate limit reached, resets in %s", time.Until(time.Unix(ts, 0)).Round(time.Second))
			}
			return fmt.Errorf("GitHub API rate limit reached, try again shortly")
		}
		return fmt.Errorf("GitHub returned 403: the token lacks scope (admin:org for an organization, repo for a repository)")
	case http.StatusNotFound:
		return fmt.Errorf("GitHub returned 404: the target does not exist, or the token cannot see it")
	default:
		return fmt.Errorf("GitHub returned %d", resp.StatusCode)
	}
}

// DeregisterResult 说明一次注销尝试的结果。
type DeregisterResult struct {
	// Done 为 true 表示 GitHub 上已经没有这个 Runner 了（本次删除的，或本来就没有）
	Done bool

	// MessageKey / MessageArgs 描述结果，由调用方渲染。
	//
	// 这里刻意不给现成的句子。同一个结果有两个去向、两种语言要求：它既要拼进 API
	// 响应（跟随请求语言），又要写进日志（固定英文，见 handler 的 i18n 说明）。
	// 一个 string 字段服务不了两边——本包又够不着翻译表，因为那是 handler 那一侧
	// 由 main 注入的。所以这里只回传「是什么事」，由拿得到语言的人去说成话。
	MessageKey  string
	MessageArgs []any
}

// Deregister 把某个 Runner 从 GitHub 注销。
//
// 用该 Runner 的 PAT 调 DELETE .../actions/runners/{id}。没有 PAT 就注销不了：
// GitHub 侧的删除需要 PAT 或一个新的 removal token，而 config.sh remove 也要 removal token，
// 二者本工具都拿不到。这种情况下如实说明，让人知道 GitHub 上还留着一个。
//
// 仍然要在删除安装目录之前调用：令牌本身已经不在那里了，但照旧文档放进去的那份
// 要先被搬出来才能用上，而搬运的源头就是那个目录。
func Deregister(ctx context.Context, store *secrets.Store, installDir, targetType, target, runnerName string) DeregisterResult {
	tok := tokenFor(store, runnerName, installDir)
	if tok.value == "" {
		return DeregisterResult{
			MessageKey:  "github.dereg.no_pat",
			MessageArgs: []any{tok.path, runnerName},
		}
	}
	client := &http.Client{Timeout: apiTimeout}
	runners, err := listRunners(ctx, client, tok, targetType, target)
	if err != nil {
		return DeregisterResult{
			MessageKey:  "github.dereg.lookup_failed",
			MessageArgs: []any{err, runnerName},
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
		return DeregisterResult{Done: true, MessageKey: "github.dereg.not_listed"}
	}
	base, err := runnersEndpoint(strings.ToLower(strings.TrimSpace(targetType)), target)
	if err != nil {
		return DeregisterResult{MessageKey: "github.dereg.endpoint_failed", MessageArgs: []any{err}}
	}
	if err := doJSON(ctx, client, http.MethodDelete, base+"/"+strconv.FormatInt(id, 10), tok, nil); err != nil {
		// 404 说明它已经不在了，目的已经达到
		if strings.Contains(err.Error(), "404") {
			return DeregisterResult{Done: true, MessageKey: "github.dereg.not_listed"}
		}
		return DeregisterResult{
			MessageKey:  "github.dereg.delete_failed",
			MessageArgs: []any{err, runnerName},
		}
	}
	return DeregisterResult{Done: true, MessageKey: "github.dereg.done"}
}
