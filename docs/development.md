# 开发与构建

生产环境请使用容器部署，见 [使用指南](guide.md)。本文档面向贡献者本地构建与调试。

## 环境要求

- Go 1.26（与 [go.mod](../go.mod) 一致）。

## 构建

```bash
# 生成可执行文件 runner-manager
go build -o runner-manager ./cmd/runner-manager

# 注入版本号（便于 /version 与排障）
go build -ldflags "-X main.Version=1.0.0" -o runner-manager ./cmd/runner-manager

# 仅构建 Runner Agent（容器模式用）
go build -o runner-agent ./cmd/runner-agent

# 或使用 Make：make build / make build-agent / make build-all
```

模板已通过 `embed` 内嵌于 Manager 二进制（`cmd/runner-manager/templates/`），可执行文件可单文件分发，无需附带 `templates/` 目录。

## 本地开发与调试

```bash
cp config.yaml.example config.yaml
go run ./cmd/runner-manager
# 或 make run（先 build 再运行）；指定配置：./runner-manager -config /path/to/config.yaml
```

默认监听 `:8080`，http://localhost:8080。Basic Auth 调试：`BASIC_AUTH_PASSWORD=密码 go run ./cmd/runner-manager`，见 [使用指南 - 安全与校验](guide.md#四安全与校验)。

## 命令行参数

- `-config <path>`：配置文件路径。
- `-version`：输出版本号后退出（构建时可通过 `-ldflags "-X main.Version=..."` 注入）。

## HTTP API

启用 Basic Auth 时，除 `/health` 外，请求需在 Header 中携带 `Authorization: Basic <base64(user:password)>`。

| 路径 | 方法 | 说明 |
|------|------|------|
| `/health` | GET | 返回 `{"status":"ok"}`，可用于 Ingress/K8s 探针；始终免鉴权。 |
| `/version` | GET | 返回 `{"version":"..."}`。 |
| `/api/runners` | GET | 返回 Runner 列表。容器模式下若状态探测失败，会返回 `status=unknown` 且带结构化 `probe`（含 `error/type/suggestion/check_command/fix_command`）。 |
| `/api/runners/:name` | GET | 返回单个 Runner 详情。容器模式下若状态探测失败，同样返回结构化 `probe`。 |
| `/api/runners/:name/start` | POST | 启动指定 Runner。容器模式下若状态探测失败，仍会尝试启动，并在响应中返回结构化 `probe`。 |
| `/api/runners/:name/stop` | POST | 停止指定 Runner。容器模式下若状态探测失败，仍会尝试停止，并在响应中返回结构化 `probe`。 |

### 升级注意（破坏性变更）

历史扁平字段 `probe_*` 已移除，请统一使用 `probe` 对象：`probe.error`、`probe.type`、`probe.suggestion`、`probe.check_command`、`probe.fix_command`。`probe.type` 可能值：`docker-access`、`agent-http`、`agent-connect`、`unknown`。WebUI 在 `status=unknown` 时仍可「启动/停止」自愈。

示例（探测失败）：

```json
{
  "name": "runner-a",
  "status": "unknown",
  "probe": {
    "error": "agent 返回 502: bad gateway",
    "type": "agent-http",
    "suggestion": "查看 runner 容器日志，确认 Agent 与 /runner 下脚本进程状态",
    "check_command": "docker ps -a | rg \"github-runner-\" && docker logs --tail=200 <runner_container_name>",
    "fix_command": "docker restart <runner_container_name>"
  }
}
```

## Makefile 目标

- `make help`：查看全部目标。
- `make build`：构建 Manager（带 Version ldflags）。
- `make build-agent`：构建 Runner Agent（容器模式用）。
- `make build-all`：同时构建 Manager 与 Agent。
- `make test`：运行测试。
- `make run`：先 build 再运行 Manager。
- `make docker-build` / `make docker-run` / `make docker-stop`：Manager 镜像构建与运行，见 [使用指南](guide.md)。
- `make docker-build-runner`：构建容器模式用的 Runner 镜像（`Dockerfile.runner`，默认 tag 见 `RUNNER_IMAGE`）。
- `make clean`：删除生成的二进制（runner-manager、runner-agent）。

容器模式用的 Agent 为 `cmd/runner-agent`，Runner 镜像用 `Dockerfile.runner` 单独构建。

[← 返回文档](README.md)
