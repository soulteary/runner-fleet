package runner

import (
	"strings"
	"testing"

	"github.com/soulteary/runner-fleet/internal/config"
)

// hostSocketSpec 一个典型的 host-socket 容器形态
func hostSocketSpec() containerSpec {
	return containerSpec{
		ContainerName: "github-runner-a",
		Image:         "img:tag",
		Network:       "runner-net",
		MountSrc:      "/host/runners/a",
		JobBackend:    "host-socket",
		DindHost:      "runner-dind",
		DockerGID:     999,
	}
}

// factsFor 按 spec 造出「一致」的 inspect 结果（带创建标签，即新版本建的容器）
func factsFor(s containerSpec) *containerFacts {
	f := legacyFactsFor(s)
	f.Labels = map[string]string{
		labelJobBackend: s.JobBackend,
		labelNetwork:    s.Network,
	}
	return f
}

// legacyFactsFor 不带标签，模拟旧版本建出来的容器
func legacyFactsFor(s containerSpec) *containerFacts {
	f := &containerFacts{
		Running:     true,
		Status:      "running",
		ImageRef:    s.Image,
		ImageID:     "sha256:aaa",
		Binds:       []string{s.runnerBind()},
		Networks:    []string{s.Network},
		NetworkMode: s.Network,
	}
	if host := s.dockerHostEnv(); host != "" {
		f.Env = append(f.Env, "DOCKER_HOST="+host)
	}
	if s.JobBackend == "host-socket" {
		f.Binds = append(f.Binds, HostDockerSocket+":"+HostDockerSocket)
		if s.DockerGID >= 0 {
			f.GroupAdd = []string{"999"}
		}
	}
	return f
}

func TestDriftReason_NoDriftWhenMatching(t *testing.T) {
	spec := hostSocketSpec()
	if got := spec.driftReason(factsFor(spec), "sha256:aaa"); got != "" {
		t.Fatalf("一致的容器不该报漂移: %s", got)
	}
	// 容器不存在时同样不该报
	if got := spec.driftReason(nil, "sha256:aaa"); got != "" {
		t.Fatalf("nil facts 不该报漂移: %s", got)
	}
}

func TestDriftReason_BackendSwitch(t *testing.T) {
	// 真实场景：容器按 host-socket 建出来，配置改成了 dind。
	// 不重建的话它依旧挂着宿主机 socket，切换等于没发生。
	old := hostSocketSpec()
	facts := factsFor(old)

	want := old
	want.JobBackend = "dind"
	got := want.driftReason(facts, "sha256:aaa")
	if !strings.Contains(got, "dind") {
		t.Fatalf("host-socket → dind 未被检出: %q", got)
	}

	// 反向：按 dind 建的容器，配置改回 host-socket
	dindSpec := old
	dindSpec.JobBackend = "dind"
	dindFacts := factsFor(dindSpec)
	if got := old.driftReason(dindFacts, "sha256:aaa"); !strings.Contains(got, "host-socket") {
		t.Fatalf("dind → host-socket 未被检出: %q", got)
	}

	// 切到 none：既不该有 DOCKER_HOST 也不该有 socket 挂载
	noneSpec := old
	noneSpec.JobBackend = "none"
	if got := noneSpec.driftReason(facts, "sha256:aaa"); !strings.Contains(got, "none") {
		t.Fatalf("host-socket → none 未被检出: %q", got)
	}
	if got := noneSpec.driftReason(factsFor(noneSpec), "sha256:aaa"); got != "" {
		t.Fatalf("none 容器与 none 配置不该报漂移: %q", got)
	}
}

func TestDriftReason_DindHostChanged(t *testing.T) {
	spec := hostSocketSpec()
	spec.JobBackend = "dind"
	facts := factsFor(spec)

	moved := spec
	moved.DindHost = "other-dind"
	got := moved.driftReason(facts, "sha256:aaa")
	if !strings.Contains(got, "other-dind") {
		t.Fatalf("DinD 地址变化未被检出: %q", got)
	}
}

