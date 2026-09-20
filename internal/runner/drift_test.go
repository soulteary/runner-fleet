package runner

import (
	"strings"
	"testing"

	"github.com/lab-dev/github-actions-runner-manager/internal/config"
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

// factsFor 按 spec 造出「一致」的 inspect 结果，测试再逐项改动它
func factsFor(s containerSpec) *containerFacts {
	f := &containerFacts{
		Running:  true,
		Status:   "running",
		ImageRef: s.Image,
		ImageID:  "sha256:aaa",
		Binds:    []string{s.runnerBind()},
		Networks: []string{s.Network},
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
	if got := driftFromFacts(t.Context(), cfg, "a", "/app/runners/a", factsFor(spec)); got != "" {
		t.Fatalf("非容器模式不该报漂移: %q", got)
	}
	if got := driftFromFacts(t.Context(), nil, "a", "/app/runners/a", factsFor(spec)); got != "" {
		t.Fatalf("配置为空不该报漂移: %q", got)
	}
}
