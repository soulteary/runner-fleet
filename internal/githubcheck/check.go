package githubcheck

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/lab-dev/github-actions-runner-manager/internal/config"
	"github.com/lab-dev/github-actions-runner-manager/internal/runner"
)

const (
	apiBase         = "https://api.github.com"
	apiTimeout      = 30 * time.Second
	apiPerPage      = 100                   // 单页数量，减少漏判（GitHub 默认 30）
	runnerTokenFile = ".github_check_token" // 各 runner 目录下可选文件，内容为用于 List runners API 的 PAT
)

// githubRunnersResponse 与 GitHub API 返回结构一致
type githubRunnersResponse struct {
	TotalCount int `json:"total_count"`
	Runners    []struct {
		ID     int64  `json:"id"`
		Name   string `json:"name"`
		OS     string `json:"os"`
		Status string `json:"status"`
	} `json:"runners"`
}

// Run 根据配置对每个 runner 调用 GitHub API 检查是否已在 GitHub 显示，并写入 .github_status.json
// Token 仅从该 runner 目录下的 .github_check_token 读取，无则跳过该 runner
func Run(cfg *config.Config) {
	if cfg == nil {
		return
	}
	client := &http.Client{Timeout: apiTimeout}
	for _, item := range cfg.Runners.Items {
		installDir := item.InstallPath(cfg.Runners.BasePath)
		token := tokenForRunner(installDir)
		if token == "" {
			continue
		}
		registered := checkOne(client, token, item.TargetType, item.Target, item.Name)
		_ = runner.WriteGitHubStatus(installDir, registered)
	}
}

// tokenForRunner 返回该 runner 用于 GitHub 检查的 token：从 installDir 下的 .github_check_token 读取，不存在或为空则返回空
func tokenForRunner(installDir string) string {
	b, err := os.ReadFile(filepath.Join(installDir, runnerTokenFile))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// isValidTargetFormat 与 config.ValidateTarget 规则一致，避免对无效 target 发起 API 请求
func isValidTargetFormat(targetType, target string) bool {
	tt := strings.ToLower(strings.TrimSpace(targetType))
	return config.ValidateTarget(tt, target) == nil
}

func checkOne(client *http.Client, token, targetType, target, runnerName string) bool {
	raw := strings.TrimSpace(target)
	tt := strings.ToLower(strings.TrimSpace(targetType))
	if !isValidTargetFormat(tt, target) {
		return false
	}
	var path string
	if tt == "org" {
		path = "/orgs/" + raw + "/actions/runners"
	} else {
		// repo
		path = "/repos/" + raw + "/actions/runners"
	}
	url := apiBase + path + "?per_page=" + strconv.Itoa(apiPerPage)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := client.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		if resp != nil {
			_ = resp.Body.Close()
		}
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	var data githubRunnersResponse
	if json.NewDecoder(resp.Body).Decode(&data) != nil {
		return false
	}
	for _, r := range data.Runners {
		if r.Name == runnerName {
			return true
		}
	}
	return false
}
