package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/soulteary/runner-fleet/internal/config"
	"github.com/soulteary/runner-fleet/internal/runner"
	"github.com/soulteary/runner-fleet/internal/runnerproc"
)

// withConfig 写一份配置并把 ConfigPath 指过去，返回 base 目录
func withConfig(t *testing.T, cfg *config.Config) string {
	t.Helper()
	dir := t.TempDir()
	cfg.Runners.BasePath = dir
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := cfg.Save(cfgPath); err != nil {
		t.Fatal(err)
	}
	old := ConfigPath
	ConfigPath = cfgPath
	t.Cleanup(func() { ConfigPath = old })
	return dir
}

func serve(t *testing.T, method, path string, register func(*echo.Echo)) *httptest.ResponseRecorder {
	t.Helper()
	e := echo.New()
	register(e)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

func stopRoute(e *echo.Echo) { e.POST("/api/runners/:name/stop", StopRunner) }

// ---- StopRunner：入参与查找 ----

func TestStopRunnerRejectsUnsafeName(t *testing.T) {
	withConfig(t, &config.Config{})
	// 名字会被拼进安装目录路径，放过 .. 就等于允许停任意目录下的进程
	for _, name := range []string{"..", "a..b", "a%2Fb"} {
		t.Run(name, func(t *testing.T) {
			rec := serve(t, http.MethodPost, "/api/runners/"+name+"/stop", stopRoute)
			if rec.Code != http.StatusBadRequest && rec.Code != http.StatusNotFound {
				t.Fatalf("得到 %d，非法名字应被 400 或 404 挡掉", rec.Code)
			}
		})
	}
}

func TestStopRunnerUnknownRunnerIs404(t *testing.T) {
	withConfig(t, &config.Config{
		Runners: config.RunnersConfig{Items: []config.RunnerItem{{Name: "r1", TargetType: "org", Target: "o1"}}},
	})
	rec := serve(t, http.MethodPost, "/api/runners/nope/stop", stopRoute)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("得到 %d，期望 404", rec.Code)
	}
}

