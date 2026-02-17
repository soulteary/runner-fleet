# Docker 部署

- **基础镜像**：运行时使用 **Ubuntu**（非 Alpine），避免 GitHub Runner 在 Alpine 下运行异常。镜像内已预装 .NET Core 6.0 所需依赖（libicu74、libkrb5-3、liblttng-ust1、libssl3、zlib1g），避免注册/运行 Runner 时报「Libicu's dependencies is missing for Dotnet Core 6.0」。
- **非 root 运行**：镜像内以 UID 1001（用户 `app`）运行，避免 GitHub Actions Runner 报错「Must not run with sudo」。挂载 `runners` 目录时，请确保宿主机上该目录对 UID 1001 可写（常见做法：`mkdir runners && chown 1001:1001 runners`）；若你自定义为 root 运行容器，需设置环境变量 `RUNNER_ALLOW_RUNASROOT=1`。
- **自动拉起 Runner**：服务启动约 15 秒后会自动启动所有「已注册但未在运行」的 Runner；定时任务每 5 分钟也会再次检查并拉起未运行的已注册 Runner，便于 DinD 或管理器重启后恢复。

## 使用已发布镜像（推荐）

从 GitHub Container Registry 拉取并运行，无需本地构建：

```bash
# 拉取（可选，run 时会自动拉取）
docker pull ghcr.io/soulteary/runner-fleet:main
```

## 使用 docker-compose 快速开始

仓库根目录提供 `docker-compose.yml`，包含 Manager + DinD，开箱可用：

```bash
# 1. 复制配置并修改 base_path
cp config.yaml.example config.yaml
# 编辑 config.yaml，将 runners.base_path 改为 /app/runners

# 2. 宿主机目录权限（Manager 以 UID 1001 运行）
chown 1001:1001 config.yaml
mkdir -p runners && chown 1001:1001 runners

# 3. 启动
docker compose up -d

# 管理界面：http://localhost:8080
```

