# 사용 가이드

**文档 / Docs:** [EN](../guide.md) · [中文](../zh/guide.md) · [Français](../fr/guide.md) · [Deutsch](../de/guide.md) · 한국어 · [日本語](../ja/guide.md)

![](../../.github/assets/fleet.jpg)

배포, 설정, Runner 추가, 보안은 여기서 다룹니다. 기여자용 빌드 및 API는 [개발 및 빌드](development.md)를 참조하세요.

---

## 1. 배포 (Docker)

- **Linux 전용**, `linux/amd64`와 `linux/arm64`. Runner가 실행 중인지는 `/proc`의 프로세스 테이블에서 읽습니다. 다른 OS에서는 모든 Runner가 "실행 중 아님"으로 보고되므로 시작·중지도 자동 복구도 동작할 수 없습니다. 배포된 이미지는 이 두 아키텍처를 지원합니다.
- **화면과 그 메시지는 언어를 따르고, 로그는 영어로 고정입니다.** UI와 서버가 돌려주는 것——API 메시지와 토스트——은 `?lang=`, 언어 cookie, `Accept-Language` 순으로 정해지며, `Accept-Language`를 보내지 않는 스크립트는 영어를 받습니다. 로그가 영어인 것은 번역이 덜 된 것이 아니라 정해진 것입니다. 로그 줄에는 따라갈 요청이 없고, 읽는 사람은 운영자이며, grep과 Loki 질의와 알림 규칙은 같은 사건이 실행할 때마다 다른 언어로 나타나면 망가집니다. 시작 자가 점검도 로그로 나가며 줄 앞에 `[preflight …]`가 붙습니다.
- 이미지는 **Ubuntu** 기반이며 .NET Core 6.0 의존성이 포함되어 있습니다. **UID 1001**로 실행되며, 호스트에 마운트된 디렉터리는 해당 사용자가 쓸 수 있어야 합니다(예: `chown 1001:1001 config runners`).
- 시작 후 약 15초 뒤에 등록되었지만 중지된 Runner가 자동으로 시작되며, 5분마다 주기적으로 검사합니다.

### 공개 이미지 사용 (권장)

운영 환경에서는 특정 버전(예: v1.8.0)을 사용하세요. 개발 시에는 `main` 태그를 쓸 수 있습니다.

```bash
docker pull ghcr.io/soulteary/runner-fleet:v1.8.0
```

### docker-compose 빠른 시작

저장소 루트에 `docker-compose.yml`이 있습니다. 컨테이너 모드에서 Job에 Docker가 필요하고 `job_docker_backend: dind`일 때만 DinD를 활성화하세요.

```bash
mkdir -p config runners && cp config.yaml.example config/config.yaml
# config/config.yaml 편집: runners.base_path를 /app/runners로 설정

sudo chown -R 1001:1001 config runners

docker network create runner-net 2>/dev/null || true
docker compose up -d
# job_docker_backend: dind인 경우: docker compose --profile dind up -d
```

UI: http://localhost:8080. 인증 세부사항은 [4. 보안 및 검증](#4-보안-및-검증)을 참조하세요.

### 컨테이너 실행 (전체 인자)

`config` 디렉터리와 `runners`를 마운트하세요(디렉터리만 마운트하고 config/config.yaml 단일 파일은 마운트하지 마세요. 없으면 Docker가 빈 파일을 만들어 실행이 실패합니다). 포트는 설정의 `server.port`와 일치해야 합니다(기본 8080).

```bash
docker run -d --name runner-manager \
  -p 8080:8080 \
  -v $(pwd)/config:/app/config \
  -v $(pwd)/runners:/app/runners \
  ghcr.io/soulteary/runner-fleet:v1.8.0
```

호스트 디렉터리는 UID 1001이 쓸 수 있어야 합니다. Basic Auth: `-e BASIC_AUTH_PASSWORD=password`, `-e BASIC_AUTH_USER=admin`. Job에서 Docker가 필요하면 `-v /var/run/docker.sock:/var/run/docker.sock`을 추가하고(이미지에 GID 999의 `docker` 그룹이 포함되어 있으며 빌드 인자 `DOCKER_GID`로 변경 가능. 호스트 docker GID가 999가 아니면 `--group-add $(getent group docker | cut -d: -f3)`도 필요), 또는 DinD 사용(저장소 `docker-compose.yml`의 `--profile dind` 참조). 두 이미지에는 Docker CLI와 함께 GitHub 호스팅 runner에 맞춘 명령줄 기반 계층이 포함되어 있습니다. `scripts/apt-packages.txt`는 `actions/runner-images`의 `toolset-2404.json` apt 패키지 집합을 가져온 것으로 `git`, `unzip`, `jq`, `rsync`, `sudo`, `xvfb` 등이 들어 있습니다. 언어·플랫폼 SDK는 의도적으로 제외했습니다 — `setup-*` action을 쓰거나 이미지를 확장하세요. 호스팅 runner와 마찬가지로 두 이미지 모두 Job 사용자에게 비밀번호 없는 `sudo`를 부여하므로 `sudo apt-get install -y …`가 그대로 동작합니다. 제거하려면 `--build-arg ALLOW_SUDO=false`로 빌드하세요.

### 자동 설치 및 등록

UI의 "Quick Add Runner"에서 이름, 대상, 토큰을 입력하고 제출하면 설치 스크립트가 먼저 실행된 뒤 등록 및 시작됩니다. 실패 시:

```bash
docker exec runner-manager /app/scripts/install-runner.sh <name> [version]
```

