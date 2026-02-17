package handler

import (
	"crypto/rand"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/lab-dev/github-actions-runner-manager/internal/config"
	"github.com/lab-dev/github-actions-runner-manager/internal/runner"
	"github.com/labstack/echo/v4"
)

// ConfigPath 配置文件路径，由 main 注入
var ConfigPath string

// Version 由 main 注入，供 /version 使用
var Version string

func getConfig(c echo.Context) (*config.Config, error) {
	cfg, err := config.Load(ConfigPath)
	if err != nil {
		return nil, echo.NewHTTPError(http.StatusInternalServerError, "加载配置失败: "+err.Error())
	}
	return cfg, nil
}

// shortRandomSuffix 生成 6 位小写字母+数字的随机后缀，用于 runner 名称去重
func shortRandomSuffix() string {
	const letters = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = letters[int(b[i])%len(letters)]
	}
	return string(b)
}

// runnerNameExists 判断配置中是否已存在同名 runner
func runnerNameExists(cfg *config.Config, name string) bool {
	for _, item := range cfg.Runners.Items {
		if item.Name == name {
			return true
		}
	}
	return false
}

// Health 健康检查，供负载均衡或 K8s 探针使用
func Health(c echo.Context) error {
	return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}

// VersionInfo 返回版本信息（未注入时返回 dev）
func VersionInfo(c echo.Context) error {
	v := Version
	if v == "" {
		v = "dev"
	}
	return c.JSON(http.StatusOK, map[string]string{"version": v})
}

// ListRunners 列出所有 runner
func ListRunners(c echo.Context) error {
	cfg, err := getConfig(c)
	if err != nil {
		return err
	}
	list := runner.List(cfg)
	return c.JSON(http.StatusOK, map[string]any{"runners": list})
}

// Index 管理界面首页
func Index(c echo.Context) error {
	cfg, err := getConfig(c)
	if err != nil {
		return err
	}
	list := runner.List(cfg)
	return c.Render(http.StatusOK, "index.html", map[string]any{
		"Runners": list,
		"Config":  cfg,
	})
}

// AddRunnerRequest 添加 runner 请求
type AddRunnerRequest struct {
	Name              string   `json:"name" form:"name"`
	Path              string   `json:"path" form:"path"`
	TargetType        string   `json:"target_type" form:"target_type"`
	Target            string   `json:"target" form:"target"`
	Labels            []string `json:"labels" form:"labels"`
	RegistrationToken string   `json:"registration_token" form:"registration_token"`
}

// AddRunner 添加并可选注册新 runner
func AddRunner(c echo.Context) error {
	cfg, err := getConfig(c)
	if err != nil {
		return err
	}
	var req AddRunnerRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "参数错误: "+err.Error())
	}
	if req.Name == "" || req.TargetType == "" || req.Target == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "name、target_type、target 必填")
	}
	if !isValidNameOrPath(req.Name) || (req.Path != "" && !isValidNameOrPath(req.Path)) {
		return echo.NewHTTPError(http.StatusBadRequest, "name、path 不可包含 / \\ .. 等非法字符")
	}
	// 若已存在同名 runner，自动添加短随机后缀直至名称唯一
	name := req.Name
	for i := 0; i < 20; i++ {
		if !runnerNameExists(cfg, name) {
			break
		}
		name = req.Name + "-" + shortRandomSuffix()
	}
	if runnerNameExists(cfg, name) {
		return echo.NewHTTPError(http.StatusConflict, "已存在同名 runner，且无法生成唯一名称，请更换 name 后重试")
	}
	item := config.RunnerItem{
		Name:       name,
		Path:       req.Path,
		TargetType: req.TargetType,
		Target:     req.Target,
		Labels:     req.Labels,
	}
	installDir, err := runner.EnsureRunnerDir(cfg, item.Name, item.Path)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "创建目录失败: "+err.Error())
	}
	if err := config.LoadAndSave(ConfigPath, func(c *config.Config) error {
		for _, i := range c.Runners.Items {
			if i.Name == item.Name {
				return echo.NewHTTPError(http.StatusConflict, "已存在同名 runner: "+item.Name)
			}
		}
		c.Runners.Items = append(c.Runners.Items, item)
		return nil
	}); err != nil {
		if he, ok := err.(*echo.HTTPError); ok {
			return he
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "保存配置失败: "+err.Error())
	}
	if req.RegistrationToken != "" {
		// 在 installDir 执行 config 脚本
		configScript := filepath.Join(installDir, runner.ConfigScriptName())
		if _, err := os.Stat(configScript); err != nil {
			return c.JSON(http.StatusOK, map[string]any{
				"message":     "配置已保存，Runner 目录已创建。请将 GitHub Actions runner 解压到 " + installDir + " 后，使用注册 token 再次提交或在该目录下手动执行 " + runner.ConfigScriptName(),
				"name":        item.Name,
				"install_dir": installDir,
			})
		}
		url := "https://github.com/" + req.Target
		cmd := exec.Command(configScript, "--url", url, "--token", req.RegistrationToken)
		cmd.Dir = installDir
		cmd.Env = os.Environ()
		out, err := cmd.CombinedOutput()
		if err != nil {
			return c.JSON(http.StatusOK, map[string]any{
				"message":     "配置已保存，注册执行失败（可能 token 过期或网络问题）: " + string(out),
				"name":        item.Name,
				"install_dir": installDir,
			})
		}
		// 注册成功后自动启动 runner
		startErr := runner.Start(installDir)
		msg := "Runner 已添加并完成注册"
		if startErr != nil {
			msg += "，但自动启动失败: " + startErr.Error()
		} else {
			msg += "，已自动启动"
		}
		return c.JSON(http.StatusOK, map[string]any{
			"message":     msg,
			"name":        item.Name,
			"install_dir": installDir,
			"output":      string(out),
			"started":     startErr == nil,
		})
	}
	return c.JSON(http.StatusOK, map[string]any{
		"message":     "Runner 已添加，请将 runner 解压到目录后使用注册 token 完成注册",
		"name":        item.Name,
		"install_dir": installDir,
	})
}

