# 部署示例：单容器与多容器

两套可直接复制走的部署配置，覆盖 Runner Fleet 的两种运行形态：

| | [`standalone/`](standalone/) 单容器 | [`fleet/`](fleet/) 多容器 |
|---|---|---|
| 形态 | 一个 Manager 容器，Runner 进程跑在它内部 | Manager 容器 + 每个 Runner 一个独立容器 |
| 对应配置 | `container_mode: false`（默认） | `container_mode: true` |
| 起步成本 | `docker run` 一条命令 | 需要 `runner-net` 网络、`volume_host_path`、一个自建镜像 |
| Runner 之间的隔离 | 弱：同一个容器、同一个用户、同一个 `$HOME` | 强：各自的容器、文件系统与资源上限 |
| 构建缓存（`~/.gradle`、`~/.m2`、`~/.npm`） | **共享**，并发 Job 可能互相踩；且在容器可写层里，容器重建即丢 | **各自独立**，落在 `runners/<名称>/` 下，跨 Job 保留 |
| 工作目录 `_work` | 每个 Runner 独立 | 每个 Runner 独立 |
| 资源上限 | 无（整个容器一份） | 每个 Runner 可单独限制（`runners.resources`） |
| 适合 | 1~2 个 Runner、工具链单一、自己人用 | 多个 Job 并行、不同项目工具链不同、需要限制单个 Job 的资源 |

两种形态下**镜像缓存都是共享的**——镜像层、BuildKit 缓存属于 Docker daemon，只要所有 Job 指向同一个
daemon（挂宿主机 `docker.sock`，或共用同一个 DinD）就已经共享，不需要额外配置。

## 快速开始

### 单容器

```bash
cd standalone
cp .env.example .env            # 按需改端口与 Basic Auth
mkdir -p config runners && sudo chown -R 1001:1001 config runners
docker compose up -d
```

不想用 compose 就用同目录的 `run.sh`（同一套参数的 `docker run` 版本）：

```bash
PORT=127.0.0.1:8080 BASIC_AUTH_PASSWORD=<密码> sh run.sh
```

### 多容器

```bash
cd fleet
docker network create runner-net
cp .env.example .env
mkdir -p config runners && cp config.yaml.example config/config.yaml
# 改 .env：VOLUME_HOST_PATH=$(realpath runners)、DOCKER_GID=$(getent group docker | cut -d: -f3)
sudo chown -R 1001:1001 config runners
docker compose --profile build build runner-image   # 构建带共享缓存的 Runner 镜像
docker compose up -d
```

打开界面逐个添加 Runner。**一个 Runner 同时只跑一个 Job**，要 N 路并发就开 N 个 Runner。

## 多容器下的缓存：什么共享、什么隔离

GitHub Actions 的「缓存」不是一件东西，三类缓存归属不同，共享与隔离要分开处理：

| 缓存 | 实际位置 | 怎么共享 |
|---|---|---|
| 镜像 / 层 / BuildKit | Docker daemon 的存储 | 所有 Job 指向同一个 daemon 即可（`job_docker_backend: host-socket`，或共用一个 DinD） |
| 工具链（setup-* 下载的 Node、Python…） | `_work/_tool` | 用 `RUNNER_TOOL_CACHE` 指到镜像里的共享目录 |
| Action 代码 | `_work/_actions` | 用 `ACTIONS_RUNNER_ACTION_ARCHIVE_CACHE` 指到预置的归档目录 |
| 构建缓存（`~/.gradle`、`~/.m2`、`~/.npm`、go build cache） | `$HOME` 下 | **不共享**：容器模式下 `HOME=/runner`，即该 Runner 自己的宿主机目录 |

`fleet/Dockerfile.runner-cached` 把前两类缓存预置进 Runner 镜像，并用 `ENV` 写好两个环境变量。
之所以走镜像层：Manager 创建 Runner 容器时只挂 `<volume_host_path>/<名称>:/runner`，没有「额外挂载」
的配置项；而同一个 daemon 上镜像层在所有容器之间只存一份（overlayfs），Job 往里写走 copy-on-write
——物理上共享一份，写入互不污染。

环境变量是通过镜像的 `ENV` 生效的：Agent 带着这套环境拉起 `run.sh`，`Runner.Listener` 继承后传给 Job。
只想给某一个 Runner 改缓存位置时，也可以在它的安装目录下放一个 `.env` 文件（Runner 启动时会读取
安装目录下的 `.env`），改完需要重启该 Runner。

