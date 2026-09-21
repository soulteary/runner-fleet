package handler

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/soulteary/runner-fleet/internal/runner"
)

// 注册是这个项目里副作用最重的一条路径：它下载 runner、跑 config.sh 向 GitHub 注册、
// 落盘注册结果、再把进程拉起来，全程在后台 goroutine 里，失败只体现为界面上的一行文字。
// 这些用例把 config.sh 与 install-runner.sh 都换成本地假脚本，覆盖分支判断与错误措辞。

// newRegDir 造一个安装目录。configScript 非空时在里面放一个假的 config.sh。
// 刻意不放 run.sh：注册成功后 runRegistrationJob 会调 Start，没有 run.sh 时它
// 只是返回一个被记进日志的错误，既不会 fork 出进程，也不影响注册结果的断言。
func newRegDir(t *testing.T, configScript string) string {
	t.Helper()
	dir := t.TempDir()
	installDir := filepath.Join(dir, "demo")
	if err := os.MkdirAll(installDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if configScript != "" {
		writeScript(t, filepath.Join(installDir, runner.ConfigScriptName()), configScript)
	}
	return installDir
}

func writeScript(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

type regResult struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	At      string `json:"at"`
}

func readRegResult(t *testing.T, installDir string) regResult {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(installDir, runner.RegistrationResultFile))
	if err != nil {
		t.Fatalf("没有写出注册结果文件: %v", err)
	}
	var r regResult
	if err := json.Unmarshal(b, &r); err != nil {
		t.Fatalf("注册结果不是合法 JSON: %s", b)
	}
	return r
}

// isolateConfigPath 把 ConfigPath 指到一个临时文件，免得用例互相影响
func isolateConfigPath(t *testing.T) {
	t.Helper()
	old := ConfigPath
	ConfigPath = filepath.Join(t.TempDir(), "config.yaml")
	t.Cleanup(func() { ConfigPath = old })
}

func job(installDir string) registrationJob {
	return registrationJob{
		BasePath:   filepath.Dir(installDir),
		InstallDir: installDir,
		RunnerName: "demo",
		URL:        "https://github.com/o/r",
		Token:      "AAAA",
		Labels:     []string{"self-hosted", "linux"},
	}
}

// ---- 安装分支 ----

// config.sh 已在，就不该再跑一次安装：重复下载解压既慢又可能覆盖已注册的凭据
func TestRunRegistrationJobSkipsInstallWhenConfigScriptExists(t *testing.T) {
	isolateConfigPath(t)
	installDir := newRegDir(t, "exit 0")

	marker := filepath.Join(t.TempDir(), "installed")
	old := installRunnerScriptPath
	installRunnerScriptPath = filepath.Join(t.TempDir(), "install.sh")
	writeScript(t, installRunnerScriptPath, "touch "+marker)
	t.Cleanup(func() { installRunnerScriptPath = old })

	runRegistrationJob(job(installDir))

	if _, err := os.Stat(marker); err == nil {
		t.Fatal("config.sh 已存在时不应再执行安装脚本")
	}
	if r := readRegResult(t, installDir); !r.Success {
		t.Fatalf("期望注册成功，实际: %+v", r)
	}
}

// config.sh 不在，先装再注册
func TestRunRegistrationJobInstallsWhenConfigScriptMissing(t *testing.T) {
	isolateConfigPath(t)
	installDir := newRegDir(t, "") // 没有 config.sh

	old := installRunnerScriptPath
	installRunnerScriptPath = filepath.Join(t.TempDir(), "install.sh")
	// 假安装脚本的职责就是把 config.sh 放到位，与真脚本一致
	writeScript(t, installRunnerScriptPath,
		`printf '#!/bin/sh\nexit 0\n' > "$RUNNERS_BASE_PATH/$1/`+runner.ConfigScriptName()+`"
chmod +x "$RUNNERS_BASE_PATH/$1/`+runner.ConfigScriptName()+`"`)
	t.Cleanup(func() { installRunnerScriptPath = old })

	runRegistrationJob(job(installDir))

	if r := readRegResult(t, installDir); !r.Success {
		t.Fatalf("期望注册成功，实际: %+v", r)
	}
}

