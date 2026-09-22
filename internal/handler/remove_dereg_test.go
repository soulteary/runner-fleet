package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/soulteary/runner-fleet/internal/config"
	"github.com/soulteary/runner-fleet/internal/githubcheck"
	"github.com/soulteary/runner-fleet/internal/secrets"
)

// tokenPlacement 测试里那份可选 PAT 放在哪儿
type tokenPlacement int

const (
	noToken          tokenPlacement = iota
	tokenInStore                    // 新位置：<配置目录>/tokens/<name>
	tokenInRunnerDir                // 旧位置：Runner 目录下，读取前会被迁移搬走
)

// removeTestSetup 造一份只含一个已注册 runner 的配置，并把 ConfigPath 指过去。
//
// 目录布局照生产摆：配置目录与 base_path 分开，因为凭据目录挂在配置目录下，
// 两者重合的话就验不出「凭据不在会被挂进容器的那棵树里」。
func removeTestSetup(t *testing.T, placement tokenPlacement) (base, installDir string) {
	t.Helper()
	root := t.TempDir()
	base = filepath.Join(root, "runners")
	installDir = filepath.Join(base, "alpha")
	if err := os.MkdirAll(installDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(installDir, ".runner"), []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}
	cfgDir := filepath.Join(root, "config")
	if err := os.MkdirAll(cfgDir, 0700); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(cfgDir, "config.yaml")
	cfg := &config.Config{
		Server: config.ServerConfig{Port: 8080},
		Runners: config.RunnersConfig{BasePath: base, Items: []config.RunnerItem{
			{Name: "alpha", Path: "alpha", TargetType: "repo", Target: "o/r"},
		}},
	}
	if err := cfg.Save(cfgPath); err != nil {
		t.Fatal(err)
	}
	orig := ConfigPath
	ConfigPath = cfgPath
	t.Cleanup(func() { ConfigPath = orig })

	switch placement {
	case tokenInStore:
		store := secrets.NewStore(cfgPath)
		if err := os.MkdirAll(store.Dir, 0700); err != nil {
			t.Fatal(err)
		}
		tp, err := store.Path("alpha")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(tp, []byte("pat"), 0600); err != nil {
			t.Fatal(err)
		}
	case tokenInRunnerDir:
		if err := os.WriteFile(filepath.Join(installDir, secrets.LegacyRunnerTokenFile), []byte("pat"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return base, installDir
}

// storedToken 读出新位置上的 PAT，空串表示没有
func storedToken(t *testing.T) string {
	t.Helper()
	return secrets.NewStore(ConfigPath).GitHubToken("alpha")
}

func doRemove(t *testing.T) (int, map[string]any) {
	t.Helper()
	e := echo.New()
	e.DELETE("/api/runners/:name", RemoveRunnerByName)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/runners/alpha", nil))
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return rec.Code, body
}

func stubDereg(t *testing.T, fn func(ctx context.Context, store *secrets.Store, installDir, targetType, target, name string) githubcheck.DeregisterResult) {
	t.Helper()
	orig := deregisterFromGitHub
	deregisterFromGitHub = fn
	t.Cleanup(func() { deregisterFromGitHub = orig })
}

// 删除 Runner 必须尝试从 GitHub 注销：只删本地会在 GitHub 上留下一个同名 Runner，
// 之后用同一名称重新添加就会因重名而注册失败。
func TestRemoveRunner_DeregistersFromGitHub(t *testing.T) {
	defer withI18n(map[string]string{
		"api.removed":       "REMOVED %s",
		"github.dereg.done": "DEREGISTERED",
	})()
	_, installDir := removeTestSetup(t, tokenInStore)
	var gotDir, gotType, gotTarget, gotName string
	called := false
	stubDereg(t, func(_ context.Context, _ *secrets.Store, dir, tt, target, name string) githubcheck.DeregisterResult {
		called = true
		gotDir, gotType, gotTarget, gotName = dir, tt, target, name
		return githubcheck.DeregisterResult{Done: true, MessageKey: "github.dereg.done"}
	})

	code, body := doRemove(t)
	if code != http.StatusOK {
		t.Fatalf("状态码 %d，响应 %v", code, body)
	}
	if !called {
		t.Fatal("删除时没有尝试从 GitHub 注销")
	}
	if gotDir != installDir || gotType != "repo" || gotTarget != "o/r" || gotName != "alpha" {
		t.Fatalf("注销参数不对: dir=%q type=%q target=%q name=%q", gotDir, gotType, gotTarget, gotName)
	}
	if body["github_deregistered"] != true {
		t.Fatalf("响应应标明已注销，得到 %v", body)
	}
	// 注销结果必须嵌进响应消息里。按键断言：外壳是 api.removed，
	// 里层是 githubcheck 回传的键，两层都由 handler 按同一个请求语言渲染。
	if !strings.Contains(body["message"].(string), "DEREGISTERED") {
		t.Fatalf("注销结果应写进响应消息，得到 %v", body["message"])
	}
}

// 顺序是硬约束：注销要用这个 Runner 的 PAT，凭据一删就没得可用了。
// 令牌已经不在安装目录里，但「先注销、后删凭据」这条约束没有变。
func TestRemoveRunner_DeregistersBeforeDeletingTheStoredToken(t *testing.T) {
	_, _ = removeTestSetup(t, tokenInStore)
	tokenReadable := false
	stubDereg(t, func(_ context.Context, store *secrets.Store, _, _, _, _ string) githubcheck.DeregisterResult {
		// 被调用的当下，凭据必须还在——否则真实实现根本拿不到它。
		// 用传进来的 store 读，顺带钉住 handler 确实把 store 传下去了。
		if store != nil && store.GitHubToken("alpha") == "pat" {
			tokenReadable = true
		}
		return githubcheck.DeregisterResult{Done: true}
	})

	if code, body := doRemove(t); code != http.StatusOK {
		t.Fatalf("状态码 %d，响应 %v", code, body)
	}
	if !tokenReadable {
		t.Fatal("注销发生在删凭据之后，那时 PAT 已经没了")
	}
	// 删完之后凭据不能留下：下一个同名 Runner 会莫名继承它
	if got := storedToken(t); got != "" {
		t.Fatalf("删除 Runner 后 tokens/alpha 应已不存在，却读到 %q", got)
	}
}

// 旧位置的 PAT 同样要能用上：注销之前它会被搬到凭据目录，
// 所以「注销必须早于删安装目录」这条也还在。
func TestRemoveRunner_DeregistersBeforeDeletingInstallDir(t *testing.T) {
	_, installDir := removeTestSetup(t, tokenInRunnerDir)
	legacy := filepath.Join(installDir, secrets.LegacyRunnerTokenFile)
	legacyReadable := false
	stubDereg(t, func(_ context.Context, _ *secrets.Store, dir, _, _, _ string) githubcheck.DeregisterResult {
		// 被调用的当下，旧文件必须还在——迁移要从这里读
		if b, err := os.ReadFile(filepath.Join(dir, secrets.LegacyRunnerTokenFile)); err == nil && strings.TrimSpace(string(b)) == "pat" {
			legacyReadable = true
		}
		return githubcheck.DeregisterResult{Done: true}
	})

	if code, body := doRemove(t); code != http.StatusOK {
		t.Fatalf("状态码 %d，响应 %v", code, body)
	}
	if !legacyReadable {
		t.Fatal("注销发生在删目录之后，那时旧位置的 PAT 已经没了")
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatal("安装目录最终仍应被删除")
	}
	if got := storedToken(t); got != "" {
		t.Fatalf("删除 Runner 后 tokens/alpha 应已不存在，却读到 %q", got)
	}
}

// 删除一个 Runner 不能连带删掉别人的凭据
func TestRemoveRunner_LeavesOtherRunnersTokensAlone(t *testing.T) {
	_, _ = removeTestSetup(t, tokenInStore)
	store := secrets.NewStore(ConfigPath)
	other, err := store.Path("beta")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other, []byte("beta-pat"), 0600); err != nil {
		t.Fatal(err)
	}
	stubDereg(t, func(_ context.Context, _ *secrets.Store, _, _, _, _ string) githubcheck.DeregisterResult {
		return githubcheck.DeregisterResult{Done: true}
	})

	if code, body := doRemove(t); code != http.StatusOK {
		t.Fatalf("状态码 %d，响应 %v", code, body)
	}
	if got := store.GitHubToken("beta"); got != "beta-pat" {
		t.Fatalf("beta 的凭据不该被动到，得到 %q", got)
	}
}

// 注销失败不阻断本地删除——用户要的是「从这里去掉它」——
// 但必须如实说出 GitHub 上还留着一个，以及该去哪儿删
func TestRemoveRunner_DeregisterFailureIsReportedNotSwallowed(t *testing.T) {
	defer withI18n(map[string]string{
		"api.removed":         "REMOVED-FROM-CONFIG %s",
		"github.dereg.no_pat": "NOT-DELETED %s Settings %q",
	})()
	_, _ = removeTestSetup(t, noToken)
	stubDereg(t, func(_ context.Context, _ *secrets.Store, _, _, _, _ string) githubcheck.DeregisterResult {
		return githubcheck.DeregisterResult{
			Done:        false,
			MessageKey:  "github.dereg.no_pat",
			MessageArgs: []any{".github_check_token", "alpha"},
		}
	})

	code, body := doRemove(t)
	if code != http.StatusOK {
		t.Fatalf("注销失败不应让删除整体失败，状态码 %d", code)
	}
	if body["github_deregistered"] != false {
		t.Fatalf("响应应标明未注销，得到 %v", body)
	}
	msg, _ := body["message"].(string)
	// 两层都按键断言：外壳是 api.removed，嵌进去的那半句是 githubcheck 回传的键，
	// 由 handler 用同一个请求语言渲染——跨包消息现在也跟着语言走了。
	for _, want := range []string{"REMOVED-FROM-CONFIG", "NOT-DELETED", "Settings"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("响应消息 %q 里应含 %q", msg, want)
		}
	}
}

// 不存在的 runner 不该触发注销
func TestRemoveRunner_UnknownNameDoesNotDeregister(t *testing.T) {
	_, _ = removeTestSetup(t, tokenInStore)
	stubDereg(t, func(_ context.Context, _ *secrets.Store, _, _, _, _ string) githubcheck.DeregisterResult {
		t.Error("未找到的 runner 不应尝试注销")
		return githubcheck.DeregisterResult{}
	})
	e := echo.New()
	e.DELETE("/api/runners/:name", RemoveRunnerByName)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/runners/不存在", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("应返回 404，得到 %d: %s", rec.Code, rec.Body.String())
	}
}

// main 没注入 Secrets 时按 ConfigPath 推导，凭据目录落在配置文件旁边
func TestSecretStore_FallsBackToConfigPath(t *testing.T) {
	origStore := Secrets
	Secrets = nil
	t.Cleanup(func() { Secrets = origStore })
	origPath := ConfigPath
	ConfigPath = filepath.Join("some", "where", "config.yaml")
	t.Cleanup(func() { ConfigPath = origPath })

	if got, want := secretStore().Dir, filepath.Join("some", "where", "tokens"); got != want {
		t.Fatalf("凭据目录为 %q，期望 %q", got, want)
	}

	// 注入了就用注入的那个
	injected := &secrets.Store{Dir: filepath.Join("injected", "tokens")}
	Secrets = injected
	if got := secretStore(); got != injected {
		t.Fatalf("应返回 main 注入的 store，得到 %+v", got)
	}
}
