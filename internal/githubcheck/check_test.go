package githubcheck

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/soulteary/runner-fleet/internal/config"
	"github.com/soulteary/runner-fleet/internal/runner"
)

// fakeAPI 把 apiBase 指向一个本地服务，返回它记录下的请求路径
func fakeAPI(t *testing.T, h http.HandlerFunc) *[]string {
	t.Helper()
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.RequestURI())
		h(w, r)
	}))
	t.Cleanup(srv.Close)
	orig := apiBase
	apiBase = srv.URL
	t.Cleanup(func() { apiBase = orig })
	return &seen
}

func runnersJSON(total int, names ...string) string {
	var rs []ghRunner
	for i, n := range names {
		rs = append(rs, ghRunner{ID: int64(100 + i), Name: n, OS: "linux", Status: "online"})
	}
	b, _ := json.Marshal(ghRunnersResponse{TotalCount: total, Runners: rs})
	return string(b)
}

func withToken(t *testing.T, token string) string {
	t.Helper()
	dir := t.TempDir()
	if token != "" {
		if err := os.WriteFile(filepath.Join(dir, RunnerTokenFile), []byte(token+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func statusOf(t *testing.T, dir string) (registered *bool, checkAt, checkErr string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, runner.GitHubStatusFile))
	if err != nil {
		return nil, "", ""
	}
	var v struct {
		Registered *bool  `json:"registered"`
		LastCheck  string `json:"last_check"`
		Error      string `json:"error"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("状态文件不是合法 JSON: %v", err)
	}
	return v.Registered, v.LastCheck, v.Error
}

func cfgFor(base, name, path, targetType, target string) *config.Config {
	return &config.Config{Runners: config.RunnersConfig{
		BasePath: base,
		Items:    []config.RunnerItem{{Name: name, Path: path, TargetType: targetType, Target: target}},
	}}
}

// runCfgIn 让 Run 落在给定的安装目录上：base + path 拼出来就是它
func runCfgIn(installDir, name, targetType, target string) *config.Config {
	return cfgFor(filepath.Dir(installDir), name, filepath.Base(installDir), targetType, target)
}

func TestRun_RegisteredWritesTrue(t *testing.T) {
	dir := withToken(t, "pat")
	fakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, runnersJSON(1, "alpha"))
	})
	Run(runCfgIn(dir, "alpha", "repo", "o/r"))

	reg, at, errMsg := statusOf(t, dir)
	if reg == nil || !*reg {
		t.Fatalf("应记为已登记，得到 %v", reg)
	}
	if at == "" {
		t.Fatal("应记录检查时间")
	}
	if errMsg != "" {
		t.Fatalf("成功时不应有错误说明，得到 %q", errMsg)
	}
}

func TestRun_NotRegisteredWritesFalse(t *testing.T) {
	dir := withToken(t, "pat")
	fakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, runnersJSON(1, "别的 runner"))
	})
	Run(runCfgIn(dir, "alpha", "repo", "o/r"))

	reg, _, _ := statusOf(t, dir)
	if reg == nil || *reg {
		t.Fatalf("目标不在列表里，应记为未登记，得到 %v", reg)
	}
}

// 这是本次修复的核心：查不出答案不等于没登记。
// 把它写成 false 会在界面上变成一句确定的「未显示」，
// 让人去排查一个并不存在的注册问题。
func TestRun_FailureIsUnknownNotUnregistered(t *testing.T) {
	cases := []struct {
		name      string
		status    int
		headers   map[string]string
		body      string
		wantInMsg string
	}{
		{name: "令牌过期", status: http.StatusUnauthorized, wantInMsg: "401"},
		{name: "权限不足", status: http.StatusForbidden, wantInMsg: "403"},
		{name: "限流", status: http.StatusForbidden,
			headers: map[string]string{"X-RateLimit-Remaining": "0"}, wantInMsg: "限流"},
		{name: "目标不可见", status: http.StatusNotFound, wantInMsg: "404"},
		{name: "服务端故障", status: http.StatusBadGateway, wantInMsg: "502"},
		{name: "响应不是JSON", status: http.StatusOK, body: "not json", wantInMsg: "解析"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := withToken(t, "pat")
			fakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
				for k, v := range tc.headers {
					w.Header().Set(k, v)
				}
				w.WriteHeader(tc.status)
				_, _ = fmt.Fprint(w, tc.body)
			})
			Run(runCfgIn(dir, "alpha", "repo", "o/r"))

			reg, at, errMsg := statusOf(t, dir)
			if reg != nil {
				t.Fatalf("查不出答案时应记为未知，却记成了 %v", *reg)
			}
			if at == "" {
				t.Fatal("即便失败也应记录检查时间，界面才能区分「查过但失败」与「从未查过」")
			}
			if !strings.Contains(errMsg, tc.wantInMsg) {
				t.Fatalf("原因说明 %q 里应含 %q", errMsg, tc.wantInMsg)
			}
		})
	}
}

// 没有 PAT 的 runner 直接跳过，不写状态文件——保持「从未检查」
func TestRun_NoTokenWritesNothing(t *testing.T) {
	dir := withToken(t, "")
	fakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("没有令牌时不应发起请求")
	})
	Run(runCfgIn(dir, "alpha", "repo", "o/r"))
	if _, err := os.Stat(filepath.Join(dir, runner.GitHubStatusFile)); !os.IsNotExist(err) {
		t.Fatal("没有令牌时不应写状态文件")
	}
}

// 超过一页的组织：只取第一页会把后面的 Runner 判成没登记
func TestRun_PaginatesBeyondFirstPage(t *testing.T) {
	dir := withToken(t, "pat")
	first := make([]string, apiPerPage)
	for i := range first {
		first[i] = fmt.Sprintf("filler-%d", i)
	}
	seen := fakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("page") {
		case "1":
			_, _ = fmt.Fprint(w, runnersJSON(apiPerPage+1, first...))
		default:
			_, _ = fmt.Fprint(w, runnersJSON(apiPerPage+1, "alpha"))
		}
	})
	Run(runCfgIn(dir, "alpha", "repo", "o/r"))

	reg, _, errMsg := statusOf(t, dir)
	if reg == nil || !*reg {
		t.Fatalf("alpha 在第二页，应判为已登记，得到 %v（err=%q）", reg, errMsg)
	}
	if len(*seen) < 2 {
		t.Fatalf("应翻到第二页，实际请求: %v", *seen)
	}
}

// 第一页不满就该停，不能白白多打一次 API
func TestRun_StopsAfterShortPage(t *testing.T) {
	dir := withToken(t, "pat")
	seen := fakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, runnersJSON(1, "alpha"))
	})
	Run(runCfgIn(dir, "alpha", "repo", "o/r"))
	if len(*seen) != 1 {
		t.Fatalf("单页即可取完，不应继续翻页，实际请求: %v", *seen)
	}
}

// target 里的特殊字符要转义，否则会打到别的端点上去
func TestListRunners_EscapesPathSegments(t *testing.T) {
	seen := fakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, runnersJSON(0))
	})
	_, err := listRunners(context.Background(), http.DefaultClient, "pat", "org", "my org?x=1")
	if err != nil {
		t.Fatal(err)
	}
	_, uri, _ := strings.Cut((*seen)[0], " ") // 记录的是 "METHOD URI"
	// 未转义时 `my org?x=1` 会让 ?x=1 变成查询串，路径缩成 /orgs/my org，打到另一个端点
	if strings.Contains(uri, " ") || strings.HasPrefix(uri, "/orgs/my org") {
		t.Fatalf("target 未转义，请求打到了 %q", uri)
	}
	got := uri
	if !strings.Contains(got, "/orgs/my%20org%3Fx=1/actions/runners") {
		t.Fatalf("请求路径 %q 不是预期的转义形式", got)
	}
}

func TestListRunners_RejectsInvalidTarget(t *testing.T) {
	fakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("target 无效时不应发起请求")
	})
	if _, err := listRunners(context.Background(), http.DefaultClient, "pat", "repo", "没有斜杠"); err == nil {
		t.Fatal("repo 类型的 target 缺少 owner/repo 结构，应直接报错")
	}
}

// ---- Deregister ----

// 从本工具删掉不等于从 GitHub 删掉：必须真的调 DELETE
func TestDeregister_DeletesByID(t *testing.T) {
	dir := withToken(t, "pat")
	seen := fakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		_, _ = fmt.Fprint(w, runnersJSON(2, "别的", "alpha"))
	})
	res := Deregister(context.Background(), dir, "repo", "o/r", "alpha")
	if !res.Done {
		t.Fatalf("应注销成功，得到 %+v", res)
	}
	// alpha 是列表里的第二个，ID 为 101
	wantDelete := "DELETE /repos/o/r/actions/runners/101"
	found := false
	for _, s := range *seen {
		if s == wantDelete {
			found = true
		}
	}
	if !found {
		t.Fatalf("应发出 %q，实际请求: %v", wantDelete, *seen)
	}
}

// GitHub 上本来就没有：目的已经达到，不该报成失败
func TestDeregister_AbsentRunnerIsDone(t *testing.T) {
	dir := withToken(t, "pat")
	seen := fakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, runnersJSON(1, "别的"))
	})
	res := Deregister(context.Background(), dir, "repo", "o/r", "alpha")
	if !res.Done {
		t.Fatalf("GitHub 上没有同名 Runner 应视为已完成，得到 %+v", res)
	}
	for _, s := range *seen {
		if strings.HasPrefix(s, "DELETE") {
			t.Fatalf("不存在时不该发 DELETE，实际请求: %v", *seen)
		}
	}
}

// 没有 PAT 就注销不了。这不是能默默吞掉的情况——
// 留在 GitHub 上的那个会让之后用同一名称重新添加时因重名而注册失败。
func TestDeregister_NoTokenSaysWhereToDeleteManually(t *testing.T) {
	dir := withToken(t, "")
	fakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("没有令牌时不应发起请求")
	})
	res := Deregister(context.Background(), dir, "repo", "o/r", "alpha")
	if res.Done {
		t.Fatal("没有令牌时不可能注销成功")
	}
	for _, want := range []string{RunnerTokenFile, "Settings", "alpha", "重名"} {
		if !strings.Contains(res.Message, want) {
			t.Fatalf("说明里应含 %q，实际为 %q", want, res.Message)
		}
	}
}

func TestDeregister_APIFailureIsReported(t *testing.T) {
	dir := withToken(t, "pat")
	fakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	res := Deregister(context.Background(), dir, "repo", "o/r", "alpha")
	if res.Done {
		t.Fatal("API 报 401 时不能算注销成功")
	}
	if !strings.Contains(res.Message, "401") || !strings.Contains(res.Message, "alpha") {
		t.Fatalf("说明 %q 里应指出 401 与 Runner 名", res.Message)
	}
}

// 删除接口返回 404：说明它已经不在了，同样算达成
func TestDeregister_DeleteReturning404IsDone(t *testing.T) {
	dir := withToken(t, "pat")
	fakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = fmt.Fprint(w, runnersJSON(1, "alpha"))
	})
	res := Deregister(context.Background(), dir, "repo", "o/r", "alpha")
	if !res.Done {
		t.Fatalf("DELETE 返回 404 说明已不存在，应视为完成，得到 %+v", res)
	}
}
