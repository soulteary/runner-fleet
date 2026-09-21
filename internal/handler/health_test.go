package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/soulteary/runner-fleet/internal/config"
)

func serveProbe(t *testing.T, path string, h echo.HandlerFunc) (int, map[string]any) {
	t.Helper()
	e := echo.New()
	e.GET(path, h)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("响应不是 JSON: %v (%s)", err, rec.Body.String())
	}
	return rec.Code, m
}

// 写一份能加载的配置，并把 ConfigPath 指过去；base 是可用的 runner 根目录。
func withWorkingConfig(t *testing.T) (base string) {
	t.Helper()
	dir := t.TempDir()
	base = filepath.Join(dir, "runners")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Server:  config.ServerConfig{Port: 8080},
		Runners: config.RunnersConfig{BasePath: base, Items: []config.RunnerItem{}},
	}
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := cfg.Save(cfgPath); err != nil {
		t.Fatal(err)
	}
	old := ConfigPath
	ConfigPath = cfgPath
	t.Cleanup(func() { ConfigPath = old })
	return base
}

// /health 是存活探针：依赖坏掉也必须继续 200。
//
// 这条不是凑数。K8s 的 livenessProbe 失败意味着重启容器，而配置坏了、卷掉了这类
// 问题重启一次不会变好，只会变成 crashloop。所以依赖检查一律放 /ready，
// /health 只回答「进程还在不在」。
func TestHealth_StaysOKEvenWhenDependenciesAreBroken(t *testing.T) {
	old := ConfigPath
	ConfigPath = filepath.Join(t.TempDir(), "不存在的目录", "config.yaml")
	defer func() { ConfigPath = old }()

	code, m := serveProbe(t, "/health", Health)
	if code != http.StatusOK {
		t.Fatalf("/health 状态码 %d，存活探针不该因依赖故障变红", code)
	}
	if m["status"] != "ok" {
		t.Errorf("status = %v，期望 ok", m["status"])
	}
}

func TestReady_OKWhenConfigAndBasePathUsable(t *testing.T) {
	withWorkingConfig(t)
	code, m := serveProbe(t, "/ready", Ready)
	if code != http.StatusOK {
		t.Fatalf("/ready 状态码 %d，期望 200；body=%v", code, m)
	}
	if m["status"] != "ok" {
		t.Errorf("status = %v，期望 ok", m["status"])
	}
}

// 这条守住 WithCriticalChecks。health-kit 里非关键项失败只算 degraded，
// 而 degraded 被映射成 **200**（"Degraded is still functional"）——
// 忘了标关键项，这两个探针就成了摆设，永远不会把实例摘出负载。
func TestReady_503WhenConfigCannotLoad(t *testing.T) {
	old := ConfigPath
	ConfigPath = filepath.Join(t.TempDir(), "不存在的目录", "config.yaml")
	defer func() { ConfigPath = old }()

	code, m := serveProbe(t, "/ready", Ready)
	if code != http.StatusServiceUnavailable {
		t.Fatalf("配置加载不了时 /ready 应为 503，实得 %d；body=%v", code, m)
	}
	if m["status"] == "ok" {
		t.Errorf("status 不该是 ok：%v", m)
	}
}

// 挂载卷掉了是这里最想抓的情况：目录还在（挂载点本身），但写不进去。
func TestReady_503WhenBasePathNotWritable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("以 root 运行时 0500 目录依然可写，测不出来")
	}
	base := withWorkingConfig(t)
	if err := os.Chmod(base, 0o500); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(base, 0o755) }()

	code, m := serveProbe(t, "/ready", Ready)
	if code != http.StatusServiceUnavailable {
		t.Fatalf("根目录不可写时 /ready 应为 503，实得 %d；body=%v", code, m)
	}
}

func TestReady_503WhenBasePathIsAFile(t *testing.T) {
	base := withWorkingConfig(t)
	if err := os.RemoveAll(base); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(base, []byte("不是目录"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, m := serveProbe(t, "/ready", Ready)
	if code != http.StatusServiceUnavailable {
		t.Fatalf("根目录是文件时 /ready 应为 503，实得 %d；body=%v", code, m)
	}
}

// 两个端点都始终免鉴权，所以都不能吐出逐项检查结果或内部路径。
// 失败时尤其重要：那正是最容易把路径、错误原文漏出去的时候。
func TestProbes_NeverLeakInternalDetails(t *testing.T) {
	old := ConfigPath
	secret := filepath.Join(t.TempDir(), "机密路径", "config.yaml")
	ConfigPath = secret
	defer func() { ConfigPath = old }()

	for _, tc := range []struct {
		path string
		h    echo.HandlerFunc
	}{{"/health", Health}, {"/ready", Ready}} {
		_, m := serveProbe(t, tc.path, tc.h)
		for _, k := range []string{"checks", "details", "error"} {
			if _, ok := m[k]; ok {
				t.Errorf("%s 泄漏了 %s 字段：%v", tc.path, k, m)
			}
		}
		raw, _ := json.Marshal(m)
		if strings.Contains(string(raw), "机密路径") {
			t.Errorf("%s 响应里出现了内部路径：%s", tc.path, raw)
		}
	}
}

// /ready 会被探针每几秒打一次，所以两个检查都只读配置与文件系统元数据，
// 不起子进程、不发网络请求。这里没法直接断言「没有 exec」，只能退一步：
// 连打 50 次都得稳定成功——真要是去 docker exec 或拨网络，这条会先慢下来、
// 再因为超时或资源耗尽而不稳。
func TestReady_IsCheapEnoughForFrequentProbing(t *testing.T) {
	withWorkingConfig(t)
	// 先跑一次，避免把首次的文件系统冷启动算进去
	if code, _ := serveProbe(t, "/ready", Ready); code != http.StatusOK {
		t.Fatalf("预热请求就失败了，状态码 %d", code)
	}
	for i := 0; i < 50; i++ {
		if code, _ := serveProbe(t, "/ready", Ready); code != http.StatusOK {
			t.Fatalf("第 %d 次 /ready 失败，状态码 %d", i, code)
		}
	}
}
