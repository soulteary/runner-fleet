# Manager：多阶段构建，BUILDPLATFORM 宿机构建、TARGETOS/TARGETARCH 交叉编译
ARG BUILDPLATFORM=linux/amd64
FROM --platform=$BUILDPLATFORM golang:1.27-bookworm AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
ARG VERSION=dev
# 未传入时为空，version-kit 会把空字段从输出里省掉
ARG COMMIT=
ARG BUILD_DATE=
ARG TARGETOS=linux
ARG TARGETARCH=amd64
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -ldflags "-X main.Version=${VERSION} -X main.Commit=${COMMIT} -X main.BuildDate=${BUILD_DATE}" -o runner-manager ./cmd/runner-manager

FROM ubuntu:24.04
LABEL org.opencontainers.image.title="Runner Fleet Manager" \
      org.opencontainers.image.description="GitHub Actions Runner 管理服务"

# 依赖清单集中在 scripts/apt-packages.txt，Manager 与 Runner 镜像共用，避免单向漂移
COPY scripts/apt-packages.txt /tmp/apt-packages.txt
RUN apt-get update \
    && awk '!/^[[:space:]]*#/ && NF' /tmp/apt-packages.txt \
       | xargs -r apt-get install -y --no-install-recommends \
    && rm -rf /var/lib/apt/lists/* /tmp/apt-packages.txt
RUN install -m 0755 -d /etc/apt/keyrings \
    && curl -fsSL https://download.docker.com/linux/ubuntu/gpg -o /etc/apt/keyrings/docker.asc \
    && chmod 644 /etc/apt/keyrings/docker.asc \
    && echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/ubuntu noble stable" > /etc/apt/sources.list.d/docker.list \
    && apt-get update && apt-get install -y --no-install-recommends docker-ce-cli \
    && rm -rf /var/lib/apt/lists/*

# 非容器模式下 Runner 进程在本容器内运行，容器模式下 Manager 需用宿主机 Docker 创建 Runner 容器，
# 两种情况都要访问挂载进来的 docker.sock，因此 app(UID 1001) 需在 socket 所属组内。
# 默认 999，构建时可 --build-arg DOCKER_GID=<宿主机 GID> 覆盖；docker-compose 中亦可用 group_add 覆盖。
ARG DOCKER_GID=999
RUN groupadd -g 1001 app && useradd -r -u 1001 -g app -d /app -s /bin/bash app \
    && if ! getent group "${DOCKER_GID}" >/dev/null; then groupadd -g "${DOCKER_GID}" docker; fi \
    && usermod -aG "$(getent group "${DOCKER_GID}" | cut -d: -f1)" app
# 托管 runner 提供免密 sudo，大量 workflow 直接写 `sudo apt-get install -y ...`。
# 只装 sudo 而不配 sudoers，等于命令在但用不了——比不装更难排查。
# 需要更严格的边界时用 --build-arg ALLOW_SUDO=false 关闭。
#
# 这不会实质扩大 Job 的权限面：容器内 app 本就拥有工作目录；真正的边界是容器本身
# （容器模式下建议配合 runners.resources），以及是否把宿主机 docker.sock 挂进去。
ARG ALLOW_SUDO=true
RUN if [ "${ALLOW_SUDO}" = "true" ]; then \
        echo "app ALL=(ALL) NOPASSWD:ALL" > /etc/sudoers.d/runner-fleet-app \
        && chmod 0440 /etc/sudoers.d/runner-fleet-app \
        && visudo -c -f /etc/sudoers.d/runner-fleet-app; \
    fi

# tini 作为 PID 1：容器里的 init 要做两件本进程做不到的事。
# 一是回收孤儿——Job 里 `cmd &`、守护进程化的工具、Gradle daemon 这些进程，
# 父进程退出后会被托付给 PID 1，而 Go 程序只 Wait 自己起的那一个子进程，
# 其余一律变僵尸；Runner 是长期存活的，僵尸会跨 Job 累积，还占着
# runners.resources.pids_limit 的名额，到顶之后 fork 失败，表现为 Job 随机报
# Resource temporarily unavailable。二是把 SIGTERM 转发给直接子进程。
# 不写进 scripts/apt-packages.txt：那份清单对齐的是 actions/runner-images 的
# toolset（给 Job 用的工具），tini 是运行时的一部分，含义不同。
RUN apt-get update \
    && apt-get install -y --no-install-recommends tini \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY --from=builder /app/runner-manager .
COPY config.yaml.example ./config/config.yaml
COPY scripts/install-runner.sh /app/scripts/install-runner.sh
RUN sed -i 's|base_path: ./runners|base_path: /app/runners|' config/config.yaml \
    && mkdir -p /app/runners \
    && chmod +x /app/scripts/install-runner.sh && chown -R app:app /app

USER app
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD curl -fsS http://127.0.0.1:8080/health || exit 1
ENTRYPOINT ["/usr/bin/tini", "--", "./runner-manager"]
CMD ["-config", "/app/config/config.yaml"]
