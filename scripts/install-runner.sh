#!/bin/sh
# 在 RUNNERS_BASE_PATH 下创建指定名称的目录，下载并解压 GitHub Actions runner。
# 用法: install-runner.sh <runner_name> [version]
# 默认版本 2.331.0，与官方文档一致；可选校验默认版本的哈希。

set -e

RUNNER_NAME="${1:?用法: install-runner.sh <runner_name> [version]}"
VERSION="${2:-2.331.0}"
BASE="${RUNNERS_BASE_PATH:-/app/runners}"
TARBALL="actions-runner-linux-x64-${VERSION}.tar.gz"
URL="https://github.com/actions/runner/releases/download/v${VERSION}/${TARBALL}"

# 默认版本 2.331.0 的官方校验哈希（可选校验）
HASH_2_331="5fcc01bd546ba5c3f1291c2803658ebd3cedb3836489eda3be357d41bfcf28a7"

INSTALL_DIR="${BASE}/${RUNNER_NAME}"
mkdir -p "$INSTALL_DIR"
cd "$INSTALL_DIR"

echo "下载 ${TARBALL} ..."
curl -o "$TARBALL" -L "$URL"

if [ "$VERSION" = "2.331.0" ]; then
  echo "校验哈希..."
  echo "${HASH_2_331}  ${TARBALL}" | sha256sum -c
fi

echo "解压..."
tar xzf "$TARBALL"
rm -f "$TARBALL"
echo "完成。安装目录: ${INSTALL_DIR}"
echo "请在管理界面「快速添加 Runner」中填写名称: ${RUNNER_NAME}、目标与 Token 完成注册。"
