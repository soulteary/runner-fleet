// Package docsconsistency 只存放文档一致性的测试，没有运行时代码。
//
// 为什么是 Go 测试而不是脚本：仓库刚把两个 shell 检查搬进 soulteary/ci-recipes
// （见 docs/ci-recipes-migration.md），再往 scripts/ 里加 shell 等于把刚拆掉的东西
// 装回来。而这几项检查又不属于那个项目——它们查的是本仓库自己的文档事实，
// 不是可复用的 recipe。落成 internal/ 下的测试，CI 的
// `go test -race ./cmd/runner-manager/... ./internal/...` 就已经在跑它们，
// 不必新增 workflow，也不会出现「本地跑的和 CI 跑的不是一套」。
//
// 与 ci-recipes 的两个检查是互补关系，不重叠：
//   - check-version-consistency：版本号在仓库内部自洽
//   - check-docs-structure：译文的标题层级序列与英文一致
//   - 本包 translations_test.go：标题之下的内容量（表格行、代码块、列表项、链接目标）
//   - 本包 pathrefs_test.go：非 Markdown 文件里引用的仓库路径确实存在
//   - 本包 examples_policy_test.go：examples/ 的语言策略是被决定过的，不是漏掉的
package docsconsistency
