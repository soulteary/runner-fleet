// 启动自检：把「配置错了但要等 Job 跑挂才发现」的问题提前到 Manager 启动那一刻暴露。
// 典型如 runner-net 未创建、挂载目录不属于 UID 1001、docker.sock 不可达、Runner 镜像不存在。
package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"sort"
	"strings"
	"time"

	preflight "github.com/soulteary/preflight-kit"
	"github.com/soulteary/runner-fleet/internal/config"
)

// 自检的结果模型来自 preflight-kit。这里用**类型别名**而不是新类型：
// CheckResult 就是 preflight.Result，既有调用方与用例一行不用改，
// 也不会出现「两个长得一样但不能互相赋值」的类型。
type (
	// CheckLevel 自检结果级别
	CheckLevel = preflight.Level
	// CheckResult 单项自检结果
	CheckResult = preflight.Result
)

const (
	CheckOK    = preflight.LevelOK
	CheckWarn  = preflight.LevelWarn  // 不影响启动，但很可能在跑 Job 时出问题
	CheckError = preflight.LevelError // 当前配置下基本可以确定跑不起来
)

func ok(name, msg string) CheckResult         { return preflight.OK(name, msg) }
func warn(name, msg, hint string) CheckResult { return preflight.Warn(name, msg, hint) }
func fail(name, msg, hint string) CheckResult { return preflight.Fail(name, msg, hint) }

// runCheck 跑一项检查，顺带拿到 preflight.Run 的 panic 兜底：
// 自检自己崩了不该把 Manager 的启动一起带走。名字只在兜底结果里用得上，
// 正常路径的 Name 由检查自己填。
func runCheck(ctx context.Context, name string, f func(context.Context) CheckResult) CheckResult {
	return preflight.Run(ctx, preflight.Named(name, f))[0]
}

// Preflight 按当前配置执行一组只读自检，不修改任何状态。
// 返回结果按检查顺序排列；调用方自行决定是记录日志还是返回给界面。
func Preflight(ctx context.Context, cfg *config.Config) []CheckResult {
	if cfg == nil {
		return []CheckResult{fail("config", "the configuration is empty", "")}
	}
	results := []CheckResult{
		runCheck(ctx, "runners directory", func(context.Context) CheckResult { return checkBasePath(cfg) }),
		runCheck(ctx, "runner directory permissions", func(context.Context) CheckResult { return checkRunnerDirPermissions(cfg) }),
	}
	if !cfg.Runners.ContainerMode {
		results = append(results, runCheck(ctx, "runner isolation", func(context.Context) CheckResult {
			return checkDefaultModeIsolation(cfg)
		}))
		return append(results, runCheck(ctx, "Docker in jobs", checkDefaultModeDocker))
	}
	results = append(results,
		runCheck(ctx, "Docker reachability", checkDockerReachable),
		runCheck(ctx, "container network", func(ctx context.Context) CheckResult { return checkNetwork(ctx, cfg) }),
	)
	// 只有真有 Runner 用 dind 时才查网络成员：别的后端下网络上没有那个免认证的 daemon，
	// 多出来的容器也就只是多出来的容器，报了反而是噪音
	if anyDindBackend(cfg) {
		results = append(results, runCheck(ctx, "container network members", func(ctx context.Context) CheckResult {
			return checkNetworkMembers(ctx, cfg)
		}))
	}
	results = append(results, checkRunnerImages(ctx, cfg)...)
	return append(results, runCheck(ctx, "Docker in jobs", func(ctx context.Context) CheckResult {
		return checkJobDockerBackend(ctx, cfg)
	}))
}

