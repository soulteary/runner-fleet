# Windows 기본 서비스

대상 플랫폼은 Windows 11 / Windows Server 2022 x64이며 GitHub.com 저장소 및 조직 Runner를 지원합니다. Manager와 Runner는 Windows 서비스로 실행되며 WSL이나 Docker가 필요하지 않습니다.

## 설치 및 시작

Windows x64 릴리스 ZIP을 풀고 **관리자 PowerShell**에서 실행합니다:

```powershell
.\runner-manager.exe service install
.\runner-manager.exe service start
Start-Process http://127.0.0.1:8080
```

설치 시 프로그램을 `%ProgramFiles%\RunnerFleet\runner-manager.exe`로 복사하고 `LocalSystem` 계정의 자동 시작 서비스 `RunnerFleet`을 등록합니다. 기본 데이터 위치:

```text
%ProgramData%\RunnerFleet\config.yaml
%ProgramData%\RunnerFleet\runners\<name>\
%ProgramData%\RunnerFleet\tokens\
%ProgramData%\RunnerFleet\logs\manager-YYYY-MM-DD.log
```

사용자 설정은 `config.windows.yaml.example`을 전용 디렉터리에 복사하고 `service install -config "D:\RunnerFleet\config.yaml"`을 실행합니다. 경로는 절대 경로로 고정되며 상대 `base_path`는 설정 파일 디렉터리를 기준으로 합니다. 설정, Runner 기본 디렉터리와 프로그램 디렉터리는 시스템 및 관리자 전용 ACL로 보호되므로 전용 디렉터리를 사용하세요. 로그는 기본 ProgramData 위치에 남습니다.

## 웹에서 Runner 추가

GitHub에서 Settings → Actions → Runners → New self-hosted runner를 열고 Windows/x64를 선택합니다. `.\config.cmd --url https://github.com/owner/repo --token TOKEN`을 Manager에 붙여 넣고 분석한 뒤 이름과 레이블을 확인하여 추가합니다. 붙여 넣은 텍스트는 데이터로만 처리하며 스크립트로 실행하지 않습니다.

Manager가 공식 Windows ZIP을 다운로드하고 공식 SHA256을 검증한 후 압축을 풀고 `NT AUTHORITY\NETWORK SERVICE` 계정의 무인 서비스를 등록합니다. 서비스 작업 전 공식 `.service` 파일과 실행 파일 경로를 확인합니다.

시작은 `desired_state: running`을 저장하고 자동 시작과 복구를 활성화합니다. 중지는 `stopped`를 저장하고 자동 시작과 복구를 비활성화한 뒤 서비스 중지를 기다립니다. 중지 상태는 재부팅 후에도 유지됩니다. Manager 재시작이나 업그레이드는 Runner를 중지하지 않으며 상태를 모르면 자동 생성이나 재시작을 하지 않습니다.

등록 실패 후에는 새 GitHub 토큰을 사용하세요. 등록은 성공했지만 서비스 설정이 실패한 경우 시작으로 재등록 없이 복구합니다. 삭제 시 파일보다 먼저 서비스와 연결된 권한 그룹을 중지하고 제거합니다. 사용 가능한 PAT가 없으면 삭제 결과 안내에 따라 남은 GitHub 항목을 직접 삭제하세요.

## 빌드 환경과 유지보수

MSVC, Windows SDK, Git 등은 `NetworkService`가 사용할 수 있도록 미리 설치해야 합니다. 개인 프로필, 자격 증명 및 매핑된 네트워크 드라이브는 자동 공유되지 않습니다. 모든 Runner는 동일 계정을 사용하며 인스턴스별 계정 격리는 없습니다. 웹은 기본적으로 로컬 주소에 바인딩되므로 원격 접속 전 주소와 기존 인증을 설정하세요.

```powershell
.\runner-manager.exe service stop
.\runner-manager.exe service start
.\runner-manager.exe service uninstall
```

제거는 Manager 서비스만 삭제하고 파일, 설정 및 Runner 서비스는 보존합니다. 업그레이드하려면 Manager를 중지하고 설치된 실행 파일을 바꾼 뒤 다시 시작합니다. Runner 작업은 계속 실행됩니다.

## Windows 인수 검증

Windows CI에는 빌드, 모의 수명 주기 실패, 두 개의 기본 SCM 서비스 및 Manager 설치와 상태 검사가 구성되어 있습니다. 실제 GitHub 자격 증명과 재부팅은 대상 Windows에서 검증해야 합니다:

- 유효한 토큰으로 Runner 두 개를 등록하고 GitHub 온라인 상태와 각각의 실제 빌드를 확인합니다.
- 하나를 중지하고 다른 Runner가 계속 실행되는지 확인한 뒤 점검 주기와 Windows 재부팅 후에도 중지 상태가 유지되는지 확인합니다.
- 긴 작업 도중 Manager를 재시작하고 작업이 계속되는지 확인합니다.
- 비정상 종료 복구와 GitHub 중복 등록 없는 서비스 복구를 확인합니다.
- Runner 삭제 후 서비스, 권한 그룹, 디렉터리 정리와 GitHub 등록 해제 결과를 확인합니다.

## 개발 검사

백엔드는 Windows 프로그램에만 포함되며 기존 Linux 커버리지 검사는 Linux 운영 코드를 대상으로 합니다. Linux/macOS에서 설치기와 모의 제어기를 검사하려면:

```bash
go test -race -tags windowsrunner_portable ./internal/windowsrunner
node --test scripts/test-command-parser.cjs
```

관리자 Windows 세션에서 기본 SCM 검사를 실행합니다:

```powershell
$env:RUNNER_FLEET_SCM_TESTS = "1"
go test -race ./internal/windowsrunner
```
