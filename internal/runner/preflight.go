// 启动自检：把「配置错了但要等 Job 跑挂才发现」的问题提前到 Manager 启动那一刻暴露。
// 典型如 runner-net 未创建、挂载目录不属于 UID 1001、docker.sock 不可达、Runner 镜像不存在。
package runner

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
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
		return append(results, checkDefaultModeDocker(ctx))
	}
	results = append(results, checkDockerReachable(ctx))
	results = append(results, checkNetwork(ctx, cfg))
	results = append(results, checkRunnerImages(ctx, cfg)...)
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
	// 用唯一临时文件名而非固定名：固定名会与目录中同名的用户文件相撞，
	// 自检本应只读，却把它打开又删掉
	f, err := os.CreateTemp(base, ".preflight-write-test-*")
	if err != nil {
		return fail(name, fmt.Sprintf("%s 对当前用户(UID %d)不可写: %v", base, os.Getuid(), err),
			fmt.Sprintf("在宿主机执行 chown -R %d:%d <宿主机上对应目录>", os.Getuid(), os.Getgid()))
	}
	probe := f.Name()
	_ = f.Close()
	// 删除失败说明目录并非真正可用（如 sticky bit 或只读挂载），不能报成可写
	if err := os.Remove(probe); err != nil {
		return fail(name, fmt.Sprintf("%s 可创建文件但无法删除（探测文件 %s 已残留）: %v", base, probe, err),
			fmt.Sprintf("检查目录权限与挂载选项，确认 UID %d 对该目录有完整读写权限", os.Getuid()))
	}
	return ok(name, fmt.Sprintf("%s 可写（UID %d）", base, os.Getuid()))
}

