# ユーザーガイド

**文档 / Docs:** [EN](../guide.md) · [中文](../zh/guide.md) · [Français](../fr/guide.md) · [Deutsch](../de/guide.md) · [한국어](../ko/guide.md) · 日本語

![](../../.github/assets/fleet.jpg)

デプロイ、設定、Runner の追加、セキュリティはここで説明します。コントリビューター向けのビルドと API は [開発とビルド](development.md) を参照してください。

---

## 1. デプロイ (Docker)

- **Linux のみ**、`linux/amd64` と `linux/arm64`。Runner が動いているかは `/proc` のプロセステーブルから読みます。ほかの OS ではすべての Runner が「停止中」と報告されるため、起動・停止も自動復帰も機能しません。公開イメージはこの 2 アーキテクチャを対象にしています。
- **画面は翻訳済み、メッセージは未翻訳。** UI の外枠は 6 言語ありますが、サーバーが返すもの——API メッセージ、トースト、ログ行——は現在すべて中国語です。セルフチェックのログ行には `[preflight …]` という接頭辞が付くので中国語が読めなくても拾えますが、本文は中国語です。これは既知の制約であり、訳し残しではありません。
- イメージは **Ubuntu** ベースで .NET Core 6.0 の依存関係を含み、**UID 1001** で実行されます。ホストにマウントするディレクトリはこのユーザーが書き込み可能である必要があります（例: `chown 1001:1001 config runners`）。
- 起動から約 15 秒後に、登録済みだが停止している Runner が自動で起動し、5 分ごとに定期チェックされます。

### 公開イメージを使う（推奨）

本番では特定バージョン（例: v1.7.1）を使用してください。開発時は `main` タグが使えます。

```bash
docker pull ghcr.io/soulteary/runner-fleet:v1.7.1
```

### docker-compose クイックスタート

リポジトリルートに `docker-compose.yml` があります。コンテナモードで Job に Docker が必要で `job_docker_backend: dind` のときだけ DinD を有効にしてください。

```bash
mkdir -p config runners && cp config.yaml.example config/config.yaml
# config/config.yaml を編集: runners.base_path を /app/runners に設定

sudo chown -R 1001:1001 config runners

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
  ghcr.io/soulteary/runner-fleet:v1.7.1
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
config/config.yaml の編集は不要。`cp .env.example .env` のあと、例: `CONTAINER_MODE=true`、`VOLUME_HOST_PATH=<runners のホスト絶対パス>`（`realpath runners` など）、`JOB_DOCKER_BACKEND=host-socket`、`CONTAINER_NETWORK=runner-net` を設定。`config/config.yaml` を用意しなくても、上記を `.env` に設定していれば初回起動時に自動生成されます。`RUNNER_IMAGE` を設定しない場合、Runner イメージは `MANAGER_IMAGE` から自動導出（例: v1.7.1 → v1.7.1-runner）。マウントする `config` と `runners` は引き続き `chown 1001:1001` が必要。詳細は `.env.example` のオーバーライド変数を参照。

**方法2: config/config.yaml で有効化**（`config.yaml.example` 参照）:

```yaml
runners:
  base_path: /app/runners
  container_mode: true
  container_image: ghcr.io/soulteary/runner-fleet:v1.7.1-runner
  container_network: runner-net
  agent_port: 8081
  job_docker_backend: dind   # dind | host-socket | none
  dind_host: runner-dind
  volume_host_path: /abs/path/on/host/to/runners
