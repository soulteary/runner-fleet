# ユーザーガイド

**文档 / Docs:** [EN](../guide.md) · [中文](../zh/guide.md) · [Français](../fr/guide.md) · [Deutsch](../de/guide.md) · [한국어](../ko/guide.md) · 日本語

![](../../.github/assets/fleet.jpg)

デプロイ、設定、Runner の追加、セキュリティはここで説明します。コントリビューター向けのビルドと API は [開発とビルド](development.md) を参照してください。

---

## 1. デプロイ (Docker)

- イメージは **Ubuntu** ベースで .NET Core 6.0 の依存関係を含み、**UID 1001** で実行されます。ホストにマウントするディレクトリはこのユーザーが書き込み可能である必要があります（例: `chown 1001:1001 config runners`）。
- 起動から約 15 秒後に、登録済みだが停止している Runner が自動で起動し、5 分ごとに定期チェックされます。

### 公開イメージを使う（推奨）

本番では特定バージョン（例: v1.4.0）を使用してください。開発時は `main` タグが使えます。

```bash
docker pull ghcr.io/soulteary/runner-fleet:v1.4.0
```

### docker-compose クイックスタート

リポジトリルートに `docker-compose.yml` があります。コンテナモードで Job に Docker が必要で `job_docker_backend: dind` のときだけ DinD を有効にしてください。

```bash
mkdir -p config && cp config.yaml.example config/config.yaml
# config/config.yaml を編集: runners.base_path を /app/runners に設定

chown 1001:1001 config runners
mkdir -p runners && chown 1001:1001 runners

docker network create runner-net 2>/dev/null || true
docker compose up -d
# job_docker_backend: dind の場合: docker compose --profile dind up -d
```

UI: http://localhost:8080。認証の詳細は [4. セキュリティと検証](#4-セキュリティと検証) を参照。

### コンテナの実行（フル引数）

`config` ディレクトリと `runners` をマウントしてください（ディレクトリのみ。config/config.yaml を単体でマウントすると、ホストにファイルがない場合 Docker が空ファイルを作成して起動に失敗します）。ポートは設定の `server.port` と一致させてください（デフォルト 8080）。

```bash
docker run -d --name runner-manager \
  -p 8080:8080 \
  -v $(pwd)/config:/app/config \
  -v $(pwd)/runners:/app/runners \
  ghcr.io/soulteary/runner-fleet:v1.4.0
```

ホストのディレクトリは UID 1001 が書き込み可能である必要があります。Basic Auth: `-e BASIC_AUTH_PASSWORD=password`、`-e BASIC_AUTH_USER=admin`。Job で Docker を使う場合は `-v /var/run/docker.sock:/var/run/docker.sock` を追加し（イメージには GID 999 の `docker` グループを用意、ビルド引数 `DOCKER_GID` で変更可。ホストの docker GID が 999 以外なら `--group-add $(getent group docker | cut -d: -f3)` も追加）、あるいはDinD を使用（リポジトリの `docker-compose.yml` の `--profile dind` 参照）。両イメージには Docker CLI に加え、GitHub ホスト runner に揃えたコマンドラインの基盤層を同梱しています。`scripts/apt-packages.txt` は `actions/runner-images` の `toolset-2404.json` の apt パッケージ集合を取り込んだもので、`git`・`unzip`・`jq`・`rsync`・`sudo`・`xvfb` などが含まれます。言語・プラットフォーム SDK は意図的に含めていません（`setup-*` action を使うか、イメージを拡張してください）。ホスト runner と同様に、両イメージとも Job ユーザーにパスワードなし `sudo` を付与しているため `sudo apt-get install -y …` がそのまま使えます。外す場合は `--build-arg ALLOW_SUDO=false` でビルドしてください。

### 自動インストールと登録

UI の「Quick Add Runner」で名前、ターゲット、トークンを入力して送信すると、まずインストールスクリプトが実行され、続いて登録と起動が行われます。失敗時は:

```bash
docker exec runner-manager /app/scripts/install-runner.sh <name> [version]
```

