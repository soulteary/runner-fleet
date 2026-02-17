# Docker 部署

## 构建镜像

```bash
# 默认构建（VERSION=dev）
docker build -t runner-manager .

# 指定版本号
docker build --build-arg VERSION=1.0.0 -t runner-manager:1.0.0 .
```

使用 Makefile：`make docker-build`（使用 Makefile 中 `VERSION` 变量，默认 `dev`，可 `VERSION=1.0.0 make docker-build`）。

## 运行容器

需挂载配置与 runners 目录，以便持久化并管理 runner：

```bash
docker run -d --name runner-manager \
  -p 8080:8080 \
  -v $(pwd)/config.yaml:/app/config.yaml \
  -v $(pwd)/runners:/app/runners \
  runner-manager
```

- 镜像内工作目录为 `/app`，`-config` 默认为 `config.yaml`（即 `/app/config.yaml`）。
- `runners.base_path` 需与挂载路径一致，例如 `/app/runners`；若使用默认 `./runners`，则相对容器内 `/app`，即 `/app/runners`，与上述挂载一致。
- 若使用 `make docker-build` 构建，镜像 tag 为 `runner-manager:$(VERSION)`，建议用 `make docker-run` 启动；或手动运行上述 `docker run` 时把镜像名改为 `runner-manager:dev`（或你传入的 VERSION）。

## Make 目标

- `make docker-build`：构建镜像。
- `make docker-run`：先执行 `docker-stop`，再 `docker run` 启动容器（使用当前目录的 `config.yaml` 与 `runners/`）。
- `make docker-stop`：停止并删除同名容器。

模板已通过 `embed` 内嵌于二进制，镜像中无需附带 `templates/` 目录。

[← 返回文档索引](README.md)