```

Runner イメージ: Manager と同じ名前で `-runner` タグ（本番はバージョン例 v1.7.1-runner、開発は main-runner）、またはローカルビルド: `docker build -f Dockerfile.runner -t ghcr.io/soulteary/runner-fleet:v1.7.1-runner .`。Manager はホストの Docker（`docker.sock` のマウント）を使う必要があり、`DOCKER_HOST` で DinD にはしないでください。Compose ではホストの docker GID 用に `group_add` または `user: "0:0"` を使用。`job_docker_backend: host-socket` の場合、Manager は Runner コンテナに `--group-add <ホストの docker GID>` を渡します（`docker.sock` から自動検出、`runners.docker_gid` / `DOCKER_GID` で上書き可）。イメージ側にも `docker` グループを用意しています（ビルド引数 `DOCKER_GID`、既定 999）。Runner 名はコンテナ名に正規化され、マッピング後の重複は衝突します。

**Runner イメージの拡張**: GitHub ホストの runner には Android SDK・Node・Python などのツールチェーンが同梱されていますが、セルフホストにはありません。`ubuntu-24.04` 向けに書かれた workflow はこれを暗黙に前提としていることが多く、移行後に `SDK location not found` などで失敗します。本リポジトリの Runner イメージの上に自分のツールチェーンを重ねてください。すぐ使える例と重要な四つの規則（/opt 配下は UID 1001 に chown、環境変数はイメージに埋め込む、パスワードなし sudo は継承、ウォームアップは `USER app` の後）は [`examples/runner-images/`](../../examples/runner-images/) にあります。`items[].container_image` で特定の Runner だけに適用し、workflow からは label で選択します。

**設定変更とコンテナの再作成**: イメージ・ネットワーク・マウント先・Job 内 Docker バックエンドは `docker create` の時点で確定し、既存のコンテナは作成時のまま動き続けます。設定を変えただけでは届きません。Manager は各コンテナの実際の作成パラメータを現在の設定と突き合わせます。**停止中**のコンテナが一致しない場合は次に起動するとき（「開始」をクリックした場合も Manager の自動起動の場合も）削除して作り直し、一覧にはその Runner に「設定変更あり」が付き、ツールチップに差分（例: `job_docker_backend: → dind`）が出ます。**実行中**のコンテナは自動では作り直しません（Job の最中かもしれないため）。すぐ反映したいときは行の「コンテナ再作成」（`POST /api/runners/:name/recreate`、実行中の Job は中断されます）を使うか、空いたときに停止して開始してください。同じ tag でイメージを作り直した場合も対象です（比較はイメージ ID）。

**すぐ使えるデプロイ例**: [`examples/deploy/`](../../examples/deploy/) には、そのままコピーして使える構成が 2 つあります。`standalone/`（Manager 1 コンテナで、Runner プロセスもその中。`docker run` でも Compose でも可）と `fleet/`（コンテナモード: Runner ごとに 1 コンテナ。イメージキャッシュはホストの daemon を共用することで共有、ツールチェーンと Action のキャッシュは Runner イメージのレイヤーに同梱、ビルドキャッシュは Runner ごとに分離）。README では両者の比較、どのキャッシュが共有でどれが分離されるか、デプロイで踏みやすい落とし穴（ディレクトリの所有者、`VOLUME_HOST_PATH`、host-socket でのディスク増加）をまとめています。

### トラブルシューティング

- **うまく動かないときはまず起動時セルフチェック**: `docker compose logs runner-manager | grep '\[preflight'`。起動時に runners ディレクトリ、Docker 到達性、ネットワーク、Runner イメージ、Job 内 Docker バックエンドを検査し、失敗項目にはそのまま実行できる修正コマンドが出ます。
- **compose down 後に Runner が起動しない**: 一度 `docker network create runner-net` を実行。まだ失敗する場合は UI の「Start」で再作成するか、`docker rm -f github-runner-<name>` のあと「Start」。
- **root で実行**: マウントしたディレクトリはプロセスユーザーが書き込み可能である必要あり。root の場合は `RUNNER_ALLOW_RUNASROOT=1` を設定。
- **Job 内で docker.sock が `permission denied`**: `job_docker_backend: host-socket` ではコンテナのユーザー（UID 1001）が socket の所有グループに属している必要があります。Manager はコンテナ作成時に検出したホストの docker GID で `--group-add` を付与します。GID が合っていないコンテナは「設定変更あり」と判定され、次回起動時に自動で作り直されます（実行中ならバッジが出るので「コンテナ再作成」を使ってください）。検出できない場合は `runners.docker_gid`（または `.env` の `DOCKER_GID`）に `getent group docker | cut -d: -f3` の値を設定します。
- **Job 内で `command not found` や SDK 不足**: セルフホスト runner には GitHub ホストのようなツールチェーンは同梱されていません。まず起動時セルフチェック（`docker compose logs runner-manager | grep '\[preflight'`）を確認してください。設定中の各 Runner イメージに `git`/`unzip`/`tar`/`curl` のどれが欠けているかを示します。言語・プラットフォーム SDK はイメージを拡張してください（[`examples/runner-images/`](../../examples/runner-images/)）。
- **古い Runner イメージ**: pull または再ビルドしてから Runner を起動すれば、Manager がイメージの変化（参照とイメージ ID の両方を見るので同じ tag の再ビルドも対象）を検出してコンテナを作り直します。実行中のコンテナには触れないので、中断してよいタイミングで行の「コンテナ再作成」を使ってください。
- **ログに 5 分ごとに `已定时拉起 runner: <名前>` が繰り返し出力され、UI でも「実行中」にならない**: 本バージョンで修正済みです。アップグレードするだけでよく、Runner の再登録は不要です。実行状態はこれまで pid ファイル（`Runner.Listener.pid`、なければ `.path`）から読んでいましたが、actions/runner はそのどちらも書きません。起動スクリプトのどこにも pid ファイルの書き出しはなく、`.path` の中身は PATH 文字列です。そのためすべての Runner が「登録済みだが未実行」と判定され、5 分ごとの巡回が毎回すべてを起動し直していました。現在はプロセステーブルを見て判定し、コンテナモードでは各コンテナ内の Agent に問い合わせます（Manager から他コンテナのプロセスは見えないため）。同じ原因で、デフォルト（非コンテナ）モードの「停止」は必ず `未找到 runner pid 文件或 pid 无效` で失敗していました。
- **UI で削除した Runner が GitHub 側に残り、同じ名前で追加し直すと登録に失敗する**: 削除時に GitHub からの登録解除も行うようになりました。ただしその Runner ディレクトリに `.github_check_token`（任意の PAT。組織は `admin:org`、リポジトリは `repo`）がある場合に限ります。無い場合は解除する手段がないため、削除レスポンスでその旨と Settings → Actions → Runners を案内します。旧バージョンで削除した Runner は解除されていないので手動で削除してください。
- **ある Runner が「GitHub 照会失敗」と表示される**: 照会は行ったが答えが得られなかった状態です（トークン期限切れ、権限不足、レート制限、対象が見えない——ホバーで原因を表示）。「GitHub 未表示」＝GitHub が応答し一覧に無かった、とは別物です。旧バージョンは前者も後者として報告していました。
- **status=unknown**: 詳細ポップアップの probe を確認。「Start/Stop」で自己修復を試す。

### イメージのローカルビルド

```bash
docker build -t runner-manager .
docker build -f Dockerfile.runner -t ghcr.io/soulteary/runner-fleet:v1.7.1-runner .
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
| `runners.container_image` | コンテナモード時の Runner イメージ（-runner タグ） | `ghcr.io/soulteary/runner-fleet:v1.7.1-runner` |
| `runners.container_network` | コンテナモード時の Runner ネットワーク | `runner-net` |
| `runners.agent_port` | コンテナ内 Agent ポート | `8081` |
| `runners.job_docker_backend` | Job 内 Docker: `dind` / `host-socket` / `none` | `dind` |
| `runners.dind_host` | `job_docker_backend=dind` 時の DinD ホスト名 | `runner-dind` |
| `runners.docker_gid` | `job_docker_backend=host-socket` のとき Runner コンテナに追加するホストの docker グループ GID。空または `0` で `docker.sock` から自動検出 | 空（自動検出） |
| `runners.volume_host_path` | コンテナモード時の runners のホスト絶対パス（必須） | 空 |
| `runners.items[].name` | 表示名。インストールディレクトリ名でもあり、コンテナモードではコンテナ名。一意で、作成後は変更不可 | 必須 |
| `runners.items[].path` | `base_path` 配下のサブディレクトリ。空なら `name` を使用 | 空（= `name`） |
| `runners.items[].target_type` | `org` または `repo` | 必須 |
| `runners.items[].target` | Organization 名、または `owner/repo` | 必須 |
| `runners.items[].labels` | カスタムラベル。workflow の `runs-on` がこれで選ぶ | 空 |
| `runners.items[].container_image` | Runner ごとのイメージ上書き（コンテナモード）。空ならグローバル値 | 空 |
| `runners.items[].job_docker_backend` | Runner ごとの Docker バックエンド上書き（コンテナモード）。空ならグローバル値 | 空 |
| `runners.resources` | Runner コンテナのリソース上限（`cpus` / `memory` / `memory_swap` / `pids_limit`）。`docker create` に渡すほか、起動時に `docker update` で既存コンテナにも適用します | 空（無制限） |

