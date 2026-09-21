// 容器配置漂移检测。
//
// Runner 容器的镜像、网络、挂载目录、Job Docker 后端全都只在 docker create 那一刻定下来，
// 之后改配置对已经存在的容器没有任何影响——docker start 只是把原样的容器再拉起来。
// 于是「改了配置却不生效」成了最容易踩的坑：以 host-socket 建出来的 Runner，在切到 dind
// 之后依旧挂着宿主机 socket，本想收紧的隔离根本没发生，而界面上一切正常。
//
// 这里把「按当前配置该建成什么样」抽成 containerSpec：创建参数与漂移比对都由它产出，
// 两边不会各写一套。比对的是 docker inspect 回来的实际值，所以旧版本建的容器同样适用。
package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/soulteary/runner-fleet/internal/config"
)

// runnerMountDest Runner 目录在容器内的挂载点
const runnerMountDest = "/runner"

// labelJobBackend 创建容器时把当时的 Job Docker 后端记在标签上。
// 不能只靠 DOCKER_HOST 反推：docker inspect 的 Config.Env 里混着镜像自带的 ENV，
// 自定义 Runner 镜像若写了 ENV DOCKER_HOST，backend=none 会被永远判成漂移，
// 于是每次启动都把容器删了重建。标签是我们自己写的，不会与镜像混淆。
const labelJobBackend = "io.runner-fleet.job-docker-backend"

// labelNetwork 创建时写下的网络名。与后端标签同理：
// 容器可以事后被 docker network connect 接进别的网络，所以「它连着哪些网络」
// 回答不了「它是按哪个网络建的」，只有创建时记下来才作数。
const labelNetwork = "io.runner-fleet.network"

// containerSpec 按当前配置推导出的容器形态
type containerSpec struct {
	ContainerName string
	Image         string
	Network       string
	MountSrc      string // 宿主机上的 runner 目录
	JobBackend    string
	DindHost      string
	DockerGID     int // unknownDockerGID 表示不追加 --group-add
	AgentToken    string
	Limits        config.ResourceLimits
}

// desiredContainerSpec 按配置推导某个 Runner 的容器形态。
// 镜像与 Job Docker 后端支持按 Runner 覆盖，未设置时回落全局配置。
func desiredContainerSpec(cfg *config.Config, runnerName, installDir, agentToken string) containerSpec {
	network := cfg.Runners.ContainerNetwork
	if network == "" {
		network = "runner-net"
	}
	dindHost := cfg.Runners.DindHost
	if dindHost == "" {
		dindHost = "runner-dind"
	}
	return containerSpec{
		ContainerName: ContainerName(runnerName),
		Image:         cfg.ContainerImageFor(runnerName),
		Network:       network,
		MountSrc:      hostMountSource(cfg, installDir),
		JobBackend:    cfg.JobDockerBackendFor(runnerName),
		DindHost:      dindHost,
		DockerGID:     runnerDockerGID(cfg),
		AgentToken:    agentToken,
		Limits:        cfg.Runners.Resources,
	}
}

// hostMountSource 返回挂载到容器 /runner 的宿主机路径。
// Manager 在容器内时 installDir 是容器内路径，docker 需要宿主机路径，用 volume_host_path 换算。
func hostMountSource(cfg *config.Config, installDir string) string {
	if cfg.Runners.VolumeHostPath != "" {
		rel, err := filepath.Rel(cfg.Runners.BasePath, installDir)
		if err != nil {
			rel = filepath.Base(installDir)
		}
		return filepath.Join(cfg.Runners.VolumeHostPath, rel)
	}
	// Manager 在宿主机时传绝对路径，避免受 cwd 影响
	if abs, err := filepath.Abs(installDir); err == nil {
		return abs
	}
	return installDir
}

// dockerHostEnv 返回该后端下注入容器的 DOCKER_HOST；none 返回空串表示不注入
func (s containerSpec) dockerHostEnv() string {
	switch s.JobBackend {
	case "dind":
		return "tcp://" + s.DindHost + ":2375"
	case "host-socket":
		return "unix://" + HostDockerSocket
	default:
		return ""
	}
}

