# 配置说明

首次使用可将项目根目录下的 `config.yaml.example` 复制为 `config.yaml`，再按需修改。

```bash
cp config.yaml.example config.yaml
```

## 字段说明

| 字段 | 说明 | 默认 |
|------|------|------|
| `server.port` | HTTP 服务端口 | `8080` |
| `server.addr` | 监听地址；空则仅绑定端口（等价所有接口） | 空 |
| `runners.base_path` | 所有 Runner 安装目录的根路径；Docker 部署时请设为 `/app/runners`（与卷挂载一致） | `./runners` |
| `runners.items` | 预置的 Runner 列表 | 也可通过 Web 界面添加 |
| `runners.container_mode` | 是否启用容器模式：Runner 运行在独立容器中，Manager 通过 Docker 启停并通过容器内 Agent 获取状态 | `false` |
| `runners.container_image` | 容器模式下 Runner 容器镜像；使用本仓库 CI 时为 `ghcr.io/<owner>/<repo>:main-runner`（与 Manager 同镜像名，tag 带 -runner） | `ghcr.io/soulteary/runner-fleet-runner:main` |
| `runners.container_network` | 容器模式下 Runner 所在网络（需与 Manager 同网） | `runner-net` |
| `runners.agent_port` | 容器内 Agent HTTP 端口 | `8081` |
| `runners.job_docker_backend` | Job 内 Docker 后端：`dind`（DinD 服务）、`host-socket`（挂载宿主机 socket）、`none`（不提供） | `dind` |
| `runners.dind_host` | 仅 `job_docker_backend=dind` 时有效，DinD 主机名 | `runner-dind` |
| `runners.volume_host_path` | 容器模式下宿主机上 runners 根目录的绝对路径（Manager 在容器内时必填，供 `docker create -v` 使用） | 空 |

**配置校验**：`runners.items` 中不得存在同名 `name`；容器模式下还会校验名称映射后的容器名冲突（如 `a.b` 与 `a-b`）。`runners.job_docker_backend` 仅允许 `dind` / `host-socket` / `none`。`container_mode=false` 时 `job_docker_backend` 必须为 `dind` 且不能设置 `volume_host_path`；容器模式且 `base_path` 为容器内路径（如 `/app/runners`）时必须设置 `volume_host_path` 且为绝对路径。

**迁移**：未配置 `job_docker_backend` 时默认为 `dind`；改为 `host-socket` 或 `none` 后需在 Web 界面或 API 重新启动 Runner 容器以应用新后端。

「GitHub 显示检查」所需的 token 仅在各 runner 目录下配置，见 [添加 Runner](adding-runner.md) 中的说明。

## 示例

```yaml
server:
    port: 8080
    addr: 0.0.0.0
runners:
    base_path: ./runners
    items: []
```

[← 返回文档索引](README.md)
