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
# '*Dockerfile*' 而非 'Dockerfile*'：后者只匹配仓库根目录，会漏掉
# examples/runner-images/ 下引用了本仓库镜像 tag 的示例
FILES=$(git ls-files '*.md' '*.yml' '*.yaml' '*.example' 'Makefile' '*Dockerfile*' |
    grep -v '^CHANGELOG\.md$' || true)

tmp=$(mktemp)
trap 'rm -f "$tmp"' EXIT

for f in $FILES; do
    [ -f "$f" ] || continue
    # 逐行扫描；带 version-check-ignore 标记的行跳过，供正文合法引用历史版本号
    grep -nE 'v[0-9]+\.[0-9]+\.[0-9]+|main\.Version=[0-9]+\.[0-9]+\.[0-9]+' "$f" |
        grep -v 'version-check-ignore' |
        while IFS= read -r hit; do
            lineno=${hit%%:*}
            text=${hit#*:}
            found=$(printf '%s\n' "$text" | grep -oE 'v[0-9]+\.[0-9]+\.[0-9]+' | sort -u)
            found_bare=$(printf '%s\n' "$text" |
                grep -oE 'main\.Version=[0-9]+\.[0-9]+\.[0-9]+' | sed 's/main\.Version=/v/' | sort -u)
            for v in $found $found_bare; do
                [ "$v" = "$EXPECTED" ] || printf '%s\t%s\t%s\n' "$f" "$lineno" "$v" >> "$tmp"
            done
        done
done

if [ -s "$tmp" ]; then
    while IFS="$(printf '\t')" read -r f lineno v; do
        echo "::error file=${f},line=${lineno}::版本号 ${v} 与基准 ${EXPECTED} 不一致"
        echo "  ${f}:${lineno} 出现 ${v}，应为 ${EXPECTED}" >&2
    done < "$tmp"
    echo "" >&2
    echo "发布新版本时请一并更新上述位置，或修正 ${SOURCE_FILE} 中的基准版本。" >&2
    echo "若该处确需引用历史版本号（如变更说明），在该行加注释标记 version-check-ignore 即可跳过。" >&2
    exit 1
fi

echo "所有文档与示例中的版本号均为 ${EXPECTED}"
