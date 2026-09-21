// 容器模式：通过 Docker CLI 与 Runner 容器内 Agent 实现 C/S 控制与状态查询。
// 本包负责 Runner 容器的创建/启停/删除及与 Agent 的 HTTP 通信；Manager 仅编排，不承载 Runner 进程。
package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/soulteary/runner-fleet/internal/config"
)

// ContainerName 将 runner 名称转为合法容器名，与 config 包规则一致
func ContainerName(name string) string {
	return config.NormalizedContainerName(name)
}

// AgentStatus 容器内 Agent /status 返回结构
type AgentStatus struct {
	Status  string `json:"status"`
	Running bool   `json:"running"`
}

// agentBaseURL 拼出容器内 Agent 的基址。做成变量是为了让测试把它指向本地
// httptest 服务——容器名在测试进程里解析不了，否则只能验到「连不上」那条路径。
var agentBaseURL = func(containerName string, port int) string {
	return fmt.Sprintf("http://%s:%d", containerName, port)
}

// GetAgentStatus 请求 Runner 容器内 Agent 的 /status，超时 5 秒。
// token 为空时不带鉴权头，兼容本特性之前创建的容器。
func GetAgentStatus(ctx context.Context, containerName string, port int, token string) (*AgentStatus, error) {
	if port <= 0 {
		port = 8081
	}
	url := agentBaseURL(containerName, port) + "/status"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	setAgentAuth(req, token)
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		msg := strings.TrimSpace(string(body))
		if msg == "" {
			return nil, fmt.Errorf("agent 返回 %d", resp.StatusCode)
		}
		return nil, fmt.Errorf("agent 返回 %d: %s", resp.StatusCode, msg)
	}
	var out AgentStatus
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CallAgentStart 请求 Runner 容器内 Agent 的 POST /start。
// token 为空时不带鉴权头，兼容本特性之前创建的容器。
func CallAgentStart(ctx context.Context, containerName string, port int, token string) error {
	if port <= 0 {
		port = 8081
	}
	url := agentBaseURL(containerName, port) + "/start"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return err
	}
	setAgentAuth(req, token)
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		msg := strings.TrimSpace(string(body))
		if msg == "" {
			return fmt.Errorf("agent /start 返回 %d", resp.StatusCode)
		}
		return fmt.Errorf("agent /start 返回 %d: %s", resp.StatusCode, msg)
	}
	return nil
}

// setAgentAuth 为 Agent 请求附加令牌；token 为空则不加头
func setAgentAuth(req *http.Request, token string) {
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
}

func dockerCmd(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "docker", args...)
	return cmd.CombinedOutput()
}

// containerNotFound 判断 docker 输出是否表示「容器不存在」（含英文/中文等）
func containerNotFound(out []byte) bool {
	s := string(out)
	lower := strings.ToLower(s)
	if strings.Contains(lower, "no such container") || strings.Contains(lower, "no such object") {
		return true
	}
	// Docker 中文环境或其它 locale 的常见提示
	if strings.Contains(s, "没有此容器") || strings.Contains(s, "没有找到容器") || strings.Contains(s, "未找到容器") {
		return true
	}
	return false
}

// containerStartUnrecoverable 判断 docker start 失败是否因网络已删除等导致无法恢复，需删容器后重建
func containerStartUnrecoverable(out []byte) bool {
	s := string(out)
	lower := strings.ToLower(s)
	if strings.Contains(lower, "network") && (strings.Contains(lower, "not found") || strings.Contains(lower, "no such")) {
		return true
	}
	if strings.Contains(lower, "could not find network") || strings.Contains(lower, "could not attach to network") {
		return true
	}
	if strings.Contains(lower, "failed to create endpoint") || strings.Contains(lower, "failed to get network") {
		return true
	}
	return false
}

// dockerPermissionDenied 判断是否为访问 Docker 权限/连接错误（宿主机 socket 需对 Manager 容器可访问）
func dockerPermissionDenied(out []byte) bool {
	s := string(out)
	lower := strings.ToLower(s)
	if strings.Contains(lower, "permission denied") || strings.Contains(lower, "permissions have not been granted") {
		return true
	}
	if strings.Contains(lower, "cannot connect to the docker daemon") || strings.Contains(lower, "is the docker daemon running") {
		return true
	}
	if strings.Contains(lower, "connection refused") {
		return true
	}
	return false
}

