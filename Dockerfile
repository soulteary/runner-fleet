# Runner Fleet Manager：多阶段构建，编译 + Ubuntu 运行时（避免 Alpine 导致 GitHub Runner 运行异常）
FROM golang:1.26-bookworm AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -ldflags "-X main.Version=${VERSION}" -o runner-manager .

FROM ubuntu:24.04
LABEL org.opencontainers.image.title="Runner Fleet Manager" \
      org.opencontainers.image.description="GitHub Actions Runner 管理服务"

# GitHub Actions Runner 需 .NET Core 6.0 依赖（libicu 等）
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    curl \
    libicu74 \
    libkrb5-3 \
    liblttng-ust1 \
    libssl3 \
    zlib1g \
    && rm -rf /var/lib/apt/lists/*

# Docker CLI：Job 内 docker build 等通过 DOCKER_HOST 连接 DinD 或宿主机 Docker
RUN install -m 0755 -d /etc/apt/keyrings \
    && curl -fsSL https://download.docker.com/linux/ubuntu/gpg -o /etc/apt/keyrings/docker.asc \
    && chmod 644 /etc/apt/keyrings/docker.asc \
    && echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/ubuntu noble stable" > /etc/apt/sources.list.d/docker.list \
    && apt-get update && apt-get install -y --no-install-recommends docker-ce-cli \
    && rm -rf /var/lib/apt/lists/*

# 非 root 运行，避免 Runner 报 "Must not run with sudo"；挂载卷时宿主机建议 chown 1001:1001
RUN groupadd -g 1001 app && useradd -r -u 1001 -g app -d /app -s /bin/bash app

WORKDIR /app
COPY --from=builder /app/runner-manager .

# 默认配置：基于 example 生成，base_path 设为 /app/runners（与下方卷挂载一致）；运行时可挂载自己的 config.yaml 覆盖
COPY config.yaml.example ./config.yaml
RUN sed -i 's|base_path: ./runners|base_path: /app/runners|' config.yaml

RUN mkdir -p /app/scripts /app/runners
COPY scripts/install-runner.sh /app/scripts/install-runner.sh
RUN chmod +x /app/scripts/install-runner.sh && chown -R app:app /app

USER app
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD curl -fsS http://127.0.0.1:8080/health || exit 1
ENTRYPOINT ["./runner-manager"]
CMD ["-config", "/app/config.yaml"]
