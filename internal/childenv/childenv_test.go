package childenv

import (
	"os"
	"slices"
	"strings"
	"testing"
)

// TestSanitize_RemovesEveryDeniedName Denied 里的每一项都要被删掉。
//
// 遍历 Denied 而不是写死三个名字：以后往 Denied 里加一个变量，这条用例自动覆盖它，
// 不会出现"加进了清单、却没人验证它真的被删"的情况。
func TestSanitize_RemovesEveryDeniedName(t *testing.T) {
	for _, name := range Denied {
		t.Run(name, func(t *testing.T) {
			in := []string{"PATH=/usr/bin", name + "=leaked", "HOME=/home/app"}
			got := Sanitize(in)
			if slices.Contains(got, name+"=leaked") {
				t.Errorf("%s 还在结果里: %v", name, got)
			}
			if want := []string{"PATH=/usr/bin", "HOME=/home/app"}; !slices.Equal(got, want) {
				t.Errorf("got %v, want %v", got, want)
			}
		})
	}
}

// TestSanitize_KeepsEverythingElse 其余变量必须原样透传。
//
// 这几个不是随手挑的，每一个被误删都有具体后果：DOCKER_HOST 没了，容器模式 + DinD 的 Job
// 连不上 daemon；PATH/HOME 没了，run.sh 直接起不来；代理变量没了，内网部署下载 action 卡住；
// RUNNER_ALLOW_RUNASROOT 没了，以 root 跑的 runner 拒绝启动。
// 它们合起来就是"为什么这里是 denylist 而不是 allowlist"的证据。
func TestSanitize_KeepsEverythingElse(t *testing.T) {
	keep := []string{
		"DOCKER_HOST=tcp://runner-dind:2375",
		"PATH=/usr/local/bin:/usr/bin:/bin",
		"HOME=/home/app",
		"HTTPS_PROXY=http://proxy.internal:3128",
		"RUNNER_ALLOW_RUNASROOT=1",
		"ACTIONS_RUNNER_HOOK_JOB_STARTED=/opt/hook.sh",
	}
	got := Sanitize(slices.Clone(keep))
	if !slices.Equal(got, keep) {
		t.Errorf("got %v, want %v", got, keep)
	}
}

// TestSanitize_MatchesNamesExactly 精确匹配，不按前缀。
//
// 把 Sanitize 里的相等判断换成 strings.HasPrefix，这条用例就会红——那正是这里要防的 bug：
// BASIC_AUTH_PASSWORD_FILE 是一种常见的"密码从文件读"约定，XAGENT_TOKEN 则可能是任何人
// 自己的变量，两者都与本仓库的凭据无关，按前缀判会把它们一起吞掉。
func TestSanitize_MatchesNamesExactly(t *testing.T) {
	in := []string{
		"BASIC_AUTH_PASSWORD_FILE=/run/secrets/pw",
		"XAGENT_TOKEN=y",
		"AGENT_TOKEN_PATH=/run/secrets/tok",
		"BASIC_AUTH_PASSWORD=should-go",
	}
	got := Sanitize(in)
	want := []string{
		"BASIC_AUTH_PASSWORD_FILE=/run/secrets/pw",
		"XAGENT_TOKEN=y",
		"AGENT_TOKEN_PATH=/run/secrets/tok",
	}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestSanitize_KeepsOddlyShapedEntries 值里带 =、值为空、整条不含 = 的条目都原样保留。
//
// environ 不保证每一格都长成 KEY=VALUE：值里带 = 完全合法（A=b=c 的键是 A，值是 b=c），
// KEY= 是一个存在但为空的变量，而不含 = 的条目根本没有键。只要键没命中 Denied，
// 这里就不替 execve 做主。
func TestSanitize_KeepsOddlyShapedEntries(t *testing.T) {
	in := []string{"A=b=c", "EMPTY=", "NOEQUALSIGN", "AGENT_TOKEN=go-away"}
	got := Sanitize(in)
	want := []string{"A=b=c", "EMPTY=", "NOEQUALSIGN"}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestSanitize_PreservesOrderOfRemainingEntries 剩下的条目保持原顺序。
//
// 顺序不是洁癖：environ 允许同名变量出现多次，取哪一个由 libc 决定（glibc 取第一条）。
// 打乱顺序等于悄悄改变重复变量的取值。
func TestSanitize_PreservesOrderOfRemainingEntries(t *testing.T) {
	in := []string{"Z=1", "AGENT_TOKEN=t", "M=2", "BASIC_AUTH_USER=admin", "A=3", "DUP=first", "DUP=second"}
	got := Sanitize(in)
	want := []string{"Z=1", "M=2", "A=3", "DUP=first", "DUP=second"}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestSanitize_DoesNotModifyItsInput 入参切片调用后不变。
//
// os.Environ() 每次返回一份新切片，但调用方不止它一个（runInstallRunnerScript 还会
// append 一条），而就地过滤 + 截断会写坏底层数组。返回新切片这件事得有人盯着。
func TestSanitize_DoesNotModifyItsInput(t *testing.T) {
	in := []string{"PATH=/usr/bin", "AGENT_TOKEN=t", "HOME=/home/app"}
	before := slices.Clone(in)
	_ = Sanitize(in)
	if !slices.Equal(in, before) {
		t.Errorf("入参被改成了 %v，原本是 %v", in, before)
	}
}

// TestSanitize_EmptyAndNil 空输入不该 panic，也不该返回 nil 以外的意外形状。
func TestSanitize_EmptyAndNil(t *testing.T) {
	if got := Sanitize(nil); len(got) != 0 {
		t.Errorf("Sanitize(nil) = %v，期望空", got)
	}
	if got := Sanitize([]string{}); len(got) != 0 {
		t.Errorf("Sanitize([]) = %v，期望空", got)
	}
}

// TestEnviron_FiltersTheProcessEnvironment Environ 就是 Sanitize(os.Environ())，
// 且不动本进程的环境——Agent 每次请求都要从环境里读 AGENT_TOKEN 去鉴权。
func TestEnviron_FiltersTheProcessEnvironment(t *testing.T) {
	t.Setenv("AGENT_TOKEN", "super-secret")
	t.Setenv("DOCKER_HOST", "tcp://d:2375")

	got := Environ()
	for _, entry := range got {
		if k, _, _ := strings.Cut(entry, "="); k == "AGENT_TOKEN" {
			t.Errorf("Environ() 里还有 %q", entry)
		}
	}
	if !slices.Contains(got, "DOCKER_HOST=tcp://d:2375") {
		t.Error("Environ() 丢了 DOCKER_HOST")
	}
	if os.Getenv("AGENT_TOKEN") != "super-secret" {
		t.Error("Environ() 动了本进程的环境；Agent 的鉴权就是从这里读令牌的")
	}
}
