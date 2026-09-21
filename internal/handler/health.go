package handler

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/labstack/echo/v4"
	health "github.com/soulteary/health-kit/v3"
	"github.com/soulteary/runner-fleet/internal/config"
)

// serviceName 出现在 /health 与 /ready 的响应体里。
const serviceName = "runner-fleet"

// livenessAgg 是 /health 用的聚合器：**一个探针都不挂**。
//
// 这是刻意的，不是没写完。/health 一直被文档写成「Ingress/K8s 探针用」，而 K8s 的
// livenessProbe 失败意味着**重启容器**。配置文件坏掉、挂载卷掉了这类问题，重启一次
// 也不会变好——只会变成 crashloop，比一个还在跑、只是报错的进程更难排查。
// 所以 /health 保持原语义：进程活着就是 200。
//
// 真正的依赖检查在 /ready，见 readinessAgg。
var livenessAgg = health.NewAggregator(
	health.DefaultConfig().WithServiceName(serviceName),
)

// Health 健康检查，供负载均衡或 K8s 探针使用。
//
// 响应体由 {"status":"ok"} 变成 {"status":"ok","service":"runner-fleet"}——
// status 取值没变（health-kit 的 StatusHealthy 就是 "ok"），只多了一个字段。
// 状态码同样没变：进程活着恒为 200。
//
// 这个端点**始终免鉴权**（basicAuthMiddleware 里有 Skipper 放行），所以用
// DefaultConfig：IncludeDetails 与 IncludeChecks 都是 false，不吐任何内部细节。
func Health(c echo.Context) error {
	return writeHealth(c, livenessAgg)
}

// readinessAgg 是 /ready 用的聚合器，挂的是「现在能不能干活」的检查。
//
// 两个探针都只读配置与文件系统元数据，不起子进程、不发网络请求——探针可能每几秒
// 打一次，不能让它去 docker exec 或者拨网络。
var readinessAgg = health.NewAggregator(
	health.DefaultConfig().
		WithServiceName(serviceName).
		// 必须显式标成关键项。非关键项失败只算 degraded，而 health-kit 把
		// degraded 映射成 200（"Degraded is still functional"）——那样这两个
		// 探针就成了摆设，永远不会把实例摘出负载。
		WithCriticalChecks([]string{"config", "base_path"}),
).AddCheckers(
	health.NewCustomChecker("config", checkConfigLoads),
	health.NewCustomChecker("base_path", checkBasePathUsable),
)

// Ready 就绪检查：配置能不能读、runner 根目录在不在且可写。
//
// 与 /health 分开是因为两者失败时该做的事不一样：/health 失败 K8s 会重启容器，
// 而配置坏了重启治不好；/ready 失败只是把这个实例摘出负载，正是想要的效果。
//
// 同样免鉴权（探针拿不到 Basic Auth 凭据），所以也用 DefaultConfig——
// 失败时只说 "status":"unhealthy"，不说是哪一项、更不说路径。
func Ready(c echo.Context) error {
	return writeHealth(c, readinessAgg)
}

func writeHealth(c echo.Context, agg *health.Aggregator) error {
	d := health.Decide(c.Request().Context(), agg, health.RequestSource(c.Request()))
	if d.Forbidden {
		// 没配 IPWhitelist 时走不到这里；留着是为了将来配上后不会静默变成 200
		return echo.NewHTTPError(d.StatusCode, "Forbidden")
	}
	return c.JSON(d.StatusCode, d.Body)
}

// checkConfigLoads 配置文件能不能读、能不能解析。
func checkConfigLoads(context.Context) error {
	if _, err := config.Load(ConfigPath); err != nil {
		return fmt.Errorf("加载配置失败: %w", err)
	}
	return nil
}

// checkBasePathUsable runner 根目录在不在、是不是目录、能不能写。
//
// 只 stat + 建删一个临时文件，不递归。挂载卷掉了是这里最想抓的情况：
// 目录还在（挂载点本身），但写不进去，此时 Manager 建不了任何 runner。
func checkBasePathUsable(context.Context) error {
	cfg, err := config.Load(ConfigPath)
	if err != nil {
		return fmt.Errorf("加载配置失败: %w", err)
	}
	base := cfg.Runners.BasePath
	info, err := os.Stat(base)
	if err != nil {
		return fmt.Errorf("runner 根目录不可用: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("runner 根目录不是目录: %s", base)
	}
	probe := filepath.Join(base, ".health-write-probe")
	if err := os.WriteFile(probe, []byte{}, 0o600); err != nil {
		return fmt.Errorf("runner 根目录不可写: %w", err)
	}
	// 删不掉不算失败：写成功已经证明了要证明的事，留个空文件也不影响什么
	_ = os.Remove(probe)
	return nil
}
