package handler

import (
	"github.com/labstack/echo/v4"
	version "github.com/soulteary/version-kit/v4"
)

// 三个都由构建时 -ldflags "-X main.Xxx=..." 注入，再由 main 赋进来。
// Commit 与 BuildDate 是本次新加的：在此之前，拿到一个 runner-manager 二进制
// 是没办法知道它出自哪个提交的，线上排查只能靠版本号猜。
//
// 注入的是 main 的变量，不是 version-kit 的包级变量，所以 kit 升大版本
// （模块路径随之变化）不必跟着改 ldflags——那正是 kit 自己的升级说明反复
// 警告、漏改时不报错、只会让二进制静默报 dev 的那一步。
var (
	// Version 语义化版本号，未注入时 /version 与 -version 都报 dev
	Version string
	// Commit 构建所用的 git 提交
	Commit string
	// BuildDate 构建时间（RFC3339）
	BuildDate string
)

// BuildInfo 按当前的 Version/Commit/BuildDate 组装一份版本信息。
// GoVersion、Platform、Compiler 由 version-kit 从 runtime 填。
//
// 每次调用重新组装，而不是在包初始化时算好一份：version-kit 的 HandlerConfig
// 会在构造时把响应体固化下来，而这里的 Version 是允许运行时改的（main 在
// flag.Parse 之后才赋值，用例也会改它）。/version 不是热路径，重算无所谓。
func BuildInfo() *version.Info {
	v := Version
	if v == "" {
		v = "dev"
	}
	return version.New(v, Commit, BuildDate)
}

// VersionInfo 返回版本信息（未注入时返回 dev）。
//
// 用 version-kit 的默认 HandlerConfig，也就是 Public() 那份精简取值——只有
// version（Branch 我们没注入）。刻意**不**开 IncludeBuildDetails：Basic Auth
// 没配置时这个端点是完全公开的，而 go_version 能让任何人把一条已公布的 Go
// 运行时 CVE 对到确切的运行时版本上。kit 自己的注释写的就是这个理由。
//
// 于是响应体与改动前逐字节相同：{"version":"..."}。commit 与构建时间只走
// -version 命令行，那是本机执行、不对外。
//
// 自己拿 ResolveConfig(...).JSONResponse() 写响应，而不是用 kit 的 HTTP handler：
// v4 把 net/http 那一套搬进了 httpadapter 子包，根包只剩「决定 serve 什么」的配置
// API，也就是 Echo、Gin、chi 这类框架该用的那一半。这个文件的代码因此在升 v4 时
// 一行没动，只换了 import。
func VersionInfo(c echo.Context) error {
	body, status := version.ResolveConfig(version.HandlerConfig{Info: BuildInfo()}).JSONResponse()
	return c.JSONBlob(status, body)
}