// RemoveRunnerRequest 删除 runner 请求（仅从配置移除，不执行 remove-token）
type RemoveRunnerRequest struct {
	Name string `json:"name" form:"name"`
}

// RemoveRunner 从配置中移除 runner（POST body 或 form 提供 name）
func RemoveRunner(c echo.Context) error {
	var req RemoveRunnerRequest
	_ = c.Bind(&req)
	if req.Name == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "请提供 name")
	}
	if err := config.LoadAndSave(ConfigPath, func(cfg *config.Config) error {
		return removeRunnerFromConfig(cfg, req.Name)
	}); err != nil {
		if he, ok := err.(*echo.HTTPError); ok {
			return he
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "保存配置失败")
	}
	return c.JSON(http.StatusOK, map[string]any{"message": "已从配置中移除"})
}

// GetRunner 查看单个 runner 配置与状态（GET /api/runners/:name）
func GetRunner(c echo.Context) error {
	cfg, err := getConfig(c)
	if err != nil {
		return err
	}
	name := c.Param("name")
	if name == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "请提供 name")
	}
	info := runner.GetByName(cfg, name)
	if info == nil {
		return echo.NewHTTPError(http.StatusNotFound, "未找到该 runner")
	}
	return c.JSON(http.StatusOK, info)
}

// StartRunner 启动指定 runner（POST /api/runners/:name/start）
func StartRunner(c echo.Context) error {
	cfg, err := getConfig(c)
	if err != nil {
		return err
	}
	name := c.Param("name")
	if name == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "请提供 name")
	}
	info := runner.GetByName(cfg, name)
	if info == nil {
		return echo.NewHTTPError(http.StatusNotFound, "未找到该 runner")
	}
	if info.Status != runner.StatusInstalled {
		return echo.NewHTTPError(http.StatusBadRequest, "仅已注册的 runner 可启动，当前状态: "+string(info.Status))
	}
	if info.Running {
		return c.JSON(http.StatusOK, map[string]any{"message": "Runner 已在运行中"})
	}
	if err := runner.Start(info.InstallDir); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "启动失败: "+err.Error())
	}
	return c.JSON(http.StatusOK, map[string]any{"message": "已发起启动"})
}

// StopRunner 停止指定 runner（POST /api/runners/:name/stop）
func StopRunner(c echo.Context) error {
	cfg, err := getConfig(c)
	if err != nil {
		return err
	}
	name := c.Param("name")
	if name == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "请提供 name")
	}
	info := runner.GetByName(cfg, name)
	if info == nil {
		return echo.NewHTTPError(http.StatusNotFound, "未找到该 runner")
	}
	if !info.Running {
		return c.JSON(http.StatusOK, map[string]any{"message": "Runner 未在运行"})
	}
	if err := runner.Stop(info.InstallDir); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "停止失败: "+err.Error())
	}
	return c.JSON(http.StatusOK, map[string]any{"message": "已发送停止信号"})
}

