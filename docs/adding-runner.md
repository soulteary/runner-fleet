# 添加 Runner 流程

## 1. 在 GitHub 获取 Token

1. 进入目标 **仓库** 或 **组织**：**Settings → Actions → Runners → New self-hosted runner**。
2. 在注册说明中复制 **Token**（约 1 小时有效）。

## 2. 在本服务中添加

在管理界面「快速添加 Runner」中填写：

- **名称**：唯一，将对应 `runners/<名称>/` 目录。
- **目标类型**：组织（org）或仓库（repo）。
- **目标**：组织名或 `owner/repo`。
- **Token**（可选）：粘贴上一步的 Token，提交时可选择自动执行注册。

若与已有 Runner 同名，会提示不可重复。

## 3. Runner 程序未安装时

若 `runners/<名称>/` 下尚未安装 runner 程序：

1. 从 [GitHub Actions Runner](https://github.com/actions/runner/releases) 下载对应平台包，解压到 `runners/<名称>/`。
2. 在界面提交「快速添加」表单时填入 Token，并勾选执行注册；或先提交表单创建配置与目录，再在该目录下手动执行：
   ```bash
   ./config.sh --url https://github.com/owner/repo --token <TOKEN>
   ```

## 一台机器多 Runner

每个 Runner 使用独立子目录（如 `runners/runner-1`、`runners/runner-2`），互不干扰，可同时运行多个 Runner 并行执行任务。

[← 返回文档索引](README.md)
