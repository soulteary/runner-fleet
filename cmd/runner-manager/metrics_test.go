package main

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
)

// 搭一个带指标中间件的 Echo，路由表与真实服务同形（含 :name 这种参数路由）。
func metricsApp() *echo.Echo {
	e := echo.New()
	e.Use(metricsMiddleware())
	e.GET("/api/runners", func(c echo.Context) error { return c.String(http.StatusOK, "list") })
	e.GET("/api/runners/:name", func(c echo.Context) error { return c.String(http.StatusOK, "one") })
	e.GET("/boom", func(c echo.Context) error {
		return echo.NewHTTPError(http.StatusBadGateway, "炸了")
	})
	e.GET("/health", func(c echo.Context) error { return c.String(http.StatusOK, "ok") })
	e.GET("/ready", func(c echo.Context) error { return c.String(http.StatusOK, "ok") })
	e.GET(metricsPath, metricsHandler())
	return e
}

func get(e *echo.Echo, target string) int {
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	return rec.Code
}

// 抓一次 /metrics，返回文本。
func scrape(t *testing.T, e *echo.Echo) string {
	t.Helper()
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, metricsPath, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("/metrics 返回 %d，期望 200", rec.Code)
	}
	return rec.Body.String()
}

// counterValue 把所有匹配 labels 的样本**加起来**。
//
// 必须求和而不是取第一条：httpMetrics 是包级变量，同一个进程里所有用例共用一份
// registry，别的用例（csrf_test、wiring_test 里那些）也会打到 /api/runners/:name，
// 只是状态码不同。只取第一条匹配行的话，拿到的是别人留下的计数。
func counterValue(t *testing.T, body, metric string, labels map[string]string) float64 {
	t.Helper()
	sum := 0.0
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, metric+"{") {
			continue
		}
		ok := true
		for k, v := range labels {
			if !strings.Contains(line, k+`="`+v+`"`) {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		fields := strings.Fields(line)
		v, err := strconv.ParseFloat(fields[len(fields)-1], 64)
		if err != nil {
			t.Fatalf("解析不了这行: %s", line)
		}
		sum += v
	}
	return sum
}

// 最基本的一条：请求打完，/metrics 上真的能抓到对应的样本。
//
// 这条特意抓 /metrics 而不是直接读 registry —— 它同时证明了
// metricsHandler 用的是 HandlerFor(httpMetrics.Registry) 而不是 Handler()。
// NewHTTPMetrics 在 Registry 为 nil 时自建一个 registry，而 Handler() 服务的是
// **默认** registry：写成 Handler() 的话这里抓到的会是一堆 Go 运行时指标，
// 唯独没有我们记的这些。
func TestMetrics_RecordedSamplesAreScrapable(t *testing.T) {
	e := metricsApp()
	before := counterValue(t, scrape(t, e), "runner_fleet_http_requests_total",
		map[string]string{"method": "GET", "path": "/api/runners", "status": "200"})

	if code := get(e, "/api/runners"); code != http.StatusOK {
		t.Fatalf("状态码 %d", code)
	}

	after := counterValue(t, scrape(t, e), "runner_fleet_http_requests_total",
		map[string]string{"method": "GET", "path": "/api/runners", "status": "200"})
	if after == 0 {
		t.Fatalf("抓不到 runner_fleet_http_requests_total，/metrics 内容:\n%s", scrape(t, e))
	}
	if after != before+1 {
		t.Errorf("计数 %v -> %v，期望 +1", before, after)
	}
}

// 基数控制，这个 PR 里最要紧的一条。
//
// path 标签取的是 Echo 的**路由模板**，不是请求 URL。三个不同 runner 名
// 必须落在同一条时间序列上；否则每建一个 runner 就多一条序列，Prometheus 会被撑爆。
func TestMetrics_PathLabelIsRouteTemplateNotRequestURL(t *testing.T) {
	e := metricsApp()
	tmpl := map[string]string{"path": "/api/runners/:name"}
	before := counterValue(t, scrape(t, e), "runner_fleet_http_requests_total", tmpl)

	for _, name := range []string{"alpha", "bravo", "charlie"} {
		if code := get(e, "/api/runners/"+name); code != http.StatusOK {
			t.Fatalf("%s 状态码 %d", name, code)
		}
	}
	body := scrape(t, e)

	if got := counterValue(t, body, "runner_fleet_http_requests_total", tmpl) - before; got != 3 {
		t.Errorf("三个不同 runner 名应合并到 :name 模板上、净增 3，实得 %v", got)
	}
	for _, name := range []string{"alpha", "bravo", "charlie"} {
		if strings.Contains(body, `path="/api/runners/`+name+`"`) {
			t.Errorf("具体的 runner 名 %q 变成了标签值，基数会爆:\n%s", name, body)
		}
	}
}