// checkBasePath 检查 runners 根目录存在且对当前进程可写（容器内以 UID 1001 运行，
// 宿主机目录未 chown 1001:1001 是最常见的部署失误）
func checkBasePath(cfg *config.Config) CheckResult {
	const name = "runners directory"
	base := cfg.Runners.BasePath
	info, err := os.Stat(base)
	if err != nil {
		return fail(name, fmt.Sprintf("%s is not reachable: %v", base, err),
			fmt.Sprintf("mkdir -p %s && chown %d:%d %s", base, os.Getuid(), os.Getgid(), base))
	}
	if !info.IsDir() {
		return fail(name, fmt.Sprintf("%s is not a directory", base), "")
	}
	// 用唯一临时文件名而非固定名：固定名会与目录中同名的用户文件相撞，
	// 自检本应只读，却把它打开又删掉
	f, err := os.CreateTemp(base, ".preflight-write-test-*")
	if err != nil {
		return fail(name, fmt.Sprintf("%s is not writable by the current user (UID %d): %v", base, os.Getuid(), err),
			fmt.Sprintf("on the host, run: chown -R %d:%d <the matching directory on the host>", os.Getuid(), os.Getgid()))
	}
	probe := f.Name()
	_ = f.Close()
	// 删除失败说明目录并非真正可用（如 sticky bit 或只读挂载），不能报成可写
	if err := os.Remove(probe); err != nil {
		return fail(name, fmt.Sprintf("%s allows creating a file but not removing it (the probe file %s was left behind): %v", base, probe, err),
			fmt.Sprintf("check the directory mode and mount options; UID %d needs full read and write access", os.Getuid()))
	}
	return ok(name, fmt.Sprintf("%s is writable (UID %d)", base, os.Getuid()))
}

// checkRunnerDirPermissions 点名可被他人进入的 Runner 安装目录。
//
// 目录里有 config.sh 写下的 .credentials_rsaparams——Runner 向 GitHub 表明身份的 RSA 私钥。
// actions/runner 不给这些文件设权限（Unix 侧完全跟 umask 走，通常 0644），所以目录的
// 权限位就是最后一道门。本版本起新建的目录是 0700，但 MkdirAll 不会改动已存在的目录，
// 老部署里的仍是 0755。
//
// 这里只报不改：UID 不匹配的部署（Manager 以 root 跑、容器内是 app(1001)）下擅自收紧
// 权限会把本来能跑的弄坏，该由人看过再决定。
func checkRunnerDirPermissions(cfg *config.Config) CheckResult {
	const name = "runner directory permissions"
	base := cfg.Runners.BasePath
	var loose []string
	for _, item := range cfg.Runners.Items {
		dir := item.InstallPath(base)
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() {
			continue // 还没建出来的目录不在本项管辖内，checkBasePath 已覆盖根目录
		}
		if info.Mode().Perm()&0o077 != 0 {
			loose = append(loose, dir)
		}
	}
	if len(loose) == 0 {
		return ok(name, "no runner directory is reachable by other local users")
	}
	sort.Strings(loose)
	return warn(name,
		fmt.Sprintf("%d runner directories are reachable by other users on the host: %s. "+
			"Each holds the .credentials_rsaparams that config.sh wrote — the private key the runner "+
			"authenticates to GitHub with. Reading it is enough to impersonate that runner, take its "+
			"jobs, and see the secrets passed to them",
			len(loose), strings.Join(loose, ", ")),
		"chmod 700 "+strings.Join(loose, " "))
}

// checkDefaultModeIsolation 默认模式下多个 Runner 共用一个容器与用户：
// 任一 Job 都能读到其他 Runner 的 .credentials_rsaparams 与 PAT。只提示，不阻止启动。
//
// 按条目数分档而不是无条件警告：只有一个 Runner 时没有「其他 Runner」可言，
// 而单人单仓库开多个 Runner 只为并发是合法场景，需要的是知情，不是拦路。
//
// PAT 已经移出 Runner 目录（internal/secrets），但那只关掉了容器模式的暴露：
// 默认模式下 config/tokens/ 就在同一个容器里，文件属主正是 Job 自己，0600 挡不住。
func checkDefaultModeIsolation(cfg *config.Config) CheckResult {
	const name = "runner isolation"
	if n := len(cfg.Runners.Items); n >= 2 {
		return warn(name,
			fmt.Sprintf("default mode: %d runners share one container and one user, so a job on "+
				"any of them can read the others' credentials", n),
			"set runners.container_mode: true to give each runner its own container (see SECURITY.md)")
	}
	return ok(name, "default mode with at most one runner")
}

