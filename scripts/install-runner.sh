#!/bin/sh
# 在 RUNNERS_BASE_PATH 下创建指定名称的目录，下载并校验解压 GitHub Actions runner。
#
# 用法: install-runner.sh <runner_name> [version]
# 环境变量:
#   RUNNERS_BASE_PATH      runner 根目录，默认 /app/runners
#   RUNNER_VERSION         版本号（不含 v），等价于第二个参数
#   RUNNER_SHA256          期望的 tarball SHA256；离线环境或自建镜像源时使用
#   RUNNER_FORCE_REINSTALL 置为 1 时即使目录里已有 runner 也重新下载
#
# 与旧版的区别：
#   - 按 uname -m 选择架构，不再写死 x64（arm64 机器上装 x64 包会直接跑不起来）
#   - 未指定版本时向 GitHub 查询最新版，查不到再退回内置的兜底版本
#   - 任何版本都强制校验 SHA256；取不到官方哈希就直接失败，不再静默跳过校验

set -e

RUNNER_NAME="${1:?用法: install-runner.sh <runner_name> [version]}"
BASE="${RUNNERS_BASE_PATH:-/app/runners}"

# 查询不到最新版时使用的兜底版本，及其官方 SHA256（仅 x64）
FALLBACK_VERSION="2.331.0"
FALLBACK_SHA256_X64="5fcc01bd546ba5c3f1291c2803658ebd3cedb3836489eda3be357d41bfcf28a7"

API="https://api.github.com/repos/actions/runner/releases"

detect_arch() {
    case "$(uname -m)" in
        x86_64 | amd64) echo "x64" ;;
        aarch64 | arm64) echo "arm64" ;;
        armv7l | armv7) echo "arm" ;;
        *) echo "" ;;
    esac
}

# resolve_latest 取最新 release 的版本号（去掉前缀 v）；失败时输出空
resolve_latest() {
    curl -fsSL --max-time 20 "${API}/latest" 2>/dev/null |
        sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"v\([^"]*\)".*/\1/p' |
        head -n 1
}

# resolve_sha256 从 release 说明中取出指定 tarball 的官方 SHA256；失败时输出空
resolve_sha256() {
    _version="$1"
    _tarball_re=$(printf '%s' "$2" | sed 's/\./\\./g')
    curl -fsSL --max-time 20 "${API}/tags/v${_version}" 2>/dev/null |
        grep -oE "[0-9a-f]{64}[[:space:]]+${_tarball_re}" |
        head -n 1 |
        cut -c1-64
}

ARCH="$(detect_arch)"
if [ -z "$ARCH" ]; then
    echo "不支持的架构: $(uname -m)" >&2
    exit 1
fi

VERSION="${2:-${RUNNER_VERSION:-}}"
if [ -z "$VERSION" ]; then
    echo "查询 actions/runner 最新版本..."
    VERSION="$(resolve_latest || true)"
    if [ -z "$VERSION" ]; then
        VERSION="$FALLBACK_VERSION"
        echo "查询失败，使用兜底版本 ${VERSION}"
    fi
fi

TARBALL="actions-runner-linux-${ARCH}-${VERSION}.tar.gz"
URL="https://github.com/actions/runner/releases/download/v${VERSION}/${TARBALL}"
INSTALL_DIR="${BASE}/${RUNNER_NAME}"

if [ -f "${INSTALL_DIR}/config.sh" ] && [ "${RUNNER_FORCE_REINSTALL:-}" != "1" ]; then
    echo "${INSTALL_DIR} 下已存在 runner，跳过下载（需重装请设置 RUNNER_FORCE_REINSTALL=1）"
    exit 0
fi

# 校验哈希：显式指定 > release 说明中的官方值 > 兜底版本的内置值
EXPECTED="${RUNNER_SHA256:-}"
if [ -z "$EXPECTED" ]; then
    EXPECTED="$(resolve_sha256 "$VERSION" "$TARBALL" || true)"
fi
if [ -z "$EXPECTED" ] && [ "$VERSION" = "$FALLBACK_VERSION" ] && [ "$ARCH" = "x64" ]; then
    EXPECTED="$FALLBACK_SHA256_X64"
fi
if [ -z "$EXPECTED" ]; then
    echo "无法获取 ${TARBALL} 的官方 SHA256（网络不可达或该版本/架构不存在）。" >&2
    echo "请从 https://github.com/actions/runner/releases/tag/v${VERSION} 取得哈希后重试：" >&2
    echo "  RUNNER_SHA256=<sha256> $0 ${RUNNER_NAME} ${VERSION}" >&2
    exit 1
fi

mkdir -p "$INSTALL_DIR"
cd "$INSTALL_DIR"

# 下载失败或中途退出时不留下半个包，避免下次误判
trap 'rm -f "${INSTALL_DIR}/${TARBALL}"' EXIT

echo "下载 ${TARBALL} ..."
# -f：HTTP 错误时直接失败，而不是把错误页面写成 tar 包
curl -fSL --retry 3 -o "$TARBALL" "$URL"

echo "校验 SHA256 ..."
echo "${EXPECTED}  ${TARBALL}" | sha256sum -c -

echo "解压..."
tar xzf "$TARBALL"

echo "完成。安装目录: ${INSTALL_DIR}（版本 ${VERSION}，架构 ${ARCH}）"
echo "请在管理界面「快速添加 Runner」中填写名称: ${RUNNER_NAME}、目标与 Token 完成注册。"
