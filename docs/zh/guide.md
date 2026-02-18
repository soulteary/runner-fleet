# 使用指南

**文档 / Docs:** [EN](../guide.md) · 中文 · [Français](../fr/guide.md) · [Deutsch](../de/guide.md) · [한국어](../ko/guide.md) · [日本語](../ja/guide.md)

![](../../.github/assets/fleet.jpg)

部署、配置、添加 Runner 与安全说明合并于此。面向贡献者的构建与 API 见 [开发与构建](development.md)。

---

## 一、部署（Docker）

- 镜像基于 **Ubuntu**，预装 .NET Core 6.0 依赖；以 **UID 1001** 运行，宿主机挂载目录需对该用户可写（如 `chown 1001:1001 config.yaml runners`）。
- 启动约 15 秒后自动拉起已注册未运行的 Runner，并每 5 分钟定时检查。

### 使用已发布镜像（推荐）

```bash
docker pull ghcr.io/soulteary/runner-fleet:main
```

### docker-compose 快速开始

仓库根目录有 `docker-compose.yml`。仅当容器模式且 Job 需要 Docker 并配置 `job_docker_backend: dind` 时再启用 DinD。

```bash
cp config.yaml.example config.yaml
# 编辑 config.yaml：runners.base_path 改为 /app/runners

chown 1001:1001 config.yaml
mkdir -p runners && chown 1001:1001 runners

docker network create runner-net 2>/dev/null || true
docker compose up -d
# 若 job_docker_backend: dind，则：docker compose --profile dind up -d
```

管理界面：http://localhost:8080。鉴权见 [四、安全与校验](#四安全与校验)。

### 运行容器（完整参数）

必须挂载 `config.yaml` 与 `runners`；端口与 `config.yaml` 中 `server.port` 一致（默认 8080）。

```bash
docker run -d --name runner-manager \
  -p 8080:8080 \
  -v $(pwd)/config.yaml:/app/config.yaml \
  -v $(pwd)/runners:/app/runners \
  ghcr.io/soulteary/runner-fleet:main
```

宿主机目录需对 UID 1001 可写。Basic Auth：`-e BASIC_AUTH_PASSWORD=密码`、`-e BASIC_AUTH_USER=admin`。Job 需要 Docker 时可加 `-v /var/run/docker.sock:/var/run/docker.sock`，或使用 DinD（见仓库 `docker-compose.yml` 的 `--profile dind`）。镜像已预装 Docker CLI，DinD 下常见 Action 可直接使用。

### 自动安装与注册

界面「快速添加 Runner」填写名称、目标、Token 提交后，会先执行安装脚本再注册并启动。失败时可：

```bash
docker exec runner-manager /app/scripts/install-runner.sh <名称> [版本号]
```

或宿主机在 `runners/<名称>/` 解压 [actions-runner](https://github.com/actions/runner/releases) 后再在界面提交或手动 `./config.sh`。

### 容器模式（Runner 独立容器）

每个 Runner 运行在独立容器中，Manager 通过宿主机 Docker 启停，经 HTTP 访问容器内 Agent 获取状态。

**config.yaml** 中启用（见 `config.yaml.example`）：

```yaml
runners:
  base_path: /app/runners
  container_mode: true
  container_image: ghcr.io/soulteary/runner-fleet:main-runner
  container_network: runner-net
  agent_port: 8081
  job_docker_backend: dind   # dind | host-socket | none
  dind_host: runner-dind
  volume_host_path: /abs/path/on/host/to/runners
```

Runner 镜像：同 Manager 镜像名、tag 带 `-runner`，或本地 `docker build -f Dockerfile.runner -t ghcr.io/soulteary/runner-fleet:main-runner .`。Manager 必须用宿主机 Docker（挂载 `docker.sock`），不可把 `DOCKER_HOST` 设为 DinD；Compose 中需 `group_add` 宿主机 docker GID 或 `user: "0:0"`。Runner 名称会规范为容器名，映射后重名会冲突。

### 排障

- **compose down 后 Runner 无法启动**：首次执行 `docker network create runner-net`。已出问题时界面点该 Runner「启动」重建，或 `docker rm -f github-runner-<名称>` 后再点「启动」。
- **root 运行**：挂载目录对运行用户可写；若用 root，需设 `RUNNER_ALLOW_RUNASROOT=1`。
- **旧 Runner 镜像**：`docker rm -f github-runner-<名称>`，再在界面点「启动」重建。
- **status=unknown**：详情弹窗看 `probe`，可尝试「启动/停止」自愈。

### 本地构建镜像

```bash
docker build -t runner-manager .
docker build -f Dockerfile.runner -t ghcr.io/soulteary/runner-fleet:main-runner .
```

Make：`make docker-build`、`make docker-run`、`make docker-stop`。

---

## 二、配置

```bash
cp config.yaml.example config.yaml
```

| 字段 | 说明 | 默认 |
|------|------|------|
| `server.port` | HTTP 服务端口 | `8080` |
| `server.addr` | 监听地址；空则绑定所有接口 | 空 |
| `runners.base_path` | Runner 安装目录根路径；**容器部署时设为 `/app/runners`** | `./runners` |
| `runners.items` | 预置 Runner 列表 | 也可通过 Web 界面添加 |
| `runners.container_mode` | 是否启用容器模式 | `false` |
| `runners.container_image` | 容器模式下 Runner 镜像（tag 带 -runner） | `ghcr.io/soulteary/runner-fleet:main-runner` |
| `runners.container_network` | 容器模式下 Runner 所在网络 | `runner-net` |
| `runners.agent_port` | 容器内 Agent 端口 | `8081` |
| `runners.job_docker_backend` | Job 内 Docker：`dind` / `host-socket` / `none` | `dind` |
| `runners.dind_host` | `job_docker_backend=dind` 时 DinD 主机名 | `runner-dind` |
| `runners.volume_host_path` | 容器模式下宿主机 runners 绝对路径（必填） | 空 |

**校验**：不得同名；容器模式会校验名称映射后容器名冲突。`job_docker_backend` 仅允许 `dind`/`host-socket`/`none`；容器模式且 `base_path` 为容器内路径时必填 `volume_host_path`。未配 `job_docker_backend` 视为 `dind`；改后端后需在界面重新启动 Runner。

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

每台机器可多 Runner，各用独立子目录即可。

---

## 四、安全与校验

**鉴权**：默认无登录鉴权，建议仅内网或本机使用。环境变量 `BASIC_AUTH_PASSWORD` 设置后启用 Basic Auth，`BASIC_AUTH_USER` 可选（默认 `admin`）。除 `GET /health` 外均需鉴权；敏感信息勿提交仓库，可放 `.env`。容器中加 `-e BASIC_AUTH_PASSWORD=...` 或 compose 的 `env_file`。

**路径与唯一性**：name/path 禁止 `..`、`/`、`\`；目录强制落在 `runners.base_path` 下。禁止同名；编辑时名称不可改。容器模式下名称规范为容器名，映射后重名会报错。

**敏感文件**：config.yaml、.env 已入 `.gitignore`。各 runner 下的 `.github_check_token` 建议 `chmod 600`，版本库中应在 `.gitignore` 加 `**/.github_check_token`。

[← 返回项目首页](../../README.md)