// checkDefaultModeDocker 默认模式下 Job 在 Manager 容器内执行，这里说明 Job 内 docker 会连到哪
func checkDefaultModeDocker(ctx context.Context) CheckResult {
	const name = "Docker in jobs"
	h := strings.TrimSpace(os.Getenv("DOCKER_HOST"))
	if h == "" {
		h = "unix://" + HostDockerSocket
	}
	if strings.HasPrefix(h, "tcp://") {
		// 仅凭前缀就报 ok 会在 DinD 未启动时给出绿色结果，而 Job 里的 docker 全都会失败
		addr := tcpAddr(strings.TrimPrefix(h, "tcp://"))
		if err := dialTCP(ctx, addr); err != nil {
			return warn(name, fmt.Sprintf("default mode, DOCKER_HOST points at %s, which is not reachable: %v", h, err),
				"docker compose --profile dind up -d, and check that DinD is running on the same network as the Manager")
		}
		return ok(name, fmt.Sprintf("default mode, docker in jobs will reach %s (DinD, reachable)", h))
	}
	sock := strings.TrimPrefix(h, "unix://")
	if _, err := os.Stat(sock); err != nil {
		return warn(name, fmt.Sprintf("default mode, but %s does not exist, so jobs cannot use docker", sock),
			"to give jobs Docker, mount -v /var/run/docker.sock:/var/run/docker.sock, or switch to DinD")
	}
	if gid := socketGID(sock); gid >= 0 && !inGroup(gid) {
		return warn(name, fmt.Sprintf("%s belongs to GID %d and this process is not in that group, so docker in jobs will report permission denied", sock, gid),
			fmt.Sprintf("set group_add: [\"%d\"] in docker-compose, or DOCKER_GID=%d in .env", gid, gid))
	}
	return ok(name, fmt.Sprintf("default mode, docker in jobs will use %s (jobs can control the host Docker daemon)", h))
}

// checkDockerReachable 容器模式下 Manager 必须能操作宿主机 Docker
func checkDockerReachable(ctx context.Context) CheckResult {
	const name = "Docker reachability"
	out, err := dockerCmd(ctx, "version", "--format", "{{.Server.Version}}")
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return fail(name, "cannot reach the Docker daemon: "+msg, dockerAccessHint)
	}
	return ok(name, "Docker daemon version "+strings.TrimSpace(string(out)))
}

// checkNetwork 容器模式下 Runner 容器与 Manager 必须同网，否则 Manager 访问不到 Agent。
// compose down 会删掉非 external 的网络，是文档里专门列过的坑。
func checkNetwork(ctx context.Context, cfg *config.Config) CheckResult {
	const name = "container network"
	network := cfg.Runners.ContainerNetwork
	if network == "" {
		network = "runner-net"
	}
	if _, err := dockerCmd(ctx, "network", "inspect", network); err != nil {
		return fail(name, fmt.Sprintf("the network %s does not exist, so runner containers cannot be created or started", network),
			"docker network create "+network)
	}
	return ok(name, "the network "+network+" exists")
}

// networkMembersFormat 只取 docker network inspect 的 .Containers 一段：它的结构是
// map[容器ID]{Name,…}，其余字段（IPAM、Options、Labels）与这项检查无关。
const networkMembersFormat = "{{json .Containers}}"

// maxListedNetworkMembers 消息里最多点名几个容器。网络上挂着几十个容器时逐个列出
// 会把这一行撑成不可读，而这项检查要说的是「有不该在这里的东西」——
// 前十个足够让人接着去 docker network inspect 看全。
const maxListedNetworkMembers = 10

// anyDindBackend 是否有 Runner 实际用 dind。items[].job_docker_backend 可以逐个覆盖，
// 所以全局值两个方向都不能直接当结论：全局 none 不代表没有 Runner 单独开了 dind，
// 全局 dind 也不代表真有 Runner 在用它。
func anyDindBackend(cfg *config.Config) bool {
	// 还没配 items：界面上后续新增的 Runner 会拿全局值，所以按全局值算
	if len(cfg.Runners.Items) == 0 {
		return cfg.JobDockerBackendFor("") == "dind"
	}
	for _, item := range cfg.Runners.Items {
		if cfg.JobDockerBackendFor(item.Name) == "dind" {
			return true
		}
	}
	return false
}

