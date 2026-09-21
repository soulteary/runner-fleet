package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/soulteary/runner-fleet/internal/config"
)

// ---- listenAddr ----

func TestListenAddr(t *testing.T) {
	cases := []struct {
		name string
		cfg  *config.Config
		want string
	}{
		{"未配置端口", &config.Config{}, ":8080"},
		{"端口为负", &config.Config{Server: config.ServerConfig{Port: -1}}, ":8080"},
		{"仅端口", &config.Config{Server: config.ServerConfig{Port: 9000}}, ":9000"},
		{"地址加端口", &config.Config{Server: config.ServerConfig{Addr: "127.0.0.1", Port: 9000}}, "127.0.0.1:9000"},
		{"配置为空", nil, ":8080"},
		// 只填 addr 不填端口时仍回落到 :8080——addr 单独出现无法构成监听地址
		{"仅地址", &config.Config{Server: config.ServerConfig{Addr: "127.0.0.1"}}, ":8080"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := listenAddr(tc.cfg); got != tc.want {
				t.Fatalf("listenAddr = %q，期望 %q", got, tc.want)
			}
		})
	}
}

// ---- httpErrorHandler ----

func TestHTTPErrorHandler(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		wantCode int
		wantMsg  string
	}{
		{"普通错误按 500", errors.New("boom"), http.StatusInternalServerError, "boom"},
		{"HTTPError 保留状态码与文案",
			echo.NewHTTPError(http.StatusNotFound, "未找到该 runner"), http.StatusNotFound, "未找到该 runner"},
		{"HTTPError 的 Message 不是字符串时回落到 Error()",
			echo.NewHTTPError(http.StatusBadRequest, map[string]string{"a": "b"}), http.StatusBadRequest, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c := echo.New().NewContext(httptest.NewRequest(http.MethodGet, "/", nil), rec)
			httpErrorHandler(tc.err, c)
			if rec.Code != tc.wantCode {
				t.Fatalf("状态码 %d，期望 %d", rec.Code, tc.wantCode)
			}
			var body map[string]string
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("响应不是 JSON: %s", rec.Body.String())
			}
			if _, ok := body["message"]; !ok {
				t.Fatalf("响应里应有 message 字段: %s", rec.Body.String())
			}
			if tc.wantMsg != "" && body["message"] != tc.wantMsg {
				t.Fatalf("message = %q，期望 %q", body["message"], tc.wantMsg)
			}
		})
	}
}

// ---- Basic Auth ----

// serveWithAuth 挂上鉴权中间件（若有）跑一个请求
func serveWithAuth(t *testing.T, path, user, pass string, withCreds bool) int {
	t.Helper()
	e := echo.New()
	if mw, _ := basicAuthMiddleware(); mw != nil {
		e.Use(mw)
	}
	e.GET("/health", func(c echo.Context) error { return c.String(http.StatusOK, "ok") })
	e.GET("/ready", func(c echo.Context) error { return c.String(http.StatusOK, "ready") })
	e.GET("/api/runners", func(c echo.Context) error { return c.String(http.StatusOK, "list") })
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if withCreds {
		req.SetBasicAuth(user, pass)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec.Code
}

// 不设 BASIC_AUTH_PASSWORD 时整个中间件都不挂载，所有接口无凭据可访问。
// 这是刻意保留的零配置起步行为，不是遗漏——用例把它写明，
// 将来若要改成「无密码则拒绝启动」，失败的会是这一条，意图一目了然。
func TestBasicAuthMiddleware_NoPasswordMeansNoAuthAtAll(t *testing.T) {
	t.Setenv("BASIC_AUTH_PASSWORD", "")
	t.Setenv("BASIC_AUTH_USER", "")
	if mw, user := basicAuthMiddleware(); mw != nil || user != "" {
		t.Fatalf("没有密码时不应构造中间件，得到 mw=%v user=%q", mw != nil, user)
	}
	if code := serveWithAuth(t, "/api/runners", "", "", false); code != http.StatusOK {
		t.Fatalf("没有密码时接口应无凭据可访问，状态码 %d", code)
	}
}

func TestBasicAuthMiddleware_RejectsWrongCredentials(t *testing.T) {
	t.Setenv("BASIC_AUTH_PASSWORD", "s3cret")
	t.Setenv("BASIC_AUTH_USER", "")
	cases := []struct {
		name      string
		user      string
		pass      string
		withCreds bool
		want      int
	}{
		{"不带凭据", "", "", false, http.StatusUnauthorized},
		{"密码错", "admin", "wrong", true, http.StatusUnauthorized},
		{"用户名错", "root", "s3cret", true, http.StatusUnauthorized},
		{"都对", "admin", "s3cret", true, http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if code := serveWithAuth(t, "/api/runners", tc.user, tc.pass, tc.withCreds); code != tc.want {
				t.Fatalf("状态码 %d，期望 %d", code, tc.want)
			}
		})
	}
}

