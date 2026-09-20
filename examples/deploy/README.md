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

怎么确认 N 路并发真的生效：

1. GitHub 的 Settings → Actions → Runners 里应当看到 **N 个**各自独立的 Runner，名字与界面上的一致，
   状态都是 Idle。只出现一个、名字是一串十六进制的，见排障里
   「GitHub 上只出现一个 Runner」那条。
2. 界面上 N 行都应显示「运行中」。**只有已注册且在运行的 Runner 才会接 Job**；
   若一行都不显示运行中，见排障里「每 5 分钟刷一遍『已定时拉起』」那条。
3. 跑一个带 `matrix` 的 workflow（N 个互不依赖的 Job），在 Actions 页面看它们是否同时进入
   in-progress。若只有一个在跑、其余排队，说明实际在线的 Runner 不足 N 个。

标签会影响分派：`runs-on` 命中哪些 Runner，就只在那批里挑空闲的。想让一组 Runner 共同承担同一类
Job，就给它们相同的标签。

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
- **Runner 标着「配置已变更」，悬停显示差异是 `agent_token`**
  容器内的 Agent 暴露 `/start`、`/stop` 等控制接口，同网络的任何容器都能访问，因此 Manager 会给
  每个 Runner 生成一个令牌，创建容器时以 `-e AGENT_TOKEN=` 注入。本特性之前创建的容器没有这个
  变量，其 Agent 谁都不拒绝，所以会被判为漂移。点「重建容器」补上即可（已停止的下次启动时自动补）。
- **日志里出现「令牌文件 … 已存在但读不到内容」，或 Agent 侧「令牌文件 … 存在但无法读取，接口将不启用鉴权」**
  令牌文件是 `runners/<名称>/.agent_token`（0600，属主为 Manager 的 UID 1001）。runners 目录被 root
  接管过时，Manager 建不出它、容器内的 app(1001) 也读不动它——前者会让这个 Runner 拿不到令牌，
  后者会让没有注入过 `AGENT_TOKEN` 的旧容器**静默不鉴权**。根因与第一条的 chown 相同：
  `sudo chown -R 1001:1001 runners`，然后点该行「重建容器」。
- **GitHub 上只出现一个 Runner，名字还是一串十六进制（像 `dd014243d356`）**
  旧版本调 `config.sh` 时没传 `--name`，而它的默认值是本机 hostname；又因为 `config.sh` 是在
  **Manager 容器内**执行的，一个部署里的每个 Runner 都用同一个名字（Manager 容器的 hostname）
  去注册，GitHub 侧自然只剩一个。同时它也没传 `--unattended`，撞上重名会进入交互式重试循环，
  在没有 TTY 的环境里一直耗到超时——而注册是单 worker 顺序执行的，后面排队的 Runner 全被堵住，
  表现就是「加了三个，只有第一个成功，另外两个连日志都没有」。
  该问题已修复。**升级不会自动修好已经注册错的 Runner**：先到目标仓库或组织的
  Settings → Actions → Runners 删掉那个以容器 ID 命名的 Runner，再在界面上重新添加，
  它们就会按各自的名称注册。
- **日志里每 5 分钟刷一遍「已定时拉起 runner: …」，把所有 Runner 轮番拉一遍；界面上也从来不显示「运行中」**
  运行状态此前取自 pid 文件（`Runner.Listener.pid`，回退到 `.path`），而 actions/runner 这两个都不写：
  它的启动脚本（`run.sh`、`run-helper.sh`、`runsvc.sh`）没有一处落 pid 文件，`.path` 里装的是 PATH
  字符串。于是每个 Runner 都被读成「已注册但没在跑」，5 分钟一次的巡检每轮都把它们再拉起一遍。
  容器模式下还多错一层：Manager 与 Runner 不在同一个 PID namespace，就算真有 pid 文件也对不上号。
  该问题已修复——现在查进程表判定，容器模式下由各容器内的 Agent 作答，探测不到时置为 `unknown`
  并跳过拉起（不确知它没在跑就别动它）。升级即可，不需要重新注册任何 Runner。
  同一个根因还有两处表现：默认（非容器）模式下点「停止」必然报
  `未找到 runner pid 文件或 pid 无效`；Agent 里「已在运行就不重复启动」的护栏形同虚设。
- **添加 Runner 后日志里出现容器名冲突**（`The container name "/github-runner-<名称>" is already in use`）
  同一个 Runner 会被多条路径同时碰：Manager 启动 15 秒后的自动拉起、每 5 分钟的定时拉起、
  注册完成后的启动、界面点击。该问题已修复——启停与重建现在按容器名串行化，后到的一方会发现
  容器已存在并转为启动，不再重复创建。仍在旧版本上时这条日志可以忽略，容器实际已经建好并在运行。
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
  `getent group docker | cut -d: -f3`；改完直接启动该 Runner 即可，GID 对不上的容器会被判为「配置已变更」并自动重建（正在运行的点「重建容器」）。
- **`host-socket` 下 Job 里的 `docker run -v $PWD:/x` 挂到了空目录**
  `-v` 的源路径由宿主机 daemon 解析，而 `$PWD` 是 Runner 容器内的路径（`/runner/_work/...`），两者不一致。
  这类 workflow 改用 DinD（`JOB_DOCKER_BACKEND=dind`，改完在界面点「启动」即按新后端重建容器），
  或在 Job 里换成宿主机上的真实路径。
- **改了 `JOB_DOCKER_BACKEND`（或 `RUNNER_IMAGE`、`CONTAINER_NETWORK`、`VOLUME_HOST_PATH`）之后**
  这些参数只在 `docker create` 时决定，已存在的容器不会自己变。Manager 会比对容器的实际创建参数
  与当前配置：**已停止的容器在点「启动」时自动删掉重建**，界面上则给出「配置已变更」徽标，
  鼠标悬停能看到具体差异（如 `job_docker_backend: → dind`）。
  **正在运行的容器不会被自动重建**——上面可能正跑着 Job；要立刻生效就点该行的「重建容器」
  （会中断正在跑的 Job），或等它空闲时停止再启动。
  重新 build 同名 tag 的镜像同样会被识别（比对的是镜像 ID），不必改 tag。
- **自检报「N 个 Runner 目录可被宿主机上的其他用户进入」**
  Runner 的安装目录里有 `config.sh` 写下的 `.credentials_rsaparams`——它向 GitHub 表明身份的
  RSA 私钥。actions/runner 不给这些文件设 Unix 权限（只在 Windows 上打 Hidden 属性，Linux 上
  跟着 umask 落成 0644），所以目录的权限位就是最后一道门；读到那个文件就能冒充该 Runner
  领取 Job，并看到传给 Job 的 secrets。
  本版本起新建的目录是 0700，但已存在的目录不会被改动。自检会把它们逐个列出并给出
  可直接执行的 `chmod 700 <目录...>`。**先看再执行**：Manager 以 root 跑、容器内是 app(1001)
  这种 UID 不匹配的部署下，收紧权限会让容器读不到自己的目录；那种情况应当先
  `sudo chown -R 1001:1001 runners` 把属主理顺。
  这条防的是宿主机上的其他本地用户。`host-socket` 后端下 Job 本来就能挂任意宿主机路径，
  那层暴露不受影响（见上文关于 `host-socket` 的说明）。
- **宿主机磁盘越用越满**
  `host-socket` 下所有 Job 的镜像与构建产物都堆在宿主机 daemon 上：
  `docker image prune -f && docker builder prune -f`。Runner 自己的目录（`_work` 与 HOME 缓存）也会长大。

其余部署与配置说明见 [使用指南](../../docs/zh/guide.md)。
