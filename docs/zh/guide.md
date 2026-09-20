# 使用指南

**文档 / Docs:** [EN](../guide.md) · 中文 · [Français](../fr/guide.md) · [Deutsch](../de/guide.md) · [한국어](../ko/guide.md) · [日本語](../ja/guide.md)

![](../../.github/assets/fleet.jpg)

部署、配置、添加 Runner 与安全说明合并于此。面向贡献者的构建与 API 见 [开发与构建](development.md)。

---

## 一、部署（Docker）

- 镜像基于 **Ubuntu**，预装 .NET Core 6.0 依赖；以 **UID 1001** 运行，宿主机挂载目录需对该用户可写（如 `chown 1001:1001 config runners`）。
- 启动约 15 秒后自动拉起已注册未运行的 Runner，并每 5 分钟定时检查。

### 使用已发布镜像（推荐）

生产环境建议使用具体版本号（如 v1.5.0）；开发可用 `main` tag。

```bash
docker pull ghcr.io/soulteary/runner-fleet:v1.5.0
```

### docker-compose 快速开始

仓库根目录有 `docker-compose.yml`。仅当容器模式且 Job 需要 Docker 并配置 `job_docker_backend: dind` 时再启用 DinD。

```bash
mkdir -p config && cp config.yaml.example config/config.yaml
# 编辑 config/config.yaml：runners.base_path 改为 /app/runners

chown 1001:1001 config runners
mkdir -p runners && chown 1001:1001 runners

docker network create runner-net 2>/dev/null || true
docker compose up -d
# 若 job_docker_backend: dind，则：docker compose --profile dind up -d
```

管理界面：http://localhost:8080。鉴权见 [四、安全与校验](#四安全与校验)。

### 运行容器（完整参数）

必须挂载 `config` 目录与 `runners`（仅挂载目录，勿单独挂载 config/config.yaml，否则宿主机无该文件时 Docker 会创建空文件导致启动失败）；端口与配置中 `server.port` 一致（默认 8080）。

```bash
docker run -d --name runner-manager \
  -p 8080:8080 \
  -v $(pwd)/config:/app/config \
  -v $(pwd)/runners:/app/runners \
  ghcr.io/soulteary/runner-fleet:v1.5.0
```

宿主机目录需对 UID 1001 可写。Basic Auth：`-e BASIC_AUTH_PASSWORD=密码`、`-e BASIC_AUTH_USER=admin`。Job 需要 Docker 时可加 `-v /var/run/docker.sock:/var/run/docker.sock`；镜像内已预置 GID 999 的 `docker` 组（构建参数 `DOCKER_GID` 可改），宿主机 docker GID 不是 999 时还需加 `--group-add $(getent group docker | cut -d: -f3)`，或使用 DinD（见仓库 `docker-compose.yml` 的 `--profile dind`）。两个镜像除 Docker CLI 外，还带有一层与 GitHub 托管 runner 对齐的命令行基础层：`scripts/apt-packages.txt` 取自 `actions/runner-images` 的 `toolset-2404.json`，`git`、`unzip`、`jq`、`rsync`、`sudo`、`xvfb` 等都在其中。语言与平台 SDK 有意不含——用 `setup-*` action 安装，或自行扩展镜像。与托管 runner 一致，两个镜像都为 Job 用户配置了免密 `sudo`，因此 `sudo apt-get install -y …` 可直接使用；需要更严格的边界时以 `--build-arg ALLOW_SUDO=false` 构建。

### 自动安装与注册

界面「快速添加 Runner」填写名称、目标、Token 提交后，会先执行安装脚本再注册并启动。失败时可：

```bash
docker exec runner-manager /app/scripts/install-runner.sh <名称> [版本号]
```

脚本会按 `uname -m` 选择架构，未指定版本时向 GitHub API 查询最新版，并且**任何版本都强制校验 SHA-256**。取不到官方哈希时（离线或走镜像源）需显式传入：`RUNNER_SHA256=<sha256> ... install-runner.sh <名称> <版本号>`。目录下已有 runner 时会跳过下载，需重装请设置 `RUNNER_FORCE_REINSTALL=1`。

