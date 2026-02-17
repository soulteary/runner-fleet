package handler

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/lab-dev/github-actions-runner-manager/internal/config"
	"github.com/lab-dev/github-actions-runner-manager/internal/runner"
	"github.com/labstack/echo/v4"
)

// installRunnerScriptPath 容器内自动安装 runner 的脚本路径（Docker 镜像中有）
const installRunnerScriptPath = "/app/scripts/install-runner.sh"

// ConfigPath 配置文件路径，由 main 注入
var ConfigPath string

// Version 由 main 注入，供 /version 使用
var Version string

// registrationJob 后台安装并注册 runner 的任务
type registrationJob struct {
	BasePath   string
	InstallDir string
	RunnerName string
	URL        string
	Token      string
	Labels     []string
}

// registrationQueue 后台任务队列，单 worker 顺序执行，避免多任务同时占满资源且 API 不阻塞
var registrationQueue = make(chan registrationJob, 128)

// StartRegistrationWorker 启动后台 worker，应在 main 中调用一次
func StartRegistrationWorker() {
	go func() {
		for j := range registrationQueue {
			runRegistrationJob(j)
		}
	}()
}

// runRegistrationJob 执行单次安装+注册+启动（在后台 goroutine 中调用）
func runRegistrationJob(j registrationJob) {
	installDir := j.InstallDir
	configScript := filepath.Join(installDir, runner.ConfigScriptName())
	if _, err := os.Stat(configScript); err != nil {
		runnerSegment := filepath.Base(installDir)
		installOut, installErr := runInstallRunnerScript(j.BasePath, runnerSegment, 3*time.Minute)
		if installErr != nil {
			msg := "自动安装 Runner 失败: " + installErr.Error()
			writeRegistrationResult(installDir, false, msg)
			log.Printf("[registration] %s 安装失败: %v\noutput: %s", j.RunnerName, installErr, string(installOut))
			return
		}
		if _, err2 := os.Stat(configScript); err2 != nil {
			writeRegistrationResult(installDir, false, "安装完成但未找到 "+runner.ConfigScriptName())
			log.Printf("[registration] %s 安装后未找到 config 脚本\noutput: %s", j.RunnerName, string(installOut))
			return
		}
	}
	out, err := runConfigScript(installDir, j.URL, j.Token, j.Labels, 2*time.Minute)
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		if strings.Contains(string(out), "Must not run with sudo") {
			msg += "（请以非 root 用户运行容器，或设置环境变量 RUNNER_ALLOW_RUNASROOT=1）"
		}
		outLower := strings.ToLower(string(out))
		if strings.Contains(outLower, "token") &&
			(strings.Contains(outLower, "invalid") || strings.Contains(outLower, "expired") ||
				strings.Contains(outLower, "already") || strings.Contains(outLower, "used")) {
			msg += "。请为每个 Runner 在 GitHub 重新生成新的注册 Token"
		}
		writeRegistrationResult(installDir, false, msg)
		log.Printf("[registration] %s 注册失败: %s", j.RunnerName, msg)
		return
	}
	writeRegistrationResult(installDir, true, "注册成功")
	cfg, _ := config.Load(ConfigPath)
	if cfg != nil && cfg.Runners.ContainerMode {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		if startErr := runner.StartRunnerContainer(ctx, cfg, j.RunnerName, installDir); startErr != nil {
			log.Printf("[registration] %s 注册成功但启动 Runner 容器失败: %v", j.RunnerName, startErr)
		} else {
			log.Printf("[registration] %s 已注册并启动 Runner 容器", j.RunnerName)
		}
	} else if startErr := runner.Start(installDir); startErr != nil {
		log.Printf("[registration] %s 注册成功但启动失败: %v", j.RunnerName, startErr)
	} else {
		log.Printf("[registration] %s 已注册并启动", j.RunnerName)
	}
}

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

// runInstallRunnerScript 执行容器内 install-runner.sh，下载并解压 runner 到 basePath/runnerName；超时返回 error
func runInstallRunnerScript(basePath, runnerName string, timeout time.Duration) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, installRunnerScriptPath, runnerName)
	cmd.Env = append(os.Environ(), "RUNNERS_BASE_PATH="+basePath)
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return out, context.DeadlineExceeded
	}
	return out, err
}

// runConfigScript 在 installDir 下执行 config 脚本向 GitHub 注册，超时 2 分钟；返回输出与 error
// 将 installDir 转为绝对路径，避免相对路径在 exec 时随进程 CWD 解析导致找不到 config 脚本
func runConfigScript(installDir, url, token string, labels []string, timeout time.Duration) ([]byte, error) {
	absDir, err := filepath.Abs(installDir)
	if err != nil {
		return nil, fmt.Errorf("解析 runner 路径失败: %w", err)
	}
	installDir = absDir
	configScript := filepath.Join(installDir, runner.ConfigScriptName())
	args := []string{"--url", url, "--token", token}
	if len(labels) > 0 {
		args = append(args, "--labels", strings.Join(labels, ","))
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, configScript, args...)
	cmd.Dir = installDir
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return out, context.DeadlineExceeded
	}
	return out, err
}

