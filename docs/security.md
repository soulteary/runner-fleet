# 安全与校验

## 鉴权

默认未做登录鉴权，请勿将服务暴露到公网，建议仅在内网或本机使用。

可通过**环境变量**启用 HTTP Basic Auth：

- **BASIC_AUTH_PASSWORD**：设置后启用 Basic Auth，留空则不鉴权。
- **BASIC_AUTH_USER**：可选，默认 `admin`。

启用后，除 `GET /health` 外，所有管理界面与 API 请求需携带正确的用户名与密码；`/health` 始终免鉴权，便于 Docker/编排器健康检查。
建议：使用强密码、生产环境配合 HTTPS（如反向代理 TLS）。**BASIC_AUTH_PASSWORD 为敏感信息**，请勿提交到仓库（可放入 `.env`，并确保 `.env` 已加入 `.gitignore`）。环境变量用法见 [Docker 部署](docker.md)。

## 路径安全

- Runner 的 **name**、**path** 禁止包含 `..`、`/`、`\`（添加时校验；查询/启动/停止/更新/删除时也会校验 URL 中的 name 参数）。
- 创建目录时强制落在 `runners.base_path` 之下，防止路径穿越。

## 唯一性

- **添加**：禁止与已有 Runner 同名。
- **编辑**：名称不可修改，与磁盘目录名一致。
- **容器模式**：Runner 名称会被规范化为容器名（前缀 `github-runner-`，并过滤特殊字符）；若两个名称映射后冲突（如 `a.b` 与 `a-b`），配置加载会直接报错并拒绝启动。

## 配置一致性校验

- `runners.job_docker_backend` 仅允许 `dind` / `host-socket` / `none`。
- `container_mode=false` 时，`job_docker_backend` 必须为 `dind`，且不能设置 `volume_host_path`。
- `container_mode=true` 且 `base_path` 为容器内路径（如 `/app/runners`）时，必须设置 `volume_host_path`，且需为宿主机绝对路径。

## Token 与敏感文件

- **config.yaml**：已列入 `.gitignore`，若含敏感信息请勿提交到仓库。
- **.env**：若含 `BASIC_AUTH_PASSWORD` 等敏感信息请勿提交；`.gitignore` 已包含 `.env`。
- **各 runner 目录下的 `.github_check_token`**：用于该 runner 的「GitHub 显示检查」，内容为 PAT。建议限制文件权限（如 `chmod 600`），若 `runners/` 被纳入版本库，请在 `.gitignore` 中加入 `**/.github_check_token` 避免泄露。

[← 返回文档索引](README.md)
