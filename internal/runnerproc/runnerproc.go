// Package runnerproc 判定某个安装目录下的 actions/runner 进程是否存活。
//
// 为什么不读 pid 文件：actions/runner 根本不写。三个启动脚本
// （src/Misc/layoutroot/run.sh、src/Misc/layoutroot/run-helper.sh.template、
// src/Misc/layoutbin/runsvc.sh）里都没有落 pid 文件的语句，pid 只存在 shell
// 变量 $PID 里；安装目录下的 .path 装的是 PATH 字符串，不是 pid——runsvc.sh
// 里就写着 `export PATH=$(cat .path)`。
//
// 早先按 "Runner.Listener.pid" / ".path" 读 pid 的做法因此对任何一个真实的
// runner 目录都恒为「未找到」，于是每个 runner 都被判定为未运行，被 Manager
// 的定时巡检每 5 分钟重复拉起一次。
//
// 这里改为扫 /proc，按 argv 里的绝对路径认领属于该安装目录的进程。
// 只在有 /proc 的系统（Linux）上有效；其余平台一律返回「找不到」，
// 与修复前的实际行为一致（那时 pid 文件同样永远不存在）。
package runnerproc

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
)

// procRoot 是 procfs 挂载点，做成变量仅为测试可替换
var procRoot = "/proc"

// ListenerBinary 返回该安装目录下 Runner.Listener 可执行文件的绝对路径。
// run-helper.sh 以 `"$DIR"/bin/Runner.Listener run $*` 拉起它，argv[0] 即此路径。
func ListenerBinary(installDir string) string {
	name := "Runner.Listener"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(installDir, "bin", name)
}

// supervisorScripts 返回看护 Runner.Listener 的脚本路径。
//
// 认监护脚本而不只认监听器，是因为 run.sh 会在两次监听器之间等待：
// run-helper.sh 对退出码 2 的处理是 `safe_sleep.sh 5` 后返回，由 run.sh 的
// while 循环重新拉起。那 5 秒里监听器进程确实不存在，若只认监听器就会误判
// 为已死，Manager 于是再拉起一个，正好制造出本包要消灭的那种重复启动。
func supervisorScripts(installDir string) []string {
	if runtime.GOOS == "windows" {
		return []string{
			filepath.Join(installDir, "run.cmd"),
			filepath.Join(installDir, "run-helper.cmd"),
		}
	}
	return []string{
		filepath.Join(installDir, "run.sh"),
		filepath.Join(installDir, "run-helper.sh"),
	}
}

// kind 表示一个进程与目标安装目录的关系
type kind int

const (
	kindNone kind = iota
	kindSupervisor
	kindListener
)

// Find 返回属于 installDir 的 runner 进程 pid，监护脚本在前、监听器在后。
//
// 顺序是给 Stop 用的：先终止 run.sh 再终止 Runner.Listener，否则 run.sh 的
// while 循环可能刚好把监听器再拉起来一次。
func Find(installDir string) []int {
	return FindMany([]string{installDir})[installDir]
}

