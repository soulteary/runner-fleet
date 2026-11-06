# Windows 原生服务

目标平台：Windows 11 / Windows Server 2022 x64，支持 GitHub.com 仓库及组织 Runner。Manager 与各 Runner 作为 Windows 服务运行，无需 WSL 或 Docker。

## 安装与启动

解压 Windows x64 Release ZIP，在**管理员 PowerShell** 中执行：

```powershell
.\runner-manager.exe service install
.\runner-manager.exe service start
Start-Process http://127.0.0.1:8080
```

安装会把程序复制到 `%ProgramFiles%\RunnerFleet\runner-manager.exe`，并以 `LocalSystem` 注册自动启动的 `RunnerFleet` 服务。默认数据位置：

```text
%ProgramData%\RunnerFleet\config.yaml
%ProgramData%\RunnerFleet\runners\<name>\
%ProgramData%\RunnerFleet\tokens\
%ProgramData%\RunnerFleet\logs\manager-YYYY-MM-DD.log
```

自定义配置时，将 `config.windows.yaml.example` 复制到专用目录，执行 `service install -config "D:\RunnerFleet\config.yaml"`。安装会固化绝对路径；相对 `base_path` 按配置文件目录解析。配置目录、Runner 根目录和程序目录会应用仅系统及管理员可访问的 ACL，因此请使用专用目录。日志仍位于默认 ProgramData 目录。

## 网页添加

在 GitHub 的 Settings → Actions → Runners → New self-hosted runner 中选择 Windows/x64。将 `.\config.cmd --url https://github.com/owner/repo --token TOKEN` 粘贴到 Manager，解析并确认名称、标签后添加。粘贴内容仅作为数据解析，不会作为整段脚本执行。

Manager 自动下载官方 Windows ZIP，校验官方 SHA256，解压并以 `NT AUTHORITY\NETWORK SERVICE` 注册无人值守服务。操作服务前，会读取官方 `.service` 文件并核对服务程序路径。

启动会保存 `desired_state: running`，启用开机启动和故障恢复；停止会保存 `stopped`，禁用启动及恢复动作，并等待服务停止。停止状态跨重启保持。重启或升级 Manager 不会停止 Runner；状态未知时不会自动创建或重启。

注册失败请使用新的 GitHub Token。已注册但服务配置失败时，点启动可修复服务，不会再次注册。删除会先停止并卸载本地服务、清理关联权限组，再删文件。没有可用 PAT 时，按删除结果提示在 GitHub 手动删除残留条目。

## 构建环境与维护

MSVC、Windows SDK、Git 等工具需预先安装并对 `NetworkService` 可用。个人配置、凭据和映射网络盘不会自动共享。所有 Runner 共用该账户，不提供实例间账户隔离。网页默认仅监听本机；远程访问前需配置监听地址与现有认证。

```powershell
.\runner-manager.exe service stop
.\runner-manager.exe service start
.\runner-manager.exe service uninstall
```

卸载仅移除 Manager 服务，保留文件、配置及 Runner 服务。升级时停止 Manager，替换已安装的 exe，再启动；Runner 任务继续运行。

## Windows 实机验收

Windows CI 已配置构建、模拟生命周期失败、原生 SCM 双实例以及 Manager 服务安装与健康检查。真实 GitHub 凭据和重启验收需要在目标 Windows 机器执行：

- 使用有效 Token 注册两个 Runner，确认 GitHub 在线状态，并各执行一次实际构建。
- 停止其中一个，确认另一个继续运行；等待巡检并重启电脑，已停止实例仍保持停止。
- 执行长任务时重启 Manager，确认任务继续。
- 验证异常退出恢复，以及服务修复不会重复注册 GitHub 身份。
- 删除后确认本地服务、权限组和目录清理完成，并核对 GitHub 注销结果。

## 开发检查

Windows 后端仅编入 Windows 程序；原有 Linux 覆盖率检查仍针对 Linux 生产代码。在 Linux/macOS 上测试安装器和模拟控制器：

```bash
go test -race -tags windowsrunner_portable ./internal/windowsrunner
node --test scripts/test-command-parser.cjs
```

在管理员 Windows 会话中运行原生 SCM 测试：

```powershell
$env:RUNNER_FLEET_SCM_TESTS = "1"
go test -race ./internal/windowsrunner
```