// checkNetworkMembers 列出 runner-net 上不属于本部署的容器。
//
// 仓库自带的 DinD 以 privileged 运行且关闭了 TLS（compose 里的 --tls=false），2375
// 端口对这张网上的任何容器免认证开放——接上来就是那个 daemon 的完全控制权，而从
// privileged 容器逃到宿主机的门槛并不高。所以这张网是一条信任边界，网上多出来的东西
// 值得让人知道。
//
// 只 warn 不 fail：多一个容器不一定是配置错误（监控 sidecar 接到同一张网上是常见做法），
// 需要的是知情，不是拦住启动。
func checkNetworkMembers(ctx context.Context, cfg *config.Config) CheckResult {
	const name = "container network members"
	network := cfg.Runners.ContainerNetwork
	if network == "" {
		network = "runner-net"
	}
	out, err := dockerCmd(ctx, "network", "inspect", network, "--format", networkMembersFormat)
	if err != nil {
		// 「网络不存在」这类情形 checkNetwork 已经报过 fail，这里再报一遍
		// 只是把同一件事说两次，而且是用一句更绕的话说
		return ok(name, fmt.Sprintf("could not read the members of %s (skipped): %s", network, firstLine(out, err)))
	}
	// 只声明用得上的 Name，其余字段（EndpointID、IPv4Address…）由 json 忽略
	var raw map[string]struct{ Name string }
	if err := json.Unmarshal(out, &raw); err != nil {
		return ok(name, fmt.Sprintf("could not parse the members of %s (skipped): %v", network, err))
	}
	members := make(map[string]string, len(raw))
	for id, m := range raw {
		members[id] = m.Name
	}
	// hostname 取不到时传空串，unexpectedNetworkMembers 会因此跳过「自身」判断：
	// 宁可把 Manager 自己也列出来，也不能让空前缀把整张网络都算成自己
	selfHostname, _ := os.Hostname()
	unexpected := unexpectedNetworkMembers(members, selfHostname, expectedNetworkMembers(cfg))
	if len(unexpected) == 0 {
		return ok(name, "the network "+network+" has no unexpected members")
	}
	return warn(name,
		fmt.Sprintf("%d containers on %s are not part of this deployment and can reach the unauthenticated "+
			"DinD daemon: %s", len(unexpected), network, listNetworkMembers(unexpected)),
		fmt.Sprintf("disconnect them (docker network disconnect %s <name>) or move this deployment to its own network", network))
}

// expectedNetworkMembers 本部署自己会接到这张网上的容器名：DinD 与各 Runner 容器。
// Manager 不在其中——配置里没有它的容器名，它由 hostname 认出来。
func expectedNetworkMembers(cfg *config.Config) map[string]bool {
	dindHost := cfg.Runners.DindHost
	if dindHost == "" {
		dindHost = "runner-dind"
	}
	expected := map[string]bool{dindHost: true}
	for _, item := range cfg.Runners.Items {
		expected[ContainerName(item.Name)] = true
	}
	return expected
}

// unexpectedNetworkMembers 从网络成员里挑出不属于本部署的，按名字排序返回。
// members 是 容器ID -> 容器名，即 docker network inspect 的 .Containers。
// 提成纯函数是为了能直接喂成员表做用例，不必先有一个 Docker。
//
// Manager 自己靠 hostname 认：未指定 hostname 时 Docker 把它设成 12 位短 ID
// （moby 的 daemon/container.go：default hostname is the container's short-ID），
// 而 compose 只在写了 hostname: 时才传这个字段（pkg/compose/create.go 里
// Hostname 直接取 service.Hostname），container_name 不参与——本仓库的 compose
// 只设 container_name，所以走的正是短 ID 这条路。写了 hostname: 的部署按名字再兜
// 一次，免得把 Manager 自己报成入侵者。
//
// selfHostname 为空时不做这个判断：HasPrefix(id, "") 恒为真，会把整张网络算成自己，
// 也就是把这项检查静默地变成永远绿的。
func unexpectedNetworkMembers(members map[string]string, selfHostname string, expected map[string]bool) []string {
	var unexpected []string
	for id, member := range members {
		if selfHostname != "" && (strings.HasPrefix(id, selfHostname) || member == selfHostname) {
			continue
		}
		if expected[member] {
			continue
		}
		if member == "" {
			// 没有名字的端点用短 ID 点名，否则消息里会出现一个空位，读的人无从下手
			member = shortContainerID(id)
		}
		unexpected = append(unexpected, member)
	}
	sort.Strings(unexpected)
	return unexpected
}