// FindMany 一次扫描 /proc，把进程分派给各自的安装目录，结果按传入的原字符串索引。
// 与逐个调用 Find 等价，区别只在于 N 个目录也只扫一遍 /proc——List 要同时判定
// 一整批 runner，逐个扫等于把整张进程表读 N 遍。
func FindMany(installDirs []string) map[string][]int {
	// 目标目录先归一成绝对路径去重，但结果仍按调用方给的原字符串索引：
	// 两个写法不同、指向同一目录的入参应当各自拿到同一份 pid 列表
	type target struct {
		keys     []string // 指向这个目录的所有原始写法
		listener string
		scripts  []string
	}
	targets := make([]target, 0, len(installDirs))
	byAbs := make(map[string]int, len(installDirs))
	for _, dir := range installDirs {
		if dir == "" {
			continue
		}
		abs, err := filepath.Abs(dir)
		if err != nil {
			abs = dir
		}
		if i, ok := byAbs[abs]; ok {
			targets[i].keys = append(targets[i].keys, dir)
			continue
		}
		byAbs[abs] = len(targets)
		targets = append(targets, target{
			keys:     []string{dir},
			listener: ListenerBinary(abs),
			scripts:  supervisorScripts(abs),
		})
	}
	if len(targets) == 0 {
		return nil
	}

	entries, err := os.ReadDir(procRoot)
	if err != nil {
		// 没有 /proc（Windows，或 procfs 未挂载）：报告找不到，不猜
		return nil
	}
	supervisors := make(map[string][]int, len(targets))
	listeners := make(map[string][]int, len(targets))
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid <= 0 {
			continue
		}
		argv := readArgv(pid)
		if len(argv) == 0 {
			continue
		}
		for _, tg := range targets {
			k := classify(argv, tg.listener, tg.scripts)
			if k == kindNone {
				continue
			}
			for _, key := range tg.keys {
				if k == kindListener {
					listeners[key] = append(listeners[key], pid)
				} else {
					supervisors[key] = append(supervisors[key], pid)
				}
			}
			// 目标已按绝对路径去重，一个进程只可能命中其中一个，认走了就不必再比
			break
		}
	}

	out := make(map[string][]int, len(targets))
	for _, tg := range targets {
		for _, key := range tg.keys {
			sup, lis := supervisors[key], listeners[key]
			if len(sup) == 0 && len(lis) == 0 {
				continue
			}
			sort.Ints(sup)
			sort.Ints(lis)
			out[key] = append(sup, lis...)
		}
	}
	return out
}

// Running 报告 installDir 下是否有 runner 进程存活
func Running(installDir string) bool {
	return len(Find(installDir)) > 0
}

// classify 判定一个进程是否属于目标安装目录。
//
// Runner.Listener 由 run-helper.sh 以绝对路径直接拉起，argv[0] 就是可执行文件全路径。
// run.sh / run-helper.sh 带 #!/bin/bash，内核走 binfmt_script，进程 argv 形如
// `/bin/bash /runner/run.sh`——脚本路径落在 argv[1]，argv[0] 是解释器。
//
// 只看这两个位置，且认 argv[1] 时要求 argv[0] 确实是个 shell，免得把
// `grep /runner/run.sh` 这类命令行也算成 runner 在跑。
func classify(argv []string, listener string, scripts []string) kind {
	if len(argv) == 0 {
		return kindNone
	}
	if argv[0] == listener {
		return kindListener
	}
	for _, s := range scripts {
		// 未经 shebang 直接 exec 脚本时 argv[0] 即脚本本身
		if argv[0] == s {
			return kindSupervisor
		}
		if len(argv) >= 2 && argv[1] == s && isShell(argv[0]) {
			return kindSupervisor
		}
	}
	return kindNone
}

// isShell 判断 argv[0] 是否为 shell 解释器；只比对文件名，
// 因为 bash 在 /bin 还是 /usr/bin 各发行版不一。
func isShell(path string) bool {
	switch filepath.Base(path) {
	case "sh", "bash", "dash", "zsh", "ksh", "ash", "busybox":
		return true
	}
	return false
}

// readArgv 读取 /proc/<pid>/cmdline 并按 NUL 切分。
//
// 用 cmdline 而不是 readlink /proc/<pid>/exe：cmdline 对同主机任何用户可读，
// 而读 exe 链接在进程属主与调用方不同时会 EACCES（需要 CAP_SYS_PTRACE）。
// Manager 以 UID 1001 运行，不该依赖那种权限。
func readArgv(pid int) []string {
	b, err := os.ReadFile(filepath.Join(procRoot, strconv.Itoa(pid), "cmdline"))
	if err != nil {
		return nil
	}
	b = bytes.TrimRight(b, "\x00")
	if len(b) == 0 {
		// 内核线程的 cmdline 为空
		return nil
	}
	parts := bytes.Split(b, []byte{0})
	argv := make([]string, 0, len(parts))
	for _, p := range parts {
		argv = append(argv, string(p))
	}
	return argv
}