// writeRegistrationResult 将本次注册结果写入 runner 目录
func writeRegistrationResult(installDir string, success bool, message string) {
	p := filepath.Join(installDir, runner.RegistrationResultFile)
	body := struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
		At      string `json:"at"`
	}{Success: success, Message: message, At: time.Now().Format(time.RFC3339)}
	b, _ := json.Marshal(body)
	_ = os.WriteFile(p, b, 0644)
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

// ListRunners 列出所有 runner；容器模式下用容器内 Agent 状态覆盖 Running/Status
func ListRunners(c echo.Context) error {
	cfg, err := getConfig(c)
	if err != nil {
		return err
	}
	list := runner.List(cfg)
	if cfg.Runners.ContainerMode {
		ctx := c.Request().Context()
		for i := range list {
			running, status, _ := runner.ContainerRunnerStatus(ctx, cfg, list[i].Name, list[i].InstallDir)
			list[i].Running = running
			list[i].Status = status
		}
	}
	return c.JSON(http.StatusOK, map[string]any{"runners": list})
}

// Index 管理界面首页；容器模式下用容器内 Agent 状态覆盖
func Index(c echo.Context) error {
	cfg, err := getConfig(c)
	if err != nil {
		return err
	}
	list := runner.List(cfg)
	if cfg.Runners.ContainerMode {
		ctx := c.Request().Context()
		for i := range list {
			running, status, _ := runner.ContainerRunnerStatus(ctx, cfg, list[i].Name, list[i].InstallDir)
			list[i].Running = running
			list[i].Status = status
		}
	}
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
	targetTypeNorm := strings.ToLower(strings.TrimSpace(req.TargetType))
	if err := validateTarget(targetTypeNorm, req.Target); err != nil {
		return err
	}
	if !isValidNameOrPath(req.Name) || (req.Path != "" && !isValidNameOrPath(req.Path)) {
		return echo.NewHTTPError(http.StatusBadRequest, "name、path 不可包含 / \\ .. 等非法字符")
	}
	targetNorm := strings.TrimSpace(req.Target)
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
		TargetType: targetTypeNorm,
		Target:     targetNorm,
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
		configScript := filepath.Join(installDir, runner.ConfigScriptName())
		if _, err := os.Stat(configScript); err != nil {
			// 目录为空时需先安装：若存在自动安装脚本则交给后台 worker 执行，避免阻塞请求
			if _, scriptErr := os.Stat(installRunnerScriptPath); scriptErr == nil {
				url := "https://github.com/" + targetNorm
				select {
				case registrationQueue <- registrationJob{
					BasePath:   cfg.Runners.BasePath,
					InstallDir: installDir,
					RunnerName: item.Name,
					URL:        url,
					Token:      req.RegistrationToken,
					Labels:     item.Labels,
				}:
					return c.JSON(http.StatusOK, map[string]any{
						"message":     "Runner 已添加，正在后台安装并注册，请稍后刷新页面查看状态",
						"name":        item.Name,
						"install_dir": installDir,
						"queued":      true,
					})
				default:
					return c.JSON(http.StatusServiceUnavailable, map[string]any{
						"message": "当前注册任务队列已满，请稍后再试",
						"name":    item.Name,
					})
				}
			}
			return c.JSON(http.StatusOK, map[string]any{
				"message":     "配置已保存，Runner 目录已创建。请将 GitHub Actions runner 解压到 " + installDir + " 后，使用注册 token 再次提交或在该目录下手动执行 " + runner.ConfigScriptName(),
				"name":        item.Name,
				"install_dir": installDir,
			})
		}
		// 已有 config 脚本（目录非空）：仅需注册，也放入后台执行，避免长时间阻塞
		url := "https://github.com/" + targetNorm
		select {
		case registrationQueue <- registrationJob{
			BasePath:   cfg.Runners.BasePath,
			InstallDir: installDir,
			RunnerName: item.Name,
			URL:        url,
			Token:      req.RegistrationToken,
			Labels:     item.Labels,
		}:
			return c.JSON(http.StatusOK, map[string]any{
				"message":     "Runner 已添加，正在后台注册，请稍后刷新页面查看状态",
				"name":        item.Name,
				"install_dir": installDir,
				"queued":      true,
			})
		default:
			return c.JSON(http.StatusServiceUnavailable, map[string]any{
				"message": "当前注册任务队列已满，请稍后再试",
				"name":    item.Name,
			})
		}
	}
	return c.JSON(http.StatusOK, map[string]any{
		"message":     "Runner 已添加，请将 runner 解压到目录后使用注册 token 完成注册",
		"name":        item.Name,
		"install_dir": installDir,
	})
}

