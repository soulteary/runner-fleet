# 多阶段构建：编译 + Ubuntu 运行时（避免 Alpine 导致 GitHub Runner 运行异常）
FROM golang:1.26-bookworm AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -ldflags "-X main.Version=${VERSION}" -o runner-manager .

FROM ubuntu:24.04
# GitHub Actions Runner 需 .NET Core 6.0 依赖（libicu 等），与 installdependencies.sh 一致
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    curl \
    libicu74 \
    libkrb5-3 \
    liblttng-ust1 \
    libssl3 \
    zlib1g \
    && rm -rf /var/lib/apt/lists/*

# 使用非 root 用户运行，避免 GitHub Actions Runner 报 "Must not run with sudo"
# UID/GID 1001 挂载 runners 卷时可按需 chown
RUN groupadd -g 1001 app && useradd -r -u 1001 -g app -d /app -s /bin/bash app

WORKDIR /app
COPY --from=builder /app/runner-manager .
COPY --from=builder /app/config.yaml ./config.yaml
RUN mkdir -p /app/scripts /app/runners
COPY scripts/install-runner.sh /app/scripts/install-runner.sh
RUN chmod +x /app/scripts/install-runner.sh && chown -R app:app /app

USER app
EXPOSE 8080
ENTRYPOINT ["./runner-manager"]
CMD ["-config", "config.yaml"]