스크립트는 `uname -m`으로 아키텍처를 판별하고, 버전 미지정 시 GitHub API로 최신 버전을 확인하며, **모든 버전에서 SHA-256을 반드시 검증**합니다. 공식 해시를 가져올 수 없으면(오프라인/미러) 직접 전달하세요: `RUNNER_SHA256=<sha256> ... install-runner.sh <name> <version>`. 이미 runner가 있는 디렉터리는 건너뜁니다(재설치는 `RUNNER_FORCE_REINSTALL=1`).

또는 호스트에서 [actions-runner](https://github.com/actions/runner/releases)를 `runners/<name>/` 아래에 풀고, UI에서 제출하거나 해당 디렉터리에서 `./config.sh`를 수동 실행하세요.

### 컨테이너 모드 (Runner당 컨테이너)

각 Runner는 자체 컨테이너에서 실행됩니다. Manager는 호스트 Docker로 시작/중지하고, 컨테이너 내 Agent로부터 HTTP로 상태를 가져옵니다.

**방법 1: env만 사용 (전체 컨테이너 시 권장)**
config/config.yaml 수정 없이 사용. `cp .env.example .env` 후 예: `CONTAINER_MODE=true`, `VOLUME_HOST_PATH=<runners 호스트 절대 경로>`(예: `realpath runners`), `JOB_DOCKER_BACKEND=host-socket`, `CONTAINER_NETWORK=runner-net` 설정. `config/config.yaml`을 만들지 않아도 위 변수를 `.env`에 설정해 두면 첫 실행 시 자동 생성됩니다. `RUNNER_IMAGE`를 설정하지 않으면 Runner 이미지는 `MANAGER_IMAGE`에서 자동 유도(예: v1.8.0 → v1.8.0-runner). 마운트한 `config`와 `runners`는 여전히 `chown 1001:1001` 필요. 자세한 내용은 `.env.example`의 오버라이드 변수 참조.

**방법 2: config/config.yaml에서 활성화** (`config.yaml.example` 참조):

```yaml
runners:
  base_path: /app/runners
  container_mode: true
  container_image: ghcr.io/soulteary/runner-fleet:v1.8.0-runner
  container_network: runner-net
  agent_port: 8081
  job_docker_backend: dind   # dind | host-socket | none
  dind_host: runner-dind
  volume_host_path: /abs/path/on/host/to/runners
```

Runner 이미지: Manager와 동일한 이름에 `-runner` 태그(운영: 버전 예 v1.8.0-runner, 개발: main-runner), 또는 로컬 빌드: `docker build -f Dockerfile.runner -t ghcr.io/soulteary/runner-fleet:v1.8.0-runner .`. Manager는 호스트 Docker(`docker.sock` 마운트)를 사용해야 하며, `DOCKER_HOST`로 DinD를 사용하면 안 됩니다. Compose에서는 호스트 docker GID용 `group_add` 또는 `user: "0:0"`을 사용하세요. `job_docker_backend: host-socket`일 때 Manager는 Runner 컨테이너에 `--group-add <호스트 docker GID>`를 전달합니다(`docker.sock`에서 자동 감지, `runners.docker_gid` / `DOCKER_GID`로 재정의 가능). 이미지에도 `docker` 그룹이 포함되어 있습니다(빌드 인자 `DOCKER_GID`, 기본 999). Runner 이름은 컨테이너 이름으로 정규화되며, 매핑 후 중복 시 충돌합니다.

**Runner 이미지 확장**: GitHub 호스팅 runner에는 Android SDK, Node, Python 등 툴체인이 포함되어 있지만 셀프 호스팅에는 없습니다. `ubuntu-24.04`용으로 작성된 workflow는 이를 암묵적으로 전제하는 경우가 많아 이전 후 `SDK location not found` 등으로 실패합니다. 이 저장소의 Runner 이미지 위에 필요한 툴체인을 얹으세요. 바로 쓸 수 있는 예제와 핵심 규칙 네 가지(/opt 아래는 UID 1001로 chown, 환경 변수는 이미지에 포함, 비밀번호 없는 sudo 상속, 워밍업은 `USER app` 이후)는 [`examples/runner-images/`](../../examples/runner-images/)에 있습니다. `items[].container_image`로 특정 Runner에만 적용하고 workflow에서는 label로 선택합니다.

**설정 변경과 컨테이너 재생성**: 이미지, 네트워크, 마운트 경로, Job 내 Docker 백엔드는 모두 `docker create` 시점에 정해지며 기존 컨테이너는 생성 당시 값을 그대로 유지합니다. 설정만 바꿔서는 닿지 않습니다. Manager는 각 컨테이너의 실제 생성 파라미터를 현재 설정과 비교합니다. **정지된** 컨테이너가 맞지 않으면 다음에 시작할 때(직접 "시작"을 누르든 Manager가 자동으로 기동하든) 삭제 후 다시 만들고, 목록에는 해당 Runner에 "설정 변경됨"이 표시되며 툴팁에 차이(예: `job_docker_backend: → dind`)가 나옵니다. **실행 중인** 컨테이너는 자동으로 재생성하지 않습니다 — Job이 돌고 있을 수 있기 때문입니다. 바로 적용하려면 행의 "컨테이너 재생성"(`POST /api/runners/:name/recreate`, 실행 중인 Job이 중단됨)을 쓰거나, 한가할 때 정지 후 다시 시작하세요. 같은 tag로 이미지를 다시 빌드한 경우도 감지합니다(이미지 ID로 비교).

**바로 쓸 수 있는 배포 예제**: [`examples/deploy/`](../../examples/deploy/)에 복사해서 그대로 쓰는 구성 두 가지가 있습니다. `standalone/`(Manager 컨테이너 하나, Runner 프로세스도 그 안에서 실행. `docker run` 또는 Compose)와 `fleet/`(컨테이너 모드: Runner마다 컨테이너 하나, 이미지 캐시는 호스트 daemon 공유로 자연히 공유되고, 툴체인·Action 캐시는 Runner 이미지 레이어에 미리 넣으며, 빌드 캐시는 Runner별로 분리). README에 두 방식의 비교, 어떤 캐시가 공유되고 어떤 것이 분리되는지, 그리고 자주 겪는 배포 함정(디렉터리 소유자, `VOLUME_HOST_PATH`, host-socket에서의 디스크 증가)을 정리했습니다.

### 문제 해결

- **문제가 있으면 먼저 시작 자가 점검 확인**: `docker compose logs runner-manager | grep '\[preflight'`. 시작 시 runners 디렉터리, Docker 접근성, 네트워크, Runner 이미지, Job 내 Docker 백엔드를 점검하며, 실패 항목에는 바로 실행 가능한 수정 명령이 표시됩니다.
- **compose down 후 Runner가 시작되지 않음**: 한 번 `docker network create runner-net` 실행. 계속 실패하면 UI에서 "Start"로 재생성하거나 `docker rm -f github-runner-<name>` 후 "Start".
- **root로 실행**: 마운트된 디렉터리는 프로세스 사용자가 쓸 수 있어야 함. root 사용 시 `RUNNER_ALLOW_RUNASROOT=1` 설정.
- **Job에서 docker.sock `permission denied`**: `job_docker_backend: host-socket`에서는 컨테이너 사용자(UID 1001)가 socket 소유 그룹에 속해야 합니다. Manager가 컨테이너 생성 시 감지한 호스트 docker GID로 `--group-add`를 추가합니다. GID가 맞지 않는 컨테이너는 "설정 변경됨"으로 표시되어 다음 시작 때 자동으로 재생성됩니다(실행 중이면 배지가 뜨므로 "컨테이너 재생성"을 사용하세요). 감지에 실패하면 `runners.docker_gid`(또는 `.env`의 `DOCKER_GID`)를 `getent group docker | cut -d: -f3` 값으로 설정합니다.
- **Job에서 `command not found` 또는 SDK 누락**: 셀프 호스팅 runner에는 GitHub 호스팅처럼 툴체인이 포함되어 있지 않습니다. 먼저 시작 자가 점검(`docker compose logs runner-manager | grep '\[preflight'`)을 확인하세요. 설정된 각 Runner 이미지에서 `git`/`unzip`/`tar`/`curl` 중 무엇이 빠졌는지 알려줍니다. 언어·플랫폼 SDK는 이미지를 확장하세요([`examples/runner-images/`](../../examples/runner-images/)).
- **이전 Runner 이미지**: pull하거나 다시 빌드한 뒤 Runner를 시작하면 Manager가 이미지 변경(참조와 이미지 ID를 모두 비교하므로 같은 tag 재빌드도 포함)을 감지해 컨테이너를 다시 만듭니다. 실행 중인 컨테이너는 건드리지 않으니 중단해도 될 때 행의 "컨테이너 재생성"을 쓰세요.
- **로그에 5분마다 `已定时拉起 runner: <이름>` 이 반복되고, UI에서도 실행 중으로 표시되지 않음**: 이번 버전에서 수정되었습니다. 업그레이드만 하면 되며 Runner를 다시 등록할 필요는 없습니다. 실행 상태를 그동안 pid 파일(`Runner.Listener.pid`, 없으면 `.path`)에서 읽었지만 actions/runner는 둘 다 쓰지 않습니다. 시작 스크립트 어디에도 pid 파일을 쓰는 곳이 없고 `.path`에는 PATH 문자열이 들어 있습니다. 그래서 모든 Runner가 "등록됨, 실행 중 아님"으로 읽혔고 5분마다 도는 점검이 매번 전부를 다시 시작시켰습니다. 이제는 프로세스 테이블에서 판단하며, 컨테이너 모드에서는 각 컨테이너 안의 Agent에게 물어봅니다 — Manager는 다른 컨테이너의 프로세스를 볼 수 없습니다. 같은 원인으로 기본(비컨테이너) 모드의 "정지"는 항상 `未找到 runner pid 文件或 pid 无效` 로 실패했습니다.
- **UI에서 삭제한 Runner가 GitHub에 남아 있고, 같은 이름으로 다시 추가하면 등록이 실패함**: 이제 삭제 시 GitHub 등록 해제도 함께 수행합니다. 단 `config/tokens/<Runner 이름>`에 선택 PAT(조직은 `admin:org`, 저장소는 `repo`)이 있어야 합니다. 없으면 해제할 자격 증명이 없으므로 삭제 응답에 그 사실과 Settings → Actions → Runners 경로를 안내합니다. 이전 버전에서 삭제한 Runner는 해제된 적이 없으니 직접 정리하세요.
- **어떤 Runner가 "GitHub 조회 실패"로 표시됨**: 조회는 했지만 답을 얻지 못한 상태입니다(토큰 만료, 권한 부족, 레이트 리밋, 대상이 보이지 않음 — 마우스를 올리면 원인 표시). "GitHub 미표시"(GitHub가 응답했고 목록에 없음)와는 다릅니다. 이전 버전은 전자도 후자로 보고했습니다.
- **status=unknown**: 상세 팝업에서 probe 확인; "Start/Stop"으로 자가 복구 시도.

### 이미지 로컬 빌드

```bash
docker build -t runner-manager .
docker build -f Dockerfile.runner -t ghcr.io/soulteary/runner-fleet:v1.8.0-runner .
```

Make: `make docker-build`, `make docker-run`, `make docker-stop`.

---

## 2. 설정

```bash
mkdir -p config && cp config.yaml.example config/config.yaml
```

| 필드 | 설명 | 기본값 |
|------|------|--------|
| `server.port` | HTTP 서버 포트 | `8080` |
| `server.addr` | 바인드 주소; 비우면 모든 인터페이스 | 비움 |
| `runners.base_path` | Runner 설치 디렉터리 루트 경로; **컨테이너에서는 `/app/runners`로 설정** | `./runners` |
| `runners.items` | 미리 정의된 Runner 목록 | Web UI에서도 추가 가능 |
| `runners.container_mode` | 컨테이너 모드 활성화 | `false` |
| `runners.container_image` | 컨테이너 모드에서 Runner 이미지(-runner 태그) | `ghcr.io/soulteary/runner-fleet:v1.8.0-runner` |
| `runners.container_network` | 컨테이너 모드에서 Runner 네트워크 | `runner-net` |
| `runners.agent_port` | 컨테이너 내 Agent 포트 | `8081` |
| `runners.job_docker_backend` | Job 내 Docker: `dind` / `host-socket` / `none` | `dind` |
| `runners.dind_host` | `job_docker_backend=dind`일 때 DinD 호스트명 | `runner-dind` |
| `runners.docker_gid` | `job_docker_backend=host-socket`일 때 Runner 컨테이너에 추가하는 호스트 docker 그룹 GID. 비우거나 `0`이면 `docker.sock`에서 자동 탐지 | 비움(자동 탐지) |
| `runners.volume_host_path` | 컨테이너 모드에서 runners의 호스트 절대 경로(필수) | 비움 |
| `runners.items[].name` | 표시 이름이자 설치 디렉터리 이름이며, 컨테이너 모드에서는 컨테이너 이름. 고유하며 생성 후 변경 불가 | 필수 |
| `runners.items[].path` | `base_path` 아래 하위 디렉터리. 비우면 `name` 사용 | 비움(= `name`) |
| `runners.items[].target_type` | `org` 또는 `repo` | 필수 |
| `runners.items[].target` | 조직 이름 또는 `owner/repo` | 필수 |
| `runners.items[].labels` | 사용자 정의 라벨. workflow의 `runs-on`이 이것으로 선택 | 비움 |
| `runners.items[].container_image` | Runner별 이미지 재정의(컨테이너 모드), 비우면 전역값 | 비움 |
| `runners.items[].job_docker_backend` | Runner별 Docker 백엔드 재정의(컨테이너 모드), 비우면 전역값 | 비움 |
| `runners.resources` | Runner 컨테이너 리소스 상한(`cpus` / `memory` / `memory_swap` / `pids_limit`), `docker create`로 전달하며 시작 시 `docker update`로 기존 컨테이너에도 적용 | 비움(무제한) |

위 표에서 컨테이너 배포가 바꿔야 하는 필드는 모두 환경 변수로도 줄 수 있어, 전체 컨테이너 구성은 `.env`만 건드리면 됩니다 — 아래 [환경 변수](#환경-변수) 참조.

### 환경 변수

시작할 때 한 번만 읽으므로 바꾸면 Manager를 재시작해야 합니다. 두 이름이 같은 설정을 가리키는
경우 표의 순서대로 읽으므로, 둘 다 설정하면 **뒤쪽**이 이깁니다.

| 변수 | 덮어쓰는 항목 | 기본값 / 참고 |
|---|---|---|
| `MANAGER_PORT`, `SERVER_PORT` | `server.port` | `8080`. compose는 컨테이너 안의 `SERVER_PORT`를 고정하고 `MANAGER_PORT`를 거기에 매핑하므로, 공개 포트와 수신 포트가 달라도 됩니다 |
| `SERVER_ADDR` | `server.addr` | 비움(모든 인터페이스) |
| `RUNNERS_BASE_PATH` | `runners.base_path` | `./runners`, 이미지 안에서는 `/app/runners` |
| `CONTAINER_MODE` | `runners.container_mode` | `false`. `true`와 `1`만 읽습니다: 이 변수는 컨테이너 모드를 **켤 수만 있고 끌 수는 없습니다**. 값 하나를 잘못 써서 배포 형태가 조용히 바뀌지 않도록 하기 위함입니다 |
| `RUNNER_IMAGE`, `CONTAINER_IMAGE` | `runners.container_image` | 미설정 시 `MANAGER_IMAGE`에서 유도(`:v1.8.0` → `:v1.8.0-runner`), 그다음 `FLEET_IMAGE_TAG` |
| `CONTAINER_NETWORK` | `runners.container_network` | `runner-net` |
| `VOLUME_HOST_PATH`, `RUNNERS_VOLUME_HOST_PATH` | `runners.volume_host_path` | 비움. 컨테이너 모드에서는 필수 |
| `JOB_DOCKER_BACKEND` | `runners.job_docker_backend` | `dind` |
| `DOCKER_GID` | `runners.docker_gid` | 비움 = `docker.sock`에서 탐지 |

다음 항목들은 설정 파일에 대응 항목이 없습니다:

| 변수 | 역할 | 기본값 |
|---|---|---|
| `BASIC_AUTH_PASSWORD` | 설정하면 Basic 인증이 켜집니다 | 비움(인증 없음) |
| `BASIC_AUTH_USER` | Basic 인증 사용자 이름 | `admin` |
| `TRUSTED_ORIGINS` | 교차 사이트 검사를 면제할 출처(쉼표 구분). [4. 보안 및 검증](#4-보안-및-검증) 참조 | 비움 |
| `LOG_LEVEL` | `trace` / `debug` / `info` / `warn` / `error` | `info` |
| `LOG_FORMAT` | 사람이 읽으면 `console`, ELK나 Loki로 보내면 `json`. 모르는 값은 시작을 실패시키지 않고 `console`로 되돌아갑니다 | `console` |
| `DOCKER_HOST` | **Manager 자신**이 사용하는 Docker 데몬. 컨테이너 모드에는 호스트 socket이 필요합니다 — DinD를 가리키면 Runner 컨테이너를 만들지 못합니다 | `unix:///var/run/docker.sock` |
| `MANAGER_IMAGE` | compose가 받아오는 Manager 이미지. Runner 이미지도 여기서 유도됩니다 | 릴리스 tag |
| `FLEET_IMAGE_TAG` | 다른 무엇도 정하지 않을 때 기본 Runner 이미지의 tag | `v1.8.0` |

`scripts/install-runner.sh`는 추가로 `RUNNER_VERSION`, `RUNNER_SHA256`,
`RUNNER_FORCE_REINSTALL`을 읽습니다 — [자동 설치 및 등록](#자동-설치-및-등록) 참조.

Runner 컨테이너 안의 Agent는 `AGENT_TOKEN`, `AGENT_PORT`, `RUNNER_INSTALL_DIR`을 읽습니다.
셋 다 Manager가 컨테이너를 만들 때 주입하며, 손으로 설정하는 것은 일반적인 배포에 포함되지
않습니다.


**검증**: 중복 이름 불가. 컨테이너 모드에서는 컨테이너 이름 충돌을 검사합니다. `job_docker_backend`는 `dind`/`host-socket`/`none`만 허용. 컨테이너 모드에서 컨테이너 `base_path` 사용 시 `volume_host_path` 필수. `job_docker_backend`를 생략하면 `dind`. 백엔드 변경 후 **중지된** 컨테이너는 다음 시작 시 자동으로 재생성되고, **실행 중인** 컨테이너는 "설정 변경됨"으로 표시되며 해당 행의 "컨테이너 재생성"으로 즉시 적용됩니다 — 위의 "설정 변경과 컨테이너 재생성" 참고.

예시:

```yaml
server:
  port: 8080
  addr: 0.0.0.0
runners:
  base_path: /app/runners
  items: []
```

---

## 3. Runner 추가

**비공개 저장소 전용**: 공개 저장소에 등록한 Runner, 또는 **Allow public repositories**를 켠 조직 Runner 그룹에 등록한 Runner는 풀 리퀘스트를 열 수 있는 누구의 워크플로든 이 머신에서 실행합니다. 여기의 Runner는 영속적입니다——작업 디렉터리, 그 아래 도구 캐시, `$HOME`이 한 Job에서 다음 Job으로 남으므로, 신뢰할 수 없는 Job 하나가 이후 모든 Job에 영향을 줍니다. 신뢰하는 비공개 저장소에만 Runner를 등록하고, 신뢰할 수 없는 코드는 GitHub 호스팅 Runner에 맡기세요. [SECURITY.md](../../SECURITY.md) 참고.

**토큰 얻기**: Repo/조직 → Settings → Actions → Runners → New self-hosted runner, 토큰 복사(약 1시간 유효). Runner마다 새 토큰 필요.

**서비스에 추가**: UI "Quick Add Runner"에서 이름(고유), 대상 유형(org/repo), 대상, 토큰(선택, 설정 시 제출 시 자동 등록 및 시작 가능) 입력. GitHub에서 `./config.sh --url ... --token ...`을 "Parse from GitHub command"에 붙여넣고 "Parse & fill" 클릭 가능. 자동 등록은 GitHub.com 전용. GitHub Enterprise는 Runner 디렉터리에서 수동 `config.sh` 필요.

**Runner가 설치되지 않은 경우**: [GitHub Actions Runner](https://github.com/actions/runner/releases)에서 다운로드 후 `runners/<name>/`에 풀고, UI에 토큰 입력 또는 해당 디렉터리에서 `./config.sh` 실행. 컨테이너 배포 시 UI에서 토큰 제출 시 먼저 설치 후 등록. 컨테이너 모드는 먼저 Runner 이미지와 `volume_host_path` 설정 필요(위 컨테이너 모드 참조).

**등록 결과**: 해당 Runner 디렉터리의 `.registration_result.json`에 기록. **GitHub 표시 확인**(선택): PAT를 `config/tokens/<Runner 이름>`(모드 0600; 조직은 `admin:org`, 저장소는 `repo` 필요)에 두면 약 5분마다 확인하며 결과는 `.github_status.json`에 기록. 이전 버전에서 Runner 디렉터리에 남은 PAT는 자동으로 그곳으로 옮겨지고 Runner 디렉터리에서 삭제됩니다 — 그 디렉터리는 Runner 컨테이너에 마운트되어 작업이 읽을 수 있기 때문입니다. 같은 검사에서 GitHub 상의 해당 Runner가 **작업을 실행 중인지**도 기록하며, 목록에는 '작업 중' 배지로, 설정 대화상자에는 별도 행으로 표시됩니다. 약 5분 주기를 공유하므로 최대 5분까지 지연될 수 있고, PAT가 없으면 '유휴'가 아니라 '알 수 없음'으로 남습니다. 목록의 '등록됨'과 'GitHub ✓'는 모두 해당 대상의 Runners 설정 페이지로 연결됩니다. 같은 파일에는 대상 저장소가 **공개**인지도 기록됩니다. 공개 저장소면 목록에 경고 색 '공개 저장소' 배지가 붙고 대화 상자에 '저장소 공개 범위' 행이 생깁니다 — 공개 저장소에서는 풀 리퀘스트를 열 수 있는 사람이면 누구나 여기서 코드를 실행할 수 있기 때문입니다. 이 확인은 저장소당 하루 최대 한 번입니다(공개 범위는 거의 바뀌지 않고, 익명 API는 시간당 60회뿐입니다). PAT가 없어도 동작하며, 있으면 사용합니다. 조직 대상은 포함되지 않습니다. 거기서 판단해야 할 것은 Runner 그룹이 공개 저장소를 허용하는지이고, 그러려면 `admin:org`가 필요합니다.

**이름 충돌 검사**: 이름을 입력하는 동안 폼이 `/api/runner-precheck`를 호출해 제출 전에 문제를 보여 줍니다 — 설정에 같은 이름의 Runner가 있음, 정규화하면 다른 Runner와 컨테이너 이름이 같아짐, 설치 디렉터리가 이미 사용 중, 등록된 Runner가 남아 있는 디렉터리(`.runner` 존재), 호스트에 같은 이름의 컨테이너가 남아 있음. 차단성 항목은 빨간색으로 표시되고 한 번의 클릭으로 쓸 수 있는 추천 이름을 제공합니다. 경고(비어 있지 않은 디렉터리를 재사용)는 계속 진행할 수 있습니다. 그대로 제출해도 서버가 **409**와 동일한 충돌 정보로 거부합니다 — 예전의 '조용히 임의 접미사를 붙이는' 동작은 없어졌습니다(원하면 `auto_rename: true`).

**Runner 삭제**: 중지하고, 해당 Runner 디렉터리에 PAT가 있으면 GitHub에서 등록을 해제하며, **설치 디렉터리를 삭제합니다** — 단 그 디렉터리가 `runners.base_path` 아래에 있을 때만입니다. 잘못 설정된 경로가 시스템 디렉터리까지 가져가지 않도록 하기 위함입니다. `_work`와 그 아래 캐시도 함께 사라지므로 삭제 후에는 되돌릴 수 없습니다.

머신당 여러 Runner: 별도 하위 디렉터리 사용.

---

## 4. 보안 및 검증

**인증**: 기본값은 로그인 없음. 내부 네트워크 또는 localhost에서만 사용 권장. Basic Auth 활성화에는 환경 변수 `BASIC_AUTH_PASSWORD` 설정. `BASIC_AUTH_USER` 선택(기본 `admin`). `GET /health`와 `GET /ready`를 제외한 모든 경로에 인증 필요. 비밀은 커밋하지 말고 `.env` 사용. 컨테이너: `-e BASIC_AUTH_PASSWORD=...` 또는 compose `env_file`.

**교차 사이트 요청**: 쓰기 엔드포인트는 브라우저가 교차 사이트라고 보고한 요청을 거부하므로, 다른 오리진의 페이지가 캐시된 Basic 인증 정보로 이 API를 조작할 수 없습니다. 설정할 것은 없습니다. 리버스 프록시가 `Host`를 바꿔 써서 본인의 요청까지 거부된다면 브라우저에 보이는 오리진을 `TRUSTED_ORIGINS`에 쉼표로 구분해 넣으세요. 브라우저가 아닌 호출자(curl, CI 스크립트)는 영향을 받지 않습니다 — 캐시된 자격 증명이 없으므로 여기서의 공격자가 될 수 없습니다. 정확한 규칙은 [개발 문서](development.md)를 참고하세요.

**경로 및 고유성**: name/path에 `..`, `/`, `\` 포함 불가. 디렉터리는 `runners.base_path` 아래에 있어야 함. 중복 이름 불가. 편집 시 이름은 읽기 전용. 컨테이너 모드에서 이름은 컨테이너 이름으로 정규화되며, 매핑 후 중복 시 오류.

**Agent 인증**(컨테이너 모드): Manager가 Runner마다 무작위 토큰을 `<runner 디렉터리>/.agent_token`(0600)에 기록하고, 컨테이너 생성 시 `AGENT_TOKEN`으로 주입하며, Agent 호출 시 `Authorization: Bearer`로 전송합니다. Agent는 환경 변수를 읽으므로 Manager와 Agent의 UID 일치에 의존하지 않습니다. 파일은 Manager 측 영구 사본입니다. Agent는 토큰 없는 `/status`, `/start`, `/stop`을 거부하며 `/health`는 HEALTHCHECK용으로 열려 있습니다. 이 기능 이전에 생성된 컨테이너는 토큰이 주입되지 않아 인증 없이 동작합니다. 이제 이런 컨테이너는 "설정 변경됨"으로 판정되어 다음 시작 때 자동으로 재생성되며 토큰이 채워집니다(실행 중이면 "컨테이너 재생성" 사용).

**Runner에 전달되는 환경 변수**: Manager와 Agent는 자신이 시작하는 모든 프로세스(`run.sh`와 그것이 실행하는 Job, `config.sh`, `install-runner.sh`)의 환경에서 `BASIC_AUTH_PASSWORD`, `BASIC_AUTH_USER`, `AGENT_TOKEN`을 제거합니다. 나머지는 `DOCKER_HOST`와 프록시 설정을 포함해 그대로 전달됩니다. 이렇게 하면 관리용 자격 증명이 Job 로그에 남지 않지만, 격리 경계는 아닙니다. 기본 모드에서 Job은 Manager와 같은 사용자로 실행됩니다([SECURITY.md](../../SECURITY.md) 참고).

**민감한 파일**: config/config.yaml, .env와 `config/tokens/`는 `.gitignore`에 있음. 각 Runner의 선택 PAT는 `config/tokens/<Runner 이름>`에 두며, Manager가 디렉터리는 0700, 파일은 0600으로 만듭니다. Runner 디렉터리에는 두지 마세요 — 컨테이너 모드에서 그 디렉터리는 Runner 컨테이너에 마운트되어 작업이 읽을 수 있습니다.

**Runner 디렉터리 권한**: 각 Runner의 설치 디렉터리는 0700으로 생성됩니다. `config.sh`가 그 안에 `.credentials_rsaparams`(Runner가 GitHub에 신원을 증명하는 RSA 개인 키)를 쓰는데, actions/runner는 이 파일들에 Unix 권한을 설정하지 않으므로 디렉터리 권한 비트가 호스트의 다른 로컬 사용자가 이를 읽고 해당 Runner를 사칭하는 것을 막는 마지막 방어선입니다. **이전 버전이 만든 디렉터리는 여전히 0755입니다.** 시작 시 자가 점검(`docker compose logs runner-manager | grep '\[preflight'`)이 해당 디렉터리를 지목하고 바로 실행 가능한 `chmod 700`을 알려줍니다. 자동으로 바꾸지는 않습니다: UID가 어긋난 배포(Manager는 root, 컨테이너는 app(1001))에서 권한을 조이면 잘 돌던 배포가 깨지므로 확인 후 실행하세요.

---

## 5. 운영

### 프로브

| 경로 | 용도 |
|---|---|
| `GET /health` | 라이브니스. 프로세스가 살아 있는 한 200이며 의존성 검사는 전혀 하지 않습니다 — 설정이 깨졌거나 마운트를 쓸 수 없어도 200입니다. K8s `livenessProbe`용: 재시작은 멈춘 프로세스에 대한 올바른 답이고, 잘못된 설정에 대한 틀린 답입니다 |
| `GET /ready` | 레디니스. 설정을 읽을 수 없거나 `runners.base_path`가 없거나 쓸 수 없으면 503 — 실제로 쓰기 프로브를 하므로 `/health`가 보지 못하는 디렉터리 소유자 문제를 잡아냅니다. K8s `readinessProbe`용이며, 배포를 바꾼 뒤 확인할 것은 이쪽입니다 |

Basic 인증을 켜도 이 둘은 인증 없이 남습니다. 프로브는 자격 증명을 지닐 수 없기 때문입니다.
둘 다 *어느* 검사가 실패했는지는 말하지 않습니다. 그것은 로그에 있습니다.

### 메트릭

`GET /metrics`는 Prometheus 텍스트를 제공합니다 — 라우트별 호출량과 지연. `path` 라벨은 요청
URL이 아니라 Echo의 라우트 템플릿(`/api/runners/:name`)이므로, Runner가 많아져도 라벨 값이
그만큼 늘지 않습니다.

프로브와 달리 Basic 인증이 켜져 있으면 `/metrics`는 **인증이 필요합니다**. 모든 엔드포인트의
호출량을 드러내는 운영 데이터이며 `/api`보다 더 공개될 이유가 없기 때문입니다. 스크레이프
작업을 그에 맞게 설정하세요:

```yaml
scrape_configs:
  - job_name: runner-fleet
    static_configs:
      - targets: ['runner-manager:8080']
    basic_auth:
      username: admin
      password: <BASIC_AUTH_PASSWORD>
```

### 업그레이드

```bash
docker compose pull && docker compose up -d
```

Runner를 다시 등록할 필요는 없습니다. Runner의 신원은 각자의 설치 디렉터리에 있고, 업그레이드는 그곳을 건드리지 않습니다.

컨테이너 모드에서는 Runner 컨테이너가 여전히 **예전** Runner 이미지로 만들어진 채 남아 있습니다.
이미지, 네트워크, 마운트 디렉터리 등은 `docker create` 시점에 고정되기 때문입니다. Manager는 이를
스스로 발견해 고칩니다: **중지된** Runner는 다음 시작 때 다시 만들어지고, **실행 중인** 것은
"설정 변경됨"으로 표시된 뒤 "컨테이너 재생성"을 누르거나 유휴 상태에서 중지했다 시작하면 다시
만들어집니다. 새 이미지가 실제로 준비되어 있는지는 두 가지로 갈립니다:

- **버전 tag**는 스스로 받아옵니다. 버전을 올린 뒤의 새 `-runner` tag는 로컬에 없으므로
  `docker create`가 가져옵니다.
- **가변 tag**(`:main`, 또는 같은 버전 tag를 다시 빌드한 것)는 로컬에서 이미 해석되므로
  `docker create`가 낡은 이미지를 그대로 씁니다. 먼저 직접 받아오세요 —
  `docker pull <Runner 이미지>`. 드리프트는 참조뿐 아니라 이미지 **ID**로도 비교하므로, 받아오고 나면
  재생성은 평소대로 일어납니다.

`runners.resources`는 다시 만들지 않고도 기존 컨테이너에 닿는 유일한 설정입니다. Manager가 시작할 때
`docker update`로 적용하므로, 리소스 상한을 지원하는 버전으로 올리려고 전부 새로 만들 필요는
없습니다.

올라갈 버전의 [변경 이력](../../CHANGELOG.md)을 읽으세요 — 호환성 변경과 수동 조치가 필요한 항목이 거기에 적혀 있습니다.

### 무엇을 백업할 것인가

README는 설정이 곧 백업이라고 말합니다. *설정*에 대해서는 맞지만 *신원*에 대해서는 아닙니다.
Runner의 자격 증명은 그 설치 디렉터리에 있고, 그것이 없으면 복원한 배포는 하나씩 다시 등록하는
수밖에 없습니다.

`config/` 디렉터리(`config.yaml`과 `tokens/`)와 각 `runners/<이름>/` 디렉터리를 백업하되 `_work/`는 제외하세요.

| `runners/<이름>/` 안의 것 | 작성자 | 잃으면 |
|---|---|---|
| `.runner`, `.credentials_rsaparams` 등 `config.sh`가 쓴 파일 | actions/runner | 그 Runner는 사라집니다. 다시 등록해야 하며, 먼저 GitHub의 남은 항목을 지우세요 — 예전 것이 목록에 있는 동안에는 같은 이름으로 재등록이 실패합니다 |
| `.agent_token` | Manager(권한 `0600`) | 다시 생성됩니다. 해당 컨테이너는 드리프트로 판정되어 다음 시작 때 재생성됩니다 |
| `config/tokens/<이름>`(Runner 디렉터리 밖) | 선택적으로 직접 둠 | 가시성 확인이 멈추고, Runner를 삭제해도 GitHub에서 등록을 해제할 수 없게 됩니다 |
| `.registration_result.json`, `.github_status.json` | Manager | 표시용일 뿐 — 다음 등록이나 확인 때 다시 만들어집니다 |
| `_work/` | Job 자신 | 남길 가치가 없습니다. 체크아웃과 빌드 산출물이며, 디스크에서 가장 크고 계속 자랍니다 |

복원에서는 소유자가 중요합니다. 최종적으로 모든 것이 UID 1001 소유여야 하며, 첫 설치 때와 같은
`sudo chown -R 1001:1001 config runners`를 실행합니다. `GET /ready`는 runners 디렉터리에 실제로
쓰기 프로브를 하므로, 복원한 것이 정말 쓸 수 있는지 확인하는 가장 빠른 방법입니다.

파일뿐 아니라 디렉터리의 권한도 중요합니다. 디렉터리가 UID 1001로 쓰기 가능하면 Manager는
`config.yaml`을 원자적으로 교체하므로, 저장 중에 읽어도 쓰다 만 내용을 보지 않고 도중에
크래시가 나도 잘리지 않습니다. 파일만 쓰기 가능하거나 그 파일만 bind mount 한 경우
(`-v ./config.yaml:/app/config/config.yaml`)에는 제자리 쓰기로 되돌아가고 경고를 한 번만 남깁니다.

### 리버스 프록시 뒤에 둘 때

Manager는 평문 HTTP만 말하고 자체 TLS가 없으므로 프록시가 TLS를 종단합니다. 여기서는 그것이 평소보다
더 중요합니다: Basic 인증은 요청마다 비밀번호를 보내므로, TLS가 없으면 매 요청마다 그것을 평문으로
네트워크에 흘리게 됩니다.

교차 사이트 검사는 설정이 필요 없습니다. 읽는 값인 `Sec-Fetch-Site`는 브라우저가 로컬에서 계산하므로
프록시가 `Host`를 고쳐 써도 깨지지 않습니다. `TRUSTED_ORIGINS`는 그럼에도 오판될 때의 탈출구입니다 —
브라우저 주소창에 보이는 그대로의 출처를 쉼표로 구분해 적고, 목록은 직접 통제하는 출처로만
한정하세요. [4. 보안 및 검증](#4-보안-및-검증)을 참조하세요.

프록시의 헬스 체크는 `/health`로, 레디니스 게이트가 있다면 `/ready`로 향하게 하세요. 둘 다 인증 없이
남습니다. `/metrics`는 공개하지 마세요: Basic 인증이 켜져 있으면 자격 증명을 요구하고, 꺼져 있으면
나머지와 똑같이 열려 있습니다.

### 버전과 로그

`GET /version`은 버전만 돌려줍니다. 빌드 세부 정보는 일부러 넣지 않았습니다: `go_version`이
있으면 누구든 공개된 Go 런타임 CVE를 당신이 실행 중인 정확한 런타임에 연결할 수 있습니다.
`runner-manager -version`은 commit, 빌드 일시, Go 버전, 플랫폼을 출력하지만, 이쪽은 호스트에서
실행되며 외부에 노출되지 않습니다.

로그는 `LOG_LEVEL`과 `LOG_FORMAT`으로 제어합니다. ELK나 Loki로 보낼 때는 `LOG_FORMAT=json`.
시작 자가 점검은 항목마다 한 줄을 쓰며 각 줄 앞에 `[preflight …]`가 붙습니다 —
[문제 해결](#문제-해결) 전반에서 쓰는 grep 앵커가 바로 이것입니다.

[← 프로젝트 홈으로](../../README.md)