或宿主机在 `runners/<名称>/` 解压 [actions-runner](https://github.com/actions/runner/releases) 后再在界面提交或手动 `./config.sh`。

### 容器模式（Runner 独立容器）

每个 Runner 运行在独立容器中，Manager 通过宿主机 Docker 启停，经 HTTP 访问容器内 Agent 获取状态。

**方式一：仅用 .env（推荐全容器时使用）**
无需改 config.yaml，复制 `cp .env.example .env` 后设置例如：`CONTAINER_MODE=true`、`VOLUME_HOST_PATH=<宿主机 runners 绝对路径>`（如 `realpath runners`）、`JOB_DOCKER_BACKEND=host-socket`、`CONTAINER_NETWORK=runner-net`。若未准备 `config/config.yaml`，只要在 `.env` 中配置了上述变量，首次启动时会自动生成该文件。不设 `RUNNER_IMAGE` 时 Runner 镜像会从 `MANAGER_IMAGE` 自动推导（如 `v1.5.0` → `v1.5.0-runner`）。挂载的 `config` 与 `runners` 目录仍需 `chown 1001:1001`。详见 `.env.example` 中「覆盖 config.yaml」相关变量。

**方式二：在 config/config.yaml 中启用**（见 `config.yaml.example`）：

```yaml
runners:
  base_path: /app/runners
  container_mode: true
  container_image: ghcr.io/soulteary/runner-fleet:v1.5.0-runner
  container_network: runner-net
  agent_port: 8081
  job_docker_backend: dind   # dind | host-socket | none
  dind_host: runner-dind
  volume_host_path: /abs/path/on/host/to/runners
```

Runner 镜像：同 Manager 镜像名、tag 带 `-runner`（生产建议用版本号如 v1.5.0-runner，开发可用 main-runner），或本地 `docker build -f Dockerfile.runner -t ghcr.io/soulteary/runner-fleet:v1.5.0-runner .`。Manager 必须用宿主机 Docker（挂载 `docker.sock`），不可把 `DOCKER_HOST` 设为 DinD；Compose 中需 `group_add` 宿主机 docker GID 或 `user: "0:0"`。`job_docker_backend: host-socket` 时，Manager 会给 Runner 容器追加 `--group-add <宿主机 docker GID>`（自动探测 `docker.sock`，可用 `runners.docker_gid` / `DOCKER_GID` 覆盖）；镜像内也预置了 `docker` 组（构建参数 `DOCKER_GID`，默认 999）。Runner 名称会规范为容器名，映射后重名会冲突。

**扩展 Runner 镜像**：GitHub 托管 runner 预装了 Android SDK、Node、Python 等工具链，自托管不会。为托管 runner 写的 workflow 常隐式依赖这些，迁过来后会报 `SDK location not found`、`node: command not found` 之类。做法是在本仓库 Runner 镜像之上叠加自己的工具链——可直接使用的示例，以及四条关键规则（装到 /opt 的工具要 chown 给 UID 1001、环境变量写进镜像、免密 sudo 会继承、预热放在 `USER app` 之后）见 [`examples/runner-images/`](../../examples/runner-images/)。用 `items[].container_image` 只让某个 Runner 使用它，workflow 里靠 label 精确选中。

**改了配置与容器重建**：镜像、网络、挂载目录、Job 内 Docker 后端都只在 `docker create` 时定下来，已存在的容器沿用创建时的参数，光改配置碰不到它。Manager 会把每个容器的实际创建参数与当前配置对一遍：**已停止**的容器若对不上，在下一次启动它时（手动点「启动」或 Manager 自动拉起）会删掉重建；列表里该 Runner 会标出「配置已变更」，鼠标悬停可看到具体差异（如 `job_docker_backend: → dind`）。**正在运行**的容器不会被自动重建——上面可能正跑着 Job——需要立刻生效就点该行的「重建容器」（`POST /api/runners/:name/recreate`，会中断正在跑的 Job），或等它空闲后停止再启动。同名 tag 重新构建镜像同样算：比对的是镜像 ID，不只是引用。

**现成的部署示例**：[`examples/deploy/`](../../examples/deploy/) 提供两套可直接复制的配置——`standalone/`（单容器：Manager 与 Runner 进程同处一个容器，`docker run` 或 Compose 均可）与 `fleet/`（容器模式：每个 Runner 一个容器，镜像缓存靠共用宿主机 daemon 共享，工具链与 Action 缓存预置在 Runner 镜像的层里，构建缓存按 Runner 隔离）。其 README 对比了两者的取舍、说明哪些缓存共享哪些隔离，并收录了部署中最容易踩的坑（目录属主、`VOLUME_HOST_PATH`、host-socket 下的磁盘增长）。

### 排障

- **哪里不对先看启动自检**：`docker compose logs runner-manager | grep 自检`。Manager 启动时会检查 runners 目录、Docker 可达性、容器网络、Runner 镜像与 Job 内 Docker 后端，失败项会直接给出可照做的修复命令。
- **compose down 后 Runner 无法启动**：首次执行 `docker network create runner-net`。已出问题时界面点该 Runner「启动」重建，或 `docker rm -f github-runner-<名称>` 后再点「启动」。
- **root 运行**：挂载目录对运行用户可写；若用 root，需设 `RUNNER_ALLOW_RUNASROOT=1`。
- **Job 中访问 docker.sock 报 `permission denied`**：`job_docker_backend: host-socket` 时，容器内用户（UID 1001）需在 socket 所属组内。Manager 创建容器时会按探测到的宿主机 docker GID 追加 `--group-add`；GID 对不上的容器会被判为「配置已变更」，下次启动时自动重建（正在运行的则标出徽标，点「重建容器」立即生效）。探测不到时可设置 `runners.docker_gid`（或 `.env` 中 `DOCKER_GID`）为 `getent group docker | cut -d: -f3` 的值。
- **Job 中 `command not found` 或缺少某个 SDK**：自托管 runner 不像 GitHub 托管的那样预装工具链。先看启动自检（`docker compose logs runner-manager | grep 自检`），它会指出配置中每个 Runner 镜像缺少 `git`/`unzip`/`tar`/`curl` 中的哪些。语言与平台 SDK 需自行扩展镜像，见 [`examples/runner-images/`](../../examples/runner-images/)。
- **旧 Runner 镜像**：拉取或重新构建后直接启动该 Runner 即可——Manager 会发现镜像变了（比对引用与镜像 ID，同名 tag 重新构建同样算）并重建容器。正在运行的容器不会被动，可在该行点「重建容器」选择何时中断。
- **日志里每 5 分钟刷一遍 `已定时拉起 runner: <名称>`，界面上也从来不显示「运行中」**：本版本已修复，升级即可，不需要重新注册任何 Runner。运行状态此前取自 pid 文件（`Runner.Listener.pid`，回退到 `.path`），而 actions/runner 这两个都不写：它的启动脚本没有一处落 pid 文件，`.path` 里装的是 PATH 字符串。于是每个 Runner 都被读成「已注册但没在跑」，5 分钟一次的巡检每轮都把它们再拉起一遍。现在改为查进程表；容器模式下则由各容器内的 Agent 作答——Manager 看不到别的容器里的进程。同一个根因还有一处：默认（非容器）模式下点「停止」必然报 `未找到 runner pid 文件或 pid 无效`。
- **status=unknown**：详情弹窗看 `probe`，可尝试「启动/停止」自愈。

### 本地构建镜像

```bash
docker build -t runner-manager .
docker build -f Dockerfile.runner -t ghcr.io/soulteary/runner-fleet:v1.5.0-runner .
```

Make：`make docker-build`、`make docker-run`、`make docker-stop`。

---

## 二、配置

```bash
mkdir -p config && cp config.yaml.example config/config.yaml
```

| 字段 | 说明 | 默认 |
|------|------|------|
| `server.port` | HTTP 服务端口 | `8080` |
| `server.addr` | 监听地址；空则绑定所有接口 | 空 |
| `runners.base_path` | Runner 安装目录根路径；**容器部署时设为 `/app/runners`** | `./runners` |
| `runners.items` | 预置 Runner 列表 | 也可通过 Web 界面添加 |
| `runners.container_mode` | 是否启用容器模式 | `false` |
| `runners.container_image` | 容器模式下 Runner 镜像（tag 带 -runner） | `ghcr.io/soulteary/runner-fleet:v1.5.0-runner` |
| `runners.container_network` | 容器模式下 Runner 所在网络 | `runner-net` |
| `runners.agent_port` | 容器内 Agent 端口 | `8081` |
| `runners.job_docker_backend` | Job 内 Docker：`dind` / `host-socket` / `none` | `dind` |
| `runners.dind_host` | `job_docker_backend=dind` 时 DinD 主机名 | `runner-dind` |
| `runners.volume_host_path` | 容器模式下宿主机 runners 绝对路径（必填） | 空 |
| `runners.items[].container_image` | 按 Runner 覆盖容器镜像（仅容器模式），留空回落全局 | 空 |
| `runners.items[].job_docker_backend` | 按 Runner 覆盖 Job Docker 后端（仅容器模式），留空回落全局 | 空 |
| `runners.resources` | Runner 容器资源上限（`cpus` / `memory` / `memory_swap` / `pids_limit`），透传给 `docker create`；启动时通过 `docker update` 对存量容器同样生效 | 空（不限制） |

以上部分字段可通过环境变量覆盖（如 `MANAGER_PORT`、`CONTAINER_MODE`、`VOLUME_HOST_PATH`、`JOB_DOCKER_BACKEND` 等），便于全容器部署时仅改 `.env` 而无需改 config/config.yaml，见 `.env.example`。

**校验**：不得同名；容器模式会校验名称映射后容器名冲突。`job_docker_backend` 仅允许 `dind`/`host-socket`/`none`；容器模式且 `base_path` 为容器内路径时必填 `volume_host_path`。未配 `job_docker_backend` 视为 `dind`。改后端后：**已停止**的容器在下次启动时自动重建，**正在运行**的会标出「配置已变更」，点该行「重建容器」立即生效——详见上文「改了配置与容器重建」。

示例：

```yaml
server:
  port: 8080
  addr: 0.0.0.0
runners:
  base_path: /app/runners
  items: []
```

---

## 三、添加 Runner

**获取 Token**：目标仓库/组织 → Settings → Actions → Runners → New self-hosted runner，复制 Token（约 1 小时有效）。每个 Runner 需新 Token。

**在服务中添加**：管理界面「快速添加 Runner」填写名称（唯一）、目标类型（org/repo）、目标、Token（可选，填则提交时可自动注册并启动）。可从 GitHub 页面复制 `./config.sh --url ... --token ...` 到「从 GitHub 复制命令解析」框，点「解析并填充」。自动注册仅面向 GitHub.com；GitHub Enterprise 需在 runner 目录下手动执行 `config.sh`。

**未安装 runner 时**：可从 [GitHub Actions Runner](https://github.com/actions/runner/releases) 下载解压到 `runners/<名称>/`，再在界面填 Token 或该目录下手动 `./config.sh`。容器部署下界面提交 Token 时会先自动安装再注册；容器模式需先配置 Runner 镜像与 `volume_host_path`（见上文容器模式）。

**注册结果**：写入该 runner 目录 `.registration_result.json`。**GitHub 显示检查**（可选）：在 runner 目录下放 `.github_check_token`（PAT，组织需 `admin:org`、仓库需 `repo`），约每 5 分钟检查，结果写入 `.github_status.json`。

**名称冲突检查**：在名称输入框里打字时，表单会调用 `/api/runner-precheck`，把可能出问题的地方提前摆出来——配置里已有同名 Runner、名称规范化后与别人撞容器名、安装目录被别的 Runner 占了、磁盘上留着一个已注册过的目录（存在 `.runner`）、宿主机上还挂着同名容器。阻塞性的问题标红并给出一键可用的建议名；仅提示性的（目录非空会被复用）不挡提交。强行提交会被服务端以 **409** 拒绝并返回同样的冲突信息——此前「静默加随机后缀」的行为已取消（需要的话传 `auto_rename: true`）。

每台机器可多 Runner，各用独立子目录即可。

---

## 四、安全与校验

**鉴权**：默认无登录鉴权，建议仅内网或本机使用。环境变量 `BASIC_AUTH_PASSWORD` 设置后启用 Basic Auth，`BASIC_AUTH_USER` 可选（默认 `admin`）。除 `GET /health` 外均需鉴权；敏感信息勿提交仓库，可放 `.env`。容器中加 `-e BASIC_AUTH_PASSWORD=...` 或 compose 的 `env_file`。

**路径与唯一性**：name/path 禁止 `..`、`/`、`\`；目录强制落在 `runners.base_path` 下。禁止同名；编辑时名称不可改。容器模式下名称规范为容器名，映射后重名会报错。

**Agent 鉴权**（容器模式）：Manager 会为每个 Runner 在 `<runner 目录>/.agent_token` 写入随机令牌（权限 0600），创建容器时以 `AGENT_TOKEN` 环境变量注入，调用 Agent 时以 `Authorization: Bearer` 带上。Agent 读的是环境变量，因此不依赖 Manager 与 Agent 的 UID 一致；文件是 Manager 侧的持久副本，Manager 重启后无需重建容器。Agent 对 `/status`、`/start`、`/stop` 强制校验，同一网络内的其它容器无法再控制 Runner；`/health` 保持开放供容器 HEALTHCHECK 使用。本特性之前创建的容器没有注入令牌、仍按不鉴权运行；现在这种容器会被判为「配置已变更」，下次启动时自动重建补上（正在运行的可点「重建容器」）。

**敏感文件**：config/config.yaml、.env 已入 `.gitignore`。各 runner 下的 `.github_check_token` 建议 `chmod 600`，版本库中应在 `.gitignore` 加 `**/.github_check_token`。

[← 返回项目首页](../../README.md)
