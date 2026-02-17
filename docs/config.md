# 配置说明

首次使用可将项目根目录下的 `config.yaml.example` 复制为 `config.yaml`，再按需修改。

```bash
cp config.yaml.example config.yaml
```

## 字段说明

| 字段 | 说明 | 默认 |
|------|------|------|
| `server.port` | HTTP 服务端口 | `8080` |
| `server.addr` | 监听地址；空则仅绑定端口（等价所有接口） | 空 |
| `runners.base_path` | 所有 Runner 安装目录的根路径 | `./runners` |
| `runners.items` | 预置的 Runner 列表 | 也可通过 Web 界面添加 |

「GitHub 显示检查」所需的 token 仅在各 runner 目录下配置，见 [添加 Runner](adding-runner.md) 中的说明。

## 示例

```yaml
server:
    port: 8080
    addr: 0.0.0.0
runners:
    base_path: ./runners
    items: []
```

[← 返回文档索引](README.md)