// UpdateRunnerRequest 更新 runner 请求（名称不可改，以 URL 路径参数为准）
type UpdateRunnerRequest struct {
	Name       string   `json:"name" form:"name"`
	Path       string   `json:"path" form:"path"`
	TargetType string   `json:"target_type" form:"target_type"`
	Target     string   `json:"target" form:"target"`
	Labels     []string `json:"labels" form:"labels"`
}

// UpdateRunner 更新 runner 配置（PUT /api/runners/:name）；名称不可改，与目录一致
func UpdateRunner(c echo.Context) error {
	name := c.Param("name")
	if name == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "请提供 name")
	}
	var req UpdateRunnerRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "参数错误: "+err.Error())
	}
	// 名称不可改，仅使用 URL 路径参数
	if req.Name != "" && req.Name != name {
		return echo.NewHTTPError(http.StatusBadRequest, "名称不可修改，请与 URL 中的 name 一致")
	}
	if req.TargetType == "" || req.Target == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "target_type、target 必填")
	}
	if req.Path != "" && !isValidNameOrPath(req.Path) {
		return echo.NewHTTPError(http.StatusBadRequest, "path 不可包含 / \\ .. 等非法字符")
	}
	var updated *runner.RunnerInfo
	if err := config.LoadAndSave(ConfigPath, func(cfg *config.Config) error {
		idx := -1
		for i, item := range cfg.Runners.Items {
			if item.Name == name {
				idx = i
				break
			}
		}
		if idx < 0 {
			return echo.NewHTTPError(http.StatusNotFound, "未找到该 runner")
		}
		cfg.Runners.Items[idx] = config.RunnerItem{
			Name:       name,
			Path:       req.Path,
			TargetType: req.TargetType,
			Target:     req.Target,
			Labels:     req.Labels,
		}
		return nil
	}); err != nil {
		if he, ok := err.(*echo.HTTPError); ok {
			return he
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "保存配置失败")
	}
	cfg, err := getConfig(c)
	if err != nil {
		return err
	}
	updated = runner.GetByName(cfg, name)
	// 若已注册且未在运行，自动启动 runner
	msg := "已更新"
	var started bool
	if updated != nil && updated.Status == runner.StatusInstalled && !updated.Running {
		startErr := runner.Start(updated.InstallDir)
		started = (startErr == nil)
		if startErr != nil {
			msg += "，但自动启动失败: " + startErr.Error()
		} else {
			msg += "，已自动启动"
		}
	}
	return c.JSON(http.StatusOK, map[string]any{
		"message": msg,
		"runner":  updated,
		"started": started,
	})
}

// RemoveRunnerByName 从路径参数获取 name 并移除（DELETE /api/runners/:name）
func RemoveRunnerByName(c echo.Context) error {
	name := c.Param("name")
	if name == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "请提供 name")
	}
	if err := config.LoadAndSave(ConfigPath, func(cfg *config.Config) error {
		return removeRunnerFromConfig(cfg, name)
	}); err != nil {
		if he, ok := err.(*echo.HTTPError); ok {
			return he
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "保存配置失败")
	}
	return c.JSON(http.StatusOK, map[string]any{"message": "已从配置中移除"})
}

// isValidNameOrPath 校验 name/path 不含路径穿越或非法字符
func isValidNameOrPath(s string) bool {
	if s == "" {
		return true
	}
	if strings.Contains(s, "..") || strings.Contains(s, "/") || strings.Contains(s, "\\") {
		return false
	}
	return true
}

// removeRunnerFromConfig 从内存中的配置移除指定 runner，不写文件（由 LoadAndSave 负责保存）
func removeRunnerFromConfig(cfg *config.Config, name string) error {
	newItems := make([]config.RunnerItem, 0, len(cfg.Runners.Items))
	for _, item := range cfg.Runners.Items {
		if item.Name != name {
			newItems = append(newItems, item)
		}
	}
	if len(newItems) == len(cfg.Runners.Items) {
		return echo.NewHTTPError(http.StatusNotFound, "未找到该 runner")
	}
	cfg.Runners.Items = newItems
	return nil
}