func TestDriftReason_ImageRefAndRebuild(t *testing.T) {
	spec := hostSocketSpec()
	facts := factsFor(spec)

	// 换了镜像引用
	newImage := spec
	newImage.Image = "img:v2"
	if got := newImage.driftReason(facts, "sha256:aaa"); !strings.Contains(got, "img:v2") {
		t.Fatalf("镜像引用变化未被检出: %q", got)
	}

	// 同名 tag 重新构建：引用没变、镜像 ID 变了，同样要重建
	if got := spec.driftReason(facts, "sha256:bbb"); !strings.Contains(got, "rebuilt") {
		t.Fatalf("同名 tag 重新构建未被检出: %q", got)
	}

	// 镜像不在本地（拿不到 ID）时不能误报
	if got := spec.driftReason(facts, ""); got != "" {
		t.Fatalf("拿不到镜像 ID 时不该报漂移: %q", got)
	}
}

func TestDriftReason_NetworkAndMount(t *testing.T) {
	spec := hostSocketSpec()
	facts := factsFor(spec)

	otherNet := spec
	otherNet.Network = "other-net"
	if got := otherNet.driftReason(facts, "sha256:aaa"); !strings.Contains(got, "other-net") {
		t.Fatalf("网络变化未被检出: %q", got)
	}

	// volume_host_path 改了，挂载目录跟着变
	moved := spec
	moved.MountSrc = "/data/runners/a"
	if got := moved.driftReason(facts, "sha256:aaa"); !strings.Contains(got, "/data/runners/a") {
		t.Fatalf("挂载目录变化未被检出: %q", got)
	}
}

// TestDriftReason_NetworkUsesCreationNotAttachment
// docker create --network 只设一个网络，但容器可以事后被 docker network connect
// 接进别的网络。所以「它连着目标网络吗」回答不了「它是按哪个网络建的」：
// 配置从 runner-net 改到 net-b、而容器两个都连着时，按「连着就算数」会放行，
// 于是它继续连着 runner-net——恰恰是这次改配置想断掉的那条。
func TestDriftReason_NetworkUsesCreationNotAttachment(t *testing.T) {
	spec := hostSocketSpec() // Network = runner-net

	// 新版本建的容器：以创建标签为准
	labeled := factsFor(spec)
	labeled.Networks = []string{"runner-net", "net-b"} // 手工 connect 了 net-b

	// 配置仍是 runner-net：多连一个网络是运维自己接的，不该因此重建
	if got := spec.driftReason(labeled, "sha256:aaa"); got != "" {
		t.Fatalf("多连一个网络不该触发重建: %q", got)
	}

	// 配置改成 net-b：标签记的还是 runner-net，必须报
	moved := spec
	moved.Network = "net-b"
	if got := moved.driftReason(labeled, "sha256:aaa"); got != "container_network: runner-net → net-b" {
		t.Fatalf("配置改网络后仍连着旧网络，未被检出: %q", got)
	}

	// 旧版本建的容器没有标签，退回 NetworkMode 推断（它才是 create 时那个参数）
	legacy := legacyFactsFor(spec)
	legacy.Networks = []string{"runner-net", "net-b"}
	if got := moved.driftReason(legacy, "sha256:aaa"); got != "container_network: runner-net → net-b" {
		t.Fatalf("旧容器按 NetworkMode 推断未被检出: %q", got)
	}
	if got := spec.driftReason(legacy, "sha256:aaa"); got != "" {
		t.Fatalf("旧容器 NetworkMode 与配置一致时不该报: %q", got)
	}

	// 连 NetworkMode 都拿不到时保持原先的「连着就算数」，宁可漏报也不凭空重建
	blind := legacyFactsFor(spec)
	blind.NetworkMode = ""
	blind.Networks = []string{"runner-net", "net-b"}
	if got := moved.driftReason(blind, "sha256:aaa"); got != "" {
		t.Fatalf("信息不全时不该报漂移: %q", got)
	}
}

// TestCreateArgs_RecordsNetworkLabel 创建时必须把网络记进标签，否则后续只能靠推断
func TestCreateArgs_RecordsNetworkLabel(t *testing.T) {
	spec := hostSocketSpec()
	args, err := spec.createArgs()
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, labelNetwork+"=runner-net") {
		t.Fatalf("create 参数缺少网络标签: %s", joined)
	}
}

