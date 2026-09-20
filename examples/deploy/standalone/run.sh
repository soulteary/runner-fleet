#!/bin/sh
# 不用 compose，直接起一个 Manager 容器（默认模式：Runner 进程跑在容器内）。
#
# 用法：
#   sh run.sh                       # 起在 127.0.0.1:8080，不鉴权
#   PORT=0.0.0.0:8080 BASIC_AUTH_PASSWORD=xxx sh run.sh
#   WITH_DOCKER=0 sh run.sh         # Job 里不需要 docker，不挂宿主机 socket
#
# 变量：
#   IMAGE                 Manager 镜像，默认 ghcr.io/soulteary/runner-fleet:v1.5.0
#   PORT                  宿主机监听地址:端口，默认 127.0.0.1:8080
#   DATA_DIR              config/ 与 runners/ 的父目录，默认当前目录
#   BASIC_AUTH_USER       默认 admin
#   BASIC_AUTH_PASSWORD   留空则不鉴权（GET /health 始终免鉴权）
#   WITH_DOCKER           1（默认）挂载宿主机 docker.sock 供 Job 使用，0 则不挂

set -e

IMAGE="${IMAGE:-ghcr.io/soulteary/runner-fleet:v1.5.0}"
PORT="${PORT:-127.0.0.1:8080}"
DATA_DIR="${DATA_DIR:-$(pwd)}"
BASIC_AUTH_USER="${BASIC_AUTH_USER:-admin}"
BASIC_AUTH_PASSWORD="${BASIC_AUTH_PASSWORD:-}"
WITH_DOCKER="${WITH_DOCKER:-1}"
NAME="${NAME:-runner-manager}"

# Manager 以 UID 1001 运行，挂载目录必须对它可写。
# 先建目录再 chown：反过来的话，之后放进去的文件仍是 root 属主，
# 写配置时会报「保存配置失败: open /app/config/config.yaml: permission denied」。
mkdir -p "$DATA_DIR/config" "$DATA_DIR/runners"
if [ "$(id -u)" = "0" ]; then
    chown -R 1001:1001 "$DATA_DIR/config" "$DATA_DIR/runners"
else
    echo "提示: 非 root 运行，请自行确认 $DATA_DIR/config 与 $DATA_DIR/runners 对 UID 1001 可写"
fi

set -- run -d --name "$NAME" \
    -p "$PORT:8080" \
    -v "$DATA_DIR/config:/app/config" \
    -v "$DATA_DIR/runners:/app/runners" \
    -e RUNNERS_BASE_PATH=/app/runners \
    -e BASIC_AUTH_USER="$BASIC_AUTH_USER" \
    -e BASIC_AUTH_PASSWORD="$BASIC_AUTH_PASSWORD" \
    --restart unless-stopped

if [ "$WITH_DOCKER" = "1" ]; then
    # 容器内是 UID 1001，必须在 socket 所属组里，否则 Job 中 docker 报 permission denied
    DOCKER_GID="${DOCKER_GID:-$(getent group docker 2>/dev/null | cut -d: -f3 || true)}"
    [ -n "$DOCKER_GID" ] || DOCKER_GID=999
    set -- "$@" -v /var/run/docker.sock:/var/run/docker.sock --group-add "$DOCKER_GID"
fi

docker rm -f "$NAME" >/dev/null 2>&1 || true
docker "$@" "$IMAGE"

echo "已启动 $NAME：http://$PORT"
echo "自检日志：docker logs $NAME | grep 自检"
