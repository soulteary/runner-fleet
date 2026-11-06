//go:build !windows

package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"github.com/soulteary/runner-fleet/internal/childenv"
	"github.com/soulteary/runner-fleet/internal/config"
	"github.com/soulteary/runner-fleet/internal/runnerproc"
)

// ConfigScriptName 返回当前系统的配置脚本名
func ConfigScriptName() string {
	return "config.sh"
}

// RunScriptName 返回当前系统的运行脚本名
func RunScriptName() string {
	return "run.sh"
}

var execCommand = exec.Command

// Start 在 installDir 下后台启动 runner（执行 run.sh/run.cmd）
// 将 installDir 转为绝对路径，避免相对路径在 exec 时随进程 CWD 解析导致找不到 run.sh
func Start(installDir string) error {
	absDir, err := filepath.Abs(installDir)
	if err != nil {
		return fmt.Errorf("cannot resolve the runner path: %w", err)
	}
	installDir = absDir
	script := filepath.Join(installDir, RunScriptName())
	if _, err := os.Stat(script); err != nil {
		return fmt.Errorf("run script %s not found: %w", script, err)
	}
	cmd := execCommand(script)
	cmd.Dir = installDir
	// run.sh 跑的是用户的 Job：不能把 Manager 自己的凭据传下去，见 internal/childenv
	cmd.Env = childenv.Environ()
	runnerproc.ConfigureCommand(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	// 必须对子进程 Wait，否则在容器内（主进程为 PID 1）退出的 run.sh 会变成僵尸进程。
	// 在后台 goroutine 中 Wait，不阻塞 Start 返回。
	go func() {
		_ = cmd.Wait()
	}()
	return nil
}

// Stop 向该安装目录下的 Runner 进程发送 SIGTERM。
//
// runnerproc.Find 把监护脚本排在监听器之前，这里按序发信号：先让 run.sh 退出，
// 它的 while 循环才不会在监听器被终止后又拉起一个新的。
// 一个都没找到时返回错误——调用方（界面上的「停止」）需要知道没停成。
func Stop(installDir string) error {
	absDir, err := filepath.Abs(installDir)
	if err != nil {
		return fmt.Errorf("cannot resolve the runner path: %w", err)
	}
	installDir = absDir
	pids := runnerproc.Find(installDir)
	if len(pids) == 0 {
		return fmt.Errorf("no running runner process found under %s", installDir)
	}
	var firstErr error
	for _, pid := range pids {
		process, err := os.FindProcess(pid)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		// 进程可能在扫描与发信号之间自己退了，ESRCH 不算失败
		if err := process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

func startLocal(ctx context.Context, dir string) error { return Start(dir) }
func applyWindowsServiceStatus(*RunnerInfo)            {}

func desiredState(config.RunnerItem) string { return "" }
func localRunning(dir string) bool          { return isProcessRunning(dir) }
func applyLocalStatuses(list []RunnerInfo, dirs []string) {
	procs := runnerproc.FindMany(dirs)
	for i := range list {
		list[i].Running = list[i].Status == StatusInstalled && len(procs[list[i].InstallDir]) > 0
	}
}

// isProcessRunning 检测本机是否有属于该安装目录的 Runner 进程存活。
//
// 只对「Runner 进程与 Manager 同在一个 PID namespace」的默认模式有意义。
// 容器模式下 Runner 跑在各自的容器里，Manager 扫自己的 /proc 必然找不到，
// 判定要走 ContainerRunnerStatus 问容器内的 Agent——见 ListWithLiveStatus。
func isProcessRunning(installDir string) bool {
	return runnerproc.Running(installDir)
}
