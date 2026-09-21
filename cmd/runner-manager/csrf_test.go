package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
)

// serveWithCSRF 把 csrfGuardMiddleware 挂在一个必定 200 的处理器前，返回实际状态码。
// headers 传 nil 表示请求不带任何来源标记（curl / 脚本的形态）。
func serveWithCSRF(t *testing.T, method, host string, headers map[string]string, trusted []string) int {
	t.Helper()
	e := echo.New()
	e.HTTPErrorHandler = httpErrorHandler
	e.Use(csrfGuardMiddleware(trusted))
	handler := func(c echo.Context) error { return c.NoContent(http.StatusOK) }
	e.Add(method, "/api/runners", handler)
	e.Add(method, "/api/runners/:name/start", handler)

	req := httptest.NewRequest(method, "/api/runners", strings.NewReader(`{"name":"a"}`))
	req.Host = host
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec.Code
}

func TestCSRFGuardSafeMethodsAlwaysPass(t *testing.T) {
	// 读接口不改变状态，跨站读取本就受同源策略约束，不该被这里挡掉
	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
		t.Run(method, func(t *testing.T) {
			got := serveWithCSRF(t, method, "localhost:8080", map[string]string{
				"Sec-Fetch-Site": "cross-site",
				"Origin":         "https://evil.example",
			}, nil)
			if got != http.StatusOK {
				t.Fatalf("%s 被拒绝（%d），安全方法应始终放行", method, got)
			}
		})
	}
}

func TestCSRFGuardSecFetchSite(t *testing.T) {
	cases := []struct {
		site     string
		wantCode int
	}{
		{"same-origin", http.StatusOK},
		// same-site 是同注册域的另一个源（子域、别的端口）——对内网管理端一律不认
		{"same-site", http.StatusForbidden},
		{"cross-site", http.StatusForbidden},
		// none 表示用户直接导航（地址栏、书签），写请求不该以这种方式发起
		{"none", http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.site, func(t *testing.T) {
			got := serveWithCSRF(t, http.MethodPost, "localhost:8080",
				map[string]string{"Sec-Fetch-Site": tc.site}, nil)
			if got != tc.wantCode {
				t.Fatalf("Sec-Fetch-Site=%s 得到 %d，期望 %d", tc.site, got, tc.wantCode)
			}
		})
	}
}

// Sec-Fetch-Site 比 Origin 可靠（浏览器本地计算，不受反代改写 Host 影响），
// 所以它在场时说了算：哪怕 Origin 看上去同源，cross-site 也要拒。
func TestCSRFGuardSecFetchSiteWinsOverOrigin(t *testing.T) {
	got := serveWithCSRF(t, http.MethodPost, "localhost:8080", map[string]string{
		"Sec-Fetch-Site": "cross-site",
		"Origin":         "http://localhost:8080",
	}, nil)
	if got != http.StatusForbidden {
		t.Fatalf("得到 %d，期望 403：Sec-Fetch-Site 在场时应以它为准", got)
	}
}

// 老浏览器（Safari 16.4 以前）不发 Sec-Fetch-Site，但跨站 POST 一定带 Origin
func TestCSRFGuardOriginFallback(t *testing.T) {
	cases := []struct {
		name     string
		host     string
		origin   string
		wantCode int
	}{
		{"同源", "localhost:8080", "http://localhost:8080", http.StatusOK},
		{"同源带末尾斜杠", "localhost:8080", "http://localhost:8080/", http.StatusOK},
		{"端口不同即跨源", "localhost:8080", "http://localhost:9090", http.StatusForbidden},
		{"主机不同", "localhost:8080", "https://evil.example", http.StatusForbidden},
		// 子域不算同源：evil 可以在 sub.localhost 上落脚
		{"子域", "localhost:8080", "http://sub.localhost:8080", http.StatusForbidden},
		{"Origin 无法解析", "localhost:8080", "://not a url", http.StatusForbidden},
		// 浏览器对不透明来源发 "null"，不该当成同源
		{"null 来源", "localhost:8080", "null", http.StatusForbidden},
		// 协议不参与比较：反代做 TLS 终止时 Host 里没有 scheme 可比
		{"仅协议不同", "localhost:8080", "https://localhost:8080", http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := serveWithCSRF(t, http.MethodPost, tc.host,
				map[string]string{"Origin": tc.origin}, nil)
			if got != tc.wantCode {
				t.Fatalf("Host=%s Origin=%s 得到 %d，期望 %d", tc.host, tc.origin, got, tc.wantCode)
			}
		})
	}
}

// 既无 Sec-Fetch-Site 也无 Origin 的请求不是浏览器发的：它不持有用户的
// Basic Auth 缓存，构不成 CSRF。挡掉它只会让 API 无法脚本化。
func TestCSRFGuardNonBrowserRequestPasses(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			if got := serveWithCSRF(t, method, "localhost:8080", nil, nil); got != http.StatusOK {
				t.Fatalf("%s 无来源标记时得到 %d，期望放行", method, got)
			}
		})
	}
}

