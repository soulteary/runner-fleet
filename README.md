# Runner Fleet - GitHub Actions Runner 管理服务

基于 Golang Echo 的 HTTP 管理界面，在一台机器上查看和管理多个 GitHub Actions 自托管 Runner。使用 YAML 配置，无需数据库。

MIT 许可证，见 [LICENSE](LICENSE)。CI/镜像/Release 见 [.github/workflows](.github/workflows)。

## 亮点

- **零数据库**：仅用 YAML 配置，无外部依赖，配置即备份、易版本管理。
- **Web 一站式**：添加、注册、启停、编辑、查看状态均在界面完成，无需 SSH 或手动执行 GitHub Runner `config.sh`。
- **自动安装与注册**：在「快速添加」中填写 Token 即可自动下载 runner、执行注册并启动；支持从 GitHub 页面复制 `./config.sh --url ... --token ...` 一键解析并填充表单。
- **容器化优先**：Docker / docker-compose 开箱即用，支持 DinD 与 host-socket 两种 Job 内 Docker 方式；可选**容器模式**（一 Runner 一容器），由 Manager 统一启停与状态采集。
- **自愈与排障**：服务启动约 15 秒后自动拉起已注册未运行 Runner，并每 5 分钟定时检查；容器模式下 `status=unknown` 时提供结构化 probe（错误类型、检查/修复命令），便于复制命令排障或尝试启停自愈。
- **可观测**：注册结果写入并在界面展示；可选配置 PAT（`.github_check_token`），定时检查 Runner 是否已在 GitHub 列表显示，结果与界面同步。

## 功能

- **查看**：列表展示所有 Runner，状态（已安装/未注册/目录缺失）、是否运行中；支持查看单个 Runner 完整配置
- **编辑**：修改子路径、目标类型、目标、标签（名称不可改）
- **快速添加**：名称 + 目标（组织/仓库）+ 可选 Token，一键添加并可自动注册
- **删除**：从配置中移除（不删磁盘目录）
- **启停**：对已注册 Runner 启动/停止
- **容器模式**（可选）：每个 Runner 独立容器，Manager 通过 C/S 启停；Runner 镜像 tag 带 `-runner` 后缀

## 快速开始

```bash
cp config.yaml.example config.yaml
# 编辑 config.yaml：runners.base_path 改为 /app/runners
# 宿主机：mkdir -p runners && chown 1001:1001 config.yaml runners

docker network create runner-net 2>/dev/null || true
docker compose up -d
```

浏览器打开 http://localhost:8080。更多方式（docker run、DinD、容器模式）见 [使用指南](docs/guide.md)。健康检查：`GET /health`；版本：`GET /version`。

## 适用场景

- **个人 / 团队**：一台机器快速配置为多个 repo 或 org 提供自托管 Runner，用 Web 界面统一管理，无需记命令。
- **内网 CI**：在内网部署，Job 需要 Docker 时选用 DinD（隔离）或 host-socket（与宿主机共享）；Manager 或 DinD 重启后 Runner 自动恢复。
- **需要隔离与可追溯**：容器模式下每 Runner 独立容器，资源与权限边界清晰；结合注册结果与 GitHub 显示检查，便于核对 Runner 是否生效。

## 文档

- **[使用指南](docs/guide.md)**：部署（Docker/docker-compose）、配置、添加 Runner、安全与排障
- **[开发与构建](docs/development.md)**：Go 构建、本地调试、HTTP API、Makefile