// shortContainerID 取容器 ID 的前 12 位，与 docker ps 显示的一致
func shortContainerID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

// listNetworkMembers 把名字拼成消息里的那一段，超出 maxListedNetworkMembers 的收成一句计数
func listNetworkMembers(names []string) string {
	if len(names) <= maxListedNetworkMembers {
		return strings.Join(names, ", ")
	}
	return fmt.Sprintf("%s, …and %d more",
		strings.Join(names[:maxListedNetworkMembers], ", "), len(names)-maxListedNetworkMembers)
}

// requiredRunnerTools Job 普遍依赖的命令。缺任何一个都会以难以定位的方式失败：
// 缺 git 时 actions/checkout 静默退化为下载 tar 包（工作目录没有 .git），
// 缺 unzip 时 setup-gradle 之类的 Action 要等下载完发行包才报错。
// 不含 sudo：command -v sudo 只能证明命令存在，无法证明 Job 用户在 sudoers 里，
// 会给出「自检通过但 sudo apt-get 仍失败」的假保证。免密 sudo 由镜像构建保证
// （两个 Dockerfile 的 ALLOW_SUDO），不靠运行时探测。
var requiredRunnerTools = []string{"git", "unzip", "tar", "curl", "jq"}

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
		present := runCheck(ctx, "runner image", func(ctx context.Context) CheckResult {
			if _, err := dockerCmd(ctx, "image", "inspect", img); err != nil {
				return warn("runner image",
					fmt.Sprintf("%s is not present locally; it is pulled the first time a runner starts (a private registry needs docker login first)", img),
					"docker pull "+img)
			}
			return ok("runner image", img+" is ready")
		})
		results = append(results, present)
		// 镜像已在本地才做工具链检查，避免在自检阶段触发一次镜像拉取
		if present.Level != CheckOK {
			continue
		}
		results = append(results, runCheck(ctx, "runner image toolchain", func(ctx context.Context) CheckResult {
			return checkRunnerImageTools(ctx, img)
		}))
	}
	return results
}

// missingToolMarker 探测脚本对每个缺失命令输出的行前缀。
// dockerCmd 用的是 CombinedOutput，docker 的非致命告警（例如 arm64 主机运行 amd64
// 镜像时的平台不匹配警告）会混进来且退出码为 0；若直接按空白切分，整段告警的每个词
// 都会被当成缺失的命令名。加标记后只认自己输出的行。
const missingToolMarker = "RUNNER_FLEET_MISSING:"

// parseMissingTools 从探测输出中提取缺失的命令名。
// 只接受带标记的行，且名字必须在 requiredRunnerTools 内——这样即便告警文本里
// 恰好出现了标记，也无法伪造出一个命令名。
func parseMissingTools(out []byte) []string {
	known := make(map[string]bool, len(requiredRunnerTools))
	for _, t := range requiredRunnerTools {
		known[t] = true
	}
	var missing []string
	for _, line := range strings.Split(string(out), "\n") {
		name, ok := strings.CutPrefix(strings.TrimSpace(line), missingToolMarker)
		if !ok {
			continue
		}
		if name = strings.TrimSpace(name); known[name] {
			missing = append(missing, name)
		}
	}
	return missing
}

