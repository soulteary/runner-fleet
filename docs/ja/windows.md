# Windows ネイティブサービス

対象は Windows 11 / Windows Server 2022 x64 と GitHub.com のリポジトリ・組織 Runner です。Manager と Runner は Windows サービスとして動作し、WSL や Docker は不要です。

## インストールと起動

Windows x64 のリリース ZIP を展開し、**管理者 PowerShell** で実行します：

```powershell
.\runner-manager.exe service install
.\runner-manager.exe service start
Start-Process http://127.0.0.1:8080
```

プログラムを `%ProgramFiles%\RunnerFleet\runner-manager.exe` にコピーし、`NetworkService` の自動起動サービス `RunnerFleet` を登録します。既定のデータ配置：

```text
%ProgramData%\RunnerFleet\config.yaml
%ProgramData%\RunnerFleet\runners\<name>\
%ProgramData%\RunnerFleet\tokens\
%ProgramData%\RunnerFleet\logs\manager-YYYY-MM-DD.log
```

独自の設定では `config.windows.yaml.example` を専用ディレクトリにコピーし、`service install -config "D:\RunnerFleet\config.yaml"` を実行します。パスは絶対パスで固定され、相対 `base_path` は設定ファイルのディレクトリを基準に解決します。設定、Runner の基点、プログラムの各ディレクトリにシステム・管理者専用 ACL を適用するため、専用ディレクトリを使用してください。ログは既定の ProgramData に残ります。

## Web 画面から追加

GitHub の Settings → Actions → Runners → New self-hosted runner で Windows/x64 を選びます。`.\config.cmd --url https://github.com/owner/repo --token TOKEN` を Manager に貼り付けて解析し、名前とラベルを確認して追加します。貼り付けた内容はデータとして解析し、スクリプトとして実行しません。

Manager は公式 Windows ZIP を取得し、公式 SHA256 を検証して展開し、`NetworkService` で無人サービス登録します。制御前に公式 `.service` ファイルと実行ファイルのパスを確認します。

起動は `desired_state: running` を保存して自動起動と障害復旧を有効化します。停止は `stopped` を保存し、起動と復旧を無効化して停止を待ちます。停止状態は再起動後も維持します。Manager の再起動や更新は Runner を停止せず、状態不明時に自動作成や再起動もしません。

登録失敗時は新しい GitHub トークンを使用します。登録済みでサービス設定のみ失敗した場合、起動で再登録せずに修復できます。削除時はファイルの前にサービスと関連権限グループを停止・削除します。有効な PAT がなければ、削除結果の案内に従って GitHub の残存エントリを手動削除します。

## ビルド環境と保守

MSVC、Windows SDK、Git などは `NetworkService` が利用できる形で事前にインストールします。個人プロファイル、資格情報、割り当てたネットワークドライブは自動共有されません。Manager は LocalSystem、Runner は低権限の NetworkService を使用します。LocalSystem の Runner から移行する場合はジョブ完了後に停止、起動してください。全 Runner が NetworkService を共有し、個別のアカウント分離はありません。Web は既定でローカルのみ待ち受けるため、遠隔アクセス前にアドレスと既存の認証を設定します。

```powershell
.\runner-manager.exe service stop
.\runner-manager.exe service start
.\runner-manager.exe service uninstall
```

アンインストールは Manager サービスのみを削除し、ファイル、設定、Runner サービスは保持します。更新時は Manager を停止し、インストール済みの exe を交換して再起動します。Runner のジョブは継続します。

## Windows のプロキシを使う

`service install` 時に Manager はプロキシ環境変数を優先し、未設定ならインストールユーザーが有効にした手動 Windows システムプロキシ（Clash/v2ray など）を読み取ります。プロキシ設定だけを `config.yaml` 隣の `proxy.env` に保存し、Manager の ACL で保護します。サービス起動時に保存値を読み込み、デスクトップ設定は常時監視しません。プロキシを有効にしたユーザーの昇格端末を使用してください。PAC と SOCKS のみの設定は取り込まず、HTTP／混合ポートを使うか `proxy.env` を編集します。

既存環境やポート変更時は管理者 PowerShell で次を実行し、ジョブ完了後に対象 Runner を画面で停止、起動してください。独自設定ではインストール時と同じ `-config` パスを指定します：

```powershell
.\runner-manager.exe service proxy-import
.\runner-manager.exe service stop
.\runner-manager.exe service start
```

新しい Runner には登録前、既存 Runner には起動時にプロキシを設定します。実行中のジョブは自動再起動しません。Windows 再起動後もプロキシアプリが稼働し、そのローカルポートに NetworkService が接続できる必要があります。`proxy.env` のない既存 LocalSystem 環境は上記の取り込みが必要です。

Runner ごとに変更する場合は停止し、`<base_path>\<runner>\.env` の `Runner Fleet managed proxy` ブロック外に以下を追加して起動します：

```text
http_proxy=http://127.0.0.1:7890
https_proxy=http://127.0.0.1:7890
no_proxy=localhost,127.0.0.1
```

手動設定が優先され、他の `.env` 設定は保持されます。Manager の `proxy.env` も同形式で、編集後は Manager を再起動します。プロキシの資格情報は利用するジョブから参照できます。プロキシサービスの導入、個人認証の転送、各ビルドツール固有のプロキシ設定は行いません。

## Windows での受け入れ確認

Windows CI にはビルド、模擬ライフサイクル障害、ネイティブ SCM の二つのサービス、Manager のインストールと正常性検査が設定されています。実際の GitHub 資格情報と再起動は対象 Windows で確認します：

- 有効なトークンで Runner を二つ登録し、GitHub のオンライン状態と各 Runner の実ビルドを確認します。
- 一方を停止して他方の継続を確認し、巡回確認と Windows 再起動後も停止状態が維持されることを確認します。
- 長時間ジョブ中に Manager を再起動し、ジョブの継続を確認します。
- 異常終了からの復旧と、GitHub に重複登録しないサービス修復を確認します。
- Runner 削除後のサービス・権限グループ・ディレクトリの整理と GitHub 登録解除結果を確認します。

## 開発時の検査

バックエンドは Windows プログラムにのみ組み込まれ、既存の Linux カバレッジ検査は Linux の本番コードを対象とします。Linux/macOS でインストーラーと模擬コントローラーを検査するには：

```bash
go test -race -tags windowsrunner_portable ./internal/windowsrunner
node --test scripts/test-command-parser.cjs
```

管理者 Windows セッションでネイティブ SCM テストを実行します：

```powershell
$env:RUNNER_FLEET_SCM_TESTS = "1"
go test -race ./internal/windowsrunner
```