// 用户名缺省为 admin，设了就用设的
func TestBasicAuthMiddleware_UserDefaultsToAdmin(t *testing.T) {
	t.Setenv("BASIC_AUTH_PASSWORD", "s3cret")
	t.Setenv("BASIC_AUTH_USER", "")
	if _, user := basicAuthMiddleware(); user != "admin" {
		t.Fatalf("缺省用户名应为 admin，得到 %q", user)
	}
	t.Setenv("BASIC_AUTH_USER", "  ops  ") // 前后空白要被裁掉，否则谁也登不进来
	if _, user := basicAuthMiddleware(); user != "ops" {
		t.Fatalf("用户名应为 ops，得到 %q", user)
	}
	if code := serveWithAuth(t, "/api/runners", "ops", "s3cret", true); code != http.StatusOK {
		t.Fatalf("自定义用户名应能通过，状态码 %d", code)
	}
}

// /health 与 /ready 给 Ingress / K8s 探针用，必须免鉴权；其余路径一概不免。
// 探针配置里通常带不了 Basic Auth 凭据，漏放行就是「开了鉴权之后探针全红」。
func TestBasicAuthMiddleware_ProbesAreAlwaysOpen(t *testing.T) {
	t.Setenv("BASIC_AUTH_PASSWORD", "s3cret")
	t.Setenv("BASIC_AUTH_USER", "")
	for _, path := range []string{"/health", "/ready"} {
		if code := serveWithAuth(t, path, "", "", false); code != http.StatusOK {
			t.Errorf("%s 应免鉴权，状态码 %d", path, code)
		}
	}
	if code := serveWithAuth(t, "/api/runners", "", "", false); code != http.StatusUnauthorized {
		t.Fatalf("除探针端点外都应要求鉴权，状态码 %d", code)
	}
}

// ---- 路由表 ----

func TestRegisterRoutes_AllEndpointsPresent(t *testing.T) {
	e := echo.New()
	registerRoutes(e)
	got := map[string]bool{}
	for _, r := range e.Routes() {
		got[r.Method+" "+r.Path] = true
	}
	want := []string{
		"GET /health",
		"GET /ready",
		"GET /version",
		"GET /",
		"GET /api/runners",
		"GET /api/runners/:name",
		"POST /api/runners",
		"GET /api/runner-precheck",
		"PUT /api/runners/:name",
		"DELETE /api/runners/:name",
		"POST /api/runners/:name/start",
		"POST /api/runners/:name/stop",
		"POST /api/runners/:name/recreate",
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("路由缺失: %s", w)
		}
	}
	if len(e.Routes()) != len(want) {
		t.Errorf("路由数为 %d，期望 %d；新增路由请同步本用例（顺带确认它是否需要鉴权）", len(e.Routes()), len(want))
	}
}

// 名为 check 的 Runner 不能把 /api/runner-precheck 抢走——
// 这正是 precheck 被放在 /api/runners/:name 之外的原因
func TestRegisterRoutes_PrecheckNotShadowedByRunnerName(t *testing.T) {
	e := echo.New()
	registerRoutes(e)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/runner-precheck?name=x", nil))
	if rec.Code == http.StatusNotFound {
		t.Fatalf("precheck 路由应命中，得到 404: %s", rec.Body.String())
	}
}
