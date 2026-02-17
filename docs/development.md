# 开发与构建

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

## 本地开发

```bash
# 需先有 config.yaml（可 cp config.yaml.example config.yaml）
go run ./cmd/runner-manager

# 或：先 build 再运行
make run
```

## 运行

```bash
# 使用当前目录 config.yaml
./runner-manager

# 指定配置文件
./runner-manager -config /path/to/config.yaml
```

默认监听 `:8080`，浏览器打开 http://localhost:8080 使用管理界面。

## 命令行参数

- `-config <path>`：配置文件路径。
- `-version`：输出版本号后退出（构建时可通过 `-ldflags "-X main.Version=..."` 注入）。

## HTTP API

| 路径 | 方法 | 说明 |
|------|------|------|
| `/health` | GET | 返回 `{"status":"ok"}`，可用于 Ingress/K8s 探针。 |
| `/version` | GET | 返回 `{"version":"..."}`。 |
| `/api/runners` | GET | 返回 Runner 列表。容器模式下若状态探测失败，会返回 `status=unknown` 且带 `probe_error`。 |
| `/api/runners/:name` | GET | 返回单个 Runner 详情。容器模式下若状态探测失败，同样返回 `probe_error` 便于排障。 |
| `/api/runners/:name/start` | POST | 启动指定 Runner。容器模式下若状态探测失败，仍会尝试启动，并在响应中返回 `probe_error`。 |
| `/api/runners/:name/stop` | POST | 停止指定 Runner。容器模式下若状态探测失败，仍会尝试停止，并在响应中返回 `probe_error`。 |

## Makefile 目标

- `make help`：查看全部目标。
- `make build`：构建 Manager（带 Version ldflags）。
- `make build-agent`：构建 Runner Agent（容器模式用）。
- `make build-all`：同时构建 Manager 与 Agent。
- `make test`：运行测试。
- `make run`：先 build 再运行 Manager。
- `make docker-build` / `make docker-run` / `make docker-stop`：Manager 镜像构建与运行，见 [Docker 部署](docker.md)。
- `make docker-build-runner`：构建容器模式用的 Runner 镜像（`Dockerfile.runner`，默认 tag 见 `RUNNER_IMAGE`）。
- `make clean`：删除生成的二进制（runner-manager、runner-agent）。

容器模式下 Runner 容器内运行的是 `cmd/runner-agent` 编译出的 Agent，仅构建 Manager 时不会包含该二进制；Runner 镜像单独用 `Dockerfile.runner` 构建。

[← 返回文档索引](README.md)
