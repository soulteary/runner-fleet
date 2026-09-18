// 启动自检：把「配置错了但要等 Job 跑挂才发现」的问题提前到 Manager 启动那一刻暴露。
// 典型如 runner-net 未创建、挂载目录不属于 UID 1001、docker.sock 不可达、Runner 镜像不存在。
package runner

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lab-dev/github-actions-runner-manager/internal/config"
)

// CheckLevel 自检结果级别
type CheckLevel string

const (
	CheckOK    CheckLevel = "ok"
	CheckWarn  CheckLevel = "warn"  // 不影响启动，但很可能在跑 Job 时出问题
	CheckError CheckLevel = "error" // 当前配置下基本可以确定跑不起来
)

// CheckResult 单项自检结果
type CheckResult struct {
	Name    string     `json:"name"`
	Level   CheckLevel `json:"level"`
	Message string     `json:"message"`
	Hint    string     `json:"hint,omitempty"` // 可直接照做的修复建议
}

func ok(name, msg string) CheckResult { return CheckResult{Name: name, Level: CheckOK, Message: msg} }
func warn(name, msg, hint string) CheckResult {
	return CheckResult{Name: name, Level: CheckWarn, Message: msg, Hint: hint}
}
func fail(name, msg, hint string) CheckResult {
	return CheckResult{Name: name, Level: CheckError, Message: msg, Hint: hint}
}

// Preflight 按当前配置执行一组只读自检，不修改任何状态。
// 返回结果按检查顺序排列；调用方自行决定是记录日志还是返回给界面。
func Preflight(ctx context.Context, cfg *config.Config) []CheckResult {
	if cfg == nil {
		return []CheckResult{fail("config", "配置为空", "")}
	}
	results := []CheckResult{checkBasePath(cfg)}
	if !cfg.Runners.ContainerMode {
		return append(results, checkDefaultModeDocker())
	}
	results = append(results, checkDockerReachable(ctx))
	results = append(results, checkNetwork(ctx, cfg))
	results = append(results, checkRunnerImage(ctx, cfg))
	results = append(results, checkJobDockerBackend(ctx, cfg))
	return results
}

// checkBasePath 检查 runners 根目录存在且对当前进程可写（容器内以 UID 1001 运行，
// 宿主机目录未 chown 1001:1001 是最常见的部署失误）
func checkBasePath(cfg *config.Config) CheckResult {
	const name = "runners 目录"
	base := cfg.Runners.BasePath
	info, err := os.Stat(base)
	if err != nil {
		return fail(name, fmt.Sprintf("%s 不可访问: %v", base, err),
			fmt.Sprintf("mkdir -p %s && chown %d:%d %s", base, os.Getuid(), os.Getgid(), base))
	}
	if !info.IsDir() {
		return fail(name, fmt.Sprintf("%s 不是目录", base), "")
	}
	probe := filepath.Join(base, ".preflight-write-test")
	f, err := os.OpenFile(probe, os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return fail(name, fmt.Sprintf("%s 对当前用户(UID %d)不可写: %v", base, os.Getuid(), err),
			fmt.Sprintf("在宿主机执行 chown -R %d:%d <宿主机上对应目录>", os.Getuid(), os.Getgid()))
	}
	_ = f.Close()
	_ = os.Remove(probe)
	return ok(name, fmt.Sprintf("%s 可写（UID %d）", base, os.Getuid()))
}

// checkDefaultModeDocker 默认模式下 Job 在 Manager 容器内执行，这里说明 Job 内 docker 会连到哪
func checkDefaultModeDocker() CheckResult {
	const name = "Job 内 Docker"
	h := strings.TrimSpace(os.Getenv("DOCKER_HOST"))
	if h == "" {
		h = "unix://" + HostDockerSocket
	}
	if strings.HasPrefix(h, "tcp://") {
		return ok(name, fmt.Sprintf("默认模式，Job 内 docker 将连接 %s（DinD）", h))
	}
	sock := strings.TrimPrefix(h, "unix://")
	if _, err := os.Stat(sock); err != nil {
		return warn(name, fmt.Sprintf("默认模式，但 %s 不存在，Job 内无法使用 docker", sock),
			"需要 Job 内 Docker 时请挂载 -v /var/run/docker.sock:/var/run/docker.sock，或改用 DinD")
	}
	if gid := socketGID(sock); gid >= 0 && !inGroup(gid) {
		return warn(name, fmt.Sprintf("%s 属于 GID %d，当前进程不在该组，Job 内 docker 会报 permission denied", sock, gid),
			fmt.Sprintf("docker-compose 中设置 group_add: [\"%d\"]，或在 .env 中 DOCKER_GID=%d", gid, gid))
	}
	return ok(name, fmt.Sprintf("默认模式，Job 内 docker 将使用 %s", h))
}

