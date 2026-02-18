// Package handler 实现 Manager 的 HTTP API 与 WebUI 逻辑。
// Manager 职责：配置读写、Runner 注册编排（安装/注册脚本）、容器启停调度、状态聚合；不直接承载 Runner 进程。
package handler

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"html/template"
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

// Supported UI languages, same as docs (en, zh, fr, ja, ko, de).
var supportedLangs = []string{"en", "zh", "fr", "ja", "ko", "de"}

// I18nLoader loads translations for a language code (e.g. "en", "zh"). Set by main from embed.
var I18nLoader func(lang string) (map[string]string, error)

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
	if cfg != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		if startErr := runner.StartIfInstalled(ctx, cfg, j.RunnerName, installDir); startErr != nil {
			log.Printf("[registration] %s 注册成功但启动失败: %v", j.RunnerName, startErr)
		} else {
			log.Printf("[registration] %s 已注册并启动", j.RunnerName)
		}
	}
}

func getConfig(c echo.Context) (*config.Config, error) {
	cfg, err := config.Load(ConfigPath)
	if err != nil {
		return nil, echo.NewHTTPError(http.StatusInternalServerError, "加载配置失败: "+err.Error())
	}
	return cfg, nil
}

func applyProbeFailure(info *runner.RunnerInfo, statusErr error) {
	info.Running = false
	info.Status = runner.StatusUnknown
	pt := runner.DetectProbeErrorType(statusErr)
	info.Probe = &runner.ProbeInfo{
		Error:        statusErr.Error(),
		Type:         string(pt),
		Suggestion:   runner.ProbeSuggestion(pt),
		CheckCommand: runner.ProbeCheckCommand(pt),
		FixCommand:   runner.ProbeFixCommand(pt),
	}
}

func clearProbe(info *runner.RunnerInfo) {
	info.Probe = nil
}

// applyContainerStatus 容器模式下用 Agent 状态覆盖 list 中每项的 Running/Status，就地修改
func applyContainerStatus(ctx context.Context, cfg *config.Config, list []runner.RunnerInfo) {
	for i := range list {
		applyContainerStatusOne(ctx, cfg, &list[i])
	}
}

// applyContainerStatusOne 容器模式下用 Agent 状态覆盖单条 info 的 Running/Status/Probe
func applyContainerStatusOne(ctx context.Context, cfg *config.Config, info *runner.RunnerInfo) {
	running, status, statusErr := runner.ContainerRunnerStatus(ctx, cfg, info.Name, info.InstallDir)
	if statusErr != nil {
		log.Printf("[container-status] name=%s: %v", info.Name, statusErr)
		applyProbeFailure(info, statusErr)
		return
	}
	clearProbe(info)
	info.Running = running
	info.Status = status
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
		applyContainerStatus(c.Request().Context(), cfg, list)
	}
	return c.JSON(http.StatusOK, map[string]any{"runners": list})
}

// resolveLang returns the UI language: Cookie "lang" > Query "lang" > Accept-Language > "en".
func resolveLang(c echo.Context) string {
	if v, err := c.Cookie("lang"); err == nil && v != nil && v.Value != "" {
		vv := strings.ToLower(strings.TrimSpace(v.Value))
		for _, supported := range supportedLangs {
			if vv == supported {
				return vv
			}
		}
	}
	if v := c.QueryParam("lang"); v != "" {
		vv := strings.ToLower(strings.TrimSpace(v))
		for _, supported := range supportedLangs {
			if vv == supported {
				return vv
			}
		}
	}
	if ah := c.Request().Header.Get("Accept-Language"); ah != "" {
		for _, part := range strings.Split(ah, ",") {
			part = strings.TrimSpace(part)
			if i := strings.Index(part, ";"); i >= 0 {
				part = strings.TrimSpace(part[:i])
			}
			code := strings.ToLower(part)
			if i := strings.Index(code, "-"); i >= 0 {
				code = code[:i]
			}
			for _, supported := range supportedLangs {
				if code == supported {
					return supported
				}
			}
		}
	}
	return "en"
}

