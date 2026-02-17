# 安全与校验

## 无鉴权

当前版本未做登录鉴权，请勿将服务暴露到公网，建议仅在内网或本机使用。

## 路径安全

- Runner 的 **name**、**path** 禁止包含 `..`、`/`、`\`（添加时校验；查询/启动/停止/更新/删除时也会校验 URL 中的 name 参数）。
- 创建目录时强制落在 `runners.base_path` 之下，防止路径穿越。

## 唯一性

- **添加**：禁止与已有 Runner 同名。
- **编辑**：名称不可修改，与磁盘目录名一致。

## Token 与敏感文件

- **config.yaml**：已列入 `.gitignore`，若含敏感信息请勿提交到仓库。
- **各 runner 目录下的 `.github_check_token`**：用于该 runner 的「GitHub 显示检查」，内容为 PAT。建议限制文件权限（如 `chmod 600`），若 `runners/` 被纳入版本库，请在 `.gitignore` 中加入 `**/.github_check_token` 避免泄露。

[← 返回文档索引](README.md)
