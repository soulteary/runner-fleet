# Docker 部署

- **基础镜像**：运行时使用 **Ubuntu**（非 Alpine），避免 GitHub Runner 在 Alpine 下运行异常。
- **自动拉起 Runner**：服务启动约 15 秒后会自动启动所有「已注册但未在运行」的 Runner；定时任务每 5 分钟也会再次检查并拉起未运行的已注册 Runner，便于 DinD 或管理器重启后恢复。

## 使用已发布镜像（推荐）

从 GitHub Container Registry 拉取并运行，无需本地构建：

```bash
# 拉取（可选，run 时会自动拉取）
docker pull ghcr.io/soulteary/runner-fleet:main
```

## 运行容器（完整参数）

**必须挂载配置与 runners 目录**，否则配置无法持久化、Runner 无法安装与运行。端口需与 `config.yaml` 中 `server.port` 一致（默认 8080）。

### 基础运行

```bash
docker run -d --name runner-manager \
  -p 8080:8080 \
  -v $(pwd)/config.yaml:/app/config.yaml \
  -v $(pwd)/runners:/app/runners \
  ghcr.io/soulteary/runner-fleet:main
```

- **`-p 8080:8080`**：宿主机端口映射，保证能从本机访问管理界面。
- **`-v $(pwd)/config.yaml:/app/config.yaml`**：挂载配置文件，修改后重启容器即可生效；不挂载则使用镜像内默认配置，无法持久化。
- **`-v $(pwd)/runners:/app/runners`**：挂载 Runner 安装目录，Runner 二进制与注册信息都保存在此；不挂载则容器删除后所有 Runner 丢失。
- 镜像内工作目录为 `/app`，`-config` 默认为 `/app/config.yaml`。`config.yaml` 中 `runners.base_path` 需为 `/app/runners`（或与挂载路径一致）。

### 前台调试（带 -it）

```bash
docker run --rm -it -p 8080:8080 \
  -v $(pwd)/config.yaml:/app/config.yaml \
  -v $(pwd)/runners:/app/runners \
  ghcr.io/soulteary/runner-fleet:main
```

### 当 Workflow 需要 Docker 时（DinD 或挂载 Socket）

Runner 在容器内执行 `run.sh`，若 Job 中有 `docker build`、`docker run` 等步骤，**容器内必须能访问 Docker 守护进程**，可采用两种方式。

#### 方式一：挂载宿主机 Docker Socket（简单，与宿主机共享 Docker）

```bash
docker run -d --name runner-manager \
  -p 8080:8080 \
  -v $(pwd)/config.yaml:/app/config.yaml \
  -v $(pwd)/runners:/app/runners \
  -v /var/run/docker.sock:/var/run/docker.sock \
  ghcr.io/soulteary/runner-fleet:main
```

- 容器内 Runner 执行的 Job 会使用宿主机上的 Docker，与宿主机共享镜像和资源。
- 宿主机需已安装并运行 Docker。

#### 方式二：DinD（Docker-in-Docker，独立 Docker 守护进程）

在独立网络中先启动 DinD 容器，再启动 Runner 管理服务，并设置 `DOCKER_HOST` 指向 DinD：

```bash
# 1. 创建网络
docker network create runner-net

# 2. 启动 DinD 容器（无 TLS 时暴露 2375）
docker run -d --name runner-dind \
  --network runner-net \
  -e DOCKER_TLS_CERTDIR= \
  --privileged \
  docker:dind

# 3. 启动 Runner 管理服务，使用 DinD 的 Docker
docker run -d --name runner-manager \
  --network runner-net \
  -p 8080:8080 \
  -e DOCKER_HOST=tcp://runner-dind:2375 \
  -v $(pwd)/config.yaml:/app/config.yaml \
  -v $(pwd)/runners:/app/runners \
  ghcr.io/soulteary/runner-fleet:main
```

- Runner 子进程会继承 `DOCKER_HOST`，Job 中的 `docker` 命令将使用 `runner-dind` 容器内的守护进程。
- DinD 需 `--privileged`，且与宿主机 Docker 隔离，适合希望 Job 与宿主机环境隔离的场景。
- **DinD 或管理器重启后**：runner-manager 启动后会延迟约 15 秒自动启动所有已注册的 Runner；之后每 5 分钟定时任务也会检查并拉起未在运行的已注册 Runner，无需手动点击「启动」。

#### Docker/DinD 下「自动注册」

本镜像**不包含** GitHub Actions runner 二进制，但**支持在 Web 界面一次完成安装与注册**：在「快速添加 Runner」中填写名称、目标、Token 并提交后，服务会先自动执行 `/app/scripts/install-runner.sh` 下载并解压 runner，再执行 `config.sh` 向 GitHub 注册并启动 Runner。无需事先手动安装。

若自动安装失败（例如网络不可达 GitHub releases），可改为手动安装后再在界面提交 Token，或手动执行注册：

1. **容器内执行安装脚本**（推荐）  
   ```bash
   docker exec runner-manager /app/scripts/install-runner.sh <名称> [版本号]
   ```
   若 `config.yaml` 中 `runners.base_path` 不是 `/app/runners`，可设置环境变量：
   ```bash
   docker exec -e RUNNERS_BASE_PATH=/app/runners runner-manager /app/scripts/install-runner.sh my-runner
   ```
2. **或在宿主机**创建 `runners/<名称>/` 并解压 [actions-runner Linux 包](https://github.com/actions/runner/releases)，再在界面填写名称、目标、Token 提交。

若已先在界面添加了 Runner（目录已创建但为空），可删除该 Runner 后重新在界面填写 Token 提交（会触发自动安装），或在目录内解压 runner 后到 GitHub 重新获取 Token，手动执行：
  ```bash
  ./config.sh --url https://github.com/<目标> --token <新TOKEN>
  ```

---

## 本地构建镜像

```bash
# 默认构建（VERSION=dev）
docker build -t runner-manager .

# 指定版本号
docker build --build-arg VERSION=1.0.0 -t runner-manager:1.0.0 .
```

使用 Makefile：`make docker-build`（使用 Makefile 中 `VERSION` 变量，默认 `dev`，可 `VERSION=1.0.0 make docker-build`）。

若使用 `make docker-build` 构建，镜像 tag 为 `runner-manager:$(VERSION)`，运行时可把上面命令中的 `ghcr.io/soulteary/runner-fleet:main` 改为 `runner-manager:dev`（或你传入的 VERSION）；或直接使用 `make docker-run`（使用当前目录的 `config.yaml` 与 `runners/`）。

## Make 目标

- `make docker-build`：构建镜像。
- `make docker-run`：先执行 `docker-stop`，再 `docker run` 启动容器（使用当前目录的 `config.yaml` 与 `runners/`）。
- `make docker-stop`：停止并删除同名容器。

模板已通过 `embed` 内嵌于二进制，镜像中无需附带 `templates/` 目录。

[← 返回文档索引](README.md)
