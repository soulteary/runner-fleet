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
// 改为扫 procfs、按 argv 里的绝对路径认领进程。扫描本身由 procfind-kit 负责，
// 本包只留 actions/runner 特有的那部分知识：哪些脚本、哪个可执行文件，
// 以及为什么两者都得认（见 runnerSpec）。
//
// 只在有 procfs 的系统（Linux）上有效；其余平台一律返回「找不到」，
// 与修复前的实际行为一致（那时 pid 文件同样永远不存在）。
package runnerproc

import (
	"path/filepath"
	"runtime"

	procfind "github.com/soulteary/procfind-kit"
)

// procRoot 是 procfs 挂载点，做成变量仅为测试可替换
var procRoot = "/proc"

func scanner() procfind.Scanner { return procfind.Scanner{Root: procRoot} }

// listenerName 是监听器可执行文件在安装目录下的相对路径。
// run-helper.sh 以 `"$DIR"/bin/Runner.Listener run $*` 拉起它，argv[0] 即该路径。
func listenerName() string {
	if runtime.GOOS == "windows" {
		return filepath.Join("bin", "Runner.Listener.exe")
	}
	return filepath.Join("bin", "Runner.Listener")
}

// ListenerBinary 返回该安装目录下 Runner.Listener 可执行文件的绝对路径。
func ListenerBinary(installDir string) string {
	return filepath.Join(installDir, listenerName())
}

// runnerSpec 描述「怎样算是属于这个安装目录的 runner 进程」。
//
// 路径相对安装目录，由 procfind-kit 拼成绝对路径后与 argv 比对。
//
// 认监护脚本而不只认监听器，是因为 run.sh 会在两次监听器之间等待：
// run-helper.sh 对退出码 2 的处理是 `safe_sleep.sh 5` 后返回，由 run.sh 的
// while 循环重新拉起。那 5 秒里监听器进程确实不存在，若只认监听器就会误判
// 为已死，Manager 于是再拉起一个，正好制造出本包要消灭的那种重复启动。
//
// 两者分列在 Scripts 与 Executables 里也不是摆设：procfind 保证脚本排在
// 可执行文件之前返回，而 Stop 正依赖这个次序——先让 run.sh 退出，
// 否则它的 while 循环可能刚好把监听器再拉起来一次。
func runnerSpec() procfind.Spec {
	if runtime.GOOS == "windows" {
		return procfind.Spec{
			Scripts:     []string{"run.cmd", "run-helper.cmd"},
			Executables: []string{listenerName()},
		}
	}
	return procfind.Spec{
		Scripts:     []string{"run.sh", "run-helper.sh"},
		Executables: []string{listenerName()},
	}
}

// Find 返回属于 installDir 的 runner 进程 pid，监护脚本在前、监听器在后。
func Find(installDir string) []int {
	return scanner().Find(installDir, runnerSpec())
}

// FindMany 一次扫描 procfs，把进程分派给各自的安装目录，结果按传入的原字符串索引。
// 与逐个调用 Find 等价，区别只在于 N 个目录也只扫一遍——List 要同时判定一整批
// runner，逐个扫等于把整张进程表读 N 遍。
func FindMany(installDirs []string) map[string][]int {
	return scanner().FindMany(installDirs, runnerSpec())
}

// Running 报告 installDir 下是否有 runner 进程存活
func Running(installDir string) bool {
	return scanner().Running(installDir, runnerSpec())
}
