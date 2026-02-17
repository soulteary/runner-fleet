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
| `runners.dind_host` | 容器内 DOCKER_HOST 主机名（Job 内 Docker 连 DinD） | `runner-dind` |
| `runners.volume_host_path` | 容器模式下宿主机上 runners 根目录的绝对路径（Manager 在容器内时必填，供 `docker create -v` 使用） | 空 |

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
