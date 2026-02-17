# 多阶段构建：编译 + Ubuntu 运行时（避免 Alpine 导致 GitHub Runner 运行异常）
FROM golang:1.26-bookworm AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -ldflags "-X main.Version=${VERSION}" -o runner-manager .

FROM ubuntu:24.04
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    curl \
    && rm -rf /var/lib/apt/lists/*

# 使用非 root 用户运行，避免 GitHub Actions Runner 报 "Must not run with sudo"
# UID/GID 1000 与常见宿主机首用户一致，挂载 runners 卷时权限更易匹配
# 基础镜像可能已有 GID 1000，先尝试创建组，失败则复用已有组
RUN (groupadd -g 1000 app 2>/dev/null || true) && useradd -r -u 1000 -g 1000 -d /app -s /bin/bash app

WORKDIR /app
COPY --from=builder /app/runner-manager .
COPY --from=builder /app/config.yaml ./config.yaml
RUN mkdir -p /app/scripts /app/runners
COPY scripts/install-runner.sh /app/scripts/install-runner.sh
RUN chmod +x /app/scripts/install-runner.sh && chown -R 1000:1000 /app

USER app
EXPOSE 8080
ENTRYPOINT ["./runner-manager"]
CMD ["-config", "config.yaml"]
