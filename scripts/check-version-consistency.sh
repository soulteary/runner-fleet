#!/bin/sh
# 校验仓库内的版本号是否一致。
#
# 以 internal/config/config.go 中默认 Runner 镜像的兜底 tag 为准——那是唯一真正
# 影响运行行为的版本号，其余都是文档与示例，必须跟它一致。
#
# 背景：v1.0.1 / v1.1.0 / v1.1.1 三次发布都漏了同步文档，导致照 README 执行
# docker compose up -d 默认拉到的是落后三个版本的镜像。只改数值不建机制的话，
# 下次还会漏。
#
# 用法: sh scripts/check-version-consistency.sh

set -e

SOURCE_FILE="internal/config/config.go"

EXPECTED=$(grep -oE 'tag = "v[0-9]+\.[0-9]+\.[0-9]+"' "$SOURCE_FILE" 2>/dev/null |
    head -n 1 | grep -oE 'v[0-9]+\.[0-9]+\.[0-9]+' || true)

if [ -z "$EXPECTED" ]; then
    echo "无法从 ${SOURCE_FILE} 中解析默认镜像 tag，请检查 DefaultRunnerContainerImage()" >&2
    exit 1
fi

echo "基准版本（取自 ${SOURCE_FILE}）: ${EXPECTED}"

# CHANGELOG 天然包含所有历史版本号，不参与校验
FILES=$(git ls-files '*.md' '*.yml' '*.yaml' '*.example' 'Makefile' 'Dockerfile*' |
    grep -v '^CHANGELOG\.md$' || true)

failed=0
for f in $FILES; do
    [ -f "$f" ] || continue
    # vX.Y.Z 形式（镜像 tag、文档里的版本示例）
    found=$(grep -oE 'v[0-9]+\.[0-9]+\.[0-9]+' "$f" | sort -u || true)
    # main.Version=X.Y.Z 形式（development 文档里的构建示例）
    found_bare=$(grep -oE 'main\.Version=[0-9]+\.[0-9]+\.[0-9]+' "$f" |
        sed 's/main\.Version=/v/' | sort -u || true)
    for v in $found $found_bare; do
        if [ "$v" != "$EXPECTED" ]; then
            line=$(grep -nE "${v}" "$f" | head -n 1 | cut -d: -f1)
            echo "::error file=${f},line=${line}::版本号 ${v} 与基准 ${EXPECTED} 不一致"
            echo "  ${f}:${line} 出现 ${v}，应为 ${EXPECTED}" >&2
            failed=1
        fi
    done
done

if [ "$failed" -ne 0 ]; then
    echo "" >&2
    echo "发布新版本时请一并更新上述位置，或修正 ${SOURCE_FILE} 中的基准版本。" >&2
    exit 1
fi

echo "所有文档与示例中的版本号均为 ${EXPECTED}"
