package runner

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/soulteary/runner-fleet/internal/config"
)

func findCheck(t *testing.T, results []CheckResult, name string) CheckResult {
	t.Helper()
	for _, r := range results {
		if r.Name == name {
			return r
		}
	}
	t.Fatalf("未找到自检项 %q，实际: %+v", name, results)
	return CheckResult{}
}

func TestCheckBasePath_WritableDir(t *testing.T) {
	cfg := &config.Config{}
	cfg.Runners.BasePath = t.TempDir()
	got := checkBasePath(cfg)
	if got.Level != CheckOK {
		t.Fatalf("可写目录应通过: %+v", got)
	}
	// 探测文件必须清理干净，否则会混进 runner 列表
	entries, err := os.ReadDir(cfg.Runners.BasePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("自检不应留下文件: %v", entries)
	}
}

func TestCheckBasePath_DoesNotTouchExistingFile(t *testing.T) {
	// 自检用固定文件名时会把同名的用户文件打开又删掉；探测必须用唯一名
	dir := t.TempDir()
	victim := filepath.Join(dir, ".preflight-write-test")
	if err := os.WriteFile(victim, []byte("用户数据"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{}
	cfg.Runners.BasePath = dir
	if got := checkBasePath(cfg); got.Level != CheckOK {
		t.Fatalf("目录可写应通过: %+v", got)
	}
	data, err := os.ReadFile(victim)
	if err != nil {
		t.Fatalf("同名文件被自检删除了: %v", err)
	}
	if string(data) != "用户数据" {
		t.Errorf("同名文件内容被改写: %q", data)
	}
}

func TestCheckBasePath_Missing(t *testing.T) {
	cfg := &config.Config{}
	cfg.Runners.BasePath = filepath.Join(t.TempDir(), "not-created")
	got := checkBasePath(cfg)
	if got.Level != CheckError {
		t.Fatalf("目录不存在应报 error: %+v", got)
	}
	if !strings.Contains(got.Hint, "mkdir -p") {
		t.Errorf("应给出可照做的建议: %q", got.Hint)
	}
}

func TestCheckBasePath_NotADirectory(t *testing.T) {
	f := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(f, []byte(""), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{}
	cfg.Runners.BasePath = f
	if got := checkBasePath(cfg); got.Level != CheckError {
		t.Fatalf("普通文件应报 error: %+v", got)
	}
}

func TestCheckBasePath_Unwritable(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root 会绕过目录权限位，无法构造不可写场景")
	}
	dir := filepath.Join(t.TempDir(), "ro")
	if err := os.Mkdir(dir, 0500); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{}
	cfg.Runners.BasePath = dir
	got := checkBasePath(cfg)
	if got.Level != CheckError || !strings.Contains(got.Hint, "chown") {
		t.Fatalf("不可写目录应报 error 并提示 chown: %+v", got)
	}
}

func TestCheckDefaultModeDocker_DinDReachable(t *testing.T) {
	// 起一个真实监听端口，确认可达时才报 ok
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()

	t.Setenv("DOCKER_HOST", "tcp://"+ln.Addr().String())
	got := checkDefaultModeDocker(context.Background())
	if got.Level != CheckOK || !strings.Contains(got.Message, "reachable") {
		t.Fatalf("DinD 可达时应为 ok: %+v", got)
	}
}

func TestCheckDefaultModeDocker_DinDUnreachable(t *testing.T) {
	// 只看 tcp:// 前缀就报 ok，会在 DinD 没起来时给出假绿灯
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close() // 立刻关掉，确保该端口不可连

	t.Setenv("DOCKER_HOST", "tcp://"+addr)
	got := checkDefaultModeDocker(context.Background())
	if got.Level != CheckWarn {
		t.Fatalf("DinD 不可达时不能报 ok: %+v", got)
	}
}

func TestTCPAddr(t *testing.T) {
	if got := tcpAddr("runner-dind"); got != "runner-dind:2375" {
		t.Errorf("缺省端口应补 2375: %q", got)
	}
	if got := tcpAddr("runner-dind:2376"); got != "runner-dind:2376" {
		t.Errorf("已带端口不应改动: %q", got)
	}
}

func TestCheckDefaultModeDocker_MissingSocket(t *testing.T) {
	t.Setenv("DOCKER_HOST", "unix://"+filepath.Join(t.TempDir(), "absent.sock"))
	got := checkDefaultModeDocker(context.Background())
	if got.Level != CheckWarn {
		t.Fatalf("socket 不存在应 warn: %+v", got)
	}
}

func TestPreflight_DefaultModeSkipsContainerChecks(t *testing.T) {
	// 默认模式下不应去 inspect 网络/镜像，避免在没有 docker 的宿主机上刷一堆无关报错
	cfg := &config.Config{}
	cfg.Runners.BasePath = t.TempDir()
	cfg.Runners.ContainerMode = false
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	t.Setenv("DOCKER_HOST", "tcp://"+ln.Addr().String())

	results := Preflight(context.Background(), cfg)
	// 按名字断言而不是按条数：加一项无关的检查不该让这个用例失败，
	// 它要钉的是「默认模式下不碰容器」
	var names []string
	for _, r := range results {
		names = append(names, r.Name)
		if r.Name == "container network" || r.Name == "runner image" {
			t.Errorf("默认模式不应执行容器相关检查: %+v", r)
		}
	}
	for _, want := range []string{"runners directory", "runner directory permissions", "Docker in jobs"} {
		findCheck(t, results, want)
	}
	if len(results) != 3 {
		t.Fatalf("默认模式的检查项应为 %v，实际 %v", []string{"runners directory", "runner directory permissions", "Docker in jobs"}, names)
	}
}

func TestPreflight_NilConfig(t *testing.T) {
	got := Preflight(context.Background(), nil)
	if len(got) != 1 || got[0].Level != CheckError {
		t.Fatalf("配置为空应返回单条 error: %+v", got)
	}
}

func TestCheckJobDockerBackend_None(t *testing.T) {
	cfg := &config.Config{}
	cfg.Runners.JobDockerBackend = "none"
	if got := checkJobDockerBackend(context.Background(), cfg); got.Level != CheckOK {
		t.Fatalf("none 后端无前置条件，应通过: %+v", got)
	}
}

func TestInGroup(t *testing.T) {
	if !inGroup(os.Getgid()) {
		t.Error("当前进程应属于自己的主组")
	}
	if inGroup(-1) {
		t.Error("不存在的 GID 不应判为属于")
	}
}

func TestLogPreflight_FormatsByLevel(t *testing.T) {
	var lines []string
	orig := preflightLogf
	preflightLogf = func(format string, args ...any) {
		lines = append(lines, strings.TrimSpace(strings.ReplaceAll(format, "%s", "")+joinAny(args)))
	}
	defer func() { preflightLogf = orig }()

	LogPreflight([]CheckResult{
		ok("A", "正常"),
		warn("B", "有点问题", "建议 B"),
		fail("C", "挂了", "建议 C"),
	})
	if len(lines) != 3 {
		t.Fatalf("应输出 3 行: %v", lines)
	}
	if !strings.Contains(lines[0], "✓") || strings.Contains(lines[0], "建议") {
		t.Errorf("ok 项不应带建议: %q", lines[0])
	}
	if !strings.Contains(lines[1], "!") || !strings.Contains(lines[1], "建议 B") {
		t.Errorf("warn 项应带建议: %q", lines[1])
	}
	if !strings.Contains(lines[2], "✗") || !strings.Contains(lines[2], "建议 C") {
		t.Errorf("error 项应带建议: %q", lines[2])
	}
}

// 六种语言的排障第一步都是 `docker compose logs runner-manager | grep '\[preflight'`。
// 那个关键词就是这里的行首标记，而标记是普通字符串常量——改掉它不会有任何东西报错，
// 排障第一步只会静默地一行都 grep 不到。这条守住每一级都带标记；
// internal/docsconsistency 那边再守住文档里写的关键词与这个常量对得上。
func TestLogPreflight_EveryLineCarriesTheDocumentedMarker(t *testing.T) {
	var lines []string
	orig := preflightLogf
	preflightLogf = func(format string, args ...any) {
		lines = append(lines, strings.TrimSpace(strings.ReplaceAll(format, "%s", "")+joinAny(args)))
	}
	defer func() { preflightLogf = orig }()

	LogPreflight([]CheckResult{
		ok("A", "正常"),
		warn("B", "有点问题", "建议 B"),
		fail("C", "挂了", "建议 C"),
	})
	for _, l := range lines {
		if !strings.HasPrefix(l, PreflightLogMarker) {
			t.Errorf("自检日志没有以 %q 开头，文档里的 grep 会捞不到它: %q", PreflightLogMarker, l)
		}
	}
	// 标记必须是语言无关的：混进汉字就等于让五份非中文文档里的 grep 又打不出来了。
	for _, r := range PreflightLogMarker {
		if r > 0x7f {
			t.Fatalf("标记 %q 含非 ASCII 字符 %q；它要能被任何键盘打出来", PreflightLogMarker, r)
		}
	}
}

func joinAny(args []any) string {
	var b strings.Builder
	for _, a := range args {
		if s, ok := a.(string); ok {
			b.WriteString(s)
		}
	}
	return b.String()
}

func TestRunnerImages_DedupAndPerRunnerOverride(t *testing.T) {
	restore := func() {
		_ = os.Unsetenv("FLEET_IMAGE_TAG")
		_ = os.Unsetenv("MANAGER_IMAGE")
	}
	t.Setenv("FLEET_IMAGE_TAG", "")
	t.Setenv("MANAGER_IMAGE", "")
	defer restore()

	cfg := &config.Config{}
	cfg.Runners.ContainerMode = true
	cfg.Runners.ContainerImage = "reg/global:1"
	cfg.Runners.Items = []config.RunnerItem{
		{Name: "a", ContainerImage: "reg/custom:1"},
		{Name: "b"}, // 回落全局
		{Name: "c", ContainerImage: "reg/custom:1"}, // 与 a 相同，应去重
		{Name: "d", ContainerImage: "reg/other:2"},
	}
	got := RunnerImages(cfg)
	want := []string{"reg/global:1", "reg/custom:1", "reg/other:2"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("RunnerImages = %v, want %v", got, want)
	}
}

func TestRunnerImages_FallsBackToDefault(t *testing.T) {
	// 全局与 items 都没配镜像时，仍要检查实际会用到的默认镜像
	cfg := &config.Config{}
	cfg.Runners.ContainerMode = true
	got := RunnerImages(cfg)
	if len(got) != 1 || got[0] != config.DefaultRunnerContainerImage() {
		t.Errorf("RunnerImages = %v, want [%s]", got, config.DefaultRunnerContainerImage())
	}
}

func TestRunnerImages_NilConfig(t *testing.T) {
	if got := RunnerImages(nil); got != nil {
		t.Errorf("nil 配置应返回 nil，实际 %v", got)
	}
}

func TestFirstLine(t *testing.T) {
	if got := firstLine([]byte("first\nsecond\nthird"), nil); got != "first" {
		t.Errorf("firstLine = %q, want %q", got, "first")
	}
	if got := firstLine([]byte("  only  \n"), nil); got != "only" {
		t.Errorf("firstLine 应去掉两侧空白: %q", got)
	}
	// 输出为空时退回 error 文本，否则自检结果会是一句没有信息量的话
	if got := firstLine(nil, errors.New("exit status 125")); got != "exit status 125" {
		t.Errorf("firstLine = %q", got)
	}
	if got := firstLine(nil, nil); got != "" {
		t.Errorf("两者皆空应返回空串: %q", got)
	}
}

func TestRequiredRunnerTools_CoversTheKnownFailureModes(t *testing.T) {
	// 这两个是实际踩过的：缺 git 时 checkout 静默退化，缺 unzip 时 setup-gradle 中途失败
	for _, want := range []string{"git", "unzip"} {
		found := false
		for _, tool := range requiredRunnerTools {
			if tool == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("requiredRunnerTools 应包含 %q", want)
		}
	}
}

func TestParseMissingTools(t *testing.T) {
	const marker = missingToolMarker
	// docker 在 arm64 主机运行 amd64 镜像时输出的真实告警，退出码仍为 0。
	// dockerCmd 用 CombinedOutput，它会混进探测输出里。
	platformWarning := "WARNING: The requested image's platform (linux/amd64) does not match " +
		"the detected host platform (linux/arm64/v8) and no specific platform was requested\n"

	cases := []struct {
		name string
		out  string
		want []string
	}{
		{"全部具备", "", nil},
		{"仅告警，不应误报", platformWarning, nil},
		{"告警与缺失混合", platformWarning + marker + "git\n" + marker + "unzip\n", []string{"git", "unzip"}},
		{"缺失项前后有空白", "  " + marker + "git  \n", []string{"git"}},
		{"未知名字不予采信", marker + "definitely-not-a-required-tool\n", nil},
		{"无标记的裸行忽略", "git\nunzip\n", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseMissingTools([]byte(tc.out))
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Errorf("parseMissingTools = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestParseMissingTools_RecognisesEveryRequiredTool(t *testing.T) {
	// 探测脚本用 requiredRunnerTools 生成，解析端必须认得其中每一个，
	// 否则今后往列表里加命令会得到「检查了但永远报不出来」的假绿灯
	var out strings.Builder
	for _, tool := range requiredRunnerTools {
		out.WriteString(missingToolMarker + tool + "\n")
	}
	got := parseMissingTools([]byte(out.String()))
	if len(got) != len(requiredRunnerTools) {
		t.Fatalf("解析出 %v，期望全部 %v", got, requiredRunnerTools)
	}
}

// 自检崩了不该把 Manager 的启动一起带走。
//
// 改用 preflight-kit 之前，Preflight 是直接顺序调用各项检查的，没有任何兜底——
// 而 checkRunnerImages / checkJobDockerBackend 这些都要起子进程去问 docker，
// 正是最容易出意外的地方。一次 panic 就等于 Manager 起不来，
// 而它本来只是想在启动时多告诉你几句环境状况。
func TestRunCheck_PanicBecomesAnErrorResultInsteadOfCrashing(t *testing.T) {
	got := runCheck(context.Background(), "会炸的检查", func(context.Context) CheckResult {
		panic("docker 客户端里的某个 nil")
	})

	if got.Level != CheckError {
		t.Fatalf("Level = %q，期望 %q", got.Level, CheckError)
	}
	if got.Name != "会炸的检查" {
		t.Errorf("Name = %q，兜底结果应当带上检查名，否则日志里看不出是哪一项炸了", got.Name)
	}
	if !strings.Contains(got.Message, "docker 客户端里的某个 nil") {
		t.Errorf("Message 里应当保留 panic 的内容，实得 %q", got.Message)
	}
}

// 正常返回的检查不受兜底影响，结果原样透传。
func TestRunCheck_PassesThroughNormalResults(t *testing.T) {
	for _, want := range []CheckResult{
		ok("甲", "一切正常"),
		warn("乙", "有点问题", "照这个改"),
		fail("丙", "跑不起来", "照那个改"),
	} {
		got := runCheck(context.Background(), "名字用不上", func(context.Context) CheckResult { return want })
		if got != want {
			t.Errorf("结果被改动了：%+v -> %+v", want, got)
		}
	}
}

// Preflight 整体也要有兜底：用例没法往真实检查里塞 panic，
// 这里退一步——确认每一项都确实经过了 runCheck，即没有哪条路径绕开它。
func TestPreflight_EveryCheckGoesThroughRunCheck(t *testing.T) {
	src, err := os.ReadFile("preflight.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)
	start := strings.Index(body, "func Preflight(")
	if start < 0 {
		t.Fatal("找不到 Preflight")
	}
	end := strings.Index(body[start:], "\n}\n")
	if end < 0 {
		t.Fatal("找不到 Preflight 的结尾")
	}
	fn := body[start : start+end]

	// 函数体里出现的 check* 调用都必须包在 runCheck 里。
	// checkRunnerImages 是例外：它自己内部逐项调 runCheck（每个镜像两项）。
	for _, name := range []string{
		"checkBasePath", "checkRunnerDirPermissions", "checkDefaultModeDocker",
		"checkDockerReachable", "checkNetwork", "checkNetworkMembers", "checkJobDockerBackend",
	} {
		if !strings.Contains(fn, name) {
			t.Errorf("Preflight 里没有 %s，用例该更新了", name)
		}
	}
	if strings.Count(fn, "runCheck(") < 7 {
		t.Errorf("Preflight 里 runCheck 的调用数 %d 少于检查项数，可能有路径绕开了 panic 兜底:\n%s",
			strings.Count(fn, "runCheck("), fn)
	}
}

// ---- container network members ----

// TestUnexpectedNetworkMembers 纯函数部分：谁算本部署的，谁不算。
//
// self 每个用例单独给，因为「取不到 hostname」是其中一条要钉的行为，
// 而它恰好是最容易写错成「永远绿」的那条。
func TestUnexpectedNetworkMembers(t *testing.T) {
	const self = "1a2b3c4d5e6f" // Manager 容器的 hostname：Docker 未指定时取 12 位短 ID
	selfID := self + strings.Repeat("0", 52)
	expected := map[string]bool{"runner-dind": true, "runner-a": true}

	tests := []struct {
		name    string
		self    string
		members map[string]string
		want    []string
	}{
		{
			name: "自身、DinD 与 Runner 都在预期内",
			self: self,
			members: map[string]string{
				selfID:     "runner-manager",
				"c0ffee11": "runner-dind",
				"c0ffee22": "runner-a",
			},
		},
		{
			name: "多出一个无关容器",
			self: self,
			members: map[string]string{
				selfID:     "runner-manager",
				"c0ffee11": "runner-dind",
				"c0ffee22": "runner-a",
				"c0ffee33": "grafana",
			},
			want: []string{"grafana"},
		},
		{
			name:    "hostname 只是容器 ID 的前缀，仍算自身",
			self:    self,
			members: map[string]string{selfID: "runner-manager"},
		},
		{
			name: "结果按名字排序",
			self: self,
			members: map[string]string{
				"c0ffee44": "zoo",
				"c0ffee55": "alpha",
				"c0ffee66": "mid",
			},
			want: []string{"alpha", "mid", "zoo"},
		},
		{
			// 空前缀会让 HasPrefix 恒为真，把整张网络算成自己——
			// 那样这项检查会静默地变成永远不报
			name:    "取不到 hostname 时不把整张网络算成自身",
			self:    "",
			members: map[string]string{selfID: "runner-manager", "c0ffee77": "grafana"},
			want:    []string{"grafana", "runner-manager"},
		},
		{
			name:    "没有名字的端点用短 ID 点名",
			self:    self,
			members: map[string]string{"abcdef0123456789": ""},
			want:    []string{"abcdef012345"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// map 的遍历顺序每次都不同，跑多遍才能说明结果真的稳定
			for i := 0; i < 20; i++ {
				got := unexpectedNetworkMembers(tc.members, tc.self, expected)
				if strings.Join(got, ",") != strings.Join(tc.want, ",") {
					t.Fatalf("第 %d 次得到 %v，期望 %v", i+1, got, tc.want)
				}
			}
		})
	}
}

// TestListNetworkMembers 超过十个就收成计数，否则一行日志会被一张网络的成员表撑满
func TestListNetworkMembers(t *testing.T) {
	var many []string
	for i := 0; i < maxListedNetworkMembers+3; i++ {
		many = append(many, fmt.Sprintf("c%d", i))
	}

	if got, want := listNetworkMembers([]string{"a", "b"}), "a, b"; got != want {
		t.Errorf("没超出上限时应原样列出：got %q, want %q", got, want)
	}
	got := listNetworkMembers(many)
	if !strings.HasSuffix(got, "…and 3 more") {
		t.Errorf("超出的部分应收成计数，实得 %q", got)
	}
	if strings.Count(got, ", ") != maxListedNetworkMembers {
		t.Errorf("应只点名 %d 个，实得 %q", maxListedNetworkMembers, got)
	}
}

// TestAnyDindBackend items[].job_docker_backend 能逐个覆盖，所以全局值两个方向都不是结论
func TestAnyDindBackend(t *testing.T) {
	tests := []struct {
		name   string
		global string
		items  []config.RunnerItem
		want   bool
	}{
		{name: "还没配 Runner 时按全局值算", global: "dind", want: true},
		{name: "还没配 Runner 且全局是 none", global: "none", want: false},
		{name: "还没配 Runner 且全局留空，按默认的 dind 算", global: "", want: true},
		{
			name:   "全局 none，但某个 Runner 单独开了 dind",
			global: "none",
			items:  []config.RunnerItem{{Name: "a"}, {Name: "b", JobDockerBackend: "dind"}},
			want:   true,
		},
		{
			name:   "全局 dind，但每个 Runner 都覆盖成了别的",
			global: "dind",
			items:  []config.RunnerItem{{Name: "a", JobDockerBackend: "none"}, {Name: "b", JobDockerBackend: "host-socket"}},
			want:   false,
		},
		{
			name:   "Runner 没覆盖时回落全局",
			global: "dind",
			items:  []config.RunnerItem{{Name: "a"}},
			want:   true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{}
			cfg.Runners.JobDockerBackend = tc.global
			cfg.Runners.Items = tc.items
			if got := anyDindBackend(cfg); got != tc.want {
				t.Fatalf("anyDindBackend = %v，期望 %v", got, tc.want)
			}
		})
	}
}

// networkMembersConfig 一份容器模式、dind 后端、注册了 runner-a 的配置
func networkMembersConfig(t *testing.T) *config.Config {
	t.Helper()
	return &config.Config{Runners: config.RunnersConfig{
		BasePath:         t.TempDir(),
		ContainerMode:    true,
		ContainerImage:   "img:tag",
		ContainerNetwork: "runner-net",
		JobDockerBackend: "dind",
		DindHost:         "runner-dind",
		AgentPort:        8081,
		// 容器名不等于 Runner 名：ContainerName("a") 是 github-runner-a，
		// 预期成员表必须按容器名比，否则本部署自己的 Runner 会被报成意外成员
		Items: []config.RunnerItem{{Name: "a", TargetType: "repo", Target: "o/r"}},
	}}
}

// fakeNetworkInspect 让假 docker 对 network inspect 返回给定的 .Containers
func fakeNetworkInspect(t *testing.T, containersJSON string) string {
	t.Helper()
	return fakeDockerProgram(t, "case \"$1\" in\n"+
		"  network)\n"+
		"    cat <<'JSON'\n"+containersJSON+"\nJSON\n"+
		"    ;;\n"+
		"esac\n"+
		"exit 0\n")
}

// TestCheckNetworkMembers_WarnsAboutContainersOutsideTheDeployment
// 网上多出来的容器要被点名：它们拿到的是一个免认证的 privileged daemon。
func TestCheckNetworkMembers_WarnsAboutContainersOutsideTheDeployment(t *testing.T) {
	host, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	// Manager 自己的那一项：容器 ID 全长 64 位，hostname 是它的前缀
	logPath := fakeNetworkInspect(t, fmt.Sprintf(`{
  %q: {"Name": "runner-manager"},
  "c0ffee11deadbeef": {"Name": "runner-dind"},
  "c0ffee22deadbeef": {"Name": "github-runner-a"},
  "c0ffee33deadbeef": {"Name": "grafana"},
  "c0ffee44deadbeef": {"Name": "prometheus"}
}`, host+strings.Repeat("0", 52)))

	got := checkNetworkMembers(context.Background(), networkMembersConfig(t))
	if got.Level != CheckWarn {
		t.Fatalf("网上有无关容器时应 warn: %+v", got)
	}
	if got.Name != "container network members" {
		t.Errorf("Name = %q", got.Name)
	}
	for _, want := range []string{"2 containers on runner-net", "grafana", "prometheus"} {
		if !strings.Contains(got.Message, want) {
			t.Errorf("消息里应含 %q，实得 %q", want, got.Message)
		}
	}
	// 本部署自己的三个容器被报成「不属于本部署」，这项检查就没人会再看第二眼
	for _, unwanted := range []string{"runner-manager", "runner-dind", "github-runner-a"} {
		if strings.Contains(got.Message, unwanted) {
			t.Errorf("消息里不该含 %q（那是本部署自己的容器），实得 %q", unwanted, got.Message)
		}
	}
	if !strings.Contains(got.Hint, "docker network disconnect runner-net") {
		t.Errorf("hint 应给出可照做的命令，实得 %q", got.Hint)
	}
	if calls := readCalls(t, logPath); !strings.Contains(calls, "network inspect runner-net --format {{json .Containers}}") {
		t.Errorf("应只取 .Containers 一段，实际执行了: %s", calls)
	}
}

// TestCheckNetworkMembers_OnlyTheDeploymentsOwnContainers 只有本部署的容器时报 ok
func TestCheckNetworkMembers_OnlyTheDeploymentsOwnContainers(t *testing.T) {
	host, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	fakeNetworkInspect(t, fmt.Sprintf(`{
  %q: {"Name": "runner-manager"},
  "c0ffee11deadbeef": {"Name": "runner-dind"},
  "c0ffee22deadbeef": {"Name": "github-runner-a"}
}`, host+strings.Repeat("0", 52)))

	got := checkNetworkMembers(context.Background(), networkMembersConfig(t))
	if got.Level != CheckOK {
		t.Fatalf("网上只有本部署的容器时应 ok: %+v", got)
	}
	if want := "the network runner-net has no unexpected members"; got.Message != want {
		t.Errorf("Message = %q，期望 %q", got.Message, want)
	}
}

// TestCheckNetworkMembers_UnreadableInspectIsSkippedNotReported
// 读不到成员表时报 ok：「网络不存在」这类情形 checkNetwork 已经报过 fail，
// 这里再报一遍等于同一件事说两次，而且是用一句更绕的话说。
func TestCheckNetworkMembers_UnreadableInspectIsSkippedNotReported(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{name: "inspect 失败", body: "echo 'Error: No such network: runner-net' >&2\nexit 1\n"},
		{name: "输出不是 JSON", body: "echo 'not json at all'\nexit 0\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fakeDockerProgram(t, tc.body)
			got := checkNetworkMembers(context.Background(), networkMembersConfig(t))
			if got.Level != CheckOK {
				t.Fatalf("读不到成员表应跳过而不是报错: %+v", got)
			}
			if !strings.Contains(got.Message, "skipped") {
				t.Errorf("消息应说明这一项被跳过了，实得 %q", got.Message)
			}
		})
	}
}

// TestPreflight_NetworkMembersOnlyWhenADindBackendIsInUse
// 没有 Runner 用 dind 时网上没有那个免认证的 daemon，这一项不该出现在结果里。
func TestPreflight_NetworkMembersOnlyWhenADindBackendIsInUse(t *testing.T) {
	const checkName = "container network members"

	t.Run("dind 时有这一项", func(t *testing.T) {
		fakeNetworkInspect(t, `{}`)
		findCheck(t, Preflight(context.Background(), networkMembersConfig(t)), checkName)
	})

	t.Run("后端是 none 时没有这一项", func(t *testing.T) {
		fakeNetworkInspect(t, `{"c0ffee33deadbeef": {"Name": "grafana"}}`)
		cfg := networkMembersConfig(t)
		cfg.Runners.JobDockerBackend = "none"
		for _, r := range Preflight(context.Background(), cfg) {
			if r.Name == checkName {
				t.Fatalf("没有 Runner 用 dind，不该执行这一项: %+v", r)
			}
		}
	})
}