func TestCSRFGuardTrustedOriginsEscapeHatch(t *testing.T) {
	trusted := []string{"https://ui.example.com"}
	cases := []struct {
		name     string
		headers  map[string]string
		wantCode int
	}{
		{"白名单来源即便被判跨站也放行",
			map[string]string{"Origin": "https://ui.example.com", "Sec-Fetch-Site": "cross-site"},
			http.StatusOK},
		{"白名单比对忽略末尾斜杠",
			map[string]string{"Origin": "https://ui.example.com/", "Sec-Fetch-Site": "cross-site"},
			http.StatusOK},
		{"不在白名单上的来源照旧拒绝",
			map[string]string{"Origin": "https://evil.example", "Sec-Fetch-Site": "cross-site"},
			http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := serveWithCSRF(t, http.MethodPost, "localhost:8080", tc.headers, trusted)
			if got != tc.wantCode {
				t.Fatalf("得到 %d，期望 %d", got, tc.wantCode)
			}
		})
	}
}

// 403 要走统一的错误格式，界面才能把原因显示出来
func TestCSRFGuardRejectionBodyNamesTheCause(t *testing.T) {
	e := echo.New()
	e.HTTPErrorHandler = httpErrorHandler
	e.Use(csrfGuardMiddleware(nil))
	e.POST("/api/runners", func(c echo.Context) error { return c.NoContent(http.StatusOK) })

	req := httptest.NewRequest(http.MethodPost, "/api/runners", nil)
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("状态码 %d，期望 403", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"message"`) {
		t.Fatalf("响应应为 {\"message\": ...}: %s", body)
	}
	if !strings.Contains(body, "TRUSTED_ORIGINS") {
		t.Fatalf("拒绝原因里应指出逃生舱 TRUSTED_ORIGINS: %s", body)
	}
}

func TestTrustedOrigins(t *testing.T) {
	cases := []struct {
		name string
		env  string
		want []string
	}{
		{"未设置", "", nil},
		{"只有空白", "   ", nil},
		{"单个", "https://a.example", []string{"https://a.example"}},
		{"多个并去掉空白", " https://a.example , https://b.example ",
			[]string{"https://a.example", "https://b.example"}},
		{"去掉末尾斜杠", "https://a.example/", []string{"https://a.example"}},
		{"跳过空项", "https://a.example,,", []string{"https://a.example"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("TRUSTED_ORIGINS", tc.env)
			got := trustedOrigins()
			if len(got) != len(tc.want) {
				t.Fatalf("trustedOrigins() = %v，期望 %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("trustedOrigins() = %v，期望 %v", got, tc.want)
				}
			}
		})
	}
}

// ---- 中间件是否真的挂上了 ----

// writeRoutes 是所有会改变状态、且不触发 CORS 预检的路由。
// 预检方法（PUT / DELETE）浏览器本就发不出跨站的简单请求，但一并盯着，
// 免得哪天有人把它们改成 POST 又忘了这层。
var writeRoutes = []struct {
	method string
	path   string
}{
	{http.MethodPost, "/api/runners"},
	{http.MethodPost, "/api/runners/demo/start"},
	{http.MethodPost, "/api/runners/demo/stop"},
	{http.MethodPost, "/api/runners/demo/recreate"},
	{http.MethodPut, "/api/runners/demo"},
	{http.MethodDelete, "/api/runners/demo"},
}

// newEchoServer 会改 handler 包的全局量（ConfigPath 之外的 I18nLoader），
// 测试之间互不干扰即可，不必还原。
func serveThroughRealServer(t *testing.T, method, path string, headers map[string]string) int {
	t.Helper()
	e := newEchoServer()
	req := httptest.NewRequest(method, path, strings.NewReader(`{"name":"demo","target_type":"repo","target":"o/r"}`))
	req.Host = "localhost:8080"
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec.Code
}

// 这是这一层真正要守住的东西：中间件写对了不等于挂上了。
func TestCSRFGuardIsMountedOnWriteRoutes(t *testing.T) {
	t.Setenv("TRUSTED_ORIGINS", "")
	for _, r := range writeRoutes {
		t.Run(r.method+" "+r.path, func(t *testing.T) {
			got := serveThroughRealServer(t, r.method, r.path, map[string]string{
				"Sec-Fetch-Site": "cross-site",
				"Origin":         "https://evil.example",
			})
			if got != http.StatusForbidden {
				t.Fatalf("%s %s 跨站调用得到 %d，期望 403——CSRF 中间件没挡住这条路由",
					r.method, r.path, got)
			}
		})
	}
}

// 反过来：同源调用不该被这层挡掉。走到处理器之后因为没有真实配置多半会失败，
// 那是另一回事，这里只断言「不是被 CSRF 挡的」。
func TestCSRFGuardLetsSameOriginThrough(t *testing.T) {
	t.Setenv("TRUSTED_ORIGINS", "")
	for _, r := range writeRoutes {
		t.Run(r.method+" "+r.path, func(t *testing.T) {
			got := serveThroughRealServer(t, r.method, r.path, map[string]string{
				"Sec-Fetch-Site": "same-origin",
				"Origin":         "http://localhost:8080",
			})
			if got == http.StatusForbidden {
				t.Fatalf("%s %s 同源调用被 403 挡掉了", r.method, r.path)
			}
		})
	}
}

// 读接口不受影响，/health 也要一直可探活
func TestReadRoutesUnaffectedByCSRFGuard(t *testing.T) {
	t.Setenv("TRUSTED_ORIGINS", "")
	for _, path := range []string{"/health", "/ready", "/version"} {
		t.Run(path, func(t *testing.T) {
			got := serveThroughRealServer(t, http.MethodGet, path, map[string]string{
				"Sec-Fetch-Site": "cross-site",
			})
			if got != http.StatusOK {
				t.Fatalf("GET %s 得到 %d，期望 200", path, got)
			}
		})
	}
}
