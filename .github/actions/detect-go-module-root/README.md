# detect-go-module-root

Composite action：在仓库根或子目录中查找包含 `go.mod` 与 `cmd/runner-manager` 的 Go 模块根目录。

- **Input**：`working_dir`（默认 `.`），优先检查该目录下是否有 `cmd/runner-manager`。
- **Output**：`root`（模块根路径）、`found`（`true`/`false`）。

供 `ci-manager.yml`、`ci-agent.yml` 等复用，便于单仓或 monorepo 共用同一套 CI。
