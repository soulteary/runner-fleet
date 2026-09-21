# 開発とビルド

**文档 / Docs:** [EN](../development.md) · [中文](../zh/development.md) · [Français](../fr/development.md) · [Deutsch](../de/development.md) · [한국어](../ko/development.md) · 日本語

![](../../.github/assets/fleet.jpg)

本番環境ではコンテナデプロイを使用してください。[ユーザーガイド](guide.md) を参照。このドキュメントはコントリビューター向け: ローカルビルドとデバッグです。

## 要件

- Go 1.27（[go.mod](../../go.mod) と一致）。

## ビルド

```bash
# runner-manager バイナリをビルド
go build -o runner-manager ./cmd/runner-manager

# バージョン付き（/version とデバッグ用）
go build -ldflags "-X main.Version=1.7.1" -o runner-manager ./cmd/runner-manager

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

Basic Auth 有効時、`/health` と `/ready` 以外のリクエストには Header に `Authorization: Basic <base64(user:password)>` が必要です。

| パス | メソッド | 説明 |
|------|----------|------|
| `/health` | GET | 存活（liveness）。`{"status":"ok","service":"runner-fleet"}` を返す。依存チェックはなく、プロセスが生きている限り常に 200。常に認証不要。 |
| `/ready` | GET | 就緒（readiness）。ボディ形状は同じ。設定が読めない、または runner のベースディレクトリが無い・書き込めない場合は 503。K8s の `readinessProbe` 用。同じく認証不要で、どの項目が失敗したかは返さない。 |
| `/version` | GET | `{"version":"..."}` を返す。 |
| `/metrics` | GET | Prometheus メトリクス（リクエスト数とレイテンシ）。`path` ラベルはリクエスト URL ではなく Echo のルートテンプレート（`/api/runners/:name`）。Basic Auth 有効時は**認証が必要**——スクレイプ設定に `basic_auth` を指定。 |
| `/api/runners` | GET | Runner 一覧。コンテナモードで probe 失敗時は `status=unknown` と構造化された `probe`（`error/type/suggestion/check_command/fix_command`）を返す。 |
| `/api/runners/:name` | GET | 単一 Runner の詳細。コンテナモードで probe 失敗時も同様に `probe`。 |
| `/api/runners/:name/start` | POST | Runner を起動。probe 失敗時も起動を試み、レスポンスに構造化された `probe` を返す。 |
| `/api/runners/:name/stop` | POST | Runner を停止。probe 失敗時も停止を試み、レスポンスに構造化された `probe` を返す。 |
| `/api/runners` | POST | Runner を追加（任意でインストールと登録）。名前が衝突した場合は黙って改名せず **409** を返し、`conflicts` と `suggested_name` を含めます。従来の自動リネームが必要なら `auto_rename: true` を送ってください。 |
| `/api/runners/:name` | DELETE | Runner を削除：停止し、PAT があれば GitHub から登録解除し、インストールディレクトリを削除し、設定から取り除きます。レスポンスには `github_deregistered` と、GitHub 側で何が起きたかを伝える `message` が入ります。 |
| `/api/runners/:name/recreate` | POST | 現在の設定で Runner コンテナを削除して作り直します（コンテナモードのみ）。実行中の Job は中断されます。停止中のコンテナは「開始」時に作成パラメータの不一致を検出して自動で作り直されます。 |
| `/api/runner-precheck` | GET | 追加前の名前チェック: `?name=&path=`。`available`、`suggested_name`、検出した `conflicts`（`name_taken`、`container_name`、`install_dir`、`dir_registered`、`dir_adopt`、`dir_exists`、`container_exists`）を返します。各項目は `level`（`error`/`warn`）、`message`、`detail`、任意の `fix_command` を持ちます。読み取り専用で、画面は入力中に随時呼び出します。 |
| `/api/runner-rows` | GET | 一覧の `<tbody>` だけを、初回描画と同じテンプレート断片からレンダリングします。画面はこれをポーリングして一覧をその場で更新します。 |
| `/static/*` | GET, HEAD | 埋め込みのスタイルシートとスクリプト（`//go:embed`）。URL は内容のフィンガープリント（`?v=<hash>`）を持ち、付きは長期キャッシュ、無しは再検証になります。死活監視やプロキシが 405 を受け取らないよう、`HEAD` も `GET` と併せて登録しています。 |

### クロスサイトリクエスト（CSRF）

書き込み系エンドポイント（`POST`・`PUT`・`DELETE`）は、ブラウザがクロスサイトと報告したリクエストを拒否します。
この防御がないと、別オリジンのページから `POST /api/runners` へフォームを送信できてしまいます。これは CORS でいう
「単純リクエスト」なのでプリフライトなしに送信され、ブラウザはこのオリジン向けにキャッシュ済みの Basic 認証情報を
自動的に添えます。つまり管理者に悪意あるページを開かせるだけで、Runner の追加や停止ができてしまう状態でした。
`PUT` と `DELETE` は必ずプリフライトされるため元々露出していません。露出していたのは `POST` で、
`/api/runners/:name/{start,stop,recreate}` はいずれも POST です。

判定はまず `Sec-Fetch-Site` を見ます。これはブラウザがローカルに算出するため、リバースプロキシが `Host` を
書き換えても壊れません（Chrome 76+、Firefox 90+、Safari 16.4+）。通過するのは `same-origin` のみで、
`same-site` は通しません——サブドメインや別ポートは別オリジンであり、ここは管理画面だからです。
このヘッダーを送らない古いブラウザでは、`Origin` とリクエストの `Host` の比較にフォールバックします。
現行のブラウザはクロスサイト POST で必ず `Origin` を送ります。

どちらのヘッダーもないリクエストはブラウザ発ではありません（curl、CI スクリプト）。cookie もキャッシュされた
Basic 認証情報も持たないため CSRF になり得ず、そのまま通します——API はスクリプトから叩けるままで、
トークンや追加ヘッダーはどこにも必要ありません。

結果として 2 点:

- **リクエストボディは JSON のみ。** `AddRunnerRequest` と `UpdateRunnerRequest` には `form` タグがないため、
  仮にミドルウェアをすり抜けてもフォームエンコードのリクエストは空の構造体にしかバインドされず、
  必須チェックで弾かれます。Web UI は元々 JSON で送信しています——`FormData` はフォーム要素から
  値を読み出すためだけに使っています。
- **`TRUSTED_ORIGINS` が脱出ハッチ。** リバースプロキシが `Host` を書き換えた結果、自分のリクエストまで
  拒否される場合は、ブラウザのアドレスバーに見えるオリジンをカンマ区切りで指定します
  （`https://ci.example.com`）。ここに挙げたオリジンは `Sec-Fetch-Site` の値によらず許可されるので、
  自分が管理するオリジンだけを入れてください。

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

### 実行状態の判定方法

`internal/runnerproc` は「この Runner のプロセスは生きているか」に、`/proc` を走査して argv が
そのインストールディレクトリを名乗るプロセス——`<dir>/bin/Runner.Listener`、あるいは
`<dir>/run.sh` や `<dir>/run-helper.sh` を実行中のシェル——を探すことで答えます。

pid ファイルは意図的に読みません。actions/runner がそもそも書かないからです。`run.sh`、
`run-helper.sh.template`、`runsvc.sh` のいずれも pid をどこにも書かず、pid はシェル変数の中にしか存在しません。
インストールディレクトリの `.path` に入っているのは PATH 文字列であって pid ではありません
（`runsvc.sh` 自身が `export PATH=$(cat .path)` としています）。どちらの名前を読んでも必ず失敗するため、
すべての Runner が永久に「登録済みだが未実行」と報告されていました。

`Runner.Listener` が一瞬いなくても、監督シェルは実行中として数えます。`run-helper.sh` は終了コード 2 で
5 秒 sleep し、その後 `run.sh` がリスナーを起動し直すため、リスナーだけを見る判定では再起動のたびに
Runner を死んだと判断してしまいます。

呼び出し側への影響は 2 つ:

- `runner.List` はディスク視点のビューです。コンテナモードではその `Running` は常に false——Manager と
  Runner は別の PID 名前空間にいます。判定が動作の引き金になる場面では常に
  `runner.ListWithLiveStatus` を使ってください。各コンテナの Agent に問い合わせ、探査失敗を `installed`
  ではなく `status=unknown` にマップするため、「登録済みで未実行だから起動する」が到達できない Runner に
  対して発火することはありません。
- 検出には `/proc` が必要なので Linux 限定です。それ以外では「未実行」と報告します。

### Runner ディレクトリのパーミッション

Runner のインストールディレクトリは 0700 で作成されます。`config.sh` はそこに
`.credentials_rsaparams`——Runner が GitHub に認証するための RSA 秘密鍵——を書き込みますが、
actions/runner は Unix パーミッションを一切設定しません（`ConfigurationStore` は Windows の Hidden 属性を
立てるだけなので、ファイルは umask に従い通常 0644 になります）。したがってディレクトリのモードが、
その鍵とホスト上の他のローカルユーザーとの間に立つ唯一の壁です。

`MkdirAll` は既存ディレクトリのモードを変更しないため、旧バージョンが作ったディレクトリは 0755 のままです。
`Preflight` はそれらをその場で変更するのではなく、すぐ実行できる `chmod 700` を添えて報告します。
Manager とコンテナが別 UID で動いている状況で締め付けると、現に動いている構成を壊しかねず、
その判断は人間がすべきものだからです。

`base_path` 自体は通過可能なままにしています——資格情報を置かない場所であり、
コンテナモードではホスト側のマウントポイントだからです。

これは Job が到達できる範囲を変えるものではありません。`job_docker_backend: host-socket` では Job は
ホストの任意のパスをバインドマウントでき、その点はデプロイ文書が既に警告しています。
ここでのモードが守るのは同一ホスト上の他のローカルユーザーに対してであって、そちらではありません。

### Runner の削除と GitHub

`DELETE /api/runners/:name` は Runner を本ツールから**および** GitHub からも削除します。GitHub 側には
資格情報が必要ですが、本ツールが持ち得る唯一のものは Runner ごとの任意の PAT——
`<runner ディレクトリ>/.github_check_token`、可視性チェックが使うのと同じファイルです。そのため登録解除は
インストールディレクトリを削除する**前**に実行されます。トークンがそこに置かれているからで、
順序を誤ると登録解除する手段そのものを黙って失います。

PAT がなければ登録解除はできません。GitHub は PAT か新しい removal token を要求し、
`config.sh remove` は後者を要求します。その場合はレスポンスでその旨を伝え、手作業で削除する場所を示します。
黙って飲み込まずに伝える価値があります——取り残された Runner があると、次の
`config.sh --name <同じ名前>` が `A runner exists with the same name` で失敗するからです。

`registered_on_github` は**null 許容**の bool です。`true` / `false` は答えであり、`null` は
答えに到達できなかったことを意味し、理由は `github_check_error` が持ちます。テンプレートで
`{{if .RegisteredOnGitHub}}` と判定してはいけません——`html/template` はポインタを nil かどうかだけで
判断するため、`false` を指すポインタも真になります。`RunnerInfo` の `GitHubYes` / `GitHubNo` /
`GitHubUnknown` ヘルパーを使ってください。

## Makefile ターゲット

- `make help`: 全ターゲットを表示。
- `make build`: Manager をビルド（Version ldflags 付き）。
- `make build-agent`: Runner Agent をビルド（コンテナモード用）。
- `make build-all`: Manager と Agent をビルド。
- `make test`: テストを実行。
- `make test-race`: レース検出付きでテストを実行（CI が実行するもの）。
- `make lint`: `./...` に golangci-lint を実行。CI の Test job の Lint ステップに対応。
- `make check`: CI が確認する内容を一つのターゲットに集約——gofmt、vet、lint、`-race` テスト、および 2 つの整合性チェック。push 前にこれを実行。
- `make run`: Manager をビルドしてから実行。
- `make docker-build` / `make docker-run` / `make docker-stop`: Manager イメージのビルドと実行。[ユーザーガイド](guide.md) 参照。
- `make docker-build-runner`: コンテナモード用 Runner イメージをビルド（`Dockerfile.runner`、デフォルトタグは `RUNNER_IMAGE`）。
- `make docker-build-runner-example`: カスタム Runner イメージの例をビルド（`EXAMPLE=android|node`、[`examples/runner-images/`](../../examples/runner-images/) 参照）。
- `make clean`: ビルドしたバイナリを削除（runner-manager、runner-agent）。

コンテナモードでは `cmd/runner-agent` の Agent と `Dockerfile.runner` の Runner イメージを使用します。

## テスト

`go test ./...`、または CI が実際に実行する `make test-race`。すべての CI とリリースの workflow が
`go test -race` を実行するので、データ競合は後から本番で「たまに状態がおかしい」として現れるのではなく、
その場でビルドを失敗させます——このプロジェクトの並行性（単一ワーカーの登録キュー、Runner ごとの
`runnerOps` ロック、`EnsureAgentToken` の単一勝者による生成）は、型システムが強制しない規約の上に
成り立っているためです。リポジトリの golangci-lint は CI で走ります。ローカルで実行できない場合は、
`go run` 経由の `errcheck` と `staticcheck` が指摘内容の大半をカバーします。

テストを追加する前に知っておくとよい慣例:

- **プロセス検出は実プロセスに対して検証します**。偽の `/proc` ではありません（`internal/runnerproc`、
  `internal/runner`、`cmd/runner-agent`）。ブロックする使い捨ての `run.sh` を Runner の代役にしますが、
  `exec` してはいけません——exec するとシェルが置き換わり、argv が `internal/runnerproc` の探す形と
  一致しなくなります。
- **`cmd/runner-manager` のテストは `main()` が呼ぶ関数を通して配線に到達します**（`basicAuthMiddleware`、
  `httpErrorHandler`、`registerRoutes`、`listenAddr`、`loadI18n`）。ルートを追加したら
  `TestRegisterRoutes_AllEndpointsPresent` も更新してください。これは集合そのものを表明しており、
  新しいルートに認証が要るかどうかを考える契機になります。
- **ミドルウェアは単体だけでなく `newEchoServer()` 越しにテストします。** 正しく書けているのに
  マウントされていない、というのがこの種の防御の典型的な壊れ方なので、
  `TestCSRFGuardIsMountedOnWriteRoutes` は実際のルートテーブルを叩きます。書き込み系ルートを
  追加したら、そこの `writeRoutes` にも追加してください。
- **i18n ファイルは相互にチェックされます。** `en.json` にあって他にないキーは黙って空白として描画されるため、
  6 言語すべてでキー集合が一致すること、値が空でないこと、テンプレートが参照するキーがすべて存在することを
  テストが表明します。
- **テンプレート由来の不具合にはテンプレートレベルのテストを。** 過去 2 件のバグは Go ではなく
  `index.html` に潜んでいました——`*bool` に対する `{{if}}` が `false` を指すポインタを真と読んだ件と、
  `innerHTML` 代入が `escapeHtml` を飛ばした件です。どちらも実テンプレートを描画するか走査することで
  カバーしています。Go のヘルパーだけをテストしても、どちらにも気づけないからです。

- **ドキュメントもコードと同じように検査される。** `internal/docsconsistency` にはテストだけがあり、
  ランタイムコードはありません。ci-recipes が見る見出しレベルの**下**——表の行、コードブロック、
  リスト項目、解決後のリンク先——で各訳文を英語原文と比べます。実際に起きた乖離が見出しではなく
  表の 1 行だったからです。さらに、Markdown 以外のファイルから参照されるパスが実在すること、
  トラブルシューティングが `grep` を勧めるマーカーがコードの出力そのものであること、
  `examples/` 配下のすべての README に言語方針が登録されていることも検査します。

## リリース

ドキュメントと例のバージョン参照は `internal/config/config.go` のデフォルトイメージタグと一致させる必要があります。CI は `ci-recipes runner-fleet check-version-consistency` で検証します。リリース PR を開く前にローカルで実行してください:

```bash
ci-recipes runner-fleet check-version-consistency
```

両方のチェックは [soulteary/ci-recipes](https://github.com/soulteary/ci-recipes) が提供します。各リポジトリ
固有の CI シェルをテスト済みの単一 Go バイナリに置き換えるもので、本リポジトリは
`scripts/ci-recipes.conf` だけを持ちます。CI が固定しているバージョンを入れてください:

```bash
make install-ci-recipes
```

固定値は `.github/workflows/ci-consistency.yml` の 1 か所だけにあり、Makefile はそこから読みます。

古いバージョンを正当に引用する行（リリースノート、アップグレード手順）には `version-check-ignore` マーカーを付けます。

翻訳版にも同じ仕組みがあります。`ci-recipes runner-fleet check-docs-structure` は各 `docs/<lang>/*.md` の
見出しレベルの並びを英語版と突き合わせます——見出しの文言は違って当然ですが、構造は違ってはいけません。
英語版に節を追加して 5 言語の翻訳が追従していない場合、見過ごされずにその場で PR が失敗します:

```bash
ci-recipes runner-fleet check-docs-structure
```

二つの検査は PR ごとではなくリリース時に動きます。`.github/actions/check-release-version` は
`v*.*.*` タグで動き、`internal/config/config.go` の基準バージョンがそのタグと等しく、かつ
`CHANGELOG.md` に対応する `## [X.Y.Z]` 節とリンク定義があることを要求します。
`check-version-consistency` にはこれが見えません。その基準自体を真実として扱うため、
公開済みリリースより 1 パッチ遅れていても内部的に整合していればそのまま通ります——
あるリリースが出たのにツリーのどこもそれを指していなかったのは、まさにこれが理由です。
`CI (Consistency)` の `Quick start runs` は、ローカルでビルドしたイメージに対してガイドの
クイックスタートのコマンド列をそのまま実行し、`GET /ready` を要求します。
誰も実行しない手順書は、壊れていることを誰も知らない手順書だからです。

[← ドキュメントへ戻る](README.md)