// checkDefaultModeDocker 默认模式下 Job 在 Manager 容器内执行，这里说明 Job 内 docker 会连到哪
func checkDefaultModeDocker(ctx context.Context) CheckResult {
	const name = "Job 内 Docker"
	h := strings.TrimSpace(os.Getenv("DOCKER_HOST"))
	if h == "" {
		h = "unix://" + HostDockerSocket
	}
	if strings.HasPrefix(h, "tcp://") {
		// 仅凭前缀就报 ok 会在 DinD 未启动时给出绿色结果，而 Job 里的 docker 全都会失败
		addr := tcpAddr(strings.TrimPrefix(h, "tcp://"))
		if err := dialTCP(ctx, addr); err != nil {
			return warn(name, fmt.Sprintf("默认模式，DOCKER_HOST 指向 %s，但当前不可达: %v", h, err),
				"docker compose --profile dind up -d；确认 DinD 已启动且与 Manager 同网")
		}
		return ok(name, fmt.Sprintf("默认模式，Job 内 docker 将连接 %s（DinD，可达）", h))
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

// requiredRunnerTools Job 普遍依赖的命令。缺任何一个都会以难以定位的方式失败：
// 缺 git 时 actions/checkout 静默退化为下载 tar 包（工作目录没有 .git），
// 缺 unzip 时 setup-gradle 之类的 Action 要等下载完发行包才报错。
var requiredRunnerTools = []string{"git", "unzip", "tar", "curl", "jq", "sudo"}

// RunnerImages 返回配置中用到的全部 Runner 镜像（去重，保持稳定顺序）。
// 自 items[].container_image 支持按 Runner 覆盖后，镜像可能不止一个。
func RunnerImages(cfg *config.Config) []string {
	if cfg == nil {
		return nil
	}
	seen := map[string]bool{}
	var images []string
	add := func(img string) {
		img = strings.TrimSpace(img)
		if img == "" || seen[img] {
			return
		}
		seen[img] = true
		images = append(images, img)
	}
	// 未配置 items 的 Runner（含界面后续新增的）会用全局镜像，所以它必然在列
	add(cfg.Runners.ContainerImage)
	if len(images) == 0 {
		add(config.DefaultRunnerContainerImage())
	}
	for _, item := range cfg.Runners.Items {
		add(cfg.ContainerImageFor(item.Name))
	}
	return images
}

// checkRunnerImages 逐个检查配置用到的镜像：是否在本地、以及镜像内是否具备
// Job 所需的基础命令。镜像不在本地不算致命（docker create 会自动拉）。
func checkRunnerImages(ctx context.Context, cfg *config.Config) []CheckResult {
	var results []CheckResult
	for _, img := range RunnerImages(cfg) {
		if _, err := dockerCmd(ctx, "image", "inspect", img); err != nil {
			results = append(results, warn("Runner 镜像",
				fmt.Sprintf("%s 不在本地，首次启动 Runner 时才会拉取（私有仓库需先 docker login）", img),
				"docker pull "+img))
			continue
		}
		results = append(results, ok("Runner 镜像", img+" 已就绪"))
		// 镜像已在本地才做工具链检查，避免在自检阶段触发一次镜像拉取
		results = append(results, checkRunnerImageTools(ctx, img))
	}
	return results
}

// checkRunnerImageTools 起一个一次性容器确认镜像内具备 requiredRunnerTools。
// 自定义 Runner 镜像很容易漏装这些，而缺失要等 Job 跑到一半才暴露。
func checkRunnerImageTools(ctx context.Context, img string) CheckResult {
	const name = "Runner 镜像工具链"
	// 镜像的 ENTRYPOINT 是 Agent，这里覆盖为 shell；--network none 省掉网络配置开销
	script := "for t in " + strings.Join(requiredRunnerTools, " ") +
		"; do command -v \"$t\" >/dev/null 2>&1 || echo \"$t\"; done"
	runCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, err := dockerCmd(runCtx, "run", "--rm", "--network", "none", "--entrypoint", "sh", img, "-c", script)
	if err != nil {
		return warn(name, fmt.Sprintf("无法检查 %s 内的命令（跳过）: %s", img, firstLine(out, err)),
			"可手动执行: docker run --rm --entrypoint sh "+img+" -c \"command -v git unzip\"")
	}
	missing := strings.Fields(string(out))
	if len(missing) > 0 {
		return warn(name, fmt.Sprintf("%s 缺少 %s，Job 会在用到时才失败（缺 git 时 actions/checkout 会静默退化为无 .git 的 tar 包）",
			img, strings.Join(missing, "、")),
			"在自定义镜像中补装这些包，可参考 examples/runner-images/")
	}
	return ok(name, img+" 具备 "+strings.Join(requiredRunnerTools, "、"))
}

// firstLine 取命令输出或错误的首行，避免把整段 docker 输出塞进自检结果
func firstLine(out []byte, err error) string {
	msg := strings.TrimSpace(string(out))
	if msg == "" && err != nil {
		msg = err.Error()
	}
	if i := strings.IndexByte(msg, '\n'); i >= 0 {
		msg = msg[:i]
	}
	return msg
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
		if err := dialTCP(ctx, addr); err != nil {
			return warn(name, fmt.Sprintf("dind，但 %s 当前不可达: %v", addr, err),
				"docker compose --profile dind up -d；若 Job 不需要 Docker 可将 job_docker_backend 设为 none")
		}
		return ok(name, "dind，"+addr+" 可达")
	}
}

// tcpAddr 补全缺省端口，DOCKER_HOST 可能写成 tcp://host 而不带端口
func tcpAddr(addr string) string {
	if _, _, err := net.SplitHostPort(addr); err != nil {
		return net.JoinHostPort(addr, "2375")
	}
	return addr
}

// dialTCP 检测 TCP 端点可达性，供默认模式与容器模式的 DinD 检查复用
func dialTCP(ctx context.Context, addr string) error {
	dialCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var d net.Dialer
	conn, err := d.DialContext(dialCtx, "tcp", addr)
	if err != nil {
		return err
	}
	return conn.Close()
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
