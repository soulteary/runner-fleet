# 開発とビルド

**文档 / Docs:** [EN](../development.md) · [中文](../zh/development.md) · [Français](../fr/development.md) · [Deutsch](../de/development.md) · [한국어](../ko/development.md) · 日本語

![](../../.github/assets/fleet.jpg)

本番環境ではコンテナデプロイを使用してください。[ユーザーガイド](guide.md) を参照。このドキュメントはコントリビューター向け: ローカルビルドとデバッグです。

## 要件

- Go 1.26（[go.mod](../../go.mod) と一致）。

## ビルド

```bash
# runner-manager バイナリをビルド
go build -o runner-manager ./cmd/runner-manager

# バージョン付き（/version とデバッグ用）
go build -ldflags "-X main.Version=1.4.0" -o runner-manager ./cmd/runner-manager

# Runner Agent のみビルド（コンテナモード用）
go build -o runner-agent ./cmd/runner-agent

# または Make: make build / make build-agent / make build-all
```

テンプレートは Manager バイナリに埋め込まれています（`cmd/runner-manager/templates/`）。単一バイナリで `templates/` ディレクトリは不要です。

## ローカル実行とデバッグ

```bash
mkdir -p config && cp config.yaml.example config/config.yaml
go run ./cmd/runner-manager
# または make run（ビルドしてから実行）; 設定ファイル指定: ./runner-manager -config /path/to/config.yaml
```

`:8080` で待ち受け、http://localhost:8080。デバッグ用 Basic Auth: `BASIC_AUTH_PASSWORD=secret go run ./cmd/runner-manager`。[ユーザーガイド – セキュリティ](guide.md#4-セキュリティと検証) を参照。

## CLI フラグ

- `-config <path>`: 設定ファイルのパス。
- `-version`: バージョンを表示して終了（ビルド時に `-ldflags "-X main.Version=..."` で注入）。

## HTTP API

Basic Auth 有効時、`/health` 以外のリクエストには Header に `Authorization: Basic <base64(user:password)>` が必要です。

| パス | メソッド | 説明 |
|------|----------|------|
| `/health` | GET | `{"status":"ok"}` を返す。Ingress/K8s プローブ用。常に認証不要。 |
| `/version` | GET | `{"version":"..."}` を返す。 |
| `/api/runners` | GET | Runner 一覧。コンテナモードで probe 失敗時は `status=unknown` と構造化された `probe`（`error/type/suggestion/check_command/fix_command`）を返す。 |
| `/api/runners/:name` | GET | 単一 Runner の詳細。コンテナモードで probe 失敗時も同様に `probe`。 |
| `/api/runners/:name/start` | POST | Runner を起動。probe 失敗時も起動を試み、レスポンスに構造化された `probe` を返す。 |
| `/api/runners/:name/stop` | POST | Runner を停止。probe 失敗時も停止を試み、レスポンスに構造化された `probe` を返す。 |
| `/api/runners` | POST | Runner を追加（任意でインストールと登録）。名前が衝突した場合は黙って改名せず **409** を返し、`conflicts` と `suggested_name` を含めます。従来の自動リネームが必要なら `auto_rename: true` を送ってください。 |
| `/api/runners/:name/recreate` | POST | 現在の設定で Runner コンテナを削除して作り直します（コンテナモードのみ）。実行中の Job は中断されます。停止中のコンテナは「開始」時に作成パラメータの不一致を検出して自動で作り直されます。 |
| `/api/runner-precheck` | GET | 追加前の名前チェック: `?name=&path=`。`available`、`suggested_name`、検出した `conflicts`（`name_taken`、`container_name`、`install_dir`、`dir_registered`、`dir_adopt`、`dir_exists`、`container_exists`）を返します。各項目は `level`（`error`/`warn`）、`message`、`detail`、任意の `fix_command` を持ちます。読み取り専用で、画面は入力中に随時呼び出します。 |

### 破壊的変更（アップグレード注意）

従来のフラットな `probe_*` フィールドは削除されています。`probe` オブジェクトを使用: `probe.error`、`probe.type`、`probe.suggestion`、`probe.check_command`、`probe.fix_command`。`probe.type` の値: `docker-access`、`agent-http`、`agent-connect`、`unknown`。Web UI は `status=unknown` のときも「Start/Stop」で自己修復できます。

例（probe 失敗）:

```json
{
  "name": "runner-a",
  "status": "unknown",
  "probe": {
    "error": "agent returned 502: bad gateway",
    "type": "agent-http",
    "suggestion": "Check runner container logs and Agent + /runner process state",
    "check_command": "docker ps -a | rg \"github-runner-\" && docker logs --tail=200 <runner_container_name>",
    "fix_command": "docker restart <runner_container_name>"
  }
}
```

## Makefile ターゲット

- `make help`: 全ターゲットを表示。
- `make build`: Manager をビルド（Version ldflags 付き）。
- `make build-agent`: Runner Agent をビルド（コンテナモード用）。
- `make build-all`: Manager と Agent をビルド。
- `make test`: テストを実行。
- `make run`: Manager をビルドしてから実行。
- `make docker-build` / `make docker-run` / `make docker-stop`: Manager イメージのビルドと実行。[ユーザーガイド](guide.md) 参照。
- `make docker-build-runner`: コンテナモード用 Runner イメージをビルド（`Dockerfile.runner`、デフォルトタグは `RUNNER_IMAGE`）。
- `make docker-build-runner-example`: カスタム Runner イメージの例をビルド（`EXAMPLE=android|node`、[`examples/runner-images/`](../../examples/runner-images/) 参照）。
- `make clean`: ビルドしたバイナリを削除（runner-manager、runner-agent）。

コンテナモードでは `cmd/runner-agent` の Agent と `Dockerfile.runner` の Runner イメージを使用します。

## リリース

ドキュメントと例のバージョン参照は `internal/config/config.go` のデフォルトイメージタグと一致させる必要があります。CI は `scripts/check-version-consistency.sh` で検証します。リリース PR を開く前にローカルで実行してください:

```bash
sh scripts/check-version-consistency.sh
```

古いバージョンを正当に引用する行（リリースノート、アップグレード手順）には `version-check-ignore` マーカーを付けます。

[← ドキュメントへ戻る](README.md)