func dockerCmdError(op string, out []byte, err error) error {
	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" {
		trimmed = "(无输出)"
	}
	if dockerPermissionDenied(out) {
		return fmt.Errorf("%s 失败（权限不足或无法连接 daemon）。%s。输出: %s: %w", op, dockerAccessHint, trimmed, err)
	}
	return fmt.Errorf("%s 失败。输出: %s: %w", op, trimmed, err)
}

const dockerAccessHint = "若 Manager 在容器内，请为 runner-manager 配置 group_add 使用宿主机 docker 组 GID（.env 中 DOCKER_GID=$(getent group docker | cut -d: -f3)），或使用 user: \"0:0\" 以 root 访问 socket"

// managerMustUseHostDocker 提示：容器模式下 Manager 必须用宿主机 Docker 创建 Runner 容器，不能把 DOCKER_HOST 设为 DinD
const errContainerModeNeedHostDocker = "容器模式下 Manager 必须使用宿主机 Docker（unix socket）创建/启停 Runner 容器，不能使用 DinD。请在 .env 中移除或注释 DOCKER_HOST=tcp://runner-dind:2375，使 Manager 使用默认 unix:///var/run/docker.sock；DinD 仅供 Runner 容器内 Job 的 docker build 等使用"

func managerDockerHostIsDind() bool {
	h := os.Getenv("DOCKER_HOST")
	return strings.HasPrefix(strings.TrimSpace(h), "tcp://")
}

// ManagerDockerHostIsDind 供启动时检查：若为 true 且开启容器模式，Manager 无法创建 Runner 容器
func ManagerDockerHostIsDind() bool {
	return managerDockerHostIsDind()
}

// HostDockerSocket job_docker_backend=host-socket 时挂载进 Runner 容器的宿主机 Docker socket
const HostDockerSocket = "/var/run/docker.sock"

// runnerOps 按 Runner 串行化启停与重建。
//
// 同一个 Runner 会被多条路径同时碰：Manager 启动 15 秒后的自动拉起、每 5 分钟的定时拉起、
// 注册完成后的启动、界面上的点击。以前撞车最多留下一条「container name is already in use」
// 的日志；现在重建会先 docker rm，两个调用交叉执行就可能删掉对方刚建好的容器。
var runnerOps sync.Map // 容器名 -> *sync.Mutex

