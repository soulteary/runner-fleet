# 自定义 Runner 镜像

GitHub 托管的 runner（`runs-on: ubuntu-24.04` 等）预装了大量工具链——Android SDK、
多版本 Node、Python、浏览器等。**自托管 runner 不会有这些**。为托管 runner 写的
workflow 往往隐式依赖了它们，迁到自托管后会以各种形式失败：

| 现象 | 缺的东西 |
|------|----------|
| `git: command not found`，或 checkout 后工作目录没有 `.git` | `git` |
| `Unable to locate executable file: unzip` | `unzip` |
| 解压 `.tar.xz` 分发的 SDK 失败 | `xz-utils` |
| `SDK location not found. Define a valid SDK location with an ANDROID_HOME…` | Android SDK |
| `node: command not found` | Node.js |

前三项本仓库的 Runner 镜像已预装（见 `scripts/apt-packages.txt`）。语言/平台 SDK
体积大、版本因项目而异，不适合塞进基础镜像，应当按需扩展。

## 扩展方式

以本仓库的 Runner 镜像为基础叠加自己的工具链即可：

```dockerfile
FROM ghcr.io/soulteary/runner-fleet:v1.3.0-runner
USER root
RUN apt-get update && apt-get install -y --no-install-recommends <你的包> \
    && rm -rf /var/lib/apt/lists/*
USER app
```

本目录下有两个可直接使用的完整示例：

- [`Dockerfile.android`](Dockerfile.android) — Android SDK + Flutter
- [`Dockerfile.node`](Dockerfile.node) — Node.js + 常用全局包

构建：

```bash
make docker-build-runner-example EXAMPLE=android IMAGE=your-registry/android-runner:1
# 等价于
docker build -f examples/runner-images/Dockerfile.android -t your-registry/android-runner:1 .
```

## 三条必须注意的规则

**1. 容器以 UID 1001（`app`）运行，装到 `/opt` 等目录的工具必须 `chown`**

许多 SDK 在运行时要往自己的安装目录写东西（license 标记、缓存、下载的组件）。
以 root 装完不改属主，Job 里会报权限错误：

```dockerfile
RUN chown -R 1001:1001 /opt/your-sdk
```

**2. 环境变量要写进镜像，别指望 workflow 去设**

托管 runner 会设 `ANDROID_HOME` 之类的变量，自托管不会。凡是构建工具会去读的
变量，都在镜像里 `ENV` 好：

```dockerfile
ENV ANDROID_HOME=/opt/android-sdk
ENV PATH=$PATH:$ANDROID_HOME/platform-tools
```

**3. 预热放在 `USER app` 之后**

`flutter precache`、`sdkmanager --licenses` 这类会往安装目录写文件的步骤，要以
最终运行用户的身份执行，否则产物属主还是 root。

## 接入 Manager

镜像推到仓库后，在 `config/config.yaml` 里按 Runner 指定，其余 Runner 仍用全局镜像：

```yaml
runners:
    container_mode: true
    container_image: ghcr.io/soulteary/runner-fleet:v1.3.0-runner   # 全局默认
    items:
        - name: android-builder
          target_type: repo
          target: owner/repo
          labels: [android]
          container_image: your-registry/android-runner:1   # 仅此 Runner 使用
          job_docker_backend: none                          # 该项目不需要 Job 内 Docker
```

workflow 里用 label 精确落到对应 Runner：

```yaml
runs-on: [self-hosted, linux, android]
```

## 验证

Manager 启动时会检查配置中用到的每个镜像是否具备 `git`、`unzip`、`tar`、`curl`，
缺失会在日志里指出：

```bash
docker compose logs runner-manager | grep 自检
```

也可手动确认：

```bash
docker run --rm --entrypoint sh your-registry/android-runner:1 \
  -c 'id && command -v git unzip && echo $ANDROID_HOME'
```