func TestRunRegistrationJobReportsInstallFailure(t *testing.T) {
	isolateConfigPath(t)
	installDir := newRegDir(t, "")

	old := installRunnerScriptPath
	installRunnerScriptPath = filepath.Join(t.TempDir(), "install.sh")
	writeScript(t, installRunnerScriptPath, "echo 'download failed: 404' >&2; exit 1")
	t.Cleanup(func() { installRunnerScriptPath = old })

	runRegistrationJob(job(installDir))

	r := readRegResult(t, installDir)
	if r.Success {
		t.Fatal("安装失败时不应记为成功")
	}
	if !strings.Contains(r.Message, "installing the runner automatically failed") {
		t.Fatalf("消息应说明是安装阶段失败，实际: %q", r.Message)
	}
}

// 安装脚本退出码为 0，却没产出 config.sh——真实发生过的形态（下载到的包不完整）。
// 不单独判这一下的话，接着就会以「找不到文件」的形式报在注册阶段，指错地方。
func TestRunRegistrationJobDetectsInstallWithoutConfigScript(t *testing.T) {
	isolateConfigPath(t)
	installDir := newRegDir(t, "")

	old := installRunnerScriptPath
	installRunnerScriptPath = filepath.Join(t.TempDir(), "install.sh")
	writeScript(t, installRunnerScriptPath, "exit 0") // 装了个寂寞
	t.Cleanup(func() { installRunnerScriptPath = old })

	runRegistrationJob(job(installDir))

	r := readRegResult(t, installDir)
	if r.Success {
		t.Fatal("没有 config.sh 就不可能注册成功")
	}
	if !strings.Contains(r.Message, "the install finished but") {
		t.Fatalf("消息应指出安装完成却没有 config 脚本，实际: %q", r.Message)
	}
}

// ---- 注册成功 ----

func TestRunRegistrationJobPassesNameAndUnattended(t *testing.T) {
	isolateConfigPath(t)
	argsFile := filepath.Join(t.TempDir(), "args")
	installDir := newRegDir(t, `printf '%s\n' "$*" > `+argsFile+`
exit 0`)

	runRegistrationJob(job(installDir))

	b, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("config.sh 没有被调用: %v", err)
	}
	args := string(b)
	// --name：不传的话 config.sh 会取 Manager 容器的 hostname，一个部署里每个 Runner
	// 都用同一个名字注册，GitHub 上只看得到一个（v1.5.1 修过） version-check-ignore
	if !strings.Contains(args, "--name demo") {
		t.Errorf("必须传 --name，实际参数: %s", args)
	}
	// --unattended：重名时才会立刻失败退出，否则进交互重试循环耗到超时，
	// 把单 worker 队列里后面排队的 Runner 全堵住（同为 v1.5.1 所修） version-check-ignore
	if !strings.Contains(args, "--unattended") {
		t.Errorf("必须传 --unattended，实际参数: %s", args)
	}
	if !strings.Contains(args, "--labels self-hosted,linux") {
		t.Errorf("标签应逗号拼接后传入，实际参数: %s", args)
	}
	if !strings.Contains(args, "--url https://github.com/o/r") || !strings.Contains(args, "--token AAAA") {
		t.Errorf("url/token 应原样传入，实际参数: %s", args)
	}
}

func TestRunRegistrationJobWritesTimestampedResult(t *testing.T) {
	isolateConfigPath(t)
	installDir := newRegDir(t, "exit 0")

	runRegistrationJob(job(installDir))

	r := readRegResult(t, installDir)
	if !r.Success || r.Message != "registered" {
		t.Fatalf("期望成功，实际: %+v", r)
	}
	if _, err := time.Parse(time.RFC3339, r.At); err != nil {
		t.Fatalf("at 应为 RFC3339 时间戳，实际 %q: %v", r.At, err)
	}
}

// ---- 失败时的措辞 ----