// 没在跑就不必停，也不该报错——界面上重复点一下是很正常的
func TestStopRunnerIdleRunnerSucceedsWithoutSignalling(t *testing.T) {
	defer withI18n(map[string]string{"api.not_running": "NOT-RUNNING"})()
	base := withConfig(t, &config.Config{
		Runners: config.RunnersConfig{Items: []config.RunnerItem{{Name: "r1", TargetType: "org", Target: "o1"}}},
	})
	if err := os.MkdirAll(filepath.Join(base, "r1"), 0o700); err != nil {
		t.Fatal(err)
	}
	rec := serve(t, http.MethodPost, "/api/runners/r1/stop", stopRoute)
	if rec.Code != http.StatusOK {
		t.Fatalf("得到 %d（%s），期望 200", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "NOT-RUNNING") {
		t.Fatalf("应说明本来就没在跑，实际: %s", rec.Body.String())
	}
}

// ---- StopRunner：真把进程停掉 ----

func TestStopRunnerSignalsRunningProcess(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("进程探测依赖 /proc，仅在 Linux 上验证")
	}
	base := withConfig(t, &config.Config{
		Runners: config.RunnersConfig{Items: []config.RunnerItem{{Name: "r1", TargetType: "org", Target: "o1"}}},
	})
	installDir := filepath.Join(base, "r1")
	if err := os.MkdirAll(installDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(installDir, ".runner"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	// run.sh 刻意不 exec，让 bash 留在进程表里，argv 形态与 actions/runner 一致
	fifo := filepath.Join(installDir, ".test-block")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatalf("创建 FIFO 失败: %v", err)
	}
	script := "#!/bin/bash\nread -r -t 120 < \"" + fifo + "\"\n"
	if err := os.WriteFile(filepath.Join(installDir, "run.sh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, pid := range runnerproc.Find(installDir) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})

	if err := runner.Start(installDir); err != nil {
		t.Fatalf("准备阶段启动失败: %v", err)
	}
	if !waitFor(func() bool { return runnerproc.Running(installDir) }) {
		t.Fatal("准备阶段：进程应已在运行")
	}

	rec := serve(t, http.MethodPost, "/api/runners/r1/stop", stopRoute)
	if rec.Code != http.StatusOK {
		t.Fatalf("得到 %d（%s），期望 200", rec.Code, rec.Body.String())
	}
	if !waitFor(func() bool { return !runnerproc.Running(installDir) }) {
		t.Fatal("收到停止请求后进程应退出")
	}
}

func waitFor(cond func() bool) bool {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return cond()
}

// 容器模式下探测不到状态时仍要尝试停止，并把 probe 一并带回去：
// 「探测不出来」往往正是需要停一下的场景，这时候拒绝操作等于把人锁在门外
func TestStopRunnerContainerModeProbeFailureStillAttempts(t *testing.T) {
	base := withConfig(t, &config.Config{
		Runners: config.RunnersConfig{
			ContainerMode:  true,
			VolumeHostPath: "/host/runners",
			Items:          []config.RunnerItem{{Name: "r1", TargetType: "org", Target: "o1"}},
		},
	})
	installDir := filepath.Join(base, "r1")
	if err := os.MkdirAll(installDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(installDir, ".runner"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	// 让 docker 整个不可用：inspect 与 stop 都失败
	t.Setenv("PATH", t.TempDir())

	rec := serve(t, http.MethodPost, "/api/runners/r1/stop", stopRoute)
	// 停不下来就该报错，但不能是「未在运行」那种假成功
	if rec.Code == http.StatusOK && !strings.Contains(rec.Body.String(), "probe") {
		t.Fatalf("docker 不可用时不应报告已停止，实际: %s", rec.Body.String())
	}
	if rec.Code != http.StatusOK && rec.Code != http.StatusInternalServerError {
		t.Fatalf("得到 %d，期望 200（带 probe）或 500", rec.Code)
	}
	// 断言键而不是译文：这条用例没装 I18nLoader，tr 会回落成键本身，
	// 而键在整条链路上是稳定的——换一版英文措辞不该让这条守卫悄悄失效。
	if strings.Contains(rec.Body.String(), "api.not_running") {
		t.Fatalf("探测失败不能被当成「没在跑」，实际: %s", rec.Body.String())
	}
}

// ---- 读接口 ----

func TestListRunnersReturnsConfiguredRunners(t *testing.T) {
	base := withConfig(t, &config.Config{
		Runners: config.RunnersConfig{Items: []config.RunnerItem{
			{Name: "r1", TargetType: "org", Target: "o1"},
			{Name: "r2", TargetType: "repo", Target: "o/r"},
		}},
	})
	if err := os.MkdirAll(filepath.Join(base, "r1"), 0o700); err != nil {
		t.Fatal(err)
	}

	rec := serve(t, http.MethodGet, "/api/runners", func(e *echo.Echo) {
		e.GET("/api/runners", ListRunners)
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("得到 %d（%s）", rec.Code, rec.Body.String())
	}
	var list []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		// 响应也可能是 {"runners": [...]} 这种包装，两种都接受
		var wrapped map[string][]map[string]any
		if err2 := json.Unmarshal(rec.Body.Bytes(), &wrapped); err2 != nil {
			t.Fatalf("响应不是 JSON 列表: %s", rec.Body.String())
		}
		for _, v := range wrapped {
			list = v
		}
	}
	if len(list) != 2 {
		t.Fatalf("应返回 2 个 runner，实际 %d 个: %s", len(list), rec.Body.String())
	}
}

func TestGetRunner(t *testing.T) {
	base := withConfig(t, &config.Config{
		Runners: config.RunnersConfig{Items: []config.RunnerItem{{Name: "r1", TargetType: "org", Target: "o1"}}},
	})
	if err := os.MkdirAll(filepath.Join(base, "r1"), 0o700); err != nil {
		t.Fatal(err)
	}

	t.Run("存在", func(t *testing.T) {
		rec := serve(t, http.MethodGet, "/api/runners/r1", func(e *echo.Echo) {
			e.GET("/api/runners/:name", GetRunner)
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("得到 %d（%s）", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), `"r1"`) {
			t.Fatalf("响应里应有 runner 名，实际: %s", rec.Body.String())
		}
	})

	t.Run("不存在", func(t *testing.T) {
		rec := serve(t, http.MethodGet, "/api/runners/nope", func(e *echo.Echo) {
			e.GET("/api/runners/:name", GetRunner)
		})
		if rec.Code != http.StatusNotFound {
			t.Fatalf("得到 %d，期望 404", rec.Code)
		}
	})

	t.Run("名字非法", func(t *testing.T) {
		rec := serve(t, http.MethodGet, "/api/runners/..", func(e *echo.Echo) {
			e.GET("/api/runners/:name", GetRunner)
		})
		if rec.Code == http.StatusOK {
			t.Fatal("非法名字不该返回 200")
		}
	})
}

// ---- resolveLang ----

// 六种语言的界面全靠这个函数选，选错就是整页英文。
//
// 优先级 ?lang= > cookie > Accept-Language > en。query 排在 cookie 前面：
// cookie 是这台浏览器上的长期偏好（语言下拉框写的就是它，写完 reload，URL 上不带参数），
// ?lang= 则是本次访问的明确指定。反过来的话，带 ?lang=ja 的链接发给一个早先选过中文的人，
// 对方看到的仍是中文——这个参数对每一个设过偏好的人都失效，而那正是它唯一有用的场合。
func TestResolveLang(t *testing.T) {
	cases := []struct {
		name   string
		query  string
		cookie string
		header string
		want   string
	}{
		{"默认英文", "", "", "", "en"},
		{"query 优先于 cookie", "zh", "ja", "ko", "zh"},
		{"没有 query 时看 cookie", "", "ja", "ko", "ja"},
		{"最后 Accept-Language", "", "", "ko", "ko"},
		{"Accept-Language 带权重", "", "", "de-DE,de;q=0.9,en;q=0.8", "de"},
		{"不支持的 query 值跳过，继续看 cookie", "xx", "ja", "", "ja"},
		{"不支持的 cookie 值跳过，继续看 Accept-Language", "", "xx", "ko", "ko"},
		{"都不支持时回落英文", "xx", "yy", "zz", "en"},
		{"大小写不敏感", "ZH", "", "", "zh"},
		{"前后空白会被去掉", "  ja  ", "", "", "ja"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			target := "/"
			if tc.query != "" {
				// 必须转义：值里可能有空格，直接拼进 URL 会连请求行都解析不了
				target += "?lang=" + url.QueryEscape(tc.query)
			}
			req := httptest.NewRequest(http.MethodGet, target, nil)
			if tc.cookie != "" {
				req.AddCookie(&http.Cookie{Name: "lang", Value: tc.cookie})
			}
			if tc.header != "" {
				req.Header.Set("Accept-Language", tc.header)
			}
			c := echo.New().NewContext(req, httptest.NewRecorder())
			if got := resolveLang(c); got != tc.want {
				t.Fatalf("resolveLang = %q，期望 %q", got, tc.want)
			}
		})
	}
}

// ---- shortRandomSuffix ----

// 名称冲突时用它生成建议名，撞了就等于没建议
func TestShortRandomSuffix(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 200; i++ {
		s := shortRandomSuffix()
		if len(s) == 0 {
			t.Fatal("后缀不能为空")
		}
		// 会被拼进容器名与目录名，只能是 [a-z0-9]。
		// 用白名单而不是两段区间比较：区间写法要么是 staticcheck 会挑的
		// 「!a && !b」（QF1001），要么是反过来的四段不等式，两种都不如
		// 直接照抄 shortRandomSuffix 里那一串字符看得明白。
		const allowed = "abcdefghijklmnopqrstuvwxyz0123456789"
		for _, r := range s {
			if !strings.ContainsRune(allowed, r) {
				t.Fatalf("后缀含非法字符 %q: %s", r, s)
			}
		}
		seen[s] = true
	}
	if len(seen) < 100 {
		t.Fatalf("200 次只生成了 %d 个不同后缀，随机性不足", len(seen))
	}
}
