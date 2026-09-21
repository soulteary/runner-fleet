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
# 这个校验该不该搬进 soulteary/ci-recipes，见 docs/ci-recipes-migration.md。
#
# 用法: sh scripts/check-docs-structure.sh

set -e

LANGS="zh fr de ko ja"
DOCS="development.md guide.md README.md"

# headings 提取标题层级序列，跳过 ``` 与 ~~~ 围栏内的行——代码块里的 `# 注释` 不是标题。
#
# 收栏只认开栏用的那种字符。早先只认 ```，于是 ~~~ 围栏根本不算围栏，块里顶格的
# `# 注释` 被当成标题计进序列（实测：两个标题的文档报成 3 个）。两边都用 ~~~ 时序列
# 一致，看不出来；一边改成 ``` 就成了凭空的红灯，反过来也能把真的结构漂移盖住。
# 只认同种字符收栏，还顺带避免了 ``` 块里出现 ~~~ 时把围栏提前关掉。
headings() {
    awk '
        /^(```|~~~)/ {
            marker = substr($0, 1, 1)
            if (!fence) { fence = 1; opener = marker }
            else if (marker == opener) { fence = 0 }
            next
        }
        !fence && /^#+[ \t]/ {
            match($0, /^#+/)
            print substr($0, 1, RLENGTH)
        }
    ' "$1"
}

failed=0

# compared 记真正比过的英文原文份数。一份都没比就不能算通过：DOCS 里每一份都走
# 「[ -f "$base" ] || continue」的话，循环体一次都不进，failed 保持 0，脚本照样打印
# 「各语言文档章节结构一致」并 exit 0。复现过：在没有 docs/ 的目录里跑，全绿。
# 目录挪了名字、路径写错、在错误的工作目录里跑，都是这一条。
compared=0

for doc in $DOCS; do
    base="docs/$doc"
    [ -f "$base" ] || continue
    compared=$((compared + 1))
    base_seq=$(headings "$base")
    base_count=$(printf '%s\n' "$base_seq" | grep -c '#' || true)

    # doc_failed 与 failed 分开记：只有一个 failed 的话，前面某个文档一失败，后面
    # 全部一致的文档连「全部一致」那行都不再打印，看起来像是根本没检查。
    doc_failed=0

    for lang in $LANGS; do
        target="docs/$lang/$doc"
        if [ ! -f "$target" ]; then
            echo "::error file=$base::缺少译文 $target"
            doc_failed=1
            continue
        fi
        target_seq=$(headings "$target")
        if [ "$base_seq" != "$target_seq" ]; then
            target_count=$(printf '%s\n' "$target_seq" | grep -c '#' || true)
            echo "::error file=$target::章节结构与 $base 不一致（英文 $base_count 个标题，本文件 $target_count 个）。"
            echo "  英文版新增或调整了章节而这里没跟上时就会这样。逐节对照 $base 补齐后重跑本脚本。"
            echo "  英文层级序列: $(printf '%s' "$base_seq" | tr '\n' ' ')"
            echo "  本文件层级序列: $(printf '%s' "$target_seq" | tr '\n' ' ')"
            doc_failed=1
        fi
    done

    if [ "$doc_failed" = "0" ]; then
        echo "$base: $base_count 个标题，${LANGS} 全部一致"
    else
        failed=1
    fi
done

if [ "$failed" != "0" ]; then
    exit 1
fi

if [ "$compared" = "0" ]; then
    echo "docs/ 下没有找到任何一份英文原文（${DOCS}），一份都没比过，不能判定通过。" >&2
    exit 1
fi

echo "各语言文档章节结构一致"