// lockRunnerOps 取得某个容器的操作锁，返回解锁函数
func lockRunnerOps(containerName string) func() {
	v, _ := runnerOps.LoadOrStore(containerName, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// 启动容器后给 Agent 的就绪时间：新建的容器要等镜像入口起来，已存在的容器快一些。
// 做成变量只为测试能调小，生产路径上取值与此前一致。
var (
	agentReadyDelayAfterCreate = 3 * time.Second
	agentReadyDelayAfterStart  = 2 * time.Second
)

// unknownDockerGID 表示无法确定 docker.sock 所属组，此时不追加 --group-add
const unknownDockerGID = -1

// socketGID 返回 path 所属组 GID；读取失败或平台不支持时返回 unknownDockerGID。
// Manager 在容器内时 docker.sock 为宿主机挂载，stat 得到的即宿主机 docker 组 GID。
func socketGID(path string) int {
	info, err := os.Stat(path)
	if err != nil {
		return unknownDockerGID
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return unknownDockerGID
	}
	return int(st.Gid)
}

// runnerDockerGID 决定 Runner 容器需追加的 docker 组 GID：
// 优先 runners.docker_gid（可由环境变量 DOCKER_GID 覆盖），否则自动探测 docker.sock 所属组。
func runnerDockerGID(cfg *config.Config) int {
	if cfg != nil && cfg.Runners.DockerGID > 0 {
		return cfg.Runners.DockerGID
	}
	return socketGID(HostDockerSocket)
}

// runnerCreateArgs 组装创建 Runner 容器的 docker create 参数。
// 实际形态由 containerSpec 定义（见 drift.go）：创建参数与漂移比对共用同一份定义，
// 免得改了创建逻辑、漂移比对还停在旧写法上。
func runnerCreateArgs(containerName, mountSrc, network, img, jobBackend, dindHost string, dockerGID int, limits config.ResourceLimits, agentToken string) ([]string, error) {
	spec := containerSpec{
		ContainerName: containerName,
		Image:         img,
		Network:       network,
		MountSrc:      mountSrc,
		JobBackend:    jobBackend,
		DindHost:      dindHost,
		DockerGID:     dockerGID,
		AgentToken:    agentToken,
		Limits:        limits,
	}
	return spec.createArgs()
}

// applyResourceLimitsToExisting 对已存在的容器施加资源上限。
//
// docker create 的资源参数只在创建时生效：升级到支持 runners.resources 的版本后，
// 已有容器仍是原来的无限制状态，仅靠停止/启动也不会变。这里用 docker update 补上，
// 使配置对存量容器同样生效，无需删掉重建。
// 未配置上限时不执行任何操作，保持与旧版本一致。
func applyResourceLimitsToExisting(ctx context.Context, containerName string, limits config.ResourceLimits) {
	updateArgs := resourceUpdateArgs(containerName, limits)
	if updateArgs == nil {
		return
	}
	if out, err := dockerCmd(ctx, updateArgs...); err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		log.Printf("警告: 为已存在容器 %s 施加资源上限失败，该容器仍沿用创建时的限制: %s（可 docker rm -f %s 后在界面重新启动以重建）",
			containerName, msg, containerName)
	}
}

// resourceUpdateArgs 组装 docker update 参数；未配置任何上限时返回 nil 表示无需执行
func resourceUpdateArgs(containerName string, limits config.ResourceLimits) []string {
	args := limits.Args()
	if len(args) == 0 {
		return nil
	}
	return append(append([]string{"update"}, args...), containerName)
}

// StartRunnerContainer 若容器不存在则创建并启动，若存在则 start；创建时挂载 installDir 到 /runner。
// 已存在的容器若创建参数与当前配置不一致（换了镜像、网络、挂载目录或 Job Docker 后端），
// 会先删除再按新配置重建——但仅限已停止的容器，正在运行的不动，见 startRunnerContainer。
func StartRunnerContainer(ctx context.Context, cfg *config.Config, runnerName, installDir string) error {
	return startRunnerContainer(ctx, cfg, runnerName, installDir, false)
}

// RecreateRunnerContainer 无条件按当前配置重建容器，正在运行也照删。
// 用于「容器在跑、但配置已经变了」这种只能由人来决定何时中断的情况。
func RecreateRunnerContainer(ctx context.Context, cfg *config.Config, runnerName, installDir string) error {
	return startRunnerContainer(ctx, cfg, runnerName, installDir, true)
}

func startRunnerContainer(ctx context.Context, cfg *config.Config, runnerName, installDir string, forceRecreate bool) error {
	if cfg.Runners.ContainerMode && managerDockerHostIsDind() {
		return fmt.Errorf("%s", errContainerModeNeedHostDocker)
	}
	cn := ContainerName(runnerName)
	defer lockRunnerOps(cn)()
	// 在创建/启动容器之前写好令牌：容器内 Agent 启动时即可从挂载目录读到
	token, tokenErr := EnsureAgentToken(installDir)
	if tokenErr != nil {
		// 拿不到令牌不阻断启停，降级为不带鉴权头（与旧版本行为一致）
		log.Printf("警告: %s %v，本次调用 Agent 不带鉴权头", runnerName, tokenErr)
	}
	facts, err := inspectRunnerContainer(ctx, cn)
	if err != nil {
		return err
	}
	if facts != nil {
		// 启停路径不用缓存：刚 build 完就点「启动」是常见操作，读到旧镜像 ID 会让重建不发生
		drift := driftFromFacts(ctx, cfg, runnerName, installDir, token, facts, resolveImageID)
		switch {
		case forceRecreate:
			log.Printf("按要求重建容器 %s%s", cn, driftSuffix(drift))
			if out, rmErr := dockerCmd(ctx, "rm", "-f", cn); rmErr != nil {
				return dockerCmdError("docker rm", out, rmErr)
			}
			// 落到下方「创建新容器」
		case facts.Running:
			// 正在运行的容器不自动重建：上面很可能正跑着 Job，删掉就是把它拦腰截断。
			// 只记一条日志，由界面提示用户停止后再启动，或显式点「重建容器」。
			if drift != "" {
				log.Printf("提示: 容器 %s 的创建参数与当前配置不一致（%s）。正在运行的容器不会自动重建，"+
					"停止后再启动，或在界面点「重建容器」即可按新配置重建", cn, drift)
			}
			// 存量容器可能是在配置资源上限之前创建的，这里补一次
			applyResourceLimitsToExisting(ctx, cn, cfg.Runners.Resources)
			// 容器已在跑，可选：调 Agent /start 确保 listener 启动（若容器刚启动 agent 可能尚未起 run.sh）
			_ = CallAgentStart(ctx, cn, cfg.Runners.AgentPort, token)
			return nil
		case drift != "":
			log.Printf("容器 %s 的创建参数与当前配置不一致（%s），删除后按新配置重建", cn, drift)
			if out, rmErr := dockerCmd(ctx, "rm", "-f", cn); rmErr != nil {
				return dockerCmdError("docker rm", out, rmErr)
			}
			// 落到下方「创建新容器」
		default:
			startOut, startErr := dockerCmd(ctx, "start", cn)
			if startErr == nil {
				applyResourceLimitsToExisting(ctx, cn, cfg.Runners.Resources)
				time.Sleep(agentReadyDelayAfterStart)
				return CallAgentStart(ctx, cn, cfg.Runners.AgentPort, token)
			}
			// start 失败且为网络已删除等不可恢复原因时，删除旧容器后重建（如 compose down 后网络被删）
			if !containerStartUnrecoverable(startOut) {
				return dockerCmdError("docker start", startOut, startErr)
			}
			_, _ = dockerCmd(ctx, "rm", "-f", cn)
		}
	}
	// 创建新容器
	// 容器模式下若 Manager 在容器内（base_path 通常为 /app/runners），未设置 volume_host_path 会导致 docker create -v 使用容器内路径，宿主机上无效
	if cfg.Runners.ContainerMode && strings.TrimSpace(cfg.Runners.VolumeHostPath) == "" {
		baseClean := filepath.Clean(cfg.Runners.BasePath)
		if strings.HasPrefix(baseClean, "/app") || strings.HasPrefix(filepath.Clean(installDir), "/app") {
			return fmt.Errorf("容器模式下 Manager 若在容器内运行，必须在 config/config.yaml 中设置 runners.volume_host_path 为宿主机上 runners 根目录的绝对路径（当前 base_path 为 %s）", cfg.Runners.BasePath)
		}
	}
	spec := desiredContainerSpec(cfg, runnerName, installDir, token)
	createArgs, err := spec.createArgs()
	if err != nil {
		return err
	}
	out, err := dockerCmd(ctx, createArgs...)
	if err != nil {
		return dockerCmdError("docker create", out, err)
	}
	out, err = dockerCmd(ctx, "start", cn)
	if err != nil {
		return dockerCmdError("docker start", out, err)
	}
	// 等待 agent 就绪后调 /start
	time.Sleep(agentReadyDelayAfterCreate)
	return CallAgentStart(ctx, cn, cfg.Runners.AgentPort, token)
}

// driftSuffix 把漂移原因拼成日志后缀，没有差异时不啰嗦
func driftSuffix(drift string) string {
	if drift == "" {
		return ""
	}
	return "（" + drift + "）"
}

// StopRunnerContainer 停止容器（不删除，便于下次 start）
func StopRunnerContainer(ctx context.Context, runnerName string) error {
	cn := ContainerName(runnerName)
	defer lockRunnerOps(cn)()
	out, err := dockerCmd(ctx, "stop", "-t", "30", cn)
	if err != nil {
		inspectOut, _ := dockerCmd(ctx, "inspect", "-f", "{{.State.Running}}", cn)
		if containerNotFound(inspectOut) {
			return nil
		}
		return dockerCmdError("docker stop", out, err)
	}
	return nil
}

// RemoveRunnerContainer 停止并删除 Runner 容器（移除 runner 时调用）
func RemoveRunnerContainer(ctx context.Context, runnerName string) error {
	cn := ContainerName(runnerName)
	defer lockRunnerOps(cn)()
	_, _ = dockerCmd(ctx, "stop", "-t", "30", cn)
	out, err := dockerCmd(ctx, "rm", "-f", cn)
	if err != nil {
		if containerNotFound(out) {
			return nil
		}
		return dockerCmdError("docker rm", out, err)
	}
	return nil
}

// ContainerRunnerStatus 在容器模式下获取某 runner 的状态：先看容器是否运行，再问 Agent。
// 同一次 inspect 顺带比对创建参数，drift 非空表示容器是按旧配置建的。
//
// 容器不存在或未运行时没有人可问，此时如实回落到磁盘状态（已注册的仍为 StatusInstalled，
// 界面据此显示「已注册未运行」）。这里曾经不看磁盘、一律返回 StatusInstalled，于是配置里
// 一个从没注册过的 runner（磁盘上是 new/missing）会被说成「已注册未运行」——界面显示错误，
// 而后台的「已注册未运行则拉起」会照着给它建容器、发 /start；安装目录压根不存在时，
// bind mount 的源路径还会被 Docker 以 root 属主创建出来，之后 Manager（UID 1001）就写不进去了。
func ContainerRunnerStatus(ctx context.Context, cfg *config.Config, runnerName, installDir string) (running bool, status Status, drift string, err error) {
	cn := ContainerName(runnerName)
	facts, err := inspectRunnerContainer(ctx, cn)
	if err != nil {
		return false, StatusUnknown, "", newProbeError(ProbeErrorTypeDockerAccess, err)
	}
	if facts == nil {
		return false, diskStatus(installDir), "", nil
	}
	// 这里也要 Ensure 而不是 Read：升级时那些正在运行的旧容器目录里还没有令牌文件，
	// 而自动拉起只管没在跑的，它们永远等不到有人替它生成——不生成就报不出 agent_token 漂移，
	// 界面上不提示，人也就不知道该点「重建容器」。生成失败时返回空串，
	// 退化为原先「没有令牌就不谈漂移」的行为，不会把容器反复删了重建。
	token, tokenErr := EnsureAgentToken(installDir)
	// 拿不到令牌意味着这个 Runner 的 Agent 不鉴权，而且 agent_token 漂移检查会因为
	// 「手头没有令牌」而跳过自己，界面上什么都看不到——至少要在日志里说一次
	WarnAgentTokenUnavailable(installDir, tokenErr)
	drift = driftFromFacts(ctx, cfg, runnerName, installDir, token, facts, cachedImageID)
	if !facts.Running {
		// 容器未跑时保留磁盘状态（已注册即「已注册未运行」），不覆盖为 unknown
		return false, diskStatus(installDir), drift, nil
	}
	agent, err := GetAgentStatus(ctx, cn, cfg.Runners.AgentPort, token)
	if err != nil {
		agentErrType := ProbeErrorTypeAgentConnect
		if strings.Contains(err.Error(), "agent 返回") {
			agentErrType = ProbeErrorTypeAgentHTTP
		}
		return true, StatusUnknown, drift, newProbeError(agentErrType, err)
	}
	switch agent.Status {
	case "installed":
		return agent.Running, StatusInstalled, drift, nil
	case "new":
		return false, StatusNew, drift, nil
	default:
		return false, StatusMissing, drift, nil
	}
}

// ContainerState 返回容器是否存在及其状态（running / exited / created 等）。
// 容器不存在时返回 exists=false 且不报错——调用方多数只关心「名字是否被占用」。
func ContainerState(ctx context.Context, containerName string) (exists bool, status string, err error) {
	facts, err := inspectRunnerContainer(ctx, containerName)
	if err != nil || facts == nil {
		return false, "", err
	}
	return true, facts.Status, nil
}
