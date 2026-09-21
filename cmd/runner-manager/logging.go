package main

import (
	"io"
	"log"
	"os"
	"strings"

	"github.com/labstack/echo/v4"
	logkit "github.com/soulteary/logger-kit/v3"
)

// serviceName 会写进每条日志的 service 字段。
const serviceName = "runner-fleet"

// setupLogging 装配日志：按环境变量决定级别与格式，并把标准库 log 的输出接过来。
//
// 接管 log 是关键一步。仓库里还有 34 处 log.Printf，如果只把请求日志换成
// logger-kit，两种格式会交替出现在同一份输出里——人读着别扭，机器更没法解析。
// zerolog.Logger 本身实现了 io.Writer，交给 log.SetOutput 即可，
// 那 34 处调用一行都不用改就自动走同一套格式与级别。
//
// 默认 console 而不是 kit 的默认 JSON：现在的输出是人读的，
// 默认切成 JSON 等于让所有在终端里看日志的人先吃一记退化。
// 要 JSON 的场合（采集到 ELK/Loki）设 LOG_FORMAT=json。
func setupLogging() *logkit.Logger { return setupLoggingTo(os.Stderr) }

// setupLoggingTo 是 setupLogging 的可测形式：输出目标由调用方给。
func setupLoggingTo(out io.Writer) *logkit.Logger {
	cfg := logkit.Config{
		Level:       parseLogLevel(os.Getenv("LOG_LEVEL")),
		Format:      parseLogFormat(os.Getenv("LOG_FORMAT")),
		Output:      out,
		ServiceName: serviceName,
	}
	lg := logkit.New(cfg)
	logkit.SetDefault(lg)

	// 标准库 log 自己会加时间戳前缀，zerolog 也会加一个——去掉前者，
	// 否则每行都是「时间 时间 内容」。
	log.SetFlags(0)
	log.SetOutput(stdlogWriter{lg})
	return lg
}

// stdlogWriter 把标准库 log 写来的每一行当作一条 Info 记录。
//
// 不直接把 lg.Zerolog() 交给 log.SetOutput：zerolog.Logger 确实实现了 io.Writer，
// 但那条路径不带级别，console 格式下打出来是 "???"，JSON 里则没有 level 字段——
// 按级别过滤日志的地方会整段漏掉这 34 行。
type stdlogWriter struct{ lg *logkit.Logger }

func (w stdlogWriter) Write(p []byte) (int, error) {
	// log.Printf 带着结尾换行来，zerolog 会自己加，留着就是空行
	w.lg.Info().Msg(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}

func parseLogLevel(v string) logkit.Level {
	if lvl, err := logkit.ParseLevel(strings.TrimSpace(v)); err == nil {
		return lvl
	}
	return logkit.InfoLevel
}

// 取值不认识时回落 console，而不是报错退出：日志格式配错了不该让服务起不来。
func parseLogFormat(v string) logkit.Format {
	if strings.EqualFold(strings.TrimSpace(v), "json") {
		return logkit.FormatJSON
	}
	return logkit.FormatConsole
}

// requestLogger 请求日志中间件。
//
// 换掉 Echo 自带的 middleware.RequestLogger()，换来的是：每条请求日志带一个
// 真实的 request_id（Echo 那个一直打印 request_id=""），以及查询串与请求头里的
// 敏感字段会被 logger-kit 按既定规则脱敏。
//
// logger-kit v3 根包只认 net/http，所以这里走 echo.WrapMiddleware 适配。
func requestLogger() echo.MiddlewareFunc {
	cfg := logkit.DefaultMiddlewareConfig()
	cfg.Logger = logkit.Default()
	// 探针每几秒打一次，进日志只会把真正有用的行淹掉
	cfg.SkipPaths = []string{"/health", "/ready"}
	return echo.WrapMiddleware(logkit.Middleware(cfg))
}
