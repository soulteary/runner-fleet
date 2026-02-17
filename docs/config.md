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
| `github.token` | 预留，用于今后通过 GitHub API 获取 token 等能力 | 当前未使用 |
| `runners.base_path` | 所有 Runner 安装目录的根路径 | `./runners` |
| `runners.items` | 预置的 Runner 列表 | 也可通过 Web 界面添加 |

## 示例

```yaml
server:
    port: 8080
    addr: 0.0.0.0
github:
    token: ""
runners:
    base_path: ./runners
    items: []
```

[← 返回文档索引](README.md)