// Index 管理界面首页；容器模式下用容器内 Agent 状态覆盖
func Index(c echo.Context) error {
	cfg, err := getConfig(c)
	if err != nil {
		return err
	}
	list := runner.List(cfg)
	if cfg.Runners.ContainerMode {
		applyContainerStatus(c.Request().Context(), cfg, list)
	}
	lang := resolveLang(c)
	var T map[string]string
	if I18nLoader != nil {
		T, _ = I18nLoader(lang)
	}
	if T == nil && I18nLoader != nil {
		T, _ = I18nLoader("en")
	}
	if T == nil {
		T = make(map[string]string)
	}
	tjson, _ := json.Marshal(T)
	return c.Render(http.StatusOK, "index.html", map[string]any{
		"Runners": list,
		"Config":  cfg,
		"T":       T,
		"Lang":    lang,
		"TJSON":   template.JS(tjson),
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
	req.Name = strings.TrimSpace(req.Name)
	req.Path = strings.TrimSpace(req.Path)
	req.TargetType = strings.TrimSpace(req.TargetType)
	req.Target = strings.TrimSpace(req.Target)
	req.Labels = normalizeLabels(req.Labels)
	if req.Name == "" || req.TargetType == "" || req.Target == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "name、target_type、target 必填")
	}
	targetTypeNorm := strings.ToLower(strings.TrimSpace(req.TargetType))
	if err := config.ValidateTarget(targetTypeNorm, req.Target); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	if !config.IsSafeRunnerNameOrPath(req.Name) || (req.Path != "" && !config.IsSafeRunnerNameOrPath(req.Path)) {
		return echo.NewHTTPError(http.StatusBadRequest, "name、path 不可包含 / \\ .. 等非法字符")
	}
	targetNorm := req.Target
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
	if !config.IsSafeRunnerNameOrPath(name) {
		return echo.NewHTTPError(http.StatusBadRequest, "name 不可包含 / \\ .. 等非法字符")
	}
	info := runner.GetByName(cfg, name)
	if info == nil {
		return echo.NewHTTPError(http.StatusNotFound, "未找到该 runner")
	}
	if cfg.Runners.ContainerMode {
		applyContainerStatusOne(c.Request().Context(), cfg, info)
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
	if !config.IsSafeRunnerNameOrPath(name) {
		return echo.NewHTTPError(http.StatusBadRequest, "name 不可包含 / \\ .. 等非法字符")
	}
	info := runner.GetByName(cfg, name)
	if info == nil {
		return echo.NewHTTPError(http.StatusNotFound, "未找到该 runner")
	}
	probeFailed := false
	if cfg.Runners.ContainerMode {
		applyContainerStatusOne(c.Request().Context(), cfg, info)
		if info.Probe != nil {
			probeFailed = true
			log.Printf("[start] 容器 Runner 状态探测失败 name=%s，将继续尝试启动: %v", info.Name, info.Probe.Error)
		}
	}
	if info.Status != runner.StatusInstalled {
		return echo.NewHTTPError(http.StatusBadRequest, "仅已注册的 runner 可启动，当前状态: "+string(info.Status))
	}
	if info.Running {
		return c.JSON(http.StatusOK, map[string]any{"message": "Runner 已在运行中"})
	}
	ctx, cancel := context.WithTimeout(c.Request().Context(), 60*time.Second)
	defer cancel()
	if err := runner.StartIfInstalled(ctx, cfg, name, info.InstallDir); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "启动失败: "+err.Error())
	}
	if probeFailed {
		return c.JSON(http.StatusOK, map[string]any{
			"message": "状态探测失败，但已尝试启动 Runner 并通知 Agent",
			"probe":   info.Probe,
		})
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
	if !config.IsSafeRunnerNameOrPath(name) {
		return echo.NewHTTPError(http.StatusBadRequest, "name 不可包含 / \\ .. 等非法字符")
	}
	info := runner.GetByName(cfg, name)
	if info == nil {
		return echo.NewHTTPError(http.StatusNotFound, "未找到该 runner")
	}
	probeFailed := false
	if cfg.Runners.ContainerMode {
		applyContainerStatusOne(c.Request().Context(), cfg, info)
		if info.Probe != nil {
			probeFailed = true
			log.Printf("[stop] 容器 Runner 状态探测失败 name=%s，将继续尝试停止: %v", info.Name, info.Probe.Error)
		}
	}
	if !info.Running && !probeFailed {
		return c.JSON(http.StatusOK, map[string]any{"message": "Runner 未在运行"})
	}
	if cfg.Runners.ContainerMode {
		ctx, cancel := context.WithTimeout(c.Request().Context(), 35*time.Second)
		defer cancel()
		if err := runner.StopRunnerContainer(ctx, name); err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "停止 Runner 容器失败: "+err.Error())
		}
		if probeFailed {
			return c.JSON(http.StatusOK, map[string]any{
				"message": "状态探测失败，但已尝试停止 Runner 容器",
				"probe":   info.Probe,
			})
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
	if !config.IsSafeRunnerNameOrPath(name) {
		return echo.NewHTTPError(http.StatusBadRequest, "name 不可包含 / \\ .. 等非法字符")
	}
	var req UpdateRunnerRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "参数错误: "+err.Error())
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Path = strings.TrimSpace(req.Path)
	req.TargetType = strings.TrimSpace(req.TargetType)
	req.Target = strings.TrimSpace(req.Target)
	req.Labels = normalizeLabels(req.Labels)
	// 名称不可改，仅使用 URL 路径参数
	if req.Name != "" && req.Name != name {
		return echo.NewHTTPError(http.StatusBadRequest, "名称不可修改，请与 URL 中的 name 一致")
	}
	if req.TargetType == "" || req.Target == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "target_type、target 必填")
	}
	targetTypeNorm := strings.ToLower(strings.TrimSpace(req.TargetType))
	if err := config.ValidateTarget(targetTypeNorm, req.Target); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	if req.Path != "" && !config.IsSafeRunnerNameOrPath(req.Path) {
		return echo.NewHTTPError(http.StatusBadRequest, "path 不可包含 / \\ .. 等非法字符")
	}
	targetNorm := req.Target
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
	// 若已注册且未在运行，自动启动（容器/进程模式统一走 StartIfInstalled）
	msg := "已更新"
	var started bool
	if updated != nil && updated.Status == runner.StatusInstalled && !updated.Running {
		ctx, cancel := context.WithTimeout(c.Request().Context(), 60*time.Second)
		defer cancel()
		startErr := runner.StartIfInstalled(ctx, cfg, name, updated.InstallDir)
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
// 会先停止 runner 进程，再删除其安装目录，最后从配置中移除。
func RemoveRunnerByName(c echo.Context) error {
	name := c.Param("name")
	if name == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "请提供 name")
	}
	if !config.IsSafeRunnerNameOrPath(name) {
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

func normalizeLabels(labels []string) []string {
	if len(labels) == 0 {
		return nil
	}
	out := make([]string, 0, len(labels))
	for _, label := range labels {
		v := strings.TrimSpace(label)
		if v != "" {
			out = append(out, v)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
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