// 这些提示是用户唯一能看到的东西。config.sh 的原始输出只说「哪里错了」，
// 说不了「该去哪儿改」，补充的那一句才是能照着做的部分。
func TestRunRegistrationJobEnrichesKnownFailures(t *testing.T) {
	cases := []struct {
		name       string
		scriptOut  string
		wantInMsg  string
		wantOrigin string // 原始输出也要保留
	}{
		{
			name:       "重名",
			scriptOut:  "A runner exists with the same name",
			wantInMsg:  "Settings → Actions → Runners",
			wantOrigin: "A runner exists with the same name",
		},
		{
			name:       "token 失效",
			scriptOut:  "Invalid registration token",
			wantInMsg:  "Generate a fresh registration token",
			wantOrigin: "Invalid registration token",
		},
		{
			name:       "token 已被用过",
			scriptOut:  "The registration token has already been used",
			wantInMsg:  "Generate a fresh registration token",
			wantOrigin: "already been used",
		},
		{
			name:       "token 过期",
			scriptOut:  "Registration token expired",
			wantInMsg:  "Generate a fresh registration token",
			wantOrigin: "expired",
		},
		{
			name:       "以 root 运行",
			scriptOut:  "Must not run with sudo",
			wantInMsg:  "RUNNER_ALLOW_RUNASROOT=1",
			wantOrigin: "Must not run with sudo",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			isolateConfigPath(t)
			installDir := newRegDir(t, "echo '"+tc.scriptOut+"' >&2\nexit 1")

			runRegistrationJob(job(installDir))

			r := readRegResult(t, installDir)
			if r.Success {
				t.Fatal("config.sh 退出码非 0，不该记为成功")
			}
			if !strings.Contains(r.Message, tc.wantInMsg) {
				t.Errorf("消息里应补上可操作的提示 %q，实际: %q", tc.wantInMsg, r.Message)
			}
			if !strings.Contains(r.Message, tc.wantOrigin) {
				t.Errorf("消息里应保留 config.sh 的原始输出 %q，实际: %q", tc.wantOrigin, r.Message)
			}
		})
	}
}

// 不认识的失败照样要落盘，并且带上原始输出——否则界面上只有「注册失败」四个字
func TestRunRegistrationJobKeepsUnknownFailureOutput(t *testing.T) {
	isolateConfigPath(t)
	installDir := newRegDir(t, "echo 'Http response code: NotFound from POST' >&2\nexit 1")

	runRegistrationJob(job(installDir))

	r := readRegResult(t, installDir)
	if r.Success {
		t.Fatal("不该记为成功")
	}
	if !strings.Contains(r.Message, "NotFound") {
		t.Fatalf("消息应保留原始输出，实际: %q", r.Message)
	}
}

// config.sh 一句话都不说就失败时，退而用 error 本身，不能写出一条空消息
func TestRunRegistrationJobFallsBackToErrWhenScriptSilent(t *testing.T) {
	isolateConfigPath(t)
	installDir := newRegDir(t, "exit 3")

	runRegistrationJob(job(installDir))

	r := readRegResult(t, installDir)
	if r.Success {
		t.Fatal("不该记为成功")
	}
	if strings.TrimSpace(r.Message) == "" {
		t.Fatal("消息不能为空")
	}
}

// ---- 队列 ----

// 队列是单 worker 顺序消费的：并发跑 config.sh 会同时占满 CPU 和网络，
// 而且 actions/runner 的注册本身就不适合并行。这里确认任务确实被逐个执行。
func TestRegistrationWorkerProcessesQueueSequentially(t *testing.T) {
	isolateConfigPath(t)
	StartRegistrationWorker()

	const n = 3
	dirs := make([]string, n)
	for i := range dirs {
		// 每个脚本写一行到共用的日志里，顺序执行时不会交错
		dirs[i] = newRegDir(t, "exit 0")
	}
	for i, d := range dirs {
		j := job(d)
		j.RunnerName = "demo" + string(rune('a'+i))
		registrationQueue <- j
	}

	deadline := time.Now().Add(10 * time.Second)
	for _, d := range dirs {
		for {
			if _, err := os.Stat(filepath.Join(d, runner.RegistrationResultFile)); err == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("超时：%s 的注册结果一直没写出来", d)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	for _, d := range dirs {
		if r := readRegResult(t, d); !r.Success {
			t.Fatalf("%s 应注册成功，实际: %+v", d, r)
		}
	}
}
