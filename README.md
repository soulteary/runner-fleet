# Runner Fleet - GitHub Actions Runner 管理服务

基于 Golang Echo 的 HTTP 管理界面，在一台机器上查看和管理多个 GitHub Actions 自托管 Runner。使用 YAML 配置，无需数据库。

许可证：MIT，见 [LICENSE](LICENSE)。  
CI：push 到 `main`/`master` 时通过 [GitHub Actions](.github/workflows/build.yml) 自动执行 vet、测试与构建。

## 功能

- **查看**：列表展示所有 Runner，状态（已安装/未注册/目录缺失）、是否运行中；支持查看单个 Runner 完整配置
- **编辑**：修改子路径、目标类型、目标、标签（名称不可改）
- **快速添加**：名称 + 目标（组织/仓库）+ 可选 Token，一键添加并可自动注册
- **删除**：从配置中移除（不删磁盘目录）
- **启停**：对已注册 Runner 启动/停止

## 快速开始

```bash
# 1. 复制配置（config.yaml 已在 .gitignore，需从示例复制）
cp config.yaml.example config.yaml

# 2. 二选一：本地运行 或 Docker
go run .                    # 需 Go 1.26
# 或
make docker-build && make docker-run
```

浏览器打开 http://localhost:8080。健康检查：`GET /health`；版本：`GET /version` 或 `./runner-manager -version`。

## 文档

详细说明见 [docs/](docs/README.md)：

| 文档 | 说明 |
|------|------|
| [配置说明](docs/config.md) | config.yaml 字段与示例 |
| [Docker 部署](docs/docker.md) | 镜像构建、运行、Make 目标 |
| [添加 Runner](docs/adding-runner.md) | Token 获取、界面添加、多 Runner 同机 |
| [安全与校验](docs/security.md) | 鉴权、路径安全、唯一性 |
| [开发与构建](docs/development.md) | 构建、开发、API、Makefile |
