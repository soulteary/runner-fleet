# ユーザーガイド

デプロイ、設定、Runner の追加、セキュリティはここで説明します。コントリビューター向けのビルドと API は [開発とビルド](development.md) を参照してください。

---

## 1. デプロイ (Docker)

- イメージは **Ubuntu** ベースで .NET Core 6.0 の依存関係を含み、**UID 1001** で実行されます。ホストにマウントするディレクトリはこのユーザーが書き込み可能である必要があります（例: `chown 1001:1001 config.yaml runners`）。
- 起動から約 15 秒後に、登録済みだが停止している Runner が自動で起動し、5 分ごとに定期チェックされます。

### 公開イメージを使う（推奨）

```bash
docker pull ghcr.io/soulteary/runner-fleet:main
```

### docker-compose クイックスタート

リポジトリルートに `docker-compose.yml` があります。コンテナモードで Job に Docker が必要で `job_docker_backend: dind` のときだけ DinD を有効にしてください。

```bash
cp config.yaml.example config.yaml
# config.yaml を編集: runners.base_path を /app/runners に設定

chown 1001:1001 config.yaml
mkdir -p runners && chown 1001:1001 runners

docker network create runner-net 2>/dev/null || true
docker compose up -d
# job_docker_backend: dind の場合: docker compose --profile dind up -d
```

UI: http://localhost:8080。認証の詳細は [4. セキュリティと検証](#4-セキュリティと検証) を参照。

### コンテナの実行（フル引数）

`config.yaml` と `runners` をマウントする必要があります。ポートは設定の `server.port` と一致させてください（デフォルト 8080）。

```bash
docker run -d --name runner-manager \
  -p 8080:8080 \
  -v $(pwd)/config.yaml:/app/config.yaml \
  -v $(pwd)/runners:/app/runners \
  ghcr.io/soulteary/runner-fleet:main
```

ホストのディレクトリは UID 1001 が書き込み可能である必要があります。Basic Auth: `-e BASIC_AUTH_PASSWORD=password`、`-e BASIC_AUTH_USER=admin`。Job で Docker を使う場合は `-v /var/run/docker.sock:/var/run/docker.sock` を追加するか、DinD を使用（リポジトリの `docker-compose.yml` の `--profile dind` 参照）。イメージには Docker CLI が含まれており、DinD で一般的な Action が動作します。

### 自動インストールと登録

UI の「Quick Add Runner」で名前、ターゲット、トークンを入力して送信すると、まずインストールスクリプトが実行され、続いて登録と起動が行われます。失敗時は:

```bash
docker exec runner-manager /app/scripts/install-runner.sh <name> [version]
```

