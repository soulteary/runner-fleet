// Package childenv 为 Manager 与 Agent 派生的子进程构造环境变量。
//
// 子进程——run.sh 及其运行的 Job、config.sh、install-runner.sh——不需要、也不该拿到
// Manager / Agent 自己的凭据。此前这四处都是 cmd.Env = os.Environ()，于是默认模式下
// docker-compose.yml 用 environment: 注入 Manager 的 BASIC_AUTH_PASSWORD 一路传到了
// Job 的环境里：workflow 里一个 env、printenv 或 set 步骤，或者某个会打印环境变量的
// 第三方 Action，就能把管理后台密码写进 Job 日志。它不是 Actions secret，GitHub 不打码。
// 容器模式下同理，Manager 用 -e 注入给 Agent 的 AGENT_TOKEN 会进到 Job 的环境里。
//
// 为什么是 denylist 而不是 allowlist：传下去的那份环境里，绝大多数变量是 Job 要用的。
// 默认模式 + DinD 靠从 Manager 继承来的 DOCKER_HOST 找到 daemon；用户还常设
// HTTP_PROXY/HTTPS_PROXY/NO_PROXY、RUNNER_ALLOW_RUNASROOT、TZ、LANG，以及 actions/runner
// 自己认的一堆 RUNNER_* 与 ACTIONS_*。allowlist 漏掉其中任何一个都是静默失效——
// Job 不会说"少了个变量"，它只会以一种看不出原因的方式失败。denylist 漏掉一个变量的
// 代价是它继续泄露，而那一类有 guard_test.go 盯着：新增的凭据类变量不进 Denied 就红。
//
// 这挡的是被动泄露——日志、第三方 Action、错误上报——不是隔离边界。默认模式下 Job 与
// Manager 同为 UID 1001，可以直接读 /proc/<manager pid>/environ；镜像里还有免密 sudo
// 和 docker.sock。想挡住主动去拿凭据的 Job，要动的是信任边界，不是这里（见 SECURITY.md）。
package childenv

import (
	"os"
	"strings"
)

// Denied 是绝不能出现在子进程环境里的变量名，精确匹配、区分大小写。
//
// BASIC_AUTH_USER 不是秘密，密码才是，但它同样只属于 Manager，Job 拿着没有用处；
// 一起去掉，泄露的就不是"半个凭据"。注意 guard_test.go 的 PASSWORD|TOKEN|SECRET|_KEY$
// 那条模式匹配不到这个名字——它是刻意加进来的，不是模式扫出来的。
var Denied = []string{
	"BASIC_AUTH_PASSWORD",
	"BASIC_AUTH_USER",
	"AGENT_TOKEN",
}

// Sanitize 返回去掉 Denied 各项后的 environ 副本，其余条目保持原顺序；不修改入参。
//
// 键取第一个 = 之前的部分，精确匹配：BASIC_AUTH_PASSWORD_FILE 与 XAGENT_TOKEN 都得留下，
// 按前缀判会把它们一起误删。不含 = 的畸形条目没有键可言，原样保留——execve 本来也不管
// 这一格是什么形状，这里没有理由替它做主。
func Sanitize(environ []string) []string {
	out := make([]string, 0, len(environ))
	for _, entry := range environ {
		key, _, ok := strings.Cut(entry, "=")
		if ok && isDenied(key) {
			continue
		}
		out = append(out, entry)
	}
	return out
}

func isDenied(key string) bool {
	for _, name := range Denied {
		if key == name {
			return true
		}
	}
	return false
}

// Environ 等价于 Sanitize(os.Environ())，供 exec.Cmd.Env 直接使用。
//
// 只作用于交给子进程的那份副本，本进程的环境不动：Agent 每次请求都要
// os.Getenv("AGENT_TOKEN") 去鉴权（见 cmd/runner-agent 的 expectedToken），
// 对自己 Unsetenv 等于把 /start、/stop 的鉴权关掉。
func Environ() []string {
	return Sanitize(os.Environ())
}