func TestDriftReason_DockerGID(t *testing.T) {
	spec := hostSocketSpec()
	facts := factsFor(spec)

	changed := spec
	changed.DockerGID = 983
	if got := changed.driftReason(facts, "sha256:aaa"); !strings.Contains(got, "983") {
		t.Fatalf("docker 组 GID 变化未被检出: %q", got)
	}

	// 探测不到 GID 时不追加 --group-add，也就无从比对，不该误报
	unknown := spec
	unknown.DockerGID = unknownDockerGID
	if got := unknown.driftReason(facts, "sha256:aaa"); got != "" {
		t.Fatalf("GID 未知时不该报漂移: %q", got)
	}
}

// TestDriftReason_LegacyContainerWithoutNetworkInfo 旧容器或 inspect 缺字段时保持沉默，
// 宁可漏报也不能凭空把容器删了重建。
func TestDriftReason_MissingFieldsStaySilent(t *testing.T) {
	spec := hostSocketSpec()
	facts := factsFor(spec)
	facts.Networks = nil
	facts.ImageRef = ""
	facts.ImageID = ""
	if got := spec.driftReason(facts, "sha256:aaa"); got != "" {
		t.Fatalf("字段缺失时不该报漂移: %q", got)
	}
}

func TestParseInspect(t *testing.T) {
	out := []byte(`[{
	  "Image": "sha256:abc",
	  "State": {"Status": "running", "Running": true},
	  "Config": {"Image": "img:tag", "Env": ["PATH=/usr/bin", "DOCKER_HOST=unix:///var/run/docker.sock"]},
	  "HostConfig": {
	    "Binds": ["/host/runners/a:/runner", "/var/run/docker.sock:/var/run/docker.sock"],
	    "GroupAdd": ["999"],
	    "NetworkMode": "runner-net"
	  },
	  "NetworkSettings": {"Networks": {"runner-net": {}}}
	}]`)
	facts, err := parseInspect(out)
	if err != nil {
		t.Fatal(err)
	}
	if !facts.Running || facts.Status != "running" {
		t.Fatalf("状态解析错误: %+v", facts)
	}
	if facts.ImageRef != "img:tag" || facts.ImageID != "sha256:abc" {
		t.Fatalf("镜像解析错误: %+v", facts)
	}
	if !containsString(facts.Networks, "runner-net") {
		t.Fatalf("网络解析错误: %+v", facts.Networks)
	}
	if src, ok := bindSource(facts.Binds, runnerMountDest); !ok || src != "/host/runners/a" {
		t.Fatalf("挂载解析错误: %v", facts.Binds)
	}
	if v, ok := envValue(facts.Env, "DOCKER_HOST"); !ok || v != "unix:///var/run/docker.sock" {
		t.Fatalf("环境变量解析错误: %v", facts.Env)
	}

	// 空数组表示容器不存在
	empty, err := parseInspect([]byte(`[]`))
	if err != nil || empty != nil {
		t.Fatalf("空结果应返回 nil, nil，得到 %v, %v", empty, err)
	}
}

// TestDesiredContainerSpec_UsesVolumeHostPath Manager 在容器内时，挂载源必须换算成宿主机路径
func TestDesiredContainerSpec_UsesVolumeHostPath(t *testing.T) {
	cfg := &config.Config{Runners: config.RunnersConfig{
		BasePath:         "/app/runners",
		ContainerMode:    true,
		ContainerImage:   "img:tag",
		ContainerNetwork: "runner-net",
		JobDockerBackend: "none",
		VolumeHostPath:   "/data/runners",
	}}
	spec := desiredContainerSpec(cfg, "a", "/app/runners/a", "")
	if spec.MountSrc != "/data/runners/a" {
		t.Fatalf("MountSrc = %q, want /data/runners/a", spec.MountSrc)
	}
	if spec.ContainerName != "github-runner-a" {
		t.Fatalf("ContainerName = %q", spec.ContainerName)
	}
	args, err := spec.createArgs()
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "/data/runners/a:/runner") {
		t.Fatalf("create 参数未使用宿主机路径: %s", joined)
	}
}