// GetRunner 查看单个 runner 配置与状态（GET /api/runners/:name）；容器模式下用 Agent 状态覆盖
func GetRunner(c echo.Context) error {
	cfg, err := getConfig(c)
	if err != nil {
		return err
	}
	name := c.Param("name")
	if name == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "请提供 name")
	}
	if !isValidNameOrPath(name) {
		return echo.NewHTTPError(http.StatusBadRequest, "name 不可包含 / \\ .. 等非法字符")
	}
	info := runner.GetByName(cfg, name)
	if info == nil {
		return echo.NewHTTPError(http.StatusNotFound, "未找到该 runner")
	}
	if cfg.Runners.ContainerMode {
		ctx := c.Request().Context()
		running, status, _ := runner.ContainerRunnerStatus(ctx, cfg, info.Name, info.InstallDir)
		info.Running = running
		info.Status = status
	}
	return c.JSON(http.StatusOK, info)
}

// StartRunner 启动指定 runner（POST /api/runners/:name/start）；容器模式下启动 Runner 容器并调 Agent /start
func StartRunner(c echo.Context) error {
	cfg, err := getConfig(c)
	if err != nil {
		return err
	}
	name := c.Param("name")
	if name == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "请提供 name")
	}
	if !isValidNameOrPath(name) {
		return echo.NewHTTPError(http.StatusBadRequest, "name 不可包含 / \\ .. 等非法字符")
	}
	info := runner.GetByName(cfg, name)
	if info == nil {
		return echo.NewHTTPError(http.StatusNotFound, "未找到该 runner")
	}
	if cfg.Runners.ContainerMode {
		ctx := c.Request().Context()
		running, status, _ := runner.ContainerRunnerStatus(ctx, cfg, info.Name, info.InstallDir)
		info.Running = running
		info.Status = status
	}
	if info.Status != runner.StatusInstalled {
		return echo.NewHTTPError(http.StatusBadRequest, "仅已注册的 runner 可启动，当前状态: "+string(info.Status))
	}
	if info.Running {
		return c.JSON(http.StatusOK, map[string]any{"message": "Runner 已在运行中"})
	}
	if cfg.Runners.ContainerMode {
		ctx, cancel := context.WithTimeout(c.Request().Context(), 60*time.Second)
		defer cancel()
		if err := runner.StartRunnerContainer(ctx, cfg, name, info.InstallDir); err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "启动 Runner 容器失败: "+err.Error())
		}
		return c.JSON(http.StatusOK, map[string]any{"message": "已启动 Runner 容器并通知 Agent 启动"})
	}
	if err := runner.Start(info.InstallDir); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "启动失败: "+err.Error())
	}
	return c.JSON(http.StatusOK, map[string]any{"message": "已发起启动"})
}

// StopRunner 停止指定 runner（POST /api/runners/:name/stop）；容器模式下停止 Runner 容器
func StopRunner(c echo.Context) error {
	cfg, err := getConfig(c)
	if err != nil {
		return err
	}
	name := c.Param("name")
	if name == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "请提供 name")
	}
	if !isValidNameOrPath(name) {
		return echo.NewHTTPError(http.StatusBadRequest, "name 不可包含 / \\ .. 等非法字符")
	}
	info := runner.GetByName(cfg, name)
	if info == nil {
		return echo.NewHTTPError(http.StatusNotFound, "未找到该 runner")
	}
	if cfg.Runners.ContainerMode {
		ctx := c.Request().Context()
		running, status, _ := runner.ContainerRunnerStatus(ctx, cfg, info.Name, info.InstallDir)
		info.Running = running
		info.Status = status
	}
	if !info.Running {
		return c.JSON(http.StatusOK, map[string]any{"message": "Runner 未在运行"})
	}
	if cfg.Runners.ContainerMode {
		ctx, cancel := context.WithTimeout(c.Request().Context(), 35*time.Second)
		defer cancel()
		if err := runner.StopRunnerContainer(ctx, name); err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "停止 Runner 容器失败: "+err.Error())
		}
		return c.JSON(http.StatusOK, map[string]any{"message": "已停止 Runner 容器"})
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
	if !isValidNameOrPath(name) {
		return echo.NewHTTPError(http.StatusBadRequest, "name 不可包含 / \\ .. 等非法字符")
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
	targetTypeNorm := strings.ToLower(strings.TrimSpace(req.TargetType))
	if err := validateTarget(targetTypeNorm, req.Target); err != nil {
		return err
	}
	if req.Path != "" && !isValidNameOrPath(req.Path) {
		return echo.NewHTTPError(http.StatusBadRequest, "path 不可包含 / \\ .. 等非法字符")
	}
	targetNorm := strings.TrimSpace(req.Target)
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
			TargetType: targetTypeNorm,
			Target:     targetNorm,
			Labels:     req.Labels,
		}
		return nil
	}); err != nil {
		if he, ok := err.(*echo.HTTPError); ok {
			return he
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "保存配置失败: "+err.Error())
	}
	cfg, err := getConfig(c)
	if err != nil {
		return err
	}
	updated = runner.GetByName(cfg, name)
	// 若已注册且未在运行，自动启动 runner（容器模式则启动容器）
	msg := "已更新"
	var started bool
	if updated != nil && updated.Status == runner.StatusInstalled && !updated.Running {
		if cfg.Runners.ContainerMode {
			ctx, cancel := context.WithTimeout(c.Request().Context(), 60*time.Second)
			defer cancel()
			startErr := runner.StartRunnerContainer(ctx, cfg, name, updated.InstallDir)
			started = (startErr == nil)
			if startErr != nil {
				msg += "，但自动启动 Runner 容器失败: " + startErr.Error()
			} else {
				msg += "，已自动启动 Runner 容器"
			}
		} else {
			startErr := runner.Start(updated.InstallDir)
			started = (startErr == nil)
			if startErr != nil {
				msg += "，但自动启动失败: " + startErr.Error()
			} else {
				msg += "，已自动启动"
			}
		}
	}
	return c.JSON(http.StatusOK, map[string]any{
		"message": msg,
		"runner":  updated,
		"started": started,
	})
}

