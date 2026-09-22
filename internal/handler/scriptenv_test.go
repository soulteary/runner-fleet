package handler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/soulteary/runner-fleet/internal/childenv"
	"github.com/soulteary/runner-fleet/internal/runner"
)

// install-runner.sh 与 config.sh 不跑用户的 Job，所以这里不是「密码进了 Job 日志」那条路径。
// 但它们的输出被 CombinedOutput 收下来，注册失败时会原样写进 .registration_result.json 再显示
// 到界面上——一个把环境打出来的脚本（真脚本的 set -x、或者某天有人加的一行诊断）就够了。
// 四个调用点一起改，就不必每次再判断哪一个「够不着 Job」。
func TestRegistrationScripts_DoNotSeeCredentials(t *testing.T) {
	isolateConfigPath(t)
	t.Setenv("BASIC_AUTH_PASSWORD", "s3cret")
	t.Setenv("BASIC_AUTH_USER", "admin")
	t.Setenv("AGENT_TOKEN", "agent-secret")
	t.Setenv("DOCKER_HOST", "tcp://d:2375")

	installDir := newRegDir(t, "") // 没有 config.sh，安装分支会先跑 install-runner.sh
	dumps := t.TempDir()

	old := installRunnerScriptPath
	installRunnerScriptPath = filepath.Join(t.TempDir(), "install.sh")
	// 假安装脚本做两件事：把自己的环境落盘，再把一个同样会落盘的 config.sh 放到位。
	writeScript(t, installRunnerScriptPath, `env > "`+dumps+`/install.env"
printf '#!/bin/sh\nenv > "`+dumps+`/config.env"\nexit 0\n' > "$RUNNERS_BASE_PATH/$1/`+runner.ConfigScriptName()+`"
chmod +x "$RUNNERS_BASE_PATH/$1/`+runner.ConfigScriptName()+`"`)
	t.Cleanup(func() { installRunnerScriptPath = old })

	runRegistrationJob(job(installDir))

	if r := readRegResult(t, installDir); !r.Success {
		t.Fatalf("两个假脚本都返回 0，注册该记为成功，实际: %+v", r)
	}

	for _, name := range []string{"install.env", "config.env"} {
		b, err := os.ReadFile(filepath.Join(dumps, name))
		if err != nil {
			t.Fatalf("%s 没写出来，脚本可能没被执行: %v", name, err)
		}
		dump := string(b)
		for _, denied := range childenv.Denied {
			if v, ok := scriptEnvLookup(dump, denied); ok {
				t.Errorf("%s 的环境里有 %s=%q", name, denied, v)
			}
		}
		// 其余变量照常透传
		if v, ok := scriptEnvLookup(dump, "DOCKER_HOST"); !ok || v != "tcp://d:2375" {
			t.Errorf("%s: DOCKER_HOST = %q（存在: %v），期望 tcp://d:2375", name, v, ok)
		}
	}

	// install-runner.sh 是四处里唯一往 environ 上 append 的（RUNNERS_BASE_PATH），
	// 过滤之后那一条还得在——它没了，安装脚本就不知道往哪儿解压。
	b, err := os.ReadFile(filepath.Join(dumps, "install.env"))
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := scriptEnvLookup(string(b), "RUNNERS_BASE_PATH"); !ok || v != filepath.Dir(installDir) {
		t.Errorf("install.env: RUNNERS_BASE_PATH = %q（存在: %v），期望 %q", v, ok, filepath.Dir(installDir))
	}
}

// scriptEnvLookup 在 env(1) 的输出里按变量名取值。
// 按行比对 KEY=，而不是 strings.Contains——后者会把 BASIC_AUTH_PASSWORD_FILE 也算成命中。
func scriptEnvLookup(dump, name string) (string, bool) {
	for _, line := range strings.Split(dump, "\n") {
		k, v, ok := strings.Cut(line, "=")
		if ok && k == name {
			return v, true
		}
	}
	return "", false
}