// TestDriftFromFacts_SkipsNonContainerMode 非容器模式下没有 Runner 容器可言，不该谈漂移
func TestDriftFromFacts_SkipsNonContainerMode(t *testing.T) {
	cfg := &config.Config{Runners: config.RunnersConfig{BasePath: "/app/runners"}}
	spec := hostSocketSpec()
	if got := driftFromFacts(t.Context(), cfg, "a", "/app/runners/a", "tok", factsFor(spec), resolveImageID); got != "" {
		t.Fatalf("非容器模式不该报漂移: %q", got)
	}
	if got := driftFromFacts(t.Context(), nil, "a", "/app/runners/a", "tok", factsFor(spec), resolveImageID); got != "" {
		t.Fatalf("配置为空不该报漂移: %q", got)
	}
}

// TestDriftReason_ImageProvidedDockerHostIsNotOurs
// 自定义 Runner 镜像在 Dockerfile 里写了 ENV DOCKER_HOST 时，docker inspect 的 Config.Env
// 里也会有它。若把它当成我们注入的，backend=none 会被反复判成漂移，每次启动都删容器重建。
// 标签是正解：带标签的容器一律按标签判，镜像自带的 ENV 再也干扰不到。
func TestDriftReason_ImageProvidedDockerHostIsNotOurs(t *testing.T) {
	spec := hostSocketSpec()
	spec.JobBackend = "none"

	withLabel := factsFor(spec)
	withLabel.Env = append(withLabel.Env, "DOCKER_HOST=tcp://my-own-dind:2375")
	if got := spec.driftReason(withLabel, "sha256:aaa"); got != "" {
		t.Fatalf("有标签时不该被镜像自带的 DOCKER_HOST 干扰: %q", got)
	}

	// 真正由我们注入的值要认出来：之前是 host-socket，现在配置成 none
	wasHostSocket := legacyFactsFor(hostSocketSpec())
	if got := spec.driftReason(wasHostSocket, "sha256:aaa"); got == "" {
		t.Fatal("host-socket → none 漏报")
	}
}

// TestDriftReason_LegacyImageDockerHostSettlesAfterOneRebuild
// 没有标签的旧容器分不出 DOCKER_HOST 是我们注入的还是镜像自带的（见 looksInjectedDockerHost），
// 这里选择宁可多重建一次也不漏报。这一次的代价必须是有界的：重建后容器带上标签，
// 从此走标签比对，不能变成每次启动都重建。
func TestDriftReason_LegacyImageDockerHostSettlesAfterOneRebuild(t *testing.T) {
	spec := hostSocketSpec()
	spec.JobBackend = "none"

	legacy := legacyFactsFor(spec)
	legacy.Env = append(legacy.Env, "DOCKER_HOST=tcp://my-own-dind:2375")
	if got := spec.driftReason(legacy, "sha256:aaa"); got == "" {
		t.Fatal("旧容器上的 tcp://…:2375 一律当作可能是我们注入的，应报漂移")
	}

	// 重建之后：同样的 ENV 还在（none 不会去掉镜像自带的变量），但标签在了
	rebuilt := factsFor(spec)
	rebuilt.Env = append(rebuilt.Env, "DOCKER_HOST=tcp://my-own-dind:2375")
	if got := spec.driftReason(rebuilt, "sha256:aaa"); got != "" {
		t.Fatalf("重建一次后应当安静下来，否则就是反复重建: %q", got)
	}
}

// TestDriftReason_LegacyDindHostChangedThenNone
// 旧容器按 dind + dind_host=old-dind 建出来，之后配置改成 none 并且把 dind_host 也换了
// （不用 dind 了顺手改掉，很自然）。若只认当前的 dind_host，tcp://old-dind:2375 会被当成
// 与我们无关而漏报，容器于是带着通往旧 DinD 的 DOCKER_HOST 继续跑——none 想断的正是这个。
func TestDriftReason_LegacyDindHostChangedThenNone(t *testing.T) {
	old := hostSocketSpec()
	old.JobBackend = "dind"
	old.DindHost = "old-dind"
	legacy := legacyFactsFor(old) // DOCKER_HOST=tcp://old-dind:2375，无 socket 挂载

	for _, nowHost := range []string{"new-dind", "runner-dind"} {
		now := hostSocketSpec()
		now.JobBackend = "none"
		now.DindHost = nowHost // 改成别的地址 / 删掉后回落默认值
		if got := now.driftReason(legacy, "sha256:aaa"); got == "" {
			t.Fatalf("dind_host=%s 时漏报：容器仍可达 old-dind，配置却是 none", nowHost)
		}
	}
}