// RemoveRunnerByName 从路径参数获取 name 并移除（DELETE /api/runners/:name）
// 会先停止 runner 进程，再删除其安装目录，最后从配置中移除。
func RemoveRunnerByName(c echo.Context) error {
	name := c.Param("name")
	if name == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "请提供 name")
	}
	if !isValidNameOrPath(name) {
		return echo.NewHTTPError(http.StatusBadRequest, "name 不可包含 / \\ .. 等非法字符")
	}
	cfg, err := config.Load(ConfigPath)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "加载配置失败: "+err.Error())
	}
	info := runner.GetByName(cfg, name)
	if info == nil {
		return echo.NewHTTPError(http.StatusNotFound, "未找到该 runner")
	}
	installDir := info.InstallDir
	// 先停止 runner：容器模式下停止并删除容器，否则停止本地进程
	if cfg.Runners.ContainerMode {
		ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
		defer cancel()
		_ = runner.RemoveRunnerContainer(ctx, name)
	} else {
		_ = runner.Stop(installDir)
	}
	// 仅当安装目录在 base_path 下时才删除，防止误删系统路径
	if installDir != "" && isUnderBasePath(cfg.Runners.BasePath, installDir) {
		_ = os.RemoveAll(installDir)
	}
	if err := config.LoadAndSave(ConfigPath, func(cfg *config.Config) error {
		return removeRunnerFromConfig(cfg, name)
	}); err != nil {
		if he, ok := err.(*echo.HTTPError); ok {
			return he
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "保存配置失败: "+err.Error())
	}
	return c.JSON(http.StatusOK, map[string]any{"message": "已从配置中移除"})
}

// isUnderBasePath 判断 dir 是否在 basePath 之下（用于安全删除），且不为 basePath 自身
func isUnderBasePath(basePath, dir string) bool {
	baseAbs, err := filepath.Abs(basePath)
	if err != nil {
		return false
	}
	dirAbs, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(baseAbs, dirAbs)
	if err != nil {
		return false
	}
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
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

// validateTarget 校验 target 格式：org 为组织名（不含 /），repo 为 owner/repo（恰好一个斜杠且两端非空）
func validateTarget(targetType, target string) error {
	t := strings.TrimSpace(target)
	if t == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "target 不能为空")
	}
	switch targetType {
	case "org":
		if strings.Contains(t, "/") {
			return echo.NewHTTPError(http.StatusBadRequest, "目标类型为组织(org)时，target 应为组织名，不能包含 /")
		}
		return nil
	case "repo":
		parts := strings.SplitN(t, "/", 2)
		if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
			return echo.NewHTTPError(http.StatusBadRequest, "目标类型为仓库(repo)时，target 应为 owner/repo 格式（且 owner 与 repo 均非空）")
		}
		if strings.Contains(parts[1], "/") {
			return echo.NewHTTPError(http.StatusBadRequest, "target 只能包含一个 /，格式为 owner/repo")
		}
		return nil
	default:
		return echo.NewHTTPError(http.StatusBadRequest, "target_type 必须为 org 或 repo")
	}
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
