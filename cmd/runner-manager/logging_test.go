package main

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/labstack/echo/v4"
	logkit "github.com/soulteary/logger-kit/v3"
)

// syncBuf：日志可能从请求处理的 goroutine 里写出来，-race 下不加锁会直接报数据竞争。
type syncBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuf) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuf) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// 装好日志并打一个请求，返回捕获到的输出。
func captureLog(t *testing.T, format, target string, status int) string {
	t.Helper()
	t.Setenv("LOG_FORMAT", format)
	t.Setenv("LOG_LEVEL", "debug")

	out := &syncBuf{}
	setupLoggingTo(out)
	t.Cleanup(restoreStdlog)

	e := echo.New()
	e.Use(requestLogger())
	e.Any("/*", func(c echo.Context) error { return c.String(status, "body") })
	e.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, target, nil))
	return out.String()
}

// 换掉 Echo 自带 RequestLogger 的头号理由：它把整个 uri 原样打进日志，
// 查询串里的 token 会明文落盘。logger-kit 按既定规则脱敏。
func TestRequestLogger_RedactsSensitiveQueryParams(t *testing.T) {
	got := captureLog(t, "json", "/api/runners?token=SECRET123&q=ok", http.StatusOK)
	if strings.Contains(got, "SECRET123") {
		t.Errorf("敏感查询参数明文落进了日志:\n%s", got)
	}
	if !strings.Contains(got, "token=***") {
		t.Errorf("期望 token 被替换成 ***，实际:\n%s", got)
	}
	// 非敏感参数不该被误伤
	if !strings.Contains(got, "q=ok") {
		t.Errorf("普通查询参数不该被脱敏:\n%s", got)
	}
}

// Echo 的 RequestLogger 一直打印 request_id=""——它只读请求头里的 X-Request-Id，
// 没有就空着。logger-kit 会自己生成一个，这才是它能用来串联一次请求的前提。
func TestRequestLogger_AlwaysHasRequestID(t *testing.T) {
	var m map[string]any
	line := firstJSONLine(t, captureLog(t, "json", "/api/runners", http.StatusOK))
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		t.Fatalf("不是 JSON: %v\n%s", err, line)
	}
	id, _ := m["request_id"].(string)
	if id == "" {
		t.Errorf("request_id 为空，日志串不起来:\n%s", line)
	}
}

// 走 echo.WrapMiddleware 适配 net/http 中间件时最容易出问题的一点：
// 状态码是从被包装的 ResponseWriter 上读的，接错了就会永远记成 200。
func TestRequestLogger_CapturesRealStatusCode(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusTeapot, http.StatusInternalServerError} {
		line := firstJSONLine(t, captureLog(t, "json", "/api/runners", status))
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("不是 JSON: %v\n%s", err, line)
		}
		if got := int(m["status"].(float64)); got != status {
			t.Errorf("记录的状态码 %d，实际 %d", got, status)
		}
	}
}

// 探针与 Prometheus 抓取每几秒一次，进日志只会把真正有用的行淹掉。
//
// /metrics 这条是两个 PR 合流后才显形的：logger 侧原本只跳过两个探针，
// 而 Prometheus 默认 15s 抓一次 /metrics，不跳过的话请求日志里绝大多数行都是它。
func TestRequestLogger_SkipsProbeEndpoints(t *testing.T) {
	for _, p := range []string{"/health", "/ready", metricsPath} {
		if got := captureLog(t, "json", p, http.StatusOK); strings.Contains(got, `"path":"`+p+`"`) {
			t.Errorf("%s 不该进请求日志:\n%s", p, got)
		}
	}
	// 反向：普通路径必须进
	if got := captureLog(t, "json", "/api/runners", http.StatusOK); !strings.Contains(got, `"path":"/api/runners"`) {
		t.Errorf("普通路径应当记录:\n%s", got)
	}
}

// 仓库里还有 34 处 log.Printf。它们必须和请求日志走同一套格式与级别，
// 否则同一份输出里两种格式交替出现，机器没法解析。
func TestStdlibLogGoesThroughTheSameLogger(t *testing.T) {
	t.Setenv("LOG_FORMAT", "json")
	t.Setenv("LOG_LEVEL", "debug")
	out := &syncBuf{}
	setupLoggingTo(out)
	t.Cleanup(restoreStdlog)

	log.Printf("配置已加载: %s", "config.yaml")

	line := firstJSONLine(t, out.String())
	var m map[string]any
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		t.Fatalf("log.Printf 没走 JSON 格式: %v\n%s", err, line)
	}
	// 关键：必须带级别。直接把 lg.Zerolog() 交给 log.SetOutput 的话这里会缺 level，
	// console 下显示成 "???"，按级别过滤时这 34 行会整段漏掉。
	if m["level"] != "info" {
		t.Errorf("level = %v，期望 info（stdlogWriter 的存在理由）：%s", m["level"], line)
	}
	if m["message"] != "配置已加载: config.yaml" {
		t.Errorf("message = %v", m["message"])
	}
	if m["service"] != serviceName {
		t.Errorf("service = %v，期望 %s", m["service"], serviceName)
	}
	// log.Printf 带的结尾换行不该变成日志里的空行
	if s, _ := m["message"].(string); strings.HasSuffix(s, "\n") {
		t.Errorf("message 结尾多了换行: %q", s)
	}
}

// 默认保持 console：现在的输出是人在终端里读的，
// 默认切成 JSON 等于让所有看日志的人先吃一记退化。JSON 是 opt-in。
func TestParseLogFormat_DefaultsToConsole(t *testing.T) {
	cases := []struct {
		in   string
		want logkit.Format
	}{
		{"", logkit.FormatConsole},
		{"console", logkit.FormatConsole},
		{"json", logkit.FormatJSON},
		{"JSON", logkit.FormatJSON},
		{"  json  ", logkit.FormatJSON},
		{"什么鬼", logkit.FormatConsole}, // 配错了不该让服务起不来
	}
	for _, tc := range cases {
		if got := parseLogFormat(tc.in); got != tc.want {
			t.Errorf("parseLogFormat(%q) = %v，期望 %v", tc.in, got, tc.want)
		}
	}
}

func TestParseLogLevel(t *testing.T) {
	cases := []struct {
		in   string
		want logkit.Level
	}{
		{"", logkit.InfoLevel},
		{"debug", logkit.DebugLevel},
		{"warn", logkit.WarnLevel},
		{"error", logkit.ErrorLevel},
		{"  debug  ", logkit.DebugLevel},
		{"什么鬼", logkit.InfoLevel}, // 同上，回落而不是报错
	}
	for _, tc := range cases {
		if got := parseLogLevel(tc.in); got != tc.want {
			t.Errorf("parseLogLevel(%q) = %v，期望 %v", tc.in, got, tc.want)
		}
	}
}

// restoreStdlog 把标准库 log 复位。
//
// 必须复位成 os.Stderr 而不是 nil：setupLoggingTo 改的是进程级的全局状态，
// 用例结束后仓库里其余 34 处 log.Printf 仍会写——交给 nil 会直接 panic，
// 表现为「单跑通过、全量跑挂在另一个用例上」。
func restoreStdlog() {
	log.SetOutput(os.Stderr)
	log.SetFlags(log.LstdFlags)
}

// 取第一行能解析成 JSON 的输出。
func firstJSONLine(t *testing.T, out string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var probe map[string]any
		if json.Unmarshal([]byte(line), &probe) == nil {
			return line
		}
	}
	t.Fatalf("输出里没有 JSON 行:\n%s", out)
	return ""
}
