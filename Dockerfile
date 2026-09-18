# Manager：多阶段构建，BUILDPLATFORM 宿机构建、TARGETOS/TARGETARCH 交叉编译
ARG BUILDPLATFORM=linux/amd64
FROM --platform=$BUILDPLATFORM golang:1.26-bookworm AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
ARG VERSION=dev
ARG TARGETOS=linux
ARG TARGETARCH=amd64
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -ldflags "-X main.Version=${VERSION}" -o runner-manager ./cmd/runner-manager

FROM ubuntu:24.04
LABEL org.opencontainers.image.title="Runner Fleet Manager" \
      org.opencontainers.image.description="GitHub Actions Runner 管理服务"

# Runner 依赖（libicu 等）；Docker CLI 供容器模式与 Job 内 docker 使用
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates curl libicu74 libkrb5-3 liblttng-ust1 libssl3 zlib1g \
    && rm -rf /var/lib/apt/lists/*
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
ENTRYPOINT ["./runner-manager"]
CMD ["-config", "/app/config/config.yaml"]