几条容易踩的：

- **不要把多个 Runner 的 `_work/_actions` 指向同一个目录。** Runner 用 `<目录>.completed` 作水印，
  未命中时会先删掉目标目录再解压，另一个 Job 正在用同一路径时会被删。共享 Action 要用只读的
  `ACTIONS_RUNNER_ACTION_ARCHIVE_CACHE`（按 `<owner>_<repo>/<sha>.tar.gz` 存放的归档）。
- **不要开 `ACTIONS_RUNNER_SYMLINK_CACHED_ACTIONS=true`。** 它会把 `_actions/<action>` 做成指向共享
  目录的符号链接，Job 里对 action 目录的任何写入都会污染所有 Runner。
- **共享的工具链缓存要预热。** `@actions/tool-cache` 安装时会先删目标目录再复制，最后写 `.complete`。
  冷缓存下两个 Job 同时 `setup-node` 同一版本会打架；预置进镜像（已带 `.complete`）后就是纯读命中。
- **Action 归档缓存收益很小。** 它按 commit sha 命名，workflow 里写 `@v4` 这类 tag 时 sha 会漂移，
  而单个 action 通常只有几百 KB。优先级：镜像层缓存 ≫ 工具链缓存 ≫ Action 缓存。

验证是否命中：跑一个用 `actions/setup-node` 的 workflow，日志里出现 `Found in cache` 即生效。

语言与平台 SDK（Android SDK、Flutter 等）不属于上面任何一类缓存，应当扩展 Runner 镜像，
见 [`../runner-images/`](../runner-images/)。

## 排障

- **`保存配置失败: open /app/config/config.yaml: permission denied`**
  Manager 以 UID 1001 运行，而 `config.yaml` 是 root 放进去的。**先放文件，再 chown**：
  `sudo chown -R 1001:1001 config runners`。不用重启，下一次写配置就会成功。
- **添加 Runner 后日志里出现容器名冲突**（`The container name "/github-runner-<名称>" is already in use`）
  Manager 启动 15 秒后会把「已注册但未运行」的 Runner 统一拉起一次，若此时后台注册任务正好也在
  创建同一个容器，就会有一方报冲突。容器实际已经建好并在运行，可以忽略；想避开就在 Manager
  启动 20 秒后再添加 Runner。
- **`docker create 失败。输出: (无输出): signal: killed`**
  旧版本里，启停 Runner 的上下文挂在 HTTP 请求上，浏览器刷新（添加成功后有 5 秒自动刷新）会取消
  在途请求，连带把 `docker create` 子进程 SIGKILL 掉。该问题已修复；仍在旧版本上时，改用 API 触发
  可规避：`curl -u admin:<密码> -X POST http://127.0.0.1:8080/api/runners/<名称>/start`。
  若怀疑留下了半截容器：`docker ps -a --filter name=github-runner-`，有残留就 `docker rm -f` 后重新启动。
- **Runner 容器起来了但状态一直是 `new`**
  多半是 `VOLUME_HOST_PATH` 填错，容器挂到了空目录。它必须是宿主机上 runners 目录的绝对路径
  （在 compose 所在目录执行 `realpath runners`）。
- **Job 里 `docker` 报 `permission denied`**
  容器内是 UID 1001，需要在宿主机 `docker.sock` 的属组里。核对 `.env` 的 `DOCKER_GID` 是否等于
  `getent group docker | cut -d: -f3`；修改后需重建 Runner 容器（`docker rm -f github-runner-<名称>` 再启动）。
- **`host-socket` 下 Job 里的 `docker run -v $PWD:/x` 挂到了空目录**
  `-v` 的源路径由宿主机 daemon 解析，而 `$PWD` 是 Runner 容器内的路径（`/runner/_work/...`），两者不一致。
  这类 workflow 改用 DinD（`JOB_DOCKER_BACKEND=dind`），或在 Job 里换成宿主机上的真实路径。
- **宿主机磁盘越用越满**
  `host-socket` 下所有 Job 的镜像与构建产物都堆在宿主机 daemon 上：
  `docker image prune -f && docker builder prune -f`。Runner 自己的目录（`_work` 与 HOME 缓存）也会长大。

其余部署与配置说明见 [使用指南](../../docs/zh/guide.md)。
