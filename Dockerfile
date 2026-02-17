# 多阶段构建：编译 + 最小运行时
FROM golang:1.26-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -ldflags "-X main.Version=${VERSION}" -o runner-manager .

FROM alpine:3.19
RUN apk --no-cache add ca-certificates curl
WORKDIR /app
COPY --from=builder /app/runner-manager .
COPY --from=builder /app/config.yaml ./config.yaml
RUN mkdir -p /app/scripts
COPY scripts/install-runner.sh /app/scripts/install-runner.sh
RUN chmod +x /app/scripts/install-runner.sh
EXPOSE 8080
ENTRYPOINT ["./runner-manager"]
CMD ["-config", "config.yaml"]
