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
	"time"

	"github.com/soulteary/runner-fleet/internal/config"
	"github.com/soulteary/runner-fleet/internal/runner"
	"github.com/soulteary/runner-fleet/internal/secrets"
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

// withToken 建一个 Runner 安装目录，并把 PAT 放进 Manager 侧的凭据目录。
//
// 凭据目录刻意与安装目录分开：新位置在配置目录下，而配置目录不会被挂进
// Runner 容器。两者同根只是为了 t.TempDir 好清理。
func withToken(t *testing.T, token string) (string, *secrets.Store) {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "runners", "alpha")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	store := &secrets.Store{Dir: filepath.Join(root, "config", "tokens")}
	if token != "" {
		if err := os.MkdirAll(store.Dir, 0700); err != nil {
			t.Fatal(err)
		}
		p, err := store.Path("alpha")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(token+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return dir, store
}

// withLegacyToken 建一个安装目录，并把 PAT 放进旧位置（Runner 目录下），
// 用来验证迁移：读取前会把它搬走。
func withLegacyToken(t *testing.T, token string) (string, *secrets.Store) {
	t.Helper()
	dir, store := withToken(t, "")
	if err := os.WriteFile(filepath.Join(dir, secrets.LegacyRunnerTokenFile), []byte(token+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return dir, store
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

// busyOf 单独读 busy，免得改动 statusOf 的签名牵动上面一长串用例
func busyOf(t *testing.T, dir string) *bool {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, runner.GitHubStatusFile))
	if err != nil {
		return nil
	}
	var v struct {
		Busy *bool `json:"busy"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("状态文件不是合法 JSON: %v", err)
	}
	return v.Busy
}

// busyRunnersJSON 与 runnersJSON 相同，但把第一个 runner 标成正在跑 Job
func busyRunnersJSON(busy bool, names ...string) string {
	var rs []ghRunner
	for i, n := range names {
		rs = append(rs, ghRunner{ID: int64(100 + i), Name: n, OS: "linux", Status: "online", Busy: busy && i == 0})
	}
	b, _ := json.Marshal(ghRunnersResponse{TotalCount: len(names), Runners: rs})
	return string(b)
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
	dir, store := withToken(t, "pat")
	fakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, runnersJSON(1, "alpha"))
	})
	Run(runCfgIn(dir, "alpha", "repo", "o/r"), store)

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
	dir, store := withToken(t, "pat")
	fakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, runnersJSON(1, "别的 runner"))
	})
	Run(runCfgIn(dir, "alpha", "repo", "o/r"), store)

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
			headers: map[string]string{"X-RateLimit-Remaining": "0"}, wantInMsg: "rate limit"},
		{name: "目标不可见", status: http.StatusNotFound, wantInMsg: "404"},
		{name: "服务端故障", status: http.StatusBadGateway, wantInMsg: "502"},
		{name: "响应不是JSON", status: http.StatusOK, body: "not json", wantInMsg: "could not parse"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir, store := withToken(t, "pat")
			fakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
				for k, v := range tc.headers {
					w.Header().Set(k, v)
				}
				w.WriteHeader(tc.status)
				_, _ = fmt.Fprint(w, tc.body)
			})
			Run(runCfgIn(dir, "alpha", "repo", "o/r"), store)

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

// 没有 PAT 就不查「是否已登记」，界面上仍是「从未检查」。
//
// 可见性那一半是匿名也能查的，所以这时状态文件会被写出来——但 last_check 必须留空：
// 界面正是靠它区分「从未检查」与「查过但失败」，盖上时间就等于对着一个从没查过的
// Runner 说「查过了，失败」。
func TestRun_NoTokenSkipsTheRegistrationCheck(t *testing.T) {
	dir, store := withToken(t, "")
	seen := fakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Errorf("没有 PAT 时不该带 Authorization，得到 %q", r.Header.Get("Authorization"))
		}
		_, _ = fmt.Fprint(w, `{"private":true}`)
	})
	Run(runCfgIn(dir, "alpha", "repo", "o/r"), store)

	for _, req := range *seen {
		if strings.Contains(req, "actions/runners") {
			t.Fatalf("没有令牌时不应查登记状态，实际请求: %v", *seen)
		}
	}
	reg, at, errMsg := statusOf(t, dir)
	if reg != nil || at != "" || errMsg != "" {
		t.Fatalf("没有令牌时登记状态应一概为空，得到 %v/%q/%q", reg, at, errMsg)
	}
}

// 超过一页的组织：只取第一页会把后面的 Runner 判成没登记
func TestRun_PaginatesBeyondFirstPage(t *testing.T) {
	dir, store := withToken(t, "pat")
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
	Run(runCfgIn(dir, "alpha", "repo", "o/r"), store)

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
	dir, store := withToken(t, "pat")
	seen := fakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, runnersJSON(1, "alpha"))
	})
	Run(runCfgIn(dir, "alpha", "repo", "o/r"), store)
	// 只数列 Runner 的请求：同一轮里还有一次可见性查询，它打的是另一个端点
	pages := 0
	for _, req := range *seen {
		if strings.Contains(req, "actions/runners") {
			pages++
		}
	}
	if pages != 1 {
		t.Fatalf("单页即可取完，不应继续翻页，实际请求: %v", *seen)
	}
}

// 可见性是三态，而且 404 这一格是最容易写错、代价也最大的一格：
// 匿名请求下私有仓库与压根不存在的仓库都是 404，带 PAT 时权限不够也是 404。
// 记成 false 就等于对着一个打错了名字的目标肯定地说「私有」——而「公开」正是
// 这个检查唯一要喊出来的结论，说错方向比不说更糟。
func TestRepoVisibility_ThreeStates(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		body    string
		want    *bool
		wantErr string
	}{
		{name: "公开", status: 200, body: `{"private":false}`, want: ptrBool(true)},
		{name: "私有", status: 200, body: `{"private":true}`, want: ptrBool(false)},
		{name: "看不到（404）", status: 404, body: `{}`},
		{name: "GitHub 出错（500）", status: 500, body: `{}`, wantErr: "500"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = fmt.Fprint(w, tc.body)
			})
			got, err := repoVisibility(context.Background(), &http.Client{}, tokenRef{value: "pat"}, "o/r")
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("不该报错，得到 %v", err)
			case tc.wantErr != "" && err == nil:
				t.Fatal("应带回原因，得到 nil")
			case tc.wantErr != "" && !strings.Contains(err.Error(), tc.wantErr):
				t.Fatalf("原因 %q 里应含 %q", err, tc.wantErr)
			}
			switch {
			case tc.want == nil && got != nil:
				t.Fatalf("应记为不知道，却记成了 %v", *got)
			case tc.want != nil && got == nil:
				t.Fatalf("应记为 %v，却记成了不知道", *tc.want)
			case tc.want != nil && *got != *tc.want:
				t.Fatalf("应记为 %v，得到 %v", *tc.want, *got)
			}
		})
	}
}

// 没有 PAT 时匿名发：绝大多数部署不配 PAT，而它们正是最需要这个提示的一批。
// 带一个空的 Bearer 比不带更糟——GitHub 会当成坏令牌返回 401。
func TestRepoVisibility_AnonymousWhenNoToken(t *testing.T) {
	var auth string
	var sawHeader bool
	fakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		auth, sawHeader = r.Header.Get("Authorization"), true
		_, _ = fmt.Fprint(w, `{"private":false}`)
	})
	got, err := repoVisibility(context.Background(), &http.Client{}, tokenRef{}, "o/r")
	if err != nil {
		t.Fatal(err)
	}
	if !sawHeader {
		t.Fatal("没发出请求")
	}
	if auth != "" {
		t.Fatalf("没有 PAT 时不该带 Authorization，得到 %q", auth)
	}
	if got == nil || !*got {
		t.Fatalf("private:false 应记为公开，得到 %v", got)
	}
}

// 每个仓库一天最多问一次。这个检查每五分钟跑一轮，匿名配额每小时只有 60 次，
// 照每轮都问的话十来个 Runner 就能把配额烧干，连带把带 PAT 的那半边拖进限流。
func TestRun_VisibilityIsCheckedAtMostOncePerDay(t *testing.T) {
	dir, store := withToken(t, "pat")
	visits := 0
	fakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/o/r" {
			visits++
			_, _ = fmt.Fprint(w, `{"private":false}`)
			return
		}
		_, _ = fmt.Fprint(w, runnersJSON(1, "alpha"))
	})
	cfg := runCfgIn(dir, "alpha", "repo", "o/r")

	Run(cfg, store)
	if visits != 1 {
		t.Fatalf("第一轮应查一次可见性，实际 %d 次", visits)
	}
	if p := publicOf(t, dir); p == nil || !*p {
		t.Fatalf("private:false 应记为公开，得到 %v", p)
	}

	Run(cfg, store)
	if visits != 1 {
		t.Fatalf("24 小时内不该再查，实际共 %d 次", visits)
	}
	// 结论不能被第二轮的登记检查顺手抹掉：状态文件是一次写完的
	if p := publicOf(t, dir); p == nil || !*p {
		t.Fatalf("第二轮之后可见性结论应还在，得到 %v", p)
	}

	// 把时间戳拨回 25 小时前，过期了就该再查一次
	agePriorVisibilityCheck(t, dir, 25*time.Hour)
	Run(cfg, store)
	if visits != 2 {
		t.Fatalf("超过 24 小时应重新查一次，实际共 %d 次", visits)
	}
}

// 组织目标本轮不处理：要判断的是「Runner 组允不允许公开仓库」，那要 admin:org
// 还得把 Runner 映射到组，和查一个仓库不是一回事。查错端点会拿 404 当答案。
func TestRun_OrgTargetIsNotCheckedForVisibility(t *testing.T) {
	dir, store := withToken(t, "pat")
	seen := fakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, runnersJSON(1, "alpha"))
	})
	Run(runCfgIn(dir, "alpha", "org", "acme"), store)
	for _, req := range *seen {
		if !strings.Contains(req, "actions/runners") {
			t.Fatalf("组织目标不应查仓库可见性，实际请求: %v", *seen)
		}
	}
	if p := publicOf(t, dir); p != nil {
		t.Fatalf("组织目标的可见性应留空，得到 %v", *p)
	}
}

// 两件事都没做（组织目标 + 没有 PAT）就不写文件：写了的话界面会从「从未检查」
// 变成「查过但失败」，对一个压根没查过的 Runner 说了一句确定的话。
func TestRun_OrgTargetWithoutTokenWritesNothing(t *testing.T) {
	dir, store := withToken(t, "")
	fakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("既没有 PAT 又不查可见性时不应发起请求")
	})
	Run(runCfgIn(dir, "alpha", "org", "acme"), store)
	if _, err := os.Stat(filepath.Join(dir, runner.GitHubStatusFile)); !os.IsNotExist(err) {
		t.Fatal("什么都没查时不应写状态文件")
	}
}

// publicOf 单独读 public，理由同 busyOf
func publicOf(t *testing.T, dir string) *bool {
	t.Helper()
	return runner.ReadGitHubStatus(dir).Public
}

func ptrBool(b bool) *bool { return &b }

// agePriorVisibilityCheck 把状态文件里的可见性查询时间往前拨，用来跨过 24 小时这道闸。
func agePriorVisibilityCheck(t *testing.T, dir string, d time.Duration) {
	t.Helper()
	st := runner.ReadGitHubStatus(dir)
	if st.VisibilityCheckedAt == "" {
		t.Fatal("状态文件里没有可见性查询时间，这个用例没有意义")
	}
	at, err := time.Parse(time.RFC3339, st.VisibilityCheckedAt)
	if err != nil {
		t.Fatal(err)
	}
	st.VisibilityCheckedAt = at.Add(-d).Format(time.RFC3339)
	if err := runner.WriteGitHubStatus(dir, st); err != nil {
		t.Fatal(err)
	}
}

// target 里的特殊字符要转义，否则会打到别的端点上去
func TestListRunners_EscapesPathSegments(t *testing.T) {
	seen := fakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, runnersJSON(0))
	})
	_, err := listRunners(context.Background(), http.DefaultClient, tokenRef{value: "pat"}, "org", "my org?x=1")
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
	if _, err := listRunners(context.Background(), http.DefaultClient, tokenRef{value: "pat"}, "repo", "没有斜杠"); err == nil {
		t.Fatal("repo 类型的 target 缺少 owner/repo 结构，应直接报错")
	}
}

// ---- Deregister ----

// 从本工具删掉不等于从 GitHub 删掉：必须真的调 DELETE
func TestDeregister_DeletesByID(t *testing.T) {
	dir, store := withToken(t, "pat")
	seen := fakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		_, _ = fmt.Fprint(w, runnersJSON(2, "别的", "alpha"))
	})
	res := Deregister(context.Background(), store, dir, "repo", "o/r", "alpha")
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
	dir, store := withToken(t, "pat")
	seen := fakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, runnersJSON(1, "别的"))
	})
	res := Deregister(context.Background(), store, dir, "repo", "o/r", "alpha")
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
	dir, store := withToken(t, "")
	fakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("没有令牌时不应发起请求")
	})
	res := Deregister(context.Background(), store, dir, "repo", "o/r", "alpha")
	if res.Done {
		t.Fatal("没有令牌时不可能注销成功")
	}
	// 断言键与参数，不断言措辞：句子现在由调用方按语言渲染，
	// 本包只回传「是什么事」。参数里必须带上令牌文件名与 Runner 名——
	// 少了哪一个，那句话都没法告诉人该去哪儿手动删。
	if res.MessageKey != "github.dereg.no_pat" {
		t.Fatalf("键应为 github.dereg.no_pat，实际 %q", res.MessageKey)
	}
	wantPath, err := store.Path("alpha")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.MessageArgs) != 2 || res.MessageArgs[0] != wantPath || res.MessageArgs[1] != "alpha" {
		t.Fatalf("参数应为 [%s alpha]，实际 %v", wantPath, res.MessageArgs)
	}
}

func TestDeregister_APIFailureIsReported(t *testing.T) {
	dir, store := withToken(t, "pat")
	fakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	res := Deregister(context.Background(), store, dir, "repo", "o/r", "alpha")
	if res.Done {
		t.Fatal("API 报 401 时不能算注销成功")
	}
	if res.MessageKey != "github.dereg.lookup_failed" {
		t.Fatalf("键应为 github.dereg.lookup_failed，实际 %q", res.MessageKey)
	}
	if len(res.MessageArgs) != 2 || !strings.Contains(fmt.Sprint(res.MessageArgs[0]), "401") ||
		res.MessageArgs[1] != "alpha" {
		t.Fatalf("参数里应带上 401 与 Runner 名，实际 %v", res.MessageArgs)
	}
}

// 删除接口返回 404：说明它已经不在了，同样算达成
func TestDeregister_DeleteReturning404IsDone(t *testing.T) {
	dir, store := withToken(t, "pat")
	fakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = fmt.Fprint(w, runnersJSON(1, "alpha"))
	})
	res := Deregister(context.Background(), store, dir, "repo", "o/r", "alpha")
	if !res.Done {
		t.Fatalf("DELETE 返回 404 说明已不存在，应视为完成，得到 %+v", res)
	}
}

// GitHub 的 busy 字段就是「这个 Runner 正在跑 Job」，要能落到状态文件里
func TestRun_BusyRunnerIsRecorded(t *testing.T) {
	dir, store := withToken(t, "pat")
	fakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, busyRunnersJSON(true, "alpha"))
	})
	Run(runCfgIn(dir, "alpha", "repo", "o/r"), store)

	if busy := busyOf(t, dir); busy == nil || !*busy {
		t.Fatalf("GitHub 说它忙，应记为忙碌，得到 %v", busy)
	}
}

func TestRun_IdleRunnerIsRecordedAsNotBusy(t *testing.T) {
	dir, store := withToken(t, "pat")
	fakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, busyRunnersJSON(false, "alpha"))
	})
	Run(runCfgIn(dir, "alpha", "repo", "o/r"), store)

	if busy := busyOf(t, dir); busy == nil || *busy {
		t.Fatalf("GitHub 说它不忙，应记为空闲，得到 %v", busy)
	}
}

// 「没查到这个 Runner」不等于「它不忙」。写成 false 会让界面对一个
// GitHub 上根本不存在的 Runner 打包票说它空闲——和 registered 那个三态同一个错。
func TestRun_BusyIsUnknownWhenRunnerAbsent(t *testing.T) {
	dir, store := withToken(t, "pat")
	fakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, runnersJSON(1, "别的 runner"))
	})
	Run(runCfgIn(dir, "alpha", "repo", "o/r"), store)

	if busy := busyOf(t, dir); busy != nil {
		t.Fatalf("GitHub 上没有这个 Runner，忙碌状态应为未知，得到 %v", *busy)
	}
}

// 查询本身失败时同理：不知道就是不知道
func TestRun_BusyIsUnknownWhenCheckFails(t *testing.T) {
	dir, store := withToken(t, "pat")
	fakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	Run(runCfgIn(dir, "alpha", "repo", "o/r"), store)

	if busy := busyOf(t, dir); busy != nil {
		t.Fatalf("查询失败时忙碌状态应为未知，得到 %v", *busy)
	}
}

// ---- 旧位置的 PAT：读取前先搬走 ----

// 用户照旧文档把 PAT 放进 Runner 目录的，检查照样要能用上它——
// 但用完之后那个文件不能还留在挂载进容器的目录里。
func TestRun_MigratesLegacyTokenAndStillAuthenticates(t *testing.T) {
	dir, store := withLegacyToken(t, "legacy-pat")
	var auth []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = append(auth, r.Header.Get("Authorization"))
		_, _ = fmt.Fprint(w, runnersJSON(1, "alpha"))
	}))
	t.Cleanup(srv.Close)
	orig := apiBase
	apiBase = srv.URL
	t.Cleanup(func() { apiBase = orig })

	Run(runCfgIn(dir, "alpha", "repo", "o/r"), store)

	// 1) 旧位置的令牌确实被用上了
	if len(auth) == 0 {
		t.Fatal("没有发起请求，说明旧位置的 PAT 没被读到")
	}
	if auth[0] != "Bearer legacy-pat" {
		t.Fatalf("请求应带着旧位置的 PAT，实际 Authorization=%q", auth[0])
	}
	// 2) 旧文件已经不在了——这才是本 PR 要关掉的那个暴露
	legacy := filepath.Join(dir, secrets.LegacyRunnerTokenFile)
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatalf("旧位置 %s 仍然存在（err=%v），容器里的 Job 还能读到它", legacy, err)
	}
	// 3) 令牌搬到了新位置
	if got := store.GitHubToken("alpha"); got != "legacy-pat" {
		t.Fatalf("新位置应有该令牌，得到 %q", got)
	}
	// 4) 检查结果照旧写出
	if reg, _, _ := statusOf(t, dir); reg == nil || !*reg {
		t.Fatalf("应记为已登记，得到 %v", reg)
	}
}

// 删除 Runner 时的注销同理：旧位置的 PAT 仍然管用，用完旧文件也不再留着
func TestDeregister_MigratesLegacyToken(t *testing.T) {
	dir, store := withLegacyToken(t, "legacy-pat")
	var auth []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = append(auth, r.Header.Get("Authorization"))
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		_, _ = fmt.Fprint(w, runnersJSON(1, "alpha"))
	}))
	t.Cleanup(srv.Close)
	orig := apiBase
	apiBase = srv.URL
	t.Cleanup(func() { apiBase = orig })

	res := Deregister(context.Background(), store, dir, "repo", "o/r", "alpha")
	if !res.Done {
		t.Fatalf("应注销成功，得到 %+v", res)
	}
	for _, a := range auth {
		if a != "Bearer legacy-pat" {
			t.Fatalf("请求应带着旧位置的 PAT，实际 %q", a)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, secrets.LegacyRunnerTokenFile)); !os.IsNotExist(err) {
		t.Fatalf("旧位置仍然存在（err=%v）", err)
	}
}

// 401 要指向新位置：用户得知道该去改哪个文件
func TestRun_UnauthorizedNamesTheNewLocation(t *testing.T) {
	dir, store := withToken(t, "pat")
	fakeAPI(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	Run(runCfgIn(dir, "alpha", "repo", "o/r"), store)

	_, _, errMsg := statusOf(t, dir)
	wantPath, err := store.Path("alpha")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errMsg, wantPath) {
		t.Fatalf("401 的说明应指向 %s，得到 %q", wantPath, errMsg)
	}
	if strings.Contains(errMsg, secrets.LegacyRunnerTokenFile) {
		t.Fatalf("不应再把用户指向 Runner 目录下的旧文件，得到 %q", errMsg)
	}
}

// store 为 nil 时不该 panic，也不该去打 API
func TestRun_NilStoreDoesNothing(t *testing.T) {
	dir, _ := withToken(t, "pat")
	fakeAPI(t, func(http.ResponseWriter, *http.Request) {
		t.Error("没有 store 就没有凭据，不该发起请求")
	})
	Run(runCfgIn(dir, "alpha", "repo", "o/r"), nil)
	if reg, _, _ := statusOf(t, dir); reg != nil {
		t.Fatalf("不该写出状态，得到 %v", reg)
	}
}

func TestDeregister_NilStoreNamesTheNewLocation(t *testing.T) {
	dir, _ := withToken(t, "pat")
	res := Deregister(context.Background(), nil, dir, "repo", "o/r", "alpha")
	if res.Done {
		t.Fatal("没有凭据不可能注销成功")
	}
	if res.MessageKey != "github.dereg.no_pat" {
		t.Fatalf("应回报 no_pat，得到 %q", res.MessageKey)
	}
	if len(res.MessageArgs) != 2 || res.MessageArgs[0] != secrets.PathDescription {
		t.Fatalf("参数应指向新位置的通用写法，得到 %v", res.MessageArgs)
	}
}

// TokenForRunner 是本包对外的读取入口：新位置直接读，旧位置先搬再读
func TestTokenForRunner(t *testing.T) {
	t.Run("新位置", func(t *testing.T) {
		dir, store := withToken(t, "pat")
		if got := TokenForRunner(store, "alpha", dir); got != "pat" {
			t.Fatalf("得到 %q", got)
		}
	})
	t.Run("旧位置先迁移", func(t *testing.T) {
		dir, store := withLegacyToken(t, "legacy-pat")
		if got := TokenForRunner(store, "alpha", dir); got != "legacy-pat" {
			t.Fatalf("得到 %q", got)
		}
		if _, err := os.Stat(filepath.Join(dir, secrets.LegacyRunnerTokenFile)); !os.IsNotExist(err) {
			t.Fatalf("旧文件应已被搬走（err=%v）", err)
		}
	})
	t.Run("都没有时为空串", func(t *testing.T) {
		dir, store := withToken(t, "")
		if got := TokenForRunner(store, "alpha", dir); got != "" {
			t.Fatalf("得到 %q", got)
		}
	})
	t.Run("store 为 nil 时为空串", func(t *testing.T) {
		dir, _ := withToken(t, "pat")
		if got := TokenForRunner(nil, "alpha", dir); got != "" {
			t.Fatalf("得到 %q", got)
		}
	})
}