// TestDriftReason_LabelWins 标签与配置不一致即漂移，不必再看 DOCKER_HOST
func TestDriftReason_LabelWins(t *testing.T) {
	spec := hostSocketSpec()
	facts := factsFor(spec) // 标签记的是 host-socket

	want := spec
	want.JobBackend = "dind"
	got := want.driftReason(facts, "sha256:aaa")
	if got != "job_docker_backend: host-socket → dind" {
		t.Fatalf("标签比对结果不对: %q", got)
	}
}

// TestCreateArgs_RecordsBackendLabel 创建时必须打上标签，否则后续只能靠推断
func TestCreateArgs_RecordsBackendLabel(t *testing.T) {
	for _, backend := range []string{"dind", "host-socket", "none"} {
		spec := hostSocketSpec()
		spec.JobBackend = backend
		args, err := spec.createArgs()
		if err != nil {
			t.Fatal(err)
		}
		want := labelJobBackend + "=" + backend
		if !containsString(args, want) {
			t.Fatalf("backend=%s 的 create 参数缺少标签 %s: %v", backend, want, args)
		}
	}
}

// TestDriftReason_MissingAgentToken
// 本特性之前建的容器没有 AGENT_TOKEN，Agent 退化为不鉴权——同网络里的其它容器就能控制它。
// 这种容器应当被判为漂移，下次启动时自动补上。
func TestDriftReason_MissingAgentToken(t *testing.T) {
	spec := hostSocketSpec()
	spec.AgentToken = "tok-abc"
	facts := factsFor(spec) // factsFor 不注入 AGENT_TOKEN，正是旧容器的样子

	if got := spec.driftReason(facts, "sha256:aaa"); got != "agent_token: (none) → set" {
		t.Fatalf("缺少 AGENT_TOKEN 未被检出: %q", got)
	}

	// 已注入过就不管取值是否相同：令牌轮换属于运行期故障，由探测暴露，不该删容器
	withToken := factsFor(spec)
	withToken.Env = append(withToken.Env, "AGENT_TOKEN=tok-old")
	if got := spec.driftReason(withToken, "sha256:aaa"); got != "" {
		t.Fatalf("已注入令牌的容器不该报漂移: %q", got)
	}

	// 手头没有令牌时（读不到文件）不做判断
	noToken := spec
	noToken.AgentToken = ""
	if got := noToken.driftReason(facts, "sha256:aaa"); got != "" {
		t.Fatalf("没有令牌可注入时不该报漂移: %q", got)
	}
}

// TestDriftReason_EmptyAgentTokenCountsAsMissing
// 「有这个变量」不等于「有令牌」。镜像里一句 ENV AGENT_TOKEN= 就会让变量存在而取值为空，
// Agent 侧 TrimSpace 后当作未配置，转去读挂载的 .agent_token；读不到（root 拥有的 0600，
// 正是本项要修的迁移场景）就完全不鉴权，而且没有任何 401 能暴露它。
// 只看键在不在的话，这种容器会永远绕过重建，本项的修复对它等于没做。
func TestDriftReason_EmptyAgentTokenCountsAsMissing(t *testing.T) {
	spec := hostSocketSpec()
	spec.AgentToken = "tok-abc"

	for _, v := range []string{"", "   ", "\t"} {
		facts := factsFor(spec)
		facts.Env = append(facts.Env, "AGENT_TOKEN="+v)
		if got := spec.driftReason(facts, "sha256:aaa"); got != "agent_token: (none) → set" {
			t.Fatalf("AGENT_TOKEN=%q（Agent 判定为未配置）未被检出: %q", v, got)
		}
	}

	// 对照：非空取值仍然只看「有」，不比对是否与当前令牌相同
	ok := factsFor(spec)
	ok.Env = append(ok.Env, "AGENT_TOKEN=tok-old")
	if got := spec.driftReason(ok, "sha256:aaa"); got != "" {
		t.Fatalf("非空令牌不该因取值不同而报漂移: %q", got)
	}
}
