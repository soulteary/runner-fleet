package handler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/soulteary/runner-fleet/internal/runner"
)

// fakeConfigScript 在 installDir 下放一个假的 config 脚本，把收到的参数逐行写进 args.txt
func fakeConfigScript(t *testing.T) (installDir, argsFile string) {
	t.Helper()
	installDir = t.TempDir()
	argsFile = filepath.Join(installDir, "args.txt")
	script := "#!/bin/sh\nfor a in \"$@\"; do printf '%s\\n' \"$a\" >> " + argsFile + "; done\nexit 0\n"
	if err := os.WriteFile(filepath.Join(installDir, runner.ConfigScriptName()), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	return installDir, argsFile
}

func recordedArgs(t *testing.T, argsFile string) []string {
	t.Helper()
	b, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("假 config 脚本没有记录到参数: %v", err)
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

func hasFlagValue(args []string, flag, want string) bool {
	for i, a := range args {
		if a == flag && i+1 < len(args) && args[i+1] == want {
			return true
		}
	}
	return false
}

func hasFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

// TestRunConfigScript_PassesRunnerName
// 不传 --name 时 config.sh 取本机 hostname 作为 Runner 名，而它是在 Manager 容器内执行的：
// 一个部署里的每个 Runner 都会用同一个名字（Manager 容器的 hostname）去注册，
// GitHub 上最终只看得到一个 Runner，名字还是个容器 ID，和界面上的名称对不上。
func TestRunConfigScript_PassesRunnerName(t *testing.T) {
	installDir, argsFile := fakeConfigScript(t)

	if _, err := runConfigScript(installDir, "https://github.com/o/r", "tok", "droiddesk-2", nil, 30*time.Second); err != nil {
		t.Fatal(err)
	}

	args := recordedArgs(t, argsFile)
	if !hasFlagValue(args, "--name", "droiddesk-2") {
		t.Fatalf("注册参数里没有 --name droiddesk-2，实际: %v", args)
	}
}

// TestRunConfigScript_PassesUnattended
// 撞上重名时，带 --unattended 会直接抛错退出；不带则进入交互式重试循环
// （"Failed to replace the runner. Try again or ctrl-c to quit"）。这里没有 TTY，
// 那个循环会一直耗到 2 分钟超时，而注册是单 worker 顺序执行的，后面排队的 Runner 全被堵住。
func TestRunConfigScript_PassesUnattended(t *testing.T) {
	installDir, argsFile := fakeConfigScript(t)

	if _, err := runConfigScript(installDir, "https://github.com/o/r", "tok", "a", nil, 30*time.Second); err != nil {
		t.Fatal(err)
	}

	if args := recordedArgs(t, argsFile); !hasFlag(args, "--unattended") {
		t.Fatalf("注册参数里没有 --unattended，重名时会卡在交互提示上，实际: %v", args)
	}
}

// TestRunConfigScript_KeepsUrlTokenAndLabels 原有参数不能因为新增而丢
func TestRunConfigScript_KeepsUrlTokenAndLabels(t *testing.T) {
	installDir, argsFile := fakeConfigScript(t)

	if _, err := runConfigScript(installDir, "https://github.com/o/r", "tok", "a", []string{"x", "y"}, 30*time.Second); err != nil {
		t.Fatal(err)
	}

	args := recordedArgs(t, argsFile)
	for _, c := range [][2]string{{"--url", "https://github.com/o/r"}, {"--token", "tok"}, {"--labels", "x,y"}} {
		if !hasFlagValue(args, c[0], c[1]) {
			t.Fatalf("注册参数里缺少 %s %s，实际: %v", c[0], c[1], args)
		}
	}
}

// TestRunConfigScript_OmitsEmptyName 名称为空时不能传出一个空的 --name（config.sh 会当成缺参数报错）
func TestRunConfigScript_OmitsEmptyName(t *testing.T) {
	installDir, argsFile := fakeConfigScript(t)

	if _, err := runConfigScript(installDir, "https://github.com/o/r", "tok", "", nil, 30*time.Second); err != nil {
		t.Fatal(err)
	}

	if args := recordedArgs(t, argsFile); hasFlag(args, "--name") {
		t.Fatalf("名称为空时不该传 --name，实际: %v", args)
	}
}