// runnerBind /runner 的挂载项，与 docker inspect 的 HostConfig.Binds 写法一致
func (s containerSpec) runnerBind() string {
	return s.MountSrc + ":" + runnerMountDest
}

// createArgs 组装 docker create 参数。
// DockerGID 为 unknownDockerGID 时不追加 --group-add，仅依赖镜像内预置的 docker 组。
// Limits 中未设置的字段不产生任何参数，保持与旧版本一致的「不限制」行为。
func (s containerSpec) createArgs() ([]string, error) {
	args := []string{
		"create",
		"--name", s.ContainerName,
		"-v", s.runnerBind(),
		"--network", s.Network,
		"--label", labelJobBackend + "=" + s.JobBackend,
		"--label", labelNetwork + "=" + s.Network,
	}
	// 资源上限：不限制时单个失控 Job 能耗尽整机资源，连带拖垮 Manager
	args = append(args, s.Limits.Args()...)
	// 令牌用环境变量注入，而不是只靠挂载目录下的文件：
	// Manager 以 root 或非 1001 的 UID 运行时，0600 的令牌文件对容器内 app(1001)
	// 不可读，Agent 会读到空令牌并静默降级为不鉴权。环境变量不依赖 UID 匹配。
	if s.AgentToken != "" {
		args = append(args, "-e", "AGENT_TOKEN="+s.AgentToken)
	}
	switch s.JobBackend {
	case "dind":
		args = append(args, "-e", "DOCKER_HOST="+s.dockerHostEnv())
	case "host-socket":
		args = append(args, "-v", HostDockerSocket+":"+HostDockerSocket, "-e", "DOCKER_HOST="+s.dockerHostEnv())
		// 容器内以 app(UID 1001) 运行，须加入 socket 所属组，否则 Job 中 docker 报 permission denied
		if s.DockerGID >= 0 {
			args = append(args, "--group-add", strconv.Itoa(s.DockerGID))
		}
	case "none":
		// Job 内不提供 Docker，不注入环境与挂载
	default:
		return nil, fmt.Errorf("不支持的 runners.job_docker_backend=%q（仅支持 dind/host-socket/none）", s.JobBackend)
	}
	return append(args, s.Image), nil
}

// containerFacts docker inspect 中与创建参数有关的实际值
type containerFacts struct {
	Running  bool
	Status   string
	ImageRef string // 创建时写下的镜像引用
	ImageID  string // 实际使用的镜像 ID，同名 tag 重新构建后会变
	Binds    []string
	Env      []string
	Networks []string
	// NetworkMode 是 docker create --network 传进去的那个，与事后 connect 上来的区分开
	NetworkMode string
	GroupAdd    []string
	Labels      map[string]string
}

// dockerInspectRaw 只取需要的字段，其余忽略
type dockerInspectRaw struct {
	Image string `json:"Image"`
	State struct {
		Status  string `json:"Status"`
		Running bool   `json:"Running"`
	} `json:"State"`
	Config struct {
		Image  string            `json:"Image"`
		Env    []string          `json:"Env"`
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
	HostConfig struct {
		Binds       []string `json:"Binds"`
		GroupAdd    []string `json:"GroupAdd"`
		NetworkMode string   `json:"NetworkMode"`
	} `json:"HostConfig"`
	NetworkSettings struct {
		Networks map[string]json.RawMessage `json:"Networks"`
	} `json:"NetworkSettings"`
}

// inspectRunnerContainer 取容器实际状态与创建参数；容器不存在时返回 (nil, nil)
func inspectRunnerContainer(ctx context.Context, containerName string) (*containerFacts, error) {
	out, err := dockerCmd(ctx, "inspect", containerName)
	if err != nil {
		if containerNotFound(out) {
			return nil, nil
		}
		return nil, dockerCmdError("docker inspect", out, err)
	}
	return parseInspect(out)
}

// parseInspect 解析 docker inspect 的输出（数组，取第一项）
func parseInspect(out []byte) (*containerFacts, error) {
	var raw []dockerInspectRaw
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("解析 docker inspect 输出失败: %w", err)
	}
	if len(raw) == 0 {
		return nil, nil
	}
	c := raw[0]
	facts := &containerFacts{
		Running:  c.State.Running,
		Status:   c.State.Status,
		ImageRef: c.Config.Image,
		ImageID:  c.Image,
		Binds:    c.HostConfig.Binds,
		Env:      c.Config.Env,
		GroupAdd: c.HostConfig.GroupAdd,
		Labels:   c.Config.Labels,
	}
	facts.NetworkMode = c.HostConfig.NetworkMode
	// 网络名在 NetworkMode 与 NetworkSettings.Networks 里都能拿到，两处都收
	if c.HostConfig.NetworkMode != "" {
		facts.Networks = append(facts.Networks, c.HostConfig.NetworkMode)
	}
	for name := range c.NetworkSettings.Networks {
		if name != c.HostConfig.NetworkMode {
			facts.Networks = append(facts.Networks, name)
		}
	}
	return facts, nil
}

