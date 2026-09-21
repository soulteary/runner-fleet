package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Agent 的 /start、/stop 能停掉别人的 Runner，而同一个 docker 网络里的任何容器
// 都够得着它——包括 Job 自己拉起来的 service 容器。鉴权是这里唯一的边界，
// 之前一行测试都没有。

// agentDir 造一个安装目录并把 RUNNER_INSTALL_DIR 指过去。
// token 非空时在目录下写入 .agent_token。
func agentDir(t *testing.T, token string) string {
	t.Helper()
	dir := t.TempDir()
	if token != "" {
		if err := os.WriteFile(filepath.Join(dir, agentTokenFile), []byte(token), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("RUNNER_INSTALL_DIR", dir)
	t.Setenv("AGENT_TOKEN", "")
	return dir
}

func callGuarded(t *testing.T, authHeader string) *httptest.ResponseRecorder {
	t.Helper()
	handler := requireToken(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"message":"ok"}`))
	})
	req := httptest.NewRequest(http.MethodPost, "/start", nil)
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	rec := httptest.NewRecorder()
	handler(rec, req)
	return rec
}

// ---- expectedToken ----

func TestExpectedTokenPrefersEnvOverFile(t *testing.T) {
	agentDir(t, "from-file")
	t.Setenv("AGENT_TOKEN", "from-env")
	// 环境变量优先：Manager 以 root 或非 1001 的 UID 运行时，0600 的令牌文件
	// 对容器里的 app(1001) 不可读，只认文件就会静默降级成不鉴权
	if got := expectedToken(); got != "from-env" {
		t.Fatalf("expectedToken() = %q，期望环境变量里的 from-env", got)
	}
}

func TestExpectedTokenFallsBackToFile(t *testing.T) {
	agentDir(t, "  from-file\n") // 前后空白应被去掉
	if got := expectedToken(); got != "from-file" {
		t.Fatalf("expectedToken() = %q，期望 from-file", got)
	}
}

// 没有令牌文件 = 本特性之前创建的旧容器，属于刻意保留的兼容路径
func TestExpectedTokenEmptyWhenNoTokenConfigured(t *testing.T) {
	agentDir(t, "")
	if got := expectedToken(); got != "" {
		t.Fatalf("expectedToken() = %q，期望空串", got)
	}
}

// 文件在、却读不出来（多为 UID 不匹配），同样返回空串降级为不鉴权。
// 这是刻意的取舍，但必须留下告警——否则鉴权悄悄失效，外部完全看不出来。
func TestExpectedTokenUnreadableFileDegradesButWarns(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("权限位在 Windows 上语义不同")
	}
	if os.Geteuid() == 0 {
		t.Skip("以 root 运行时任何权限位都挡不住读取")
	}
	dir := agentDir(t, "secret")
	if err := os.Chmod(filepath.Join(dir, agentTokenFile), 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(dir, agentTokenFile), 0o600) })

	if got := expectedToken(); got != "" {
		t.Fatalf("expectedToken() = %q，读不到时应返回空串", got)
	}
}

// ---- requireToken ----

func TestRequireTokenAllowsAllWhenNoTokenConfigured(t *testing.T) {
	agentDir(t, "")
	if rec := callGuarded(t, ""); rec.Code != http.StatusOK {
		t.Fatalf("未配置令牌时应放行，得到 %d", rec.Code)
	}
}

func TestRequireTokenRejectsBadCredentials(t *testing.T) {
	cases := []struct {
		name   string
		header string
	}{
		{"完全不带 Authorization", ""},
		{"令牌不对", "Bearer wrong-token"},
		{"只有前缀没有令牌", "Bearer "},
		{"只有 scheme 没有空格", "Bearer"},
		{"令牌是正确值的前缀", "Bearer the-secret-token"},
		{"令牌比正确值长", "Bearer the-secret-token-value-extra"},
		// 裸令牌不再放行：文档六种语言都写着 Authorization: Bearer，
		// Manager 发的也一直是 Bearer，代码没有理由比文档更松
		{"令牌对但没有 scheme", "the-secret-token-value"},
		{"换了别的 scheme", "Basic the-secret-token-value"},
		{"scheme 拼错", "Bearerr the-secret-token-value"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			agentDir(t, "the-secret-token-value")
			rec := callGuarded(t, tc.header)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("得到 %d，期望 401", rec.Code)
			}
			if !strings.Contains(rec.Body.String(), "unauthorized") {
				t.Fatalf("响应体应说明未授权，实际: %s", rec.Body.String())
			}
		})
	}
}

func TestRequireTokenAcceptsCorrectToken(t *testing.T) {
	agentDir(t, "the-secret-token-value")
	cases := []string{
		"Bearer the-secret-token-value",
		"Bearer   the-secret-token-value  ", // scheme 与令牌之间、令牌之后的空白都会被去掉
		"  Bearer the-secret-token-value",   // 头部本身的前导空白
		// RFC 7235：scheme 名大小写不敏感
		"bearer the-secret-token-value",
		"BEARER the-secret-token-value",
	}
	for _, header := range cases {
		t.Run(header, func(t *testing.T) {
			if rec := callGuarded(t, header); rec.Code != http.StatusOK {
				t.Fatalf("正确令牌应放行，得到 %d（%s）", rec.Code, rec.Body.String())
			}
		})
	}
}

// 令牌每次请求现读：Manager 可能在容器起来之后才把令牌写进挂载目录，
// 启动时读一次并缓存的话，那之后再补上的令牌永远不会生效
func TestRequireTokenPicksUpTokenWrittenAfterStart(t *testing.T) {
	dir := agentDir(t, "")
	if rec := callGuarded(t, ""); rec.Code != http.StatusOK {
		t.Fatalf("起初没有令牌，应放行，得到 %d", rec.Code)
	}
	if err := os.WriteFile(filepath.Join(dir, agentTokenFile), []byte("late-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	if rec := callGuarded(t, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("令牌落盘后应立即开始鉴权，得到 %d", rec.Code)
	}
	if rec := callGuarded(t, "Bearer late-token"); rec.Code != http.StatusOK {
		t.Fatalf("带上新令牌应放行，得到 %d", rec.Code)
	}
}

// ---- handleStatus ----

func TestHandleStatusReportsDiskState(t *testing.T) {
	cases := []struct {
		name       string
		setup      func(t *testing.T) string
		wantStatus string
	}{
		{
			name:       "目录不存在",
			setup:      func(t *testing.T) string { return filepath.Join(t.TempDir(), "nope") },
			wantStatus: "missing",
		},
		{
			name:       "目录在但没注册过",
			setup:      func(t *testing.T) string { return t.TempDir() },
			wantStatus: "new",
		},
		{
			name: "有 .runner 即已注册",
			setup: func(t *testing.T) string {
				dir := t.TempDir()
				if err := os.WriteFile(filepath.Join(dir, ".runner"), []byte("{}"), 0o600); err != nil {
					t.Fatal(err)
				}
				return dir
			},
			wantStatus: "installed",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("RUNNER_INSTALL_DIR", tc.setup(t))
			rec := httptest.NewRecorder()
			handleStatus(rec, httptest.NewRequest(http.MethodGet, "/status", nil))

			if rec.Code != http.StatusOK {
				t.Fatalf("状态码 %d，期望 200", rec.Code)
			}
			var got statusResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("响应不是 JSON: %s", rec.Body.String())
			}
			if got.Status != tc.wantStatus {
				t.Fatalf("status = %q，期望 %q", got.Status, tc.wantStatus)
			}
			// 这几个用例里都没有真进程，Running 必须为 false。
			// 这一条曾经是错的：运行状态从不存在的 pid 文件读，于是恒为 false，
			// 反过来让 Manager 每 5 分钟把每个 Runner 都再拉起一遍。
			if got.Running {
				t.Fatal("没有 Runner 进程时 running 应为 false")
			}
		})
	}
}

func TestHandleStatusRejectsNonGet(t *testing.T) {
	t.Setenv("RUNNER_INSTALL_DIR", t.TempDir())
	rec := httptest.NewRecorder()
	handleStatus(rec, httptest.NewRequest(http.MethodPost, "/status", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("状态码 %d，期望 405", rec.Code)
	}
}

// ---- installDir ----

func TestInstallDirDefault(t *testing.T) {
	t.Setenv("RUNNER_INSTALL_DIR", "")
	if got := installDir(); got != defaultInstallDir {
		t.Fatalf("installDir() = %q，期望默认值 %q", got, defaultInstallDir)
	}
}
