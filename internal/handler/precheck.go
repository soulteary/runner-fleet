// 添加 Runner 前的冲突预检：在界面还停留在表单上时就把「这个名字会出问题」说清楚，
// 而不是等到写配置、装 runner、建容器时才以各种形态失败。
//
// 检查的是几类真实会失败或产生意外结果的冲突：
//   - 配置里已有同名 Runner（此前会被静默加上随机后缀，用户并不知情）
//   - 名称规范化后与已有 Runner 撞容器名（github-runner-a.b 与 a-b 会映射到同一个）
//   - 安装目录与已有 Runner 相同（path 指到别人的目录）
//   - 磁盘上已有注册过的 runner 目录（带 token 会被 config.sh 拒绝，不带 token 则是接管）
//   - 宿主机上已有同名容器（上一次删除没删干净，启动时会被直接复用）
package handler

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/soulteary/runner-fleet/internal/config"
	"github.com/soulteary/runner-fleet/internal/runner"
)

// 冲突类型，前端据此做本地化文案，未知类型回落到 message
const (
	ConflictNameTaken       = "name_taken"
	ConflictContainerName   = "container_name"
	ConflictInstallDir      = "install_dir"
	ConflictDirRegistered   = "dir_registered"
	ConflictDirAdopt        = "dir_adopt"
	ConflictDirExists       = "dir_exists"
	ConflictContainerExists = "container_exists"
)

// 冲突级别：error 表示继续下去会失败或覆盖已有 Runner，warn 表示能继续但需要知情
const (
	ConflictLevelError = "error"
	ConflictLevelWarn  = "warn"
)

// RunnerConflict 一条冲突。字段与容器探测的 ProbeInfo 对齐，便于前端统一渲染。
type RunnerConflict struct {
	Type       string `json:"type"`
	Level      string `json:"level"`
	Message    string `json:"message"`
	Detail     string `json:"detail,omitempty"`      // 与之冲突的对象：已有 Runner 名、目录或容器名
	Suggestion string `json:"suggestion,omitempty"`  // 人话建议
	FixCommand string `json:"fix_command,omitempty"` // 可直接执行的命令，与语言无关
}

// PrecheckResponse GET /api/runner-precheck 的返回
type PrecheckResponse struct {
	Name          string           `json:"name"`
	InstallDir    string           `json:"install_dir"`
	ContainerName string           `json:"container_name,omitempty"` // 仅容器模式
	Available     bool             `json:"available"`                // 无 error 级冲突
	SuggestedName string           `json:"suggested_name,omitempty"` // 有冲突时给出的可用名称
	Conflicts     []RunnerConflict `json:"conflicts"`
}

// containerLookup 查询容器是否存在，便于测试注入。返回 known=false 表示查不到（如 docker 不可用），此时跳过该项检查
type containerLookup func(containerName string) (exists bool, status string, known bool)

// dockerContainerLookup 真实的容器查询，docker 不可用时静默跳过：
// 预检是锦上添花，不能因为拿不到 docker 就把添加流程挡住
func dockerContainerLookup(ctx context.Context) containerLookup {
	return func(containerName string) (bool, string, bool) {
		exists, status, err := runner.ContainerState(ctx, containerName)
		if err != nil {
			return false, "", false
		}
		return exists, status, true
	}
}

// hasErrorConflict 是否存在 error 级冲突
func hasErrorConflict(conflicts []RunnerConflict) bool {
	for _, c := range conflicts {
		if c.Level == ConflictLevelError {
			return true
		}
	}
	return false
}