若启用**容器模式**（每个 Runner 独立容器），还需：在 `config.yaml` 中取消注释 `container_mode`、`container_image` 等并设置 `volume_host_path` 为宿主机上 `runners` 的绝对路径。Runner 镜像与 Manager 同名，tag 带 `-runner` 后缀（如 `ghcr.io/<owner>/<repo>:main-runner`），或本地构建：`docker build -f Dockerfile.runner -t ghcr.io/soulteary/runner-fleet-runner:main .`。详见 [容器模式](#容器模式runner-独立容器cs)。

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
- **`-v $(pwd)/config.yaml:/app/config.yaml`**：挂载配置文件，修改后重启容器即可生效；不挂载则使用镜像内默认配置，无法持久化。镜像以 UID 1001 运行，**添加/删除/更新 Runner 时会写回该文件**，宿主机上请保证该文件对 UID 1001 可写（例如 `chown 1001:1001 config.yaml`），否则会报 500「保存配置失败」。
- **`-v $(pwd)/runners:/app/runners`**：挂载 Runner 安装目录，Runner 二进制与注册信息都保存在此；不挂载则容器删除后所有 Runner 丢失。镜像以 UID 1001 运行，宿主机上请保证该目录对 UID 1001 可写（例如 `chown 1001:1001 runners`）。若需界面「GitHub 显示」状态检查，请在各自 runner 子目录（如 `runners/xxx/`）下放置 `.github_check_token` 文件。
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

**DinD 模式下使用 docker/setup-qemu-action 等 Action**：本镜像已预装 **Docker CLI**（docker-ce-cli）。Runner 在 manager 容器内执行 Job 时，只要设置 `DOCKER_HOST`（如 docker-compose 中已配置 `DOCKER_HOST=tcp://runner-dind:2375`），即可在 PATH 中找到 `docker` 命令。因此 `docker/setup-qemu-action@v1`、`docker/build-push-action` 等依赖 `docker` 的 Action 在 DinD 模式下可正常使用，无需在 Job 中再安装 docker 客户端。

**持久化 DinD 镜像缓存**：若使用仓库内 `docker-compose.yml` 编排，已为 `runner-dind` 挂载卷 `dind-storage` 到 `/var/lib/docker`。Runner 拉取的 action 容器镜像会保存在该卷中，DinD 或 compose 重启后仍可复用，减少重复下载。

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

## 容器模式：Runner 独立容器（C/S）

当希望**每个 GitHub Runner 运行在独立容器中**、由 Manager 通过 C/S 方式启停并获取状态时，可启用**容器模式**。

### 机制简述

- **Manager**：通过宿主机 Docker（挂载 `docker.sock`）创建/启停 Runner 容器，并与 Runner 容器处于同一网络，通过 HTTP 访问容器内 **Agent** 获取 Runner 进程状态。
- **Runner 容器**：基于 `Dockerfile.runner` 构建的镜像，内含 **Runner Agent**（提供 `/status`、`/start`、`/stop`）和 Runner 运行依赖；Runner 安装目录由 Manager 挂载到容器内 `/runner`。
- **启停**：点击「启动」时 Manager 执行 `docker create`（若不存在）+ `docker start`，并请求 Agent 的 `POST /start` 启动 Runner 进程；「停止」时 `docker stop` 该容器。

### 配置与构建

1. **config.yaml 中启用并填写**（详见 `config.yaml.example`）：

   ```yaml
   runners:
     base_path: /app/runners
     container_mode: true
     container_image: ghcr.io/soulteary/runner-fleet-runner:main   # 或 CI：ghcr.io/<owner>/<repo>:main-runner
     container_network: runner-net
     agent_port: 8081
     dind_host: runner-dind
     volume_host_path: /abs/path/on/host/to/runners   # Manager 在容器内时必填
   ```

2. **Runner 镜像**：与 Manager 同镜像名，tag 带 `-runner`（如 `ghcr.io/<owner>/<repo>:main-runner`、`:1.0.0-runner`），或本地构建：

   ```bash
   docker build -f Dockerfile.runner -t ghcr.io/soulteary/runner-fleet-runner:main .
   ```

3. **部署要求**：
   - Manager 必须能执行 `docker` 且能创建与 Manager 同网络的容器，故需挂载**宿主机** `docker.sock` 并设置 `DOCKER_HOST=unix:///var/run/docker.sock`（仓库内 `docker-compose.yml` 已按此配置）。
   - Runner 容器与 Manager、DinD 在同一网络（如 `runner-net`），以便 Manager 访问 Agent、Runner Job 内访问 DinD。
   - **Runner 名称**会映射为容器名（如 `github-runner-<名称>`），仅保留字母、数字、`-`、`_`。若两个名称映射后相同（如 `a.b` 与 `a-b`）会冲突，请使用可区分名称。

### 与「本地进程」模式的区别

| 能力       | 本地进程模式           | 容器模式                         |
|------------|------------------------|----------------------------------|
| Runner 载体 | Manager 容器内子进程   | 独立容器，每 Runner 一容器      |
| 启停方式   | Manager 内 `run.sh`/pid | Docker 启停 + Agent `/start`     |
| 状态来源   | 本机 pid/目录          | 请求容器内 Agent `/status`      |
| Job 内 Docker | 同 Manager 的 DOCKER_HOST | 容器内 DOCKER_HOST 指向 DinD |

---

## 本地构建镜像

**Manager 镜像**（主程序）：

```bash
# 默认构建（VERSION=dev）
docker build -t runner-manager .

# 指定版本号
docker build --build-arg VERSION=1.0.0 -t runner-manager:1.0.0 .
```

**Runner 容器镜像**（仅容器模式需要）：

```bash
docker build -f Dockerfile.runner -t ghcr.io/soulteary/runner-fleet-runner:main .
```

使用 Makefile：`make docker-build`（构建 Manager 镜像；使用 Makefile 中 `VERSION` 变量，默认 `dev`，可 `VERSION=1.0.0 make docker-build`）。

若使用 `make docker-build` 构建，镜像 tag 为 `runner-manager:$(VERSION)`，运行时可把上面命令中的 `ghcr.io/soulteary/runner-fleet:main` 改为 `runner-manager:dev`（或你传入的 VERSION）；或直接使用 `make docker-run`（使用当前目录的 `config.yaml` 与 `runners/`）。

## Make 目标

- `make docker-build`：构建镜像。
- `make docker-run`：先执行 `docker-stop`，再 `docker run` 启动容器（使用当前目录的 `config.yaml` 与 `runners/`）。
- `make docker-stop`：停止并删除同名容器。

模板已通过 `embed` 内嵌于二进制，镜像中无需附带 `templates/` 目录。

[← 返回文档索引](README.md)
