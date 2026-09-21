#!/bin/sh
# 校验各语言文档的章节结构与英文版一致。
#
# 背景：docs/{zh,fr,de,ko,ja}/development.md 曾整整落后一个版本——英文版在 v1.5.1/v1.6.0  version-check-ignore
# 加了「运行状态如何判定」「Runner 目录权限」「删除 Runner 与 GitHub」三节和整个「测试」一节，
# 五种译文一节都没跟上，而且没有任何东西会因此报错：译文本身语法完好，站点照常渲染。
# 界面的 i18n 早就有测试盯着六种语言的键集，文档这一半却一直没有。
#
# 比不了标题文字（本来就该被翻译），所以比标题的「层级序列」：`##` `###` 的出现次序。
# 少一节、多一节、层级错位都会让序列对不上，而正常翻译不会改变它。
#
# 用法: sh scripts/check-docs-structure.sh

set -e

LANGS="zh fr de ko ja"
DOCS="development.md guide.md README.md"

# headings 提取标题层级序列，跳过 ``` 围栏内的行——代码块里的 `# 注释` 不是标题
headings() {
    awk '
        /^```/ { fence = !fence; next }
        !fence && /^#+[ \t]/ {
            match($0, /^#+/)
            print substr($0, 1, RLENGTH)
        }
    ' "$1"
}

failed=0

for doc in $DOCS; do
    base="docs/$doc"
    [ -f "$base" ] || continue
    base_seq=$(headings "$base")
    base_count=$(printf '%s\n' "$base_seq" | grep -c '#' || true)

    for lang in $LANGS; do
        target="docs/$lang/$doc"
        if [ ! -f "$target" ]; then
            echo "::error file=$base::缺少译文 $target"
            failed=1
            continue
        fi
        target_seq=$(headings "$target")
        if [ "$base_seq" != "$target_seq" ]; then
            target_count=$(printf '%s\n' "$target_seq" | grep -c '#' || true)
            echo "::error file=$target::章节结构与 $base 不一致（英文 $base_count 个标题，本文件 $target_count 个）。"
            echo "  英文版新增或调整了章节而这里没跟上时就会这样。逐节对照 $base 补齐后重跑本脚本。"
            echo "  英文层级序列: $(printf '%s' "$base_seq" | tr '\n' ' ')"
            echo "  本文件层级序列: $(printf '%s' "$target_seq" | tr '\n' ' ')"
            failed=1
        fi
    done

    if [ "$failed" = "0" ]; then
        echo "$base: $base_count 个标题，${LANGS} 全部一致"
    fi
done

if [ "$failed" != "0" ]; then
    exit 1
fi

echo "各语言文档章节结构一致"
