package runner

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lab-dev/github-actions-runner-manager/internal/config"
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
	if got.Level != CheckOK || !strings.Contains(got.Message, "可达") {
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
	if len(results) != 2 {
		t.Fatalf("默认模式应只有目录与 Job 内 Docker 两项，实际: %+v", results)
	}
	for _, r := range results {
		if r.Name == "容器网络" || r.Name == "Runner 镜像" {
			t.Errorf("默认模式不应执行容器相关检查: %+v", r)
		}
	}
	findCheck(t, results, "runners 目录")
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

func joinAny(args []any) string {
	var b strings.Builder
	for _, a := range args {
		if s, ok := a.(string); ok {
			b.WriteString(s)
		}
	}
	return b.String()
}