スクリプトは `uname -m` でアーキテクチャを判定し、バージョン未指定なら GitHub API で最新版を解決し、**どのバージョンでも必ず** SHA-256 を検証します。公式ハッシュを取得できない場合（オフライン／ミラー）は明示的に指定してください: `RUNNER_SHA256=<sha256> ... install-runner.sh <name> <version>`。既に runner がある ディレクトリはスキップされます（再インストールは `RUNNER_FORCE_REINSTALL=1`）。

またはホストで [actions-runner](https://github.com/actions/runner/releases) を `runners/<name>/` に展開し、UI で送信するか、そのディレクトリで `./config.sh` を手動実行してください。

### コンテナモード（Runner ごとにコンテナ）

各 Runner は専用コンテナで動作します。Manager はホストの Docker で起動/停止し、コンテナ内の Agent から HTTP で状態を取得します。

**方法1: 環境変数のみ（フルコンテナ時推奨）**
config/config.yaml の編集は不要。`cp .env.example .env` のあと、例: `CONTAINER_MODE=true`、`VOLUME_HOST_PATH=<runners のホスト絶対パス>`（`realpath runners` など）、`JOB_DOCKER_BACKEND=host-socket`、`CONTAINER_NETWORK=runner-net` を設定。`config/config.yaml` を用意しなくても、上記を `.env` に設定していれば初回起動時に自動生成されます。`RUNNER_IMAGE` を設定しない場合、Runner イメージは `MANAGER_IMAGE` から自動導出（例: v1.4.0 → v1.4.0-runner）。マウントする `config` と `runners` は引き続き `chown 1001:1001` が必要。詳細は `.env.example` のオーバーライド変数を参照。

**方法2: config/config.yaml で有効化**（`config.yaml.example` 参照）:

```yaml
runners:
  base_path: /app/runners
  container_mode: true
  container_image: ghcr.io/soulteary/runner-fleet:v1.4.0-runner
  container_network: runner-net
  agent_port: 8081
  job_docker_backend: dind   # dind | host-socket | none
  dind_host: runner-dind
  volume_host_path: /abs/path/on/host/to/runners
```

Runner イメージ: Manager と同じ名前で `-runner` タグ（本番はバージョン例 v1.4.0-runner、開発は main-runner）、またはローカルビルド: `docker build -f Dockerfile.runner -t ghcr.io/soulteary/runner-fleet:v1.4.0-runner .`。Manager はホストの Docker（`docker.sock` のマウント）を使う必要があり、`DOCKER_HOST` で DinD にはしないでください。Compose ではホストの docker GID 用に `group_add` または `user: "0:0"` を使用。`job_docker_backend: host-socket` の場合、Manager は Runner コンテナに `--group-add <ホストの docker GID>` を渡します（`docker.sock` から自動検出、`runners.docker_gid` / `DOCKER_GID` で上書き可）。イメージ側にも `docker` グループを用意しています（ビルド引数 `DOCKER_GID`、既定 999）。Runner 名はコンテナ名に正規化され、マッピング後の重複は衝突します。

**Runner イメージの拡張**: GitHub ホストの runner には Android SDK・Node・Python などのツールチェーンが同梱されていますが、セルフホストにはありません。`ubuntu-24.04` 向けに書かれた workflow はこれを暗黙に前提としていることが多く、移行後に `SDK location not found` などで失敗します。本リポジトリの Runner イメージの上に自分のツールチェーンを重ねてください。すぐ使える例と重要な四つの規則（/opt 配下は UID 1001 に chown、環境変数はイメージに埋め込む、パスワードなし sudo は継承、ウォームアップは `USER app` の後）は [`examples/runner-images/`](../../examples/runner-images/) にあります。`items[].container_image` で特定の Runner だけに適用し、workflow からは label で選択します。

**設定変更とコンテナの再作成**: イメージ・ネットワーク・マウント先・Job 内 Docker バックエンドは `docker create` の時点で確定し、既存のコンテナは作成時のまま動き続けます。設定を変えただけでは届きません。Manager は各コンテナの実際の作成パラメータを現在の設定と突き合わせます。**停止中**のコンテナが一致しない場合は次に起動するとき（「開始」をクリックした場合も Manager の自動起動の場合も）削除して作り直し、一覧にはその Runner に「設定変更あり」が付き、ツールチップに差分（例: `job_docker_backend: → dind`）が出ます。**実行中**のコンテナは自動では作り直しません（Job の最中かもしれないため）。すぐ反映したいときは行の「コンテナ再作成」（`POST /api/runners/:name/recreate`、実行中の Job は中断されます）を使うか、空いたときに停止して開始してください。同じ tag でイメージを作り直した場合も対象です（比較はイメージ ID）。

**すぐ使えるデプロイ例**: [`examples/deploy/`](../../examples/deploy/) には、そのままコピーして使える構成が 2 つあります。`standalone/`（Manager 1 コンテナで、Runner プロセスもその中。`docker run` でも Compose でも可）と `fleet/`（コンテナモード: Runner ごとに 1 コンテナ。イメージキャッシュはホストの daemon を共用することで共有、ツールチェーンと Action のキャッシュは Runner イメージのレイヤーに同梱、ビルドキャッシュは Runner ごとに分離）。README では両者の比較、どのキャッシュが共有でどれが分離されるか、デプロイで踏みやすい落とし穴（ディレクトリの所有者、`VOLUME_HOST_PATH`、host-socket でのディスク増加）をまとめています。

### トラブルシューティング

- **うまく動かないときはまず起動時セルフチェック**: `docker compose logs runner-manager | grep 自检`。起動時に runners ディレクトリ、Docker 到達性、ネットワーク、Runner イメージ、Job 内 Docker バックエンドを検査し、失敗項目にはそのまま実行できる修正コマンドが出ます。
- **compose down 後に Runner が起動しない**: 一度 `docker network create runner-net` を実行。まだ失敗する場合は UI の「Start」で再作成するか、`docker rm -f github-runner-<name>` のあと「Start」。
- **root で実行**: マウントしたディレクトリはプロセスユーザーが書き込み可能である必要あり。root の場合は `RUNNER_ALLOW_RUNASROOT=1` を設定。
- **Job 内で docker.sock が `permission denied`**: `job_docker_backend: host-socket` ではコンテナのユーザー（UID 1001）が socket の所有グループに属している必要があります。Manager はコンテナ作成時に検出したホストの docker GID で `--group-add` を付与するため、アップグレード後は Runner コンテナを再作成してください（`docker rm -f github-runner-<name>` のあと「Start」）。検出できない場合は `runners.docker_gid`（または `.env` の `DOCKER_GID`）に `getent group docker | cut -d: -f3` の値を設定します。
- **Job 内で `command not found` や SDK 不足**: セルフホスト runner には GitHub ホストのようなツールチェーンは同梱されていません。まず起動時セルフチェック（`docker compose logs runner-manager | grep 自检`）を確認してください。設定中の各 Runner イメージに `git`/`unzip`/`tar`/`curl` のどれが欠けているかを示します。言語・プラットフォーム SDK はイメージを拡張してください（[`examples/runner-images/`](../../examples/runner-images/)）。
- **古い Runner イメージ**: `docker rm -f github-runner-<name>` のあと、UI の「Start」で再作成。
- **status=unknown**: 詳細ポップアップの probe を確認。「Start/Stop」で自己修復を試す。

### イメージのローカルビルド

```bash
docker build -t runner-manager .
docker build -f Dockerfile.runner -t ghcr.io/soulteary/runner-fleet:v1.4.0-runner .
```

Make: `make docker-build`、`make docker-run`、`make docker-stop`。

---

## 2. 設定

```bash
mkdir -p config && cp config.yaml.example config/config.yaml
```

| フィールド | 説明 | デフォルト |
|------------|------|------------|
| `server.port` | HTTP サーバーポート | `8080` |
| `server.addr` | バインドアドレス。空なら全インターフェース | 空 |
| `runners.base_path` | Runner インストールディレクトリのルートパス。**コンテナでは `/app/runners` に設定** | `./runners` |
| `runners.items` | 事前定義 Runner 一覧 | Web UI からも追加可能 |
| `runners.container_mode` | コンテナモードを有効化 | `false` |
| `runners.container_image` | コンテナモード時の Runner イメージ（-runner タグ） | `ghcr.io/soulteary/runner-fleet:v1.4.0-runner` |
| `runners.container_network` | コンテナモード時の Runner ネットワーク | `runner-net` |
| `runners.agent_port` | コンテナ内 Agent ポート | `8081` |
| `runners.job_docker_backend` | Job 内 Docker: `dind` / `host-socket` / `none` | `dind` |
| `runners.dind_host` | `job_docker_backend=dind` 時の DinD ホスト名 | `runner-dind` |
| `runners.volume_host_path` | コンテナモード時の runners のホスト絶対パス（必須） | 空 |
| `runners.items[].container_image` | Runner ごとのイメージ上書き（コンテナモード）。空ならグローバル値 | 空 |
| `runners.items[].job_docker_backend` | Runner ごとの Docker バックエンド上書き（コンテナモード）。空ならグローバル値 | 空 |
| `runners.resources` | Runner コンテナのリソース上限（`cpus` / `memory` / `memory_swap` / `pids_limit`）。`docker create` に渡すほか、起動時に `docker update` で既存コンテナにも適用します | 空（無制限） |

上記の一部フィールドは環境変数で上書き可能（`MANAGER_PORT`、`CONTAINER_MODE`、`VOLUME_HOST_PATH`、`JOB_DOCKER_BACKEND` など）。フルコンテナ時は `.env` のみ変更すればよい。`.env.example` を参照。

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

**名前の衝突チェック**: 名前を入力している間にフォームが `/api/runner-precheck` を呼び、送信前に問題を提示します——設定に同名の Runner がある、正規化すると他とコンテナ名が同じになる、インストール先が他の Runner に使われている、登録済みの Runner が残ったディレクトリ（`.runner` がある）、ホストに同名のコンテナが残っている。送信を妨げるものは赤で表示し、ワンクリックで使える候補名を出します。警告（中身のあるディレクトリを再利用する）は続行できます。無理に送信してもサーバー側が **409** と同じ衝突情報で拒否します——従来の「黙ってランダムな接尾辞を付ける」動作は廃止しました（必要なら `auto_rename: true`）。

1 台のマシンに複数 Runner: 別々のサブディレクトリを使用。

---

## 4. セキュリティと検証

**認証**: デフォルトではログインなし。内部ネットワークまたは localhost でのみ使用推奨。Basic Auth を有効にするには環境変数 `BASIC_AUTH_PASSWORD` を設定。`BASIC_AUTH_USER` は任意（デフォルト `admin`）。`GET /health` 以外の全ルートで認証が必要。シークレットはコミットせず `.env` を使用。コンテナでは `-e BASIC_AUTH_PASSWORD=...` または compose の `env_file`。

**パスと一意性**: name/path に `..`、`/`、`\` は不可。ディレクトリは `runners.base_path` 以下である必要あり。名前の重複不可。編集時は名前は読み取り専用。コンテナモードでは名前はコンテナ名に正規化され、マッピング後の重複はエラーになります。

**Agent 認証**（コンテナモード）: Manager は Runner ごとにランダムなトークンを `<runner ディレクトリ>/.agent_token`（0600）へ書き込み、コンテナ作成時に `AGENT_TOKEN` として注入し、Agent 呼び出し時に `Authorization: Bearer` で送ります。Agent は環境変数を読むため、Manager と Agent の UID 一致に依存しません。ファイルは Manager 側の永続コピーです。Agent は `/status`、`/start`、`/stop` をトークンなしでは拒否します。`/health` は HEALTHCHECK 用に開放したままです。本機能より前に作成したコンテナはトークンがなく従来どおり動作します。再作成すると有効になります。

**機密ファイル**: config/config.yaml と .env は `.gitignore` に含まれています。各 Runner の `.github_check_token` は `chmod 600` を推奨。バージョン管理下にある場合は `.gitignore` に `**/.github_check_token` を追加。

[← プロジェクトホームへ](../../README.md)
