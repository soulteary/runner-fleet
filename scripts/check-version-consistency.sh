#!/bin/sh
# 校验仓库内的版本号是否一致。
#
# 以 internal/config/config.go 中默认 Runner 镜像的兜底 tag 为准——那是唯一真正
# 影响运行行为的版本号，其余都是文档与示例，必须跟它一致。
#
# 背景：v1.0.1 / v1.1.0 / v1.1.1 三次发布都漏了同步文档，导致照 README 执行  version-check-ignore
# docker compose up -d 默认拉到的是落后三个版本的镜像。只改数值不建机制的话，
# 下次还会漏。
#
# 这个校验该不该搬进 soulteary/ci-recipes（以及为什么仓库里其余几处 shell 不该搬），
# 见 docs/ci-recipes-migration.md。
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

raw=$(mktemp)
list=$(mktemp)
tmp=$(mktemp)
trap 'rm -f "$raw" "$list" "$tmp"' EXIT

# CHANGELOG 天然包含所有历史版本号，不参与校验（见下面循环里的跳过）
# '*Dockerfile*' 而非 'Dockerfile*'：后者只匹配仓库根目录，会漏掉
# examples/runner-images/ 下引用了本仓库镜像 tag 的示例
#
# '*.sh' 与 '*.go' 是 v1.6.0 补上的两类漏网文件：
#   - examples/deploy/standalone/run.sh 把 Manager 镜像 tag 写死成 IMAGE 的默认值，
#     照它跑起来的就是那个版本，却从来不在扫描范围内，于是上一个版本起它就没跟上过。
#   - internal/config/config.go 里基准值旁边的字段注释可以悄悄说另一个版本——
#     基准就在这个文件里，注释反而没人核对。
# 测试里拿某个版本当输入数据用（而非引用当前版本）时，照例加 version-check-ignore 跳过。
#
# --others --exclude-standard：光凭 --cached（git ls-files 的默认行为）只能看到已被
# git 跟踪的文件，于是**新增但尚未提交**的文件永远扫不到——本地提交前跑一次是绿的，
# 一旦提交进去，CI 检出的树里它已被跟踪，当场变红。这个假绿灯真的放过去过一次。
# 加上未跟踪文件后，本地看到的与 CI 看到的一致。--exclude-standard 保证 .gitignore
# 里的东西（config/config.yaml、.env 这些本地文件）仍然不参与校验。
# CI 那边检出的是干净的树，没有未跟踪文件，所以这一项对 CI 没有任何影响。
#
# git 的退出码单独判断，不能把它塞进管道：管道的状态取自末端的 sort/grep，git 一失败
# （不在仓库里、索引读不了、容器里 bind mount 触发 dubious ownership）就被压成
# 「文件清单为空」，循环一次都不进，脚本一路打到最后那句全绿。这个假绿灯复现过：在非
# git 目录里跑，git 打 fatal not a git repository，脚本照样 exit 0，唯一一处过期版本号
# 毫无声响地过去了。清单拿不到就是判不了，只能红。
if ! git ls-files --cached --others --exclude-standard \
    '*.md' '*.yml' '*.yaml' '*.example' 'Makefile' '*Dockerfile*' '*.sh' '*.go' > "$raw"; then
    echo "git ls-files 失败：拿不到待校验的文件清单，不能据此判定通过。" >&2
    exit 1
fi

# sort -u：合并冲突期间 --cached 会把未合并路径按 stage 打印多次，去重顺带让顺序稳定。
sort -u "$raw" > "$list"

# unscannable 记录「清单里有、但没能真的扫过」的文件。这类文件必须让脚本变红：
# 扫不到等于没校验，而没校验跟校验通过是两件事。
unscannable=0

# 逐行读，而不是 for f in $(...)：后者先按空白切词、再对每个词做一次路径展开，于是
# 带空格的文件名会被拆成两个不存在的路径，双双被 [ -f ] 静默跳过。复现过：把唯一一处
# 过期版本号放进 "stale doc.md"，脚本打印全绿。逐行读还顺带让文件名里的 * ? [ 不再被
# 当成通配符。
while IFS= read -r f; do
    [ -n "$f" ] || continue
    # CHANGELOG 天然包含所有历史版本号，不参与校验
    if [ "$f" = "CHANGELOG.md" ]; then
        continue
    fi
    # 含空格之外的特殊字符（换行、非 ASCII）时 git 输出的是带双引号的转义形式。
    # 在 sh 里解这层引号不值得，但也不能当没看见——静默跳过又是一个假绿灯。
    case "$f" in
    '"'*)
        echo "::error::文件名含需要转义的字符，本脚本无法校验: ${f}" >&2
        unscannable=1
        continue
        ;;
    esac
    [ -f "$f" ] || continue
    if [ ! -r "$f" ]; then
        echo "::error file=${f}::无法读取，未纳入版本号校验" >&2
        unscannable=1
        continue
    fi
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
done < "$list"

if [ -s "$tmp" ]; then
    while IFS="$(printf '\t')" read -r f lineno v; do
        echo "::error file=${f},line=${lineno}::版本号 ${v} 与基准 ${EXPECTED} 不一致"
        echo "  ${f}:${lineno} 出现 ${v}，应为 ${EXPECTED}" >&2
    done < "$tmp"
    echo "" >&2
    echo "发布新版本时请一并更新上述位置，或修正 ${SOURCE_FILE} 中的基准版本。" >&2
    echo "若该处确需引用历史版本号（如变更说明），在该行加注释标记 version-check-ignore 即可跳过。" >&2
fi

if [ -s "$tmp" ] || [ "$unscannable" != "0" ]; then
    exit 1
fi

echo "所有文档与示例中的版本号均为 ${EXPECTED}"
