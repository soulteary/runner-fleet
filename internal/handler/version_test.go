package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
)

func getVersionBody(t *testing.T) (int, string) {
	t.Helper()
	e := echo.New()
	e.GET("/version", VersionInfo)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/version", nil))
	return rec.Code, strings.TrimSpace(rec.Body.String())
}

// /version 在没配 Basic Auth 时是完全公开的。构建细节——尤其 go_version——
// 能让任何人把一条已公布的 Go 运行时 CVE 对到确切的运行时版本上，所以这个端点
// 只给版本号。commit 与构建时间只走 -version 命令行，那是本机执行、不对外。
//
// version-kit 默认就是这份精简取值（HandlerConfig.IncludeBuildDetails 默认 false），
// 这条用例守住「将来没人顺手把它打开」。
func TestVersionInfo_DoesNotLeakBuildDetails(t *testing.T) {
	Version, Commit, BuildDate = "v9.9.9", "deadbeefdeadbeef", "2026-01-02T03:04:05Z" // version-check-ignore
	defer func() { Version, Commit, BuildDate = "", "", "" }()

	code, body := getVersionBody(t)
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("响应不是 JSON: %v (%s)", err, body)
	}
	if m["version"] != "v9.9.9" { // version-check-ignore
		t.Errorf("version = %v，期望 v9.9.9", m["version"]) // version-check-ignore
	}
	for _, leaked := range []string{"commit", "build_date", "go_version", "platform", "compiler"} {
		if v, ok := m[leaked]; ok {
			t.Errorf("公开端点泄漏了 %s = %v", leaked, v)
		}
	}
	if len(m) != 1 {
		t.Errorf("响应应当只有 version 一个字段，实际 %v", m)
	}
}

// 响应体必须与改动前逐字节相同：这是个公开 HTTP 接口，外部监控可能在读它。
func TestVersionInfo_BodyUnchangedFromHandWrittenVersion(t *testing.T) {
	cases := []struct{ version, want string }{
		{"v9.9.9", `{"version":"v9.9.9"}`}, // version-check-ignore
		{"", `{"version":"dev"}`},          // 未注入时回落 dev，与改动前一致
	}
	for _, tc := range cases {
		Version, Commit, BuildDate = tc.version, "abc123", "2026-01-02T03:04:05Z"
		code, body := getVersionBody(t)
		if code != http.StatusOK || body != tc.want {
			t.Errorf("Version=%q -> %d %s，期望 200 %s", tc.version, code, body, tc.want)
		}
	}
	Version, Commit, BuildDate = "", "", ""
}

// -version 走的是另一条路：那里**要**有构建信息，否则拿到一个二进制没法知道它出自哪个提交。
func TestBuildInfo_CarriesProvenanceForCLI(t *testing.T) {
	Version, Commit, BuildDate = "v9.9.9", "d5c0e6024af4", "2026-01-02T03:04:05Z" // version-check-ignore
	defer func() { Version, Commit, BuildDate = "", "", "" }()

	full := BuildInfo().Full()
	for _, want := range []string{"v9.9.9", "d5c0e60", "2026-01-02T03:04:05Z", "go1."} { // version-check-ignore
		if !strings.Contains(full, want) {
			t.Errorf("-version 输出里没有 %q:\n%s", want, full)
		}
	}
}

func TestBuildInfo_EmptyVersionFallsBackToDev(t *testing.T) {
	Version, Commit, BuildDate = "", "", ""
	if got := BuildInfo().Version; got != "dev" {
		t.Fatalf("Version 未注入时应为 dev，得到 %q", got)
	}
}