// checkRunnerImageTools 起一个一次性容器确认镜像内具备 requiredRunnerTools。
// 自定义 Runner 镜像很容易漏装这些，而缺失要等 Job 跑到一半才暴露。
func checkRunnerImageTools(ctx context.Context, img string) CheckResult {
	const name = "runner image toolchain"
	// 镜像的 ENTRYPOINT 是 Agent，这里覆盖为 shell；--network none 省掉网络配置开销
	script := "for t in " + strings.Join(requiredRunnerTools, " ") +
		"; do command -v \"$t\" >/dev/null 2>&1 || echo \"" + missingToolMarker + "$t\"; done"
	runCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, err := dockerCmd(runCtx, "run", "--rm", "--network", "none", "--entrypoint", "sh", img, "-c", script)
	if err != nil {
		return warn(name, fmt.Sprintf("could not inspect the commands inside %s (skipped): %s", img, firstLine(out, err)),
			"check it by hand: docker run --rm --entrypoint sh "+img+" -c \"command -v git unzip\"")
	}
	missing := parseMissingTools(out)
	if len(missing) > 0 {
		return warn(name, fmt.Sprintf("%s is missing %s; a job only fails when it reaches them (without git, "+
			"actions/checkout silently degrades to a tarball with no .git)",
			img, strings.Join(missing, ", ")),
			"install those packages in a custom image; see examples/runner-images/")
	}
	return ok(name, img+" has "+strings.Join(requiredRunnerTools, ", "))
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
	const name = "Docker in jobs"
	switch strings.ToLower(strings.TrimSpace(cfg.Runners.JobDockerBackend)) {
	case "host-socket":
		if _, err := os.Stat(HostDockerSocket); err != nil {
			return fail(name, HostDockerSocket+" does not exist, so it cannot be mounted into runner containers",
				"mount -v /var/run/docker.sock:/var/run/docker.sock onto runner-manager in docker-compose")
		}
		gid := runnerDockerGID(cfg)
		if gid < 0 {
			return warn(name, "could not determine the group owning docker.sock, so --group-add is not passed when creating runner containers and docker in jobs may report permission denied",
				"set runners.docker_gid in the config, or DOCKER_GID in .env")
		}
		return ok(name, fmt.Sprintf("host-socket; runner containers get --group-add %d", gid))
	case "none":
		return ok(name, "none; jobs get no Docker")
	default: // dind
		host := cfg.Runners.DindHost
		if host == "" {
			host = "runner-dind"
		}
		addr := net.JoinHostPort(host, "2375")
		if err := dialTCP(ctx, addr); err != nil {
			return warn(name, fmt.Sprintf("dind, but %s is not reachable: %v", addr, err),
				"docker compose --profile dind up -d, or set job_docker_backend to none if jobs do not need Docker")
		}
		return ok(name, "dind; "+addr+" is reachable")
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

// PreflightLogMarker 是每条自检日志的行首标记。
//
// 六种语言的文档都把「先看启动自检」写成排障第一步，给出的命令是
// `docker compose logs runner-manager | grep '\[preflight'`。这个标记原本是
// 「[自检 …]」——于是英/法/德/日/韩五份文档里躺着一个非中文用户既打不出、
// 也看不懂的 grep 关键词。日志正文目前仍是中文（那是更大的一件事），
// 但至少让「怎么把这些行捞出来」不依赖读者认识汉字。
//
// 提成常量是为了让 preflight_test.go 能守住它：文档里的命令依赖这个字面量，
// 改了它而不改六份文档，排障第一步就会静默地什么都 grep 不到。
const PreflightLogMarker = "[preflight"

// LogPreflight 将自检结果按级别打到日志，error 项额外给出修复建议
func LogPreflight(results []CheckResult) {
	for _, r := range results {
		prefix := PreflightLogMarker + " ✓]"
		switch r.Level {
		case CheckWarn:
			prefix = PreflightLogMarker + " !]"
		case CheckError:
			prefix = PreflightLogMarker + " ✗]"
		}
		line := fmt.Sprintf("%s %s: %s", prefix, r.Name, r.Message)
		if r.Hint != "" && r.Level != CheckOK {
			line += "  → hint: " + r.Hint
		}
		preflightLogf("%s", line)
	}
}
