# 开发与构建

## 环境要求

- Go 1.26（与 [go.mod](../go.mod) 一致）。

## 构建

```bash
# 生成可执行文件 runner-manager
go build -o runner-manager .

# 注入版本号（便于 /version 与排障）
go build -ldflags "-X main.Version=1.0.0" -o runner-manager .
```

模板已通过 `embed` 内嵌，可执行文件可单文件分发，无需附带 `templates/` 目录。

## 本地开发

```bash
# 需先有 config.yaml（可 cp config.yaml.example config.yaml）
go run .

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

## Makefile 目标

- `make help`：查看全部目标。
- `make build`：构建（带 Version ldflags）。
- `make test`：运行测试。
- `make run`：先 build 再运行当前目录二进制。
- `make docker-build` / `make docker-run` / `make docker-stop`：Docker 构建与运行，见 [Docker 部署](docker.md)。
- `make clean`：删除生成的二进制。

[← 返回文档索引](README.md)
