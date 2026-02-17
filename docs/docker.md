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

仓库根目录提供 `docker-compose.yml`。**DinD 为可选**：仅当容器模式下 Job 需要 Docker 且使用 `job_docker_backend: dind` 时再启用 DinD。

```bash
# 1. 复制配置并修改 base_path
cp config.yaml.example config.yaml
# 编辑 config.yaml，将 runners.base_path 改为 /app/runners

# 2. 宿主机目录权限（Manager 以 UID 1001 运行）
chown 1001:1001 config.yaml
mkdir -p runners && chown 1001:1001 runners

# 3. 创建网络（仅首次；compose 中 runner-net 为 external，避免 down 时删网导致已注册 Runner 容器无法启动）
docker network create runner-net 2>/dev/null || true

# 4. 启动（仅 Manager，适用于 job_docker_backend: host-socket 或 none）
docker compose up -d

# 若 config.yaml 中 job_docker_backend: dind，需同时启动 DinD：
docker compose --profile dind up -d

# 管理界面：http://localhost:8080
```

若启用**容器模式**（每个 Runner 独立容器），还需：在 `config.yaml` 中取消注释 `container_mode`、`container_image`、`job_docker_backend` 等并设置 `volume_host_path` 为宿主机上 `runners` 的绝对路径。Runner 镜像与 Manager 同名，tag 带 `-runner` 后缀（如 `ghcr.io/<owner>/<repo>:main-runner`），或本地构建：`docker build -f Dockerfile.runner -t ghcr.io/soulteary/runner-fleet-runner:main .`。详见 [容器模式](#容器模式runner-独立容器cs)。

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

**DinD 模式下使用 docker/setup-qemu-action 等 Action**：本镜像已预装 **Docker CLI**（docker-ce-cli）。Runner 在 manager 容器内执行 Job 时，只要设置 `DOCKER_HOST`（例如在 `.env` 中设为 `tcp://runner-dind:2375`），即可在 PATH 中找到 `docker` 命令。因此 `docker/setup-qemu-action@v1`、`docker/build-push-action` 等依赖 `docker` 的 Action 在 DinD 模式下可正常使用，无需在 Job 中再安装 docker 客户端。

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
- **Runner 容器**：基于 `Dockerfile.runner` 构建的镜像，内含 **Runner Agent**（提供 `/status`、`/start`、`/stop`）和 Runner 运行依赖；Agent 入口为 `/app/runner-agent`（故意不在 `/runner` 下，避免挂载 `-v host_path:/runner` 时覆盖入口）。Runner 安装目录由 Manager 挂载到容器内 `/runner`。
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
     job_docker_backend: dind   # dind | host-socket | none，默认 dind
     dind_host: runner-dind     # 仅 job_docker_backend=dind 时有效
     volume_host_path: /abs/path/on/host/to/runners   # Manager 在容器内时必填
   ```

2. **Runner 镜像**：与 Manager 同镜像名，tag 带 `-runner`（如 `ghcr.io/<owner>/<repo>:main-runner`、`:1.0.0-runner`），或本地构建：

   ```bash
   docker build -f Dockerfile.runner -t ghcr.io/soulteary/runner-fleet-runner:main .
   ```

   若曾用旧版镜像创建过 Runner 容器且出现 `stat /runner/runner-agent: no such file or directory`，需先删除该容器（如 `docker rm -f github-runner-<名称>`），再在界面点击「启动」由 Manager 用新镜像重新创建。

3. **部署要求**：
   - **Manager 必须使用宿主机 Docker**（`unix:///var/run/docker.sock`）创建/启停 Runner 容器，**不能**把 `DOCKER_HOST` 设为 DinD（`tcp://runner-dind:2375`）。DinD 仅供 **Runner 容器内** Job 的 `docker build` 等使用；若在 `.env` 中设置了 `DOCKER_HOST=tcp://runner-dind:2375`，点击「启动」会报错，请移除或注释该行。宿主机需挂载 `docker.sock` 并设置 `DOCKER_HOST=unix:///var/run/docker.sock`（仓库内 `docker-compose.yml` 默认已配置）。Manager 以非 root（UID 1001）运行，访问 socket 需加入宿主机 **docker 组**：`group_add: [ "${DOCKER_GID:-999}" ]`，GID 不对时在 `.env` 设 `DOCKER_GID=`；或改为 `user: "0:0"` 以 root 运行。
   - Runner 容器与 Manager、DinD 在同一网络（如 `runner-net`），以便 Manager 访问 Agent、Runner Job 内访问 DinD。
   - **Runner 名称**会映射为容器名（如 `github-runner-<名称>`），仅保留字母、数字、`-`、`_`。若两个名称映射后相同（如 `a.b` 与 `a-b`）会冲突，请使用可区分名称。
   - **项目/目标与多实例**：每个配置项（`runners.items` 中一项）对应一个 Runner 容器；同一 target（org 或 repo）下可配置多个 Runner（多实例），由 GitHub 调度 Job，无内置并发上限。

### 与「本地进程」模式的区别

| 能力       | 本地进程模式           | 容器模式                         |
|------------|------------------------|----------------------------------|
| Runner 载体 | Manager 容器内子进程   | 独立容器，每 Runner 一容器      |
| 启停方式   | Manager 内 `run.sh`/pid | Docker 启停 + Agent `/start`     |
| 状态来源   | 本机 pid/目录          | 请求容器内 Agent `/status`      |
| Job 内 Docker | 同 Manager 的 DOCKER_HOST | 由 job_docker_backend 决定：dind / host-socket / none |

---

## 排障与迁移

### docker compose down 后 Runner 容器无法启动（状态为 Created）

原因：`docker compose down` 会删除 compose 创建的网络 `runner-net`，而由 Manager 动态创建的 Runner 容器不在 compose 中，不会被删除，仍引用已删除的网络，导致无法启动。

**预防**：仓库内 `docker-compose.yml` 已将 `runner-net` 设为 **external**，`compose down` 不会删除该网络。首次使用前执行一次：`docker network create runner-net`。

**已出现问题时**：在 Web 界面点击该 Runner 的「启动」即可——Manager 会检测到 `docker start` 因网络失效而失败，自动删除旧容器并用当前配置重新创建并启动，无需手动 `docker rm`。若仍失败，可手动删除后再点「启动」：`docker rm -f github-runner-<名称>`。无需删除 `runners/` 下对应目录或重新注册。

### root / 非 root、RUNNER_ALLOW_RUNASROOT

- **推荐**：Manager 与 Runner 容器均以非 root（如 UID 1001）运行，避免 GitHub Runner 报「Must not run with sudo」。
- 挂载目录需对运行用户可写：`chown 1001:1001 config.yaml runners`。
- 若必须用 root 运行（例如 `user: "0:0"`），需在 Runner 容器或注册脚本执行环境中设置 **RUNNER_ALLOW_RUNASROOT=1**（仅影响注册/运行 Runner 进程，不影响 Manager 自身）。

### 旧容器重建

若曾用旧版 Runner 镜像创建过容器，出现 `stat /runner/runner-agent: no such file or directory` 或镜像结构变更时：

1. 停止并删除该 Runner 容器：`docker stop github-runner-<名称> && docker rm github-runner-<名称>`。
2. 在 Web 界面点击该 Runner 的「启动」，由 Manager 用当前 `container_image` 重新创建并启动容器。
3. 无需删除 runner 目录或重新注册，配置与注册信息仍在 `volume_host_path` 下。

### 从旧配置迁移到 Job Docker 后端

- **默认行为**：未配置 `job_docker_backend` 时视为 `dind`（与旧版一致）。
- **回滚**：将 `job_docker_backend` 设为 `dind` 并确保 DinD 已启动（`docker compose --profile dind up -d`）。
- **可选**：改为 `host-socket` 时需在创建 Runner 容器时挂载宿主机 socket，Compose 已为 Manager 挂载；Runner 容器由 Manager 按 `host-socket` 自动注入挂载与 `DOCKER_HOST`。改为 `none` 时 Job 内无法使用 Docker。

### 状态 `unknown` 的处理（容器模式）

当列表中 Runner 显示 `status=unknown` 时，表示 Manager 对该 Runner 的状态探测失败。常见原因包括：

- Manager 无法访问 Docker（权限、socket、daemon）。
- Manager 无法连通 Runner 容器内 Agent（网络或容器未就绪）。
- Agent 返回非 200（Runner 进程异常、容器内依赖异常）。

排查建议：

1. 在 WebUI 详情弹窗查看 `probe` 字段（`error/type/suggestion/check_command/fix_command`）。
2. 先执行 `probe.check_command`（只读）确认问题边界。
3. 再按需执行 `probe.fix_command`（有副作用）。
4. 在 `status=unknown` 时可直接尝试 UI 的「启动 / 停止」按钮进行自愈；接口会继续尝试执行启停动作。

### 最小验收清单

- [ ] WebUI 可新增一个项目下多个 Runner，并能独立启动/停止。
- [ ] 每个 Runner 以独立容器运行时，状态在 UI 正确反映（含「Docker 后端」列）。
- [ ] `job_docker_backend: dind` 时，至少完成一次含 Docker 的 workflow（需先 `docker compose --profile dind up -d`）。
- [ ] `job_docker_backend: host-socket` 时，至少完成一次含 Docker 的 workflow。
- [ ] Manager 重启后，已注册 Runner 可自动恢复拉起，无旧容器配置污染。

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