上記のうちコンテナデプロイで変更が必要なフィールドはすべて環境変数から与えられるため、フルコンテナ構成は `.env` だけで済みます——下の[環境変数](#環境変数)を参照。

### 環境変数

起動時に一度だけ読まれるため、変更には Manager の再起動が必要です。2 つの名前が同じ設定を指す
場合は表の順に読まれるので、両方を設定したときは**後の方**が有効になります。

| 変数 | 上書き対象 | 既定値 / 備考 |
|---|---|---|
| `MANAGER_PORT`、`SERVER_PORT` | `server.port` | `8080`。compose はコンテナ内の `SERVER_PORT` を固定し `MANAGER_PORT` をそこへマップするので、公開ポートと待ち受けポートは異なってよい |
| `SERVER_ADDR` | `server.addr` | 空（全インターフェース） |
| `RUNNERS_BASE_PATH` | `runners.base_path` | `./runners`。イメージ内では `/app/runners` |
| `CONTAINER_MODE` | `runners.container_mode` | `false`。`true` と `1` のみ読む: この変数はコンテナモードを**有効にはできるが無効にはできない**。書き間違いひとつでデプロイの形態が黙って変わらないようにするため |
| `RUNNER_IMAGE`、`CONTAINER_IMAGE` | `runners.container_image` | 未設定なら `MANAGER_IMAGE` から導出（`:v1.7.1` → `:v1.7.1-runner`）、次に `FLEET_IMAGE_TAG` |
| `CONTAINER_NETWORK` | `runners.container_network` | `runner-net` |
| `VOLUME_HOST_PATH`、`RUNNERS_VOLUME_HOST_PATH` | `runners.volume_host_path` | 空。コンテナモードでは必須 |
| `JOB_DOCKER_BACKEND` | `runners.job_docker_backend` | `dind` |
| `DOCKER_GID` | `runners.docker_gid` | 空 = `docker.sock` から検出 |

以下は設定ファイルに対応項目がありません:

| 変数 | 役割 | 既定値 |
|---|---|---|
| `BASIC_AUTH_PASSWORD` | 設定すると Basic 認証が有効になる | 空（認証なし） |
| `BASIC_AUTH_USER` | Basic 認証のユーザー名 | `admin` |
| `TRUSTED_ORIGINS` | クロスサイト判定を免除するオリジン（カンマ区切り）。[4. セキュリティと検証](#4-セキュリティと検証)を参照 | 空 |
| `LOG_LEVEL` | `trace` / `debug` / `info` / `warn` / `error` | `info` |
| `LOG_FORMAT` | 人が読むなら `console`、ELK や Loki へ送るなら `json`。未知の値は起動を失敗させず `console` に回帰 | `console` |
| `DOCKER_HOST` | **Manager 自身**が使う Docker デーモン。コンテナモードではホストの socket が必須——DinD を指すと Runner コンテナを作成できない | `unix:///var/run/docker.sock` |
| `MANAGER_IMAGE` | compose が取得する Manager イメージ。Runner イメージもここから導出される | リリースの tag |
| `FLEET_IMAGE_TAG` | ほかに決め手がないときの既定 Runner イメージの tag | `v1.7.1` |

`scripts/install-runner.sh` はさらに `RUNNER_VERSION`、`RUNNER_SHA256`、
`RUNNER_FORCE_REINSTALL` を読みます——[自動インストールと登録](#自動インストールと登録)を参照。

Runner コンテナ内の Agent は `AGENT_TOKEN`、`AGENT_PORT`、`RUNNER_INSTALL_DIR` を読みます。
3 つとも Manager がコンテナ作成時に注入するもので、手で設定することは通常のデプロイには
含まれません。


**検証**: 名前の重複不可。コンテナモードではコンテナ名の衝突をチェック。`job_docker_backend` は `dind`/`host-socket`/`none` のみ。コンテナモードでコンテナの `base_path` を使う場合は `volume_host_path` 必須。`job_docker_backend` を省略すると `dind`。バックエンド変更後、**停止中**のコンテナは次回起動時に自動で作り直され、**実行中**のものは「設定変更あり」と表示され、その行の「コンテナを再作成」で即座に反映されます——上記「設定変更とコンテナの再作成」を参照。

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

**登録結果**: その Runner ディレクトリの `.registration_result.json` に書き込み。**GitHub 表示チェック**（任意）: Runner ディレクトリに `.github_check_token`（PAT。組織は `admin:org`、リポジトリは `repo` が必要）を置くと約 5 分ごとにチェックし、結果は `.github_status.json` に書き込み。 同じチェックで GitHub 側の Runner が**ジョブ実行中**かどうかも記録し、一覧では「ジョブ実行中」バッジ、設定ダイアログでは独立した行として表示します。同じ約 5 分間隔のため最大 5 分遅れることがあり、PAT がない場合は「不明」のままで、アイドルとは報告しません。一覧の「登録済み」と「GitHub ✓」はいずれも対象の Runners 設定ページへのリンクです。

**名前の衝突チェック**: 名前を入力している間にフォームが `/api/runner-precheck` を呼び、送信前に問題を提示します——設定に同名の Runner がある、正規化すると他とコンテナ名が同じになる、インストール先が他の Runner に使われている、登録済みの Runner が残ったディレクトリ（`.runner` がある）、ホストに同名のコンテナが残っている。送信を妨げるものは赤で表示し、ワンクリックで使える候補名を出します。警告（中身のあるディレクトリを再利用する）は続行できます。無理に送信してもサーバー側が **409** と同じ衝突情報で拒否します——従来の「黙ってランダムな接尾辞を付ける」動作は廃止しました（必要なら `auto_rename: true`）。

**Runner の削除**: 停止し、その Runner のディレクトリに PAT があれば GitHub から登録解除し、**インストールディレクトリを削除します**——ただし、そのディレクトリが `runners.base_path` の下にある場合のみです。設定を誤ったパスがシステムディレクトリを巻き込まないようにするためです。`_work` とその下のキャッシュも一緒に消えるので、削除後に取り戻す手段はありません。

1 台のマシンに複数 Runner: 別々のサブディレクトリを使用。

---

## 4. セキュリティと検証

**認証**: デフォルトではログインなし。内部ネットワークまたは localhost でのみ使用推奨。Basic Auth を有効にするには環境変数 `BASIC_AUTH_PASSWORD` を設定。`BASIC_AUTH_USER` は任意（デフォルト `admin`）。`GET /health` と `GET /ready` 以外の全ルートで認証が必要。シークレットはコミットせず `.env` を使用。コンテナでは `-e BASIC_AUTH_PASSWORD=...` または compose の `env_file`。

**クロスサイトリクエスト**: 書き込み系エンドポイントはブラウザがクロスサイトと報告したものを拒否するため、別オリジンのページがあなたのキャッシュ済み Basic 認証情報でこの API を操作することはできません。設定は不要です。リバースプロキシが `Host` を書き換えた結果、自分のリクエストまで拒否される場合は、ブラウザに見えるオリジンを `TRUSTED_ORIGINS` にカンマ区切りで指定してください。ブラウザ以外の呼び出し元（curl、CI スクリプト）は影響を受けません——キャッシュされた資格情報を持たないため、ここでの攻撃者にはなり得ないからです。正確な規則は[開発ドキュメント](development.md)を参照。

**パスと一意性**: name/path に `..`、`/`、`\` は不可。ディレクトリは `runners.base_path` 以下である必要あり。名前の重複不可。編集時は名前は読み取り専用。コンテナモードでは名前はコンテナ名に正規化され、マッピング後の重複はエラーになります。

**Agent 認証**（コンテナモード）: Manager は Runner ごとにランダムなトークンを `<runner ディレクトリ>/.agent_token`（0600）へ書き込み、コンテナ作成時に `AGENT_TOKEN` として注入し、Agent 呼び出し時に `Authorization: Bearer` で送ります。Agent は環境変数を読むため、Manager と Agent の UID 一致に依存しません。ファイルは Manager 側の永続コピーです。Agent は `/status`、`/start`、`/stop` をトークンなしでは拒否します。`/health` は HEALTHCHECK 用に開放したままです。本機能より前に作成したコンテナはトークンが注入されておらず、認証なしのまま動作します。現在はこれを「設定変更あり」と判定し、次回起動時に自動で作り直して補います（実行中なら「コンテナ再作成」で）。

**機密ファイル**: config/config.yaml と .env は `.gitignore` に含まれています。各 Runner の `.github_check_token` は `chmod 600` を推奨。バージョン管理下にある場合は `.gitignore` に `**/.github_check_token` を追加。

**Runner ディレクトリの権限**: 各 Runner のインストールディレクトリは 0700 で作成されます。`config.sh` はそこに `.credentials_rsaparams`（Runner が GitHub に対して身元を示す RSA 秘密鍵）を書き込みますが、actions/runner はこれらのファイルに Unix パーミッションを設定しないため、ディレクトリの権限ビットが、ホスト上の他のローカルユーザーによる読み取りと Runner のなりすましを防ぐ最後の砦になります。**旧バージョンで作成されたディレクトリは 0755 のままです**。起動時セルフチェック（`docker compose logs runner-manager | grep 自検`）が該当ディレクトリを列挙し、そのまま実行できる `chmod 700` を提示します。自動では変更しません: UID が食い違う構成（Manager が root、コンテナ内が app(1001)）で権限を絞ると動いている構成が壊れるため、確認してから実行してください。

---

## 5. 運用

### プローブ

| パス | 用途 |
|---|---|
| `GET /health` | 存活。プロセスが生きている限り 200 で、依存チェックは一切しない——設定が壊れていてもマウントが使えなくても 200 のまま。K8s の `livenessProbe` 向け: 再起動はハングしたプロセスへの正しい答えであり、壊れた設定への誤った答えです |
| `GET /ready` | 就緒。設定が読めない、または `runners.base_path` が無い・書き込めないときは 503——実際に書き込みプローブを行うので、`/health` では見えないディレクトリ所有者の誤りを捕まえます。K8s の `readinessProbe` 向けで、デプロイを変更したあとに確認すべきはこちら |

Basic 認証を有効にしても、この 2 つは認証不要のままです。プローブは資格情報を持てないからです。
どちらも*どの*チェックが失敗したかは言いません。それはログにあります。

### メトリクス

`GET /metrics` は Prometheus テキストを返します——ルートごとの呼び出し量とレイテンシ。`path`
ラベルはリクエスト URL ではなく Echo のルートテンプレート（`/api/runners/:name`）なので、
Runner の数だけラベル値が増えることはありません。

プローブと違い、Basic 認証が有効なとき `/metrics` は**認証を要求します**。全エンドポイントの
呼び出し量を露出する運用データであり、`/api` より公開される理由がないからです。スクレイプ設定は
それに合わせてください:

```yaml
scrape_configs:
  - job_name: runner-fleet
    static_configs:
      - targets: ['runner-manager:8080']
    basic_auth:
      username: admin
      password: <BASIC_AUTH_PASSWORD>
```

### アップグレード

```bash
docker compose pull && docker compose up -d
```

Runner を登録し直す必要はありません。Runner の身元は各自のインストールディレクトリにあり、アップグレードはそこに触れません。

コンテナモードでは、Runner コンテナは依然として**古い** Runner イメージで作られたままです。
イメージ、ネットワーク、マウントディレクトリなどは `docker create` の時点で確定するからです。
Manager はそれを自分で見つけて直します: **停止中**の Runner は次回起動時に作り直され、
**実行中**のものは「設定が変更されました」と印が付き、「コンテナを再作成」を押すか、
空いているときに停止して起動すると作り直されます。新しいイメージが実際に手元にあるかどうかは、
次の 2 点で決まります:

- **バージョン tag** は自分で取得されます。バージョンを上げた後の新しい `-runner` tag は
  ローカルに無いので `docker create` が取りに行きます。
- **可変 tag**（`:main`、または同じバージョン tag を再ビルドしたもの）はローカルで既に解決できるため、
  `docker create` は古いイメージをそのまま使います。先に自分で `docker pull <Runner イメージ>` を
  実行してください。ドリフトは参照だけでなくイメージ **ID** でも比較するので、pull が済めば再作成は
  いつもどおり起きます。

`runners.resources` だけは、作り直さずに既存のコンテナへ届く設定です。Manager が起動時に
`docker update` で適用するので、リソース上限に対応したバージョンへ上げるために全部を作り直す
必要はありません。

上げ先のバージョンの[変更履歴](../../CHANGELOG.md)を読んでください——破壊的変更と手作業が要る項目はそこに書かれています。

### 何をバックアップするか

README には「設定がバックアップだ」とあります。これは*設定*には当てはまりますが*身元*には
当てはまりません。Runner の資格情報はそのインストールディレクトリにあり、それが無ければ復元した
デプロイは一つずつ登録し直すしかありません。

`config/config.yaml` と、各 `runners/<名前>/` ディレクトリ（`_work/` を除く）をバックアップしてください。

| `runners/<名前>/` の中身 | 書き手 | 失うと |
|---|---|---|
| `.runner`、`.credentials_rsaparams` など `config.sh` が書いたファイル | actions/runner | その Runner は失われます。登録し直しが必要で、先に GitHub 側の古いエントリを削除してください——古いものが一覧に残っている間は、同じ名前での再登録は失敗します |
| `.agent_token` | Manager（パーミッション `0600`） | 再生成されます。そのコンテナはドリフトと判定され、次回起動時に作り直されます |
| `.github_check_token` | 任意で自分が置く | 可視性チェックが止まり、Runner を削除しても GitHub から登録解除できなくなります |
| `.registration_result.json`、`.github_status.json` | Manager | 影響は表示だけ——次の登録またはチェックで作り直されます |
| `_work/` | Job 自身 | 残す価値はありません。チェックアウトとビルド成果物で、ディスク上で最大かつ増え続ける部分です |

復元では所有者が重要です。最終的にすべてが UID 1001 の所有でなければならず、初回導入時と同じ
`sudo chown -R 1001:1001 config runners` を実行します。`GET /ready` は runners ディレクトリへ
実際に書き込みプローブを行うので、復元したものが本当に使えるかを確かめる一番早い方法です。

### リバースプロキシの背後に置く

Manager は素の HTTP しか話さず TLS を自前で持たないため、TLS はプロキシで終端します。ここでは
それが普段以上に重要です: Basic 認証はリクエストごとにパスワードを送るので、TLS が無ければ
毎回それを平文でネットワークに流すことになります。

クロスサイト判定に設定は要りません。読んでいる `Sec-Fetch-Site` はブラウザがローカルで算出する値で、
プロキシが `Host` を書き換えても壊れません。`TRUSTED_ORIGINS` は万一それでも誤判定された場合の
逃げ道です——ブラウザのアドレスバーに見えるとおりのオリジンをカンマ区切りで書き、
自分が管理しているオリジンだけに限ってください。[4. セキュリティと検証](#4-セキュリティと検証)を参照。

プロキシのヘルスチェックは `/health` へ、レディネスゲートがあるなら `/ready` へ向けてください。
どちらも認証不要のままです。`/metrics` は公開しないでください: Basic 認証が有効なら資格情報が要り、
無効ならほかと同じように開いています。

### バージョンとログ

`GET /version` はバージョンだけを返します。ビルド詳細は意図的に含めていません: `go_version` が
あると、公表済みの Go ランタイム CVE を、あなたが実行している正確なランタイムに結び付けられて
しまいます。`runner-manager -version` は commit、ビルド日時、Go バージョン、プラットフォームを
表示しますが、こちらはホスト上で実行するもので外部には出ません。

ログは `LOG_LEVEL` と `LOG_FORMAT` で制御します。ELK や Loki へ送るなら `LOG_FORMAT=json`。
起動時セルフチェックは項目ごとに 1 行を出力し、各行の先頭は `[preflight …]` ——
[トラブルシューティング](#トラブルシューティング)全体で使う grep のアンカーがこれです。

[← プロジェクトホームへ](../../README.md)