またはホストで [actions-runner](https://github.com/actions/runner/releases) を `runners/<name>/` に展開し、UI で送信するか、そのディレクトリで `./config.sh` を手動実行してください。

### コンテナモード（Runner ごとにコンテナ）

各 Runner は専用コンテナで動作します。Manager はホストの Docker で起動/停止し、コンテナ内の Agent から HTTP で状態を取得します。

**config.yaml** で有効化（`config.yaml.example` 参照）:

```yaml
runners:
  base_path: /app/runners
  container_mode: true
  container_image: ghcr.io/soulteary/runner-fleet-runner:main
  container_network: runner-net
  agent_port: 8081
  job_docker_backend: dind   # dind | host-socket | none
  dind_host: runner-dind
  volume_host_path: /abs/path/on/host/to/runners
```

Runner イメージ: Manager と同じ名前で `-runner` タグ、またはローカルビルド: `docker build -f Dockerfile.runner -t ghcr.io/soulteary/runner-fleet-runner:main .`。Manager はホストの Docker（`docker.sock` のマウント）を使う必要があり、`DOCKER_HOST` で DinD にはしないでください。Compose ではホストの docker GID 用に `group_add` または `user: "0:0"` を使用。Runner 名はコンテナ名に正規化され、マッピング後の重複は衝突します。

### トラブルシューティング

- **compose down 後に Runner が起動しない**: 一度 `docker network create runner-net` を実行。まだ失敗する場合は UI の「Start」で再作成するか、`docker rm -f github-runner-<name>` のあと「Start」。
- **root で実行**: マウントしたディレクトリはプロセスユーザーが書き込み可能である必要あり。root の場合は `RUNNER_ALLOW_RUNASROOT=1` を設定。
- **古い Runner イメージ**: `docker rm -f github-runner-<name>` のあと、UI の「Start」で再作成。
- **status=unknown**: 詳細ポップアップの probe を確認。「Start/Stop」で自己修復を試す。

### イメージのローカルビルド

```bash
docker build -t runner-manager .
docker build -f Dockerfile.runner -t ghcr.io/soulteary/runner-fleet-runner:main .
```

Make: `make docker-build`、`make docker-run`、`make docker-stop`。

---

## 2. 設定

```bash
cp config.yaml.example config.yaml
```

| フィールド | 説明 | デフォルト |
|------------|------|------------|
| `server.port` | HTTP サーバーポート | `8080` |
| `server.addr` | バインドアドレス。空なら全インターフェース | 空 |
| `runners.base_path` | Runner インストールディレクトリのルートパス。**コンテナでは `/app/runners` に設定** | `./runners` |
| `runners.items` | 事前定義 Runner 一覧 | Web UI からも追加可能 |
| `runners.container_mode` | コンテナモードを有効化 | `false` |
| `runners.container_image` | コンテナモード時の Runner イメージ（-runner タグ） | `ghcr.io/soulteary/runner-fleet-runner:main` |
| `runners.container_network` | コンテナモード時の Runner ネットワーク | `runner-net` |
| `runners.agent_port` | コンテナ内 Agent ポート | `8081` |
| `runners.job_docker_backend` | Job 内 Docker: `dind` / `host-socket` / `none` | `dind` |
| `runners.dind_host` | `job_docker_backend=dind` 時の DinD ホスト名 | `runner-dind` |
| `runners.volume_host_path` | コンテナモード時の runners のホスト絶対パス（必須） | 空 |

**検証**: 名前の重複不可。コンテナモードではコンテナ名の衝突をチェック。`job_docker_backend` は `dind`/`host-socket`/`none` のみ。コンテナモードでコンテナの `base_path` を使う場合は `volume_host_path` 必須。`job_docker_backend` を省略すると `dind`。バックエンド変更後は UI から Runner を再起動してください。

例:

```yaml
server:
  port: 8080
  addr: 0.0.0.0
runners:
  base_path: /app/runners
  items: []
```

---

## 3. Runner の追加

**トークン取得**: リポジトリ/組織 → Settings → Actions → Runners → New self-hosted runner でトークンをコピー（約 1 時間有効）。Runner ごとに新しいトークンが必要です。

**サービスに追加**: UI の「Quick Add Runner」で名前（一意）、ターゲットタイプ（org/repo）、ターゲット、トークン（任意。指定すると送信時に自動登録・起動可能）を入力。GitHub の `./config.sh --url ... --token ...` を「Parse from GitHub command」に貼り付けて「Parse & fill」をクリックできます。自動登録は GitHub.com のみ。GitHub Enterprise は Runner ディレクトリで手動で `config.sh` を実行する必要があります。

**Runner が未インストールの場合**: [GitHub Actions Runner](https://github.com/actions/runner/releases) からダウンロードし、`runners/<name>/` に展開。その後 UI でトークン入力またはそのディレクトリで `./config.sh` を実行。コンテナデプロイでは UI でトークン送信時にまずインストール、続いて登録。コンテナモードでは先に Runner イメージと `volume_host_path` の設定が必要（上記コンテナモード参照）。

**登録結果**: その Runner ディレクトリの `.registration_result.json` に書き込み。**GitHub 表示チェック**（任意）: Runner ディレクトリに `.github_check_token`（PAT。組織は `admin:org`、リポジトリは `repo` が必要）を置くと約 5 分ごとにチェックし、結果は `.github_status.json` に書き込み。

1 台のマシンに複数 Runner: 別々のサブディレクトリを使用。

---

## 4. セキュリティと検証

**認証**: デフォルトではログインなし。内部ネットワークまたは localhost でのみ使用推奨。Basic Auth を有効にするには環境変数 `BASIC_AUTH_PASSWORD` を設定。`BASIC_AUTH_USER` は任意（デフォルト `admin`）。`GET /health` 以外の全ルートで認証が必要。シークレットはコミットせず `.env` を使用。コンテナでは `-e BASIC_AUTH_PASSWORD=...` または compose の `env_file`。

**パスと一意性**: name/path に `..`、`/`、`\` は不可。ディレクトリは `runners.base_path` 以下である必要あり。名前の重複不可。編集時は名前は読み取り専用。コンテナモードでは名前はコンテナ名に正規化され、マッピング後の重複はエラーになります。

**機密ファイル**: config.yaml と .env は `.gitignore` に含まれています。各 Runner の `.github_check_token` は `chmod 600` を推奨。バージョン管理下にある場合は `.gitignore` に `**/.github_check_token` を追加。

[← プロジェクトホームへ](../../README.md)
