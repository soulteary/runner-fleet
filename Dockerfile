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
WORKDIR /app
COPY --from=builder /app/runner-manager .
COPY --from=builder /app/config.yaml ./config.yaml
RUN mkdir -p /app/scripts
COPY scripts/install-runner.sh /app/scripts/install-runner.sh
RUN chmod +x /app/scripts/install-runner.sh
EXPOSE 8080
ENTRYPOINT ["./runner-manager"]
CMD ["-config", "config.yaml"]