// resolveImageID 取镜像引用当前对应的镜像 ID；镜像不在本地时返回空串（此时只比对引用本身，不去拉取）
func resolveImageID(ctx context.Context, imageRef string) string {
	out, err := dockerCmd(ctx, "image", "inspect", "-f", "{{.Id}}", imageRef)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// imageIDCache 缓存「镜像引用 → 镜像 ID」。
//
// 列表页每个 Runner 都要比一次镜像，而它们通常共用同一个镜像：不缓存的话每刷新一次页面
// 就多出 N 次 docker image inspect，每次都是一个新进程。镜像 ID 只在重新构建或拉取时变，
// 短暂过期只会让「配置已变更」徽标晚几秒出现。
//
// 启停路径不走这个缓存：那里刚重新构建完就点「启动」是常见操作，读到旧 ID 会让重建不发生。
var imageIDCache sync.Map // imageRef -> imageIDEntry

type imageIDEntry struct {
	id string
	at time.Time
}

const imageIDCacheTTL = 10 * time.Second

// cachedImageID 带 TTL 的镜像 ID 查询，供状态展示使用
func cachedImageID(ctx context.Context, imageRef string) string {
	if v, ok := imageIDCache.Load(imageRef); ok {
		if e := v.(imageIDEntry); time.Since(e.at) < imageIDCacheTTL {
			return e.id
		}
	}
	id := resolveImageID(ctx, imageRef)
	imageIDCache.Store(imageRef, imageIDEntry{id: id, at: time.Now()})
	return id
}

// envValue 从 docker inspect 的 Env 里取某个变量的值
func envValue(env []string, key string) (string, bool) {
	prefix := key + "="
	for _, e := range env {
		if strings.HasPrefix(e, prefix) {
			return strings.TrimPrefix(e, prefix), true
		}
	}
	return "", false
}

// bindSource 从 Binds 里找挂到 dest 的宿主机路径
func bindSource(binds []string, dest string) (string, bool) {
	for _, b := range binds {
		parts := strings.Split(b, ":")
		if len(parts) >= 2 && parts[1] == dest {
			return parts[0], true
		}
	}
	return "", false
}

// driftReason 比对已有容器与期望形态，返回差异说明；"" 表示一致。
// 说明写成「配置键: 旧值 → 新值」：既与界面语言无关（会原样进日志与 tooltip），
// 也直接告诉人该去翻哪个配置项。
// desiredImageID 为空表示镜像不在本地、拿不到 ID，此时只比对镜像引用。
func (s containerSpec) driftReason(facts *containerFacts, desiredImageID string) string {
	if facts == nil {
		return ""
	}
	if facts.ImageRef != "" && facts.ImageRef != s.Image {
		return fmt.Sprintf("container_image: %s → %s", facts.ImageRef, s.Image)
	}
	// 同名 tag 重新构建后引用不变、ID 会变，这种「镜像内容换了」的情况同样要重建
	if desiredImageID != "" && facts.ImageID != "" && facts.ImageID != desiredImageID {
		return fmt.Sprintf("container_image rebuilt: %s", s.Image)
	}
	if reason := s.networkDrift(facts); reason != "" {
		return reason
	}
	if src, ok := bindSource(facts.Binds, runnerMountDest); ok && src != s.MountSrc {
		return fmt.Sprintf("volume_host_path: %s → %s", src, s.MountSrc)
	}
	if reason := s.backendDrift(facts); reason != "" {
		return reason
	}
	// 令牌只看「有没有一个非空值」：本特性之前建的容器没有 AGENT_TOKEN，Agent 退化为不鉴权，
	// 同网络里的其它容器就能控制这个 Runner。重建一次即可补上。
	//
	// 刻意不比对取值——令牌不一致属于运行期故障（Agent 返回 401），由探测去暴露，
	// 不该成为删容器的理由。但「空值」不是不一致，而是没有：Agent 侧 TrimSpace 后为空
	// 就会转去读挂载的 .agent_token，读不到（root 拥有的 0600，正是本项要修的迁移场景）
	// 便完全不鉴权，且没有任何 401 能暴露它。所以这里按 Agent 的口径判空，
	// 否则镜像里一句 ENV AGENT_TOKEN= 就能让这个容器永远绕过重建。
	if s.AgentToken != "" {
		if v, ok := envValue(facts.Env, "AGENT_TOKEN"); !ok || strings.TrimSpace(v) == "" {
			return "agent_token: (none) → set"
		}
	}
	if s.JobBackend == "host-socket" && s.DockerGID >= 0 {
		gid := strconv.Itoa(s.DockerGID)
		if len(facts.GroupAdd) > 0 && !containsString(facts.GroupAdd, gid) {
			return fmt.Sprintf("docker_gid: %s → %s", strings.Join(facts.GroupAdd, ","), gid)
		}
		if len(facts.GroupAdd) == 0 {
			return "docker_gid: (none) → " + gid
		}
	}
	return ""
}

// networkDrift 比对容器所在网络：新容器看创建标签，旧容器只能从 NetworkMode 推断。
//
// 不能只问「容器有没有连在目标网络上」：docker create --network 只设一个网络，
// 而容器可以另外被 docker network connect 接进别的网络。配置从 runner-net 改成 net-b、
// 容器又恰好两个都连着时，「任一命中」会放行，于是它继续连着 runner-net——
// 恰恰是这次改配置想断掉的那条。
//
// NetworkMode 才是 create 时那个参数，但它可能被规范化（或在旧 docker 上取值不同），
// 直接拿它做严格比对，万一对不上就会每次启动都重建。所以与后端一样记在创建标签上：
// 带标签的以标签为准，没有标签的才退回 NetworkMode 推断——推断即使误判也只重建一次，
// 重建后就有标签了，不会反复。
func (s containerSpec) networkDrift(facts *containerFacts) string {
	if got, ok := facts.Labels[labelNetwork]; ok {
		if got != s.Network {
			return fmt.Sprintf("container_network: %s → %s", got, s.Network)
		}
		return ""
	}
	if facts.NetworkMode != "" {
		if facts.NetworkMode != s.Network {
			return fmt.Sprintf("container_network: %s → %s", facts.NetworkMode, s.Network)
		}
		return ""
	}
	// 连 NetworkMode 都拿不到（旧 docker、信息不全）：退回「连着就算数」，
	// 与其余比对项一样，宁可漏报也不凭空把容器删了重建
	if len(facts.Networks) > 0 && !containsString(facts.Networks, s.Network) {
		return fmt.Sprintf("container_network: %s → %s", strings.Join(facts.Networks, ","), s.Network)
	}
	return ""
}

// backendDrift 比对 Job Docker 后端：新容器看标签，旧容器只能从 DOCKER_HOST 与 socket 挂载反推
func (s containerSpec) backendDrift(facts *containerFacts) string {
	if got, ok := facts.Labels[labelJobBackend]; ok {
		if got != s.JobBackend {
			return "job_docker_backend: " + got + " → " + s.JobBackend
		}
		if s.JobBackend == "dind" {
			if env, has := envValue(facts.Env, "DOCKER_HOST"); has && env != s.dockerHostEnv() {
				return fmt.Sprintf("dind_host: %s → %s", env, s.dockerHostEnv())
			}
		}
		return ""
	}
	return s.legacyBackendDrift(facts)
}

// legacyBackendDrift 没有标签的存量容器：只认「我们自己会写出来的 DOCKER_HOST 取值」，
// 镜像自带的同名变量一律当作与我们无关，宁可漏报也不误删容器。
// 这类容器被重建一次之后就带上标签，不再走这条路径。
func (s containerSpec) legacyBackendDrift(facts *containerFacts) string {
	got, _ := envValue(facts.Env, "DOCKER_HOST")
	_, hasSocket := bindSource(facts.Binds, HostDockerSocket)
	injected := hasSocket || looksInjectedDockerHost(got)

	switch s.JobBackend {
	case "none":
		if injected {
			return "job_docker_backend: → none"
		}
	case "host-socket":
		if got != s.dockerHostEnv() || !hasSocket {
			return "job_docker_backend: → host-socket"
		}
	case "dind":
		if hasSocket {
			return "job_docker_backend: → dind"
		}
		if got == s.dockerHostEnv() {
			return ""
		}
		// 值仍是个 DinD 地址，说明之前也是 dind，只是地址变了，报得具体些
		if strings.HasPrefix(got, "tcp://") && strings.HasSuffix(got, ":2375") {
			return fmt.Sprintf("dind_host: %s → %s", got, s.dockerHostEnv())
		}
		return "job_docker_backend: → dind"
	}
	return ""
}

// looksInjectedDockerHost DOCKER_HOST 的取值是否像我们注入的。
//
// 刻意不与当前的 dind_host 比对：容器是按「当时」的配置建的，而切到 none 时往往顺手把
// dind_host 也改掉或删掉（不用 dind 了留着它没意义）。只认当前值的话，tcp://old-dind:2375
// 会被当成与我们无关而漏报，容器于是带着通往旧 DinD 的 DOCKER_HOST 继续跑——正是 none
// 要断掉的那条路。所以按形状认，与 legacyBackendDrift 的 dind 分支保持一致。
//
// 代价：自定义镜像自带的 ENV DOCKER_HOST 若恰好也是 tcp://…:2375，这类旧容器会被多重建一次
// （重建并不会去掉镜像自带的 ENV，那一次是白做的）。但重建后它就带上了标签，之后一律走标签
// 比对，不会反复——拿一次无谓重建换「不漏掉一条真实的 Docker 通路」。
func looksInjectedDockerHost(v string) bool {
	if v == "" {
		return false
	}
	if v == "unix://"+HostDockerSocket {
		return true
	}
	// 我们注入的 DinD 地址形如 tcp://<dind_host>:2375
	return strings.HasPrefix(v, "tcp://") && strings.HasSuffix(v, ":2375")
}

func containsString(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

// imageIDResolver 查询镜像 ID 的方式：状态展示用带缓存的，启停用实时的
type imageIDResolver func(context.Context, string) string

// driftFromFacts 已经拿到 inspect 结果时的比对入口，省掉重复的 docker inspect。
//
// agentToken 由调用方用 EnsureAgentToken 取得并传入，这里不自己去读：
// 令牌「有没有」决定着要不要报 agent_token 漂移，而它是否存在取决于调用方有没有先生成。
// 早先这里直接 ReadAgentToken，于是升级时那些**正在运行**的旧容器成了死角——
// 它们的目录里还没有令牌文件，而自动拉起只管没在跑的，没人会去生成，
// 读出来永远是空，检查被跳过，界面上不提示，容器就一直不鉴权。
func driftFromFacts(ctx context.Context, cfg *config.Config, runnerName, installDir, agentToken string, facts *containerFacts, imageID imageIDResolver) string {
	if cfg == nil || !cfg.Runners.ContainerMode || facts == nil {
		return ""
	}
	// 比对只看容器有没有被注入过 AGENT_TOKEN，不比对取值
	spec := desiredContainerSpec(cfg, runnerName, installDir, agentToken)
	if _, err := spec.createArgs(); err != nil {
		// 后端配置本身非法时不谈漂移，启动时会报明确错误
		return ""
	}
	return spec.driftReason(facts, imageID(ctx, spec.Image))
}
