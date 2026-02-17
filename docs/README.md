# 文档索引

本目录为 Runner Fleet 的详细文档，主文档见 [README](../README.md)。

> 接口变更提示：探测失败信息现仅通过结构化 `probe` 返回，历史扁平字段 `probe_*` 已移除。详见 [开发与构建](development.md) 的 HTTP API 说明与示例。

| 文档 | 说明 |
|------|------|
| [配置说明](config.md) | `config.yaml` 各字段说明与示例 |
| [Docker 部署](docker.md) | 镜像构建、运行、挂载与 Make 目标 |
| [添加 Runner](adding-runner.md) | 从 GitHub 获取 Token、界面添加、多 Runner 同机部署 |
| [安全与校验](security.md) | 鉴权、路径安全、唯一性、Token 与敏感文件 |
| [开发与构建](development.md) | Go 构建、本地开发、API（health/version）、Makefile |

[← 返回项目首页](../README.md)
