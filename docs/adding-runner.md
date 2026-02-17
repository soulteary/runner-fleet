# 添加 Runner 流程

## 1. 在 GitHub 获取 Token

1. 进入目标 **仓库** 或 **组织**：**Settings → Actions → Runners → New self-hosted runner**。
2. 在注册说明中复制 **Token**（约 1 小时有效）。**每个 Runner 必须使用新生成的 Token**（同一 Token 只能成功注册一个 Runner）。

## 2. 在本服务中添加

在管理界面「快速添加 Runner」中填写：

- **名称**：唯一，将对应 `runners/<名称>/` 目录。
- **目标类型**：组织（org）或仓库（repo）。
- **目标**：组织名或 `owner/repo`。格式会做校验：org 时不能含 `/`；repo 时必须为 `owner/repo`（恰好一个斜杠，且两端非空）。
- **Token**（可选）：粘贴上一步的 Token，提交时可选择自动执行注册。注册成功后会**自动启动** Runner 程序，无需再手动点击「启动」。

若与已有 Runner 同名，会提示不可重复。

### 从 GitHub 命令一键填充

在 GitHub 的「New self-hosted runner」页面会看到类似命令：

```bash
./config.sh --url https://github.com/owner/repo --token YOUR_TOKEN
```

在管理界面「快速添加 Runner」上方的**从 GitHub 复制命令解析**框中粘贴整行命令，点击「解析并填充」，会自动识别并填入**目标类型**、**目标**、**注册 Token**，并建议**名称**（仓库名或组织名），无需手动拆分 URL 与 Token。

**说明**：自动注册（填写 Token 并提交）时，服务会向 **GitHub.com** 发起注册；若你使用的是 GitHub Enterprise，请勿在界面使用「自动注册」，需在对应 runner 目录下手动执行 `config.sh --url <你的 Enterprise URL> --token <TOKEN>`。

在界面中**编辑**已有 Runner 的配置（目标、标签等）并保存后，若该 Runner 已注册且当前未在运行，也会**自动启动**，无需再手动点击「启动」。

## 3. Runner 程序未安装时

若 `runners/<名称>/` 下尚未安装 runner 程序：

1. 从 [GitHub Actions Runner](https://github.com/actions/runner/releases) 下载对应平台包，解压到 `runners/<名称>/`。
2. 在界面提交「快速添加」表单时填入 Token，并勾选执行注册（注册成功后会自动启动 Runner）；或先提交表单创建配置与目录，再在该目录下手动执行：
   ```bash
   ./config.sh --url https://github.com/owner/repo --token <TOKEN>
   ```

**使用 Docker 或 DinD 时**：若在「快速添加」中填写了 Token 并提交，服务会**先自动执行安装脚本**（下载并解压 runner 到该目录），再执行**向 GitHub 注册**（`config.sh`，超时 2 分钟）并启动；无需事先手动执行 `install-runner.sh`。若自动安装失败（如网络问题），可按 [Docker 部署](docker.md) 中「Docker/DinD 下自动注册的前提」手动安装后，重新获取 Token 再在界面提交或在该目录下手动执行 `config.sh`。

**容器模式**（`runners.container_mode: true`）：注册成功后的「自动启动」会启动该 Runner 的**独立容器**（并通知容器内 Agent 启动 Runner 进程）。需先构建 Runner 镜像并配置 `volume_host_path` 等，见 [Docker 部署 - 容器模式](docker.md#容器模式runner-独立容器cs)。

## 注册结果与 GitHub 显示检查

- **注册结果**：仅使用添加时在表单中填写的 Token（从 GitHub 复制的短期 token）。每次在界面使用 Token 执行注册后，结果会写入该 runner 目录下的 `.registration_result.json`（成功或失败原因），并在列表与详情中显示。
- **GitHub 显示检查**（可选）：服务内建定时任务约每 5 分钟检查各 runner 是否已在 GitHub 的 Actions Runners 列表中显示。若需此功能，请在该 runner 安装目录下放置 `.github_check_token` 文件，内容为 PAT（需 scope：组织用 `admin:org`，仓库用 `repo`）。检查结果写入各 runner 目录的 `.github_status.json`，并在界面「注册 / GitHub」列与详情中展示。

## 一台机器多 Runner

每个 Runner 使用独立子目录（如 `runners/runner-1`、`runners/runner-2`），互不干扰，可同时运行多个 Runner 并行执行任务。

[← 返回文档索引](README.md)
