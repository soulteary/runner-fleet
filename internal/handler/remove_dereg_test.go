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
)

// removeTestSetup 造一份只含一个已注册 runner 的配置，并把 ConfigPath 指过去
func removeTestSetup(t *testing.T, withToken bool) (base, installDir string) {
	t.Helper()
	base = t.TempDir()
	installDir = filepath.Join(base, "alpha")
	if err := os.MkdirAll(installDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(installDir, ".runner"), []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}
	if withToken {
		if err := os.WriteFile(filepath.Join(installDir, githubcheck.RunnerTokenFile), []byte("pat"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	cfgPath := filepath.Join(base, "config.yaml")
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
	return base, installDir
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

func stubDereg(t *testing.T, fn func(ctx context.Context, installDir, targetType, target, name string) githubcheck.DeregisterResult) {
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
	_, installDir := removeTestSetup(t, true)
	var gotDir, gotType, gotTarget, gotName string
	called := false
	stubDereg(t, func(_ context.Context, dir, tt, target, name string) githubcheck.DeregisterResult {
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

// 顺序是硬约束：注销要用该目录下的 PAT，目录一删就没得可用了
func TestRemoveRunner_DeregistersBeforeDeletingInstallDir(t *testing.T) {
	_, installDir := removeTestSetup(t, true)
	tokenPath := filepath.Join(installDir, githubcheck.RunnerTokenFile)
	tokenReadable := false
	stubDereg(t, func(_ context.Context, dir, _, _, _ string) githubcheck.DeregisterResult {
		// 被调用的当下，令牌文件必须还在——否则真实实现根本拿不到凭据
		if b, err := os.ReadFile(filepath.Join(dir, githubcheck.RunnerTokenFile)); err == nil && strings.TrimSpace(string(b)) == "pat" {
			tokenReadable = true
		}
		return githubcheck.DeregisterResult{Done: true}
	})

	if code, body := doRemove(t); code != http.StatusOK {
		t.Fatalf("状态码 %d，响应 %v", code, body)
	}
	if !tokenReadable {
		t.Fatal("注销发生在删目录之后，那时 PAT 已经没了")
	}
	if _, err := os.Stat(tokenPath); !os.IsNotExist(err) {
		t.Fatal("安装目录最终仍应被删除")
	}
}

// 注销失败不阻断本地删除——用户要的是「从这里去掉它」——
// 但必须如实说出 GitHub 上还留着一个，以及该去哪儿删
func TestRemoveRunner_DeregisterFailureIsReportedNotSwallowed(t *testing.T) {
	defer withI18n(map[string]string{
		"api.removed":         "REMOVED-FROM-CONFIG %s",
		"github.dereg.no_pat": "NOT-DELETED %s Settings %q",
	})()
	_, _ = removeTestSetup(t, false)
	stubDereg(t, func(_ context.Context, _, _, _, _ string) githubcheck.DeregisterResult {
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
	_, _ = removeTestSetup(t, true)
	stubDereg(t, func(_ context.Context, _, _, _, _ string) githubcheck.DeregisterResult {
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