// 404 同理，而且更危险：任何人扫一遍随机 URL 就能造序列。
func TestMetrics_UnmatchedRoutesCollapseToOneLabel(t *testing.T) {
	e := metricsApp()
	nf := map[string]string{"path": notFoundPathLabel}
	before := counterValue(t, scrape(t, e), "runner_fleet_http_requests_total", nf)

	for _, p := range []string{"/nope-1", "/nope-2", "/admin/../etc/passwd"} {
		get(e, p)
	}
	body := scrape(t, e)

	if got := counterValue(t, body, "runner_fleet_http_requests_total", nf) - before; got != 3 {
		t.Errorf("未匹配路由应合并到 %q、净增 3，实得 %v", notFoundPathLabel, got)
	}
	for _, p := range []string{"nope-1", "nope-2", "passwd"} {
		if strings.Contains(body, p) {
			t.Errorf("未匹配的 URL 片段 %q 进了标签:\n%s", p, body)
		}
	}
}

// 错误状态码必须记成真实状态码。
//
// Echo 的 HTTPErrorHandler 是在处理链返回**之后**才写响应的，
// 那时 c.Response().Status 还停在 200——直接读它就会把所有失败请求记成成功，
// 基于 status=~"5.." 的告警永远不会触发。中间件因此从返回的 error 上取码。
func TestMetrics_ErrorStatusIsRecordedNotDefaultedTo200(t *testing.T) {
	e := metricsApp()
	if code := get(e, "/boom"); code != http.StatusBadGateway {
		t.Fatalf("客户端看到的状态码是 %d，期望 502", code)
	}
	body := scrape(t, e)

	if v := counterValue(t, body, "runner_fleet_http_requests_total",
		map[string]string{"path": "/boom", "status": "502"}); v < 1 {
		t.Errorf("502 没被记下来:\n%s", body)
	}
	if v := counterValue(t, body, "runner_fleet_http_requests_total",
		map[string]string{"path": "/boom", "status": "200"}); v > 0 {
		t.Errorf("失败请求被记成了 200，这会让 5xx 告警永远不触发")
	}
}

// 探针与 /metrics 自身不记：探针每几秒一次，/metrics 记自己更是没有意义。
func TestMetrics_SkipsProbesAndItself(t *testing.T) {
	e := metricsApp()
	for i := 0; i < 3; i++ {
		get(e, "/health")
		get(e, "/ready")
		get(e, metricsPath)
	}
	body := scrape(t, e)
	for _, p := range []string{"/health", "/ready", metricsPath} {
		if v := counterValue(t, body, "runner_fleet_http_requests_total",
			map[string]string{"path": p}); v != 0 {
			t.Errorf("%s 不该被记录，却有 %v 次", p, v)
		}
	}
}

// 延迟直方图也要真的有观测值，不然只有计数、没有分布。
func TestMetrics_DurationHistogramIsObserved(t *testing.T) {
	e := metricsApp()
	get(e, "/api/runners")
	body := scrape(t, e)

	if !regexp.MustCompile(`runner_fleet_http_request_duration_seconds_count\{[^}]*path="/api/runners"[^}]*\}`).MatchString(body) {
		t.Errorf("没有延迟直方图的样本:\n%s", body)
	}
}

// /metrics 不在 Skipper 里，所以配了 Basic Auth 之后它也要凭据。
// 指标会吐出全部接口的调用量与延迟，没理由比 /api 更公开。
func TestMetrics_EndpointIsBehindBasicAuthWhenConfigured(t *testing.T) {
	t.Setenv("BASIC_AUTH_PASSWORD", "s3cret")
	t.Setenv("BASIC_AUTH_USER", "")

	e := echo.New()
	if mw, _ := basicAuthMiddleware(); mw != nil {
		e.Use(mw)
	} else {
		t.Fatal("设了密码却没装上中间件")
	}
	e.GET(metricsPath, metricsHandler())

	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, metricsPath, nil))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("/metrics 无凭据时应 401，实得 %d", rec.Code)
	}

	req := httptest.NewRequest(http.MethodGet, metricsPath, nil)
	req.SetBasicAuth("admin", "s3cret")
	rec2 := httptest.NewRecorder()
	e.ServeHTTP(rec2, req)
	if rec2.Code != http.StatusOK {
		t.Errorf("/metrics 带正确凭据应 200，实得 %d", rec2.Code)
	}
}