// collectRunnerConflicts 汇总名称/路径在当前配置与宿主机上的冲突。
// lookup 为 nil 或非容器模式时跳过容器检查。
// willRegister 表示这次会带注册 token 去跑 config.sh —— 目录里已有注册过的 Runner 时，
// 带 token 是硬失败（config.sh 拒绝重复配置），不带 token 只是把它接管进配置，属于正常用法。
func collectRunnerConflicts(cfg *config.Config, name, path string, lookup containerLookup, willRegister bool) []RunnerConflict {
	conflicts := []RunnerConflict{}
	if cfg == nil || name == "" {
		return conflicts
	}
	item := config.RunnerItem{Name: name, Path: path}
	installDir := item.InstallPath(cfg.Runners.BasePath)
	containerName := config.NormalizedContainerName(name)

	nameTaken := false
	for _, existing := range cfg.Runners.Items {
		if existing.Name == name {
			nameTaken = true
			conflicts = append(conflicts, RunnerConflict{
				Type:       ConflictNameTaken,
				Level:      ConflictLevelError,
				Message:    "配置中已存在同名 Runner: " + name,
				Detail:     name,
				Suggestion: "换一个名称（可用下方建议名），或直接在列表里管理已有的那个 Runner",
			})
			continue
		}
		if filepath.Clean(existing.InstallPath(cfg.Runners.BasePath)) == filepath.Clean(installDir) {
			conflicts = append(conflicts, RunnerConflict{
				Type:       ConflictInstallDir,
				Level:      ConflictLevelError,
				Message:    fmt.Sprintf("安装目录与已有 Runner %s 相同: %s", existing.Name, installDir),
				Detail:     existing.Name,
				Suggestion: "换一个名称，或改 path 指向别的子目录",
			})
		}
		if cfg.Runners.ContainerMode && config.NormalizedContainerName(existing.Name) == containerName {
			conflicts = append(conflicts, RunnerConflict{
				Type:       ConflictContainerName,
				Level:      ConflictLevelError,
				Message:    fmt.Sprintf("名称规范化后与已有 Runner %s 撞容器名: %s", existing.Name, containerName),
				Detail:     existing.Name,
				Suggestion: "容器名只保留字母数字与横线，换一个区分度更高的名称",
			})
		}
	}
	// 同名 Runner 的目录与容器本来就属于它，再报一遍只是噪音
	if nameTaken {
		return conflicts
	}

	if dirState := inspectInstallDir(installDir); dirState != "" {
		switch dirState {
		case "registered":
			if willRegister {
				conflicts = append(conflicts, RunnerConflict{
					Type:       ConflictDirRegistered,
					Level:      ConflictLevelError,
					Message:    "目录 " + installDir + " 下已有注册过的 Runner（存在 .runner），带 token 再注册一次会被 config.sh 拒绝",
					Detail:     installDir,
					Suggestion: "要接管这个已注册的 Runner，把注册 Token 留空直接添加；要重新注册，先移除该目录并在 GitHub 上删掉对应 Runner",
					FixCommand: "rm -rf " + installDir,
				})
				break
			}
			conflicts = append(conflicts, RunnerConflict{
				Type:       ConflictDirAdopt,
				Level:      ConflictLevelWarn,
				Message:    "目录 " + installDir + " 下已有注册过的 Runner，添加后将直接接管它（不会重新注册）",
				Detail:     installDir,
				Suggestion: "若本意是新建一个 Runner，请换个名称",
			})
		case "nonempty":
			conflicts = append(conflicts, RunnerConflict{
				Type:       ConflictDirExists,
				Level:      ConflictLevelWarn,
				Message:    "目录 " + installDir + " 已存在且非空，将直接复用其中已解压的 runner",
				Detail:     installDir,
				Suggestion: "若是上次残留，建议换名或先清空该目录",
			})
		}
	}

	if cfg.Runners.ContainerMode && lookup != nil {
		if exists, status, known := lookup(containerName); known && exists {
			conflicts = append(conflicts, RunnerConflict{
				Type:  ConflictContainerExists,
				Level: ConflictLevelError,
				Message: fmt.Sprintf("宿主机上已存在容器 %s（状态 %s），且不属于当前配置中的任何 Runner；启动时会直接复用它，而它挂载的是创建时的目录，未必是这个 Runner 的",
					containerName, status),
				Detail:     containerName,
				Suggestion: "换个名称，或确认该容器不再使用后删除它，让 Manager 按这个 Runner 的目录重新创建",
				FixCommand: "docker rm -f " + containerName,
			})
		}
	}
	return conflicts
}

// inspectInstallDir 判断安装目录的状态：registered（已注册的 runner）、nonempty（非空）、""（不存在或空）
func inspectInstallDir(installDir string) string {
	fi, err := os.Stat(installDir)
	if err != nil || !fi.IsDir() {
		return ""
	}
	if _, err := os.Stat(filepath.Join(installDir, ".runner")); err == nil {
		return "registered"
	}
	entries, err := os.ReadDir(installDir)
	if err != nil || len(entries) == 0 {
		return ""
	}
	return "nonempty"
}

// suggestRunnerName 返回第一个没有 error 级冲突的候选名：name-2、name-3…，
// 都不行时回落到随机后缀。返回空字符串表示实在找不到。
func suggestRunnerName(cfg *config.Config, name, path string, lookup containerLookup, willRegister bool) string {
	for i := 2; i <= 20; i++ {
		candidate := fmt.Sprintf("%s-%d", name, i)
		if !hasErrorConflict(collectRunnerConflicts(cfg, candidate, path, lookup, willRegister)) {
			return candidate
		}
	}
	for i := 0; i < 20; i++ {
		candidate := name + "-" + shortRandomSuffix()
		if !hasErrorConflict(collectRunnerConflicts(cfg, candidate, path, lookup, willRegister)) {
			return candidate
		}
	}
	return ""
}

// PrecheckRunner 预检名称与路径（GET /api/runner-precheck?name=&path=）。
// 只读，不改配置、不碰目录；供界面在表单上实时提示。
func PrecheckRunner(c echo.Context) error {
	cfg, err := getConfig(c)
	if err != nil {
		return err
	}
	name := strings.TrimSpace(c.QueryParam("name"))
	path := strings.TrimSpace(c.QueryParam("path"))
	if name == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "请提供 name")
	}
	if !config.IsSafeRunnerNameOrPath(name) || (path != "" && !config.IsSafeRunnerNameOrPath(path)) {
		return echo.NewHTTPError(http.StatusBadRequest, "name、path 不可包含 / \\ .. 等非法字符")
	}

	// 容器查询要有上限：docker 卡住时表单不该跟着卡住
	ctx, cancel := context.WithTimeout(c.Request().Context(), 5*time.Second)
	defer cancel()
	var lookup containerLookup
	if cfg.Runners.ContainerMode {
		lookup = dockerContainerLookup(ctx)
	}

	// has_token=1：界面上填了注册 Token，这次会真的去 config.sh 注册
	willRegister := c.QueryParam("has_token") == "1"
	conflicts := collectRunnerConflicts(cfg, name, path, lookup, willRegister)
	resp := PrecheckResponse{
		Name:       name,
		InstallDir: config.RunnerItem{Name: name, Path: path}.InstallPath(cfg.Runners.BasePath),
		Available:  !hasErrorConflict(conflicts),
		Conflicts:  conflicts,
	}
	if cfg.Runners.ContainerMode {
		resp.ContainerName = config.NormalizedContainerName(name)
	}
	if !resp.Available {
		// path 已填时它是固定的，换名也解决不了目录冲突，此时不给建议名
		if path == "" {
			resp.SuggestedName = suggestRunnerName(cfg, name, path, lookup, willRegister)
		}
	}
	return c.JSON(http.StatusOK, resp)
}
