package main

import (
	"net/http"
	"strconv"
	"time"

	"github.com/labstack/echo/v4"
	metrics "github.com/soulteary/metrics-kit/v3"
)

// metricsPath Prometheus 抓取端点。
//
// 刻意**不**放进 basicAuthMiddleware 的 Skipper：这里会吐出全部接口的调用量与
// 延迟分布，属于运维数据，没理由比 /api 更公开。Prometheus 的 scrape_config
// 支持 basic_auth，配一下即可。
const metricsPath = "/metrics"

// httpMetricsConfig 单独留一份：HTTPMetrics 不把配置暴露出来，
// 而中间件需要用到其中的 SkipPaths 与 TransformPath。
var httpMetricsConfig = metrics.HTTPMetricsConfig{
	Namespace: "runner_fleet",
	Subsystem: "http",
	SkipPaths: []string{metricsPath, "/health", "/ready"},

	// Echo 的 c.Path() 返回的是**路由模板**（/api/runners/:name），不是请求 URL，
	// 基数天然有界。kit 对这个选项的说明正是这种情形：
	// "Only safe where every path is already bounded -- a router that reports
	//  its route pattern rather than the request URL, say."
	//
	// 反过来，若沿用默认的 DefaultPathNormalize 去处理已经是模板的路径，
	// 等于对着 :name 再做一次猜测性归一化，没有意义。
	DisablePathNormalization: true,
}

// httpMetrics 本进程的 HTTP 指标。
//
// 它的 Registry 必须留着：NewHTTPMetrics 在 cfg.Registry 为 nil 时会自己建一个，
// 而 metrics.Handler() 服务的是**默认** registry——用 Handler() 的话，这里记的
// 所有指标谁也抓不到。kit 自己的注释就在讲这件事，所以下面用 HandlerFor。
var httpMetrics = metrics.NewHTTPMetrics(httpMetricsConfig)

// metricsHandler /metrics 端点。
func metricsHandler() echo.HandlerFunc {
	return echo.WrapHandler(metrics.HandlerFor(httpMetrics.Registry))
}

// notFoundPathLabel 是没有匹配到路由时用的 path 标签。
//
// 必须是个固定字符串。404 时 Echo 的 c.Path() 为空，若退回去用 c.Request().URL.Path，
// 任何人扫一遍随机 URL 就能给每个 URL 造出一条时间序列，把 Prometheus 撑爆——
// 这正是 kit 在 PathTransformFunc 注释里点名的那种情况。
const notFoundPathLabel = "<not-found>"

// metricsMiddleware 记录每个请求的调用量与延迟。
//
// metrics-kit v3 的中间件只有 Fiber 版（在 fiberadapter 子包里），
// net/http 侧 README 明说要自己调 RecordRequest，所以这一层是手写的。
// 归一化规则仍走 kit 的 TransformPath，不自己另写一套。
func metricsMiddleware() echo.MiddlewareFunc {
	skip := make(map[string]bool, len(httpMetricsConfig.SkipPaths))
	for _, p := range httpMetricsConfig.SkipPaths {
		skip[p] = true
	}
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if skip[c.Request().URL.Path] {
				return next(c)
			}
			start := time.Now()
			err := next(c)

			// 状态码要在 next 之后取，且必须考虑 HTTPErrorHandler：
			// 它在处理链返回之后才写响应，此时 c.Response().Status 尚未更新。
			status := c.Response().Status
			if err != nil {
				if he, ok := err.(*echo.HTTPError); ok {
					status = he.Code
				} else {
					status = http.StatusInternalServerError
				}
			}

			httpMetrics.RecordRequest(
				c.Request().Method,
				routeLabel(c),
				strconv.Itoa(status),
				time.Since(start),
			)
			return err
		}
	}
}

// routeLabel 取这次请求的 path 标签：路由模板，没匹配上则是固定的 <not-found>。
func routeLabel(c echo.Context) string {
	p := c.Path()
	if p == "" {
		return notFoundPathLabel
	}
	return httpMetricsConfig.TransformPath(p)
}