// checkDockerReachable 容器模式下 Manager 必须能操作宿主机 Docker
func checkDockerReachable(ctx context.Context) CheckResult {
	const name = "Docker 可达性"
	out, err := dockerCmd(ctx, "version", "--format", "{{.Server.Version}}")
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return fail(name, "无法连接 Docker daemon: "+msg, dockerAccessHint)
	}
	return ok(name, "Docker daemon 版本 "+strings.TrimSpace(string(out)))
}

// checkNetwork 容器模式下 Runner 容器与 Manager 必须同网，否则 Manager 访问不到 Agent。
// compose down 会删掉非 external 的网络，是文档里专门列过的坑。
func checkNetwork(ctx context.Context, cfg *config.Config) CheckResult {
	const name = "容器网络"
	network := cfg.Runners.ContainerNetwork
	if network == "" {
		network = "runner-net"
	}
	if _, err := dockerCmd(ctx, "network", "inspect", network); err != nil {
		return fail(name, fmt.Sprintf("网络 %s 不存在，Runner 容器将无法创建/启动", network),
			"docker network create "+network)
	}
	return ok(name, "网络 "+network+" 存在")
}

// checkRunnerImage 镜像不在本地不算致命（docker create 会自动拉），但提前说清楚能省掉一次困惑
func checkRunnerImage(ctx context.Context, cfg *config.Config) CheckResult {
	const name = "Runner 镜像"
	img := cfg.Runners.ContainerImage
	if strings.TrimSpace(img) == "" {
		img = config.DefaultRunnerContainerImage()
	}
	if _, err := dockerCmd(ctx, "image", "inspect", img); err != nil {
		return warn(name, fmt.Sprintf("%s 不在本地，首次启动 Runner 时才会拉取（私有仓库需先 docker login）", img),
			"docker pull "+img)
	}
	return ok(name, img+" 已就绪")
}

// checkJobDockerBackend 按 job_docker_backend 检查 Job 内 Docker 的前置条件
func checkJobDockerBackend(ctx context.Context, cfg *config.Config) CheckResult {
	const name = "Job 内 Docker"
	switch strings.ToLower(strings.TrimSpace(cfg.Runners.JobDockerBackend)) {
	case "host-socket":
		if _, err := os.Stat(HostDockerSocket); err != nil {
			return fail(name, HostDockerSocket+" 不存在，无法挂载进 Runner 容器",
				"在 docker-compose 中为 runner-manager 挂载 -v /var/run/docker.sock:/var/run/docker.sock")
		}
		gid := runnerDockerGID(cfg)
		if gid < 0 {
			return warn(name, "无法确定 docker.sock 所属组，创建 Runner 容器时不会追加 --group-add，Job 内 docker 可能报 permission denied",
				"在 config 中设置 runners.docker_gid，或在 .env 中设置 DOCKER_GID")
		}
		return ok(name, fmt.Sprintf("host-socket，Runner 容器将追加 --group-add %d", gid))
	case "none":
		return ok(name, "none，Job 内不提供 Docker")
	default: // dind
		host := cfg.Runners.DindHost
		if host == "" {
			host = "runner-dind"
		}
		addr := net.JoinHostPort(host, "2375")
		dialCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		var d net.Dialer
		conn, err := d.DialContext(dialCtx, "tcp", addr)
		if err != nil {
			return warn(name, fmt.Sprintf("dind，但 %s 当前不可达: %v", addr, err),
				"docker compose --profile dind up -d；若 Job 不需要 Docker 可将 job_docker_backend 设为 none")
		}
		_ = conn.Close()
		return ok(name, "dind，"+addr+" 可达")
	}
}

// inGroup 判断当前进程是否属于该 GID（主组或附加组）
func inGroup(gid int) bool {
	if os.Getgid() == gid {
		return true
	}
	groups, err := os.Getgroups()
	if err != nil {
		return false
	}
	for _, g := range groups {
		if g == gid {
			return true
		}
	}
	return false
}

// preflightLogf 供测试替换，默认走标准库 log
var preflightLogf = log.Printf

// LogPreflight 将自检结果按级别打到日志，error 项额外给出修复建议
func LogPreflight(results []CheckResult) {
	for _, r := range results {
		prefix := "[自检 ✓]"
		switch r.Level {
		case CheckWarn:
			prefix = "[自检 !]"
		case CheckError:
			prefix = "[自检 ✗]"
		}
		line := fmt.Sprintf("%s %s: %s", prefix, r.Name, r.Message)
		if r.Hint != "" && r.Level != CheckOK {
			line += "  → 建议: " + r.Hint
		}
		preflightLogf("%s", line)
	}
}
