# 사용 가이드

**文档 / Docs:** [EN](../guide.md) · [中文](../zh/guide.md) · [Français](../fr/guide.md) · [Deutsch](../de/guide.md) · 한국어 · [日本語](../ja/guide.md)

![](../../.github/assets/fleet.jpg)

배포, 설정, Runner 추가, 보안은 여기서 다룹니다. 기여자용 빌드 및 API는 [개발 및 빌드](development.md)를 참조하세요.

---

## 1. 배포 (Docker)

- 이미지는 **Ubuntu** 기반이며 .NET Core 6.0 의존성이 포함되어 있습니다. **UID 1001**로 실행되며, 호스트에 마운트된 디렉터리는 해당 사용자가 쓸 수 있어야 합니다(예: `chown 1001:1001 config.yaml runners`).
- 시작 후 약 15초 뒤에 등록되었지만 중지된 Runner가 자동으로 시작되며, 5분마다 주기적으로 검사합니다.

### 공개 이미지 사용 (권장)

운영 환경에서는 특정 버전(예: v1.0.0)을 사용하세요. 개발 시에는 `main` 태그를 쓸 수 있습니다.

```bash
docker pull ghcr.io/soulteary/runner-fleet:v1.0.0
```

### docker-compose 빠른 시작

저장소 루트에 `docker-compose.yml`이 있습니다. 컨테이너 모드에서 Job에 Docker가 필요하고 `job_docker_backend: dind`일 때만 DinD를 활성화하세요.

```bash
cp config.yaml.example config.yaml
# config.yaml 편집: runners.base_path를 /app/runners로 설정

chown 1001:1001 config.yaml
mkdir -p runners && chown 1001:1001 runners

docker network create runner-net 2>/dev/null || true
docker compose up -d
# job_docker_backend: dind인 경우: docker compose --profile dind up -d
```

UI: http://localhost:8080. 인증 세부사항은 [4. 보안 및 검증](#4-보안-및-검증)을 참조하세요.

### 컨테이너 실행 (전체 인자)

`config.yaml`과 `runners`를 마운트해야 합니다. 포트는 설정의 `server.port`와 일치해야 합니다(기본 8080).

```bash
docker run -d --name runner-manager \
  -p 8080:8080 \
  -v $(pwd)/config.yaml:/app/config.yaml \
  -v $(pwd)/runners:/app/runners \
  ghcr.io/soulteary/runner-fleet:v1.0.0
```

호스트 디렉터리는 UID 1001이 쓸 수 있어야 합니다. Basic Auth: `-e BASIC_AUTH_PASSWORD=password`, `-e BASIC_AUTH_USER=admin`. Job에서 Docker가 필요하면 `-v /var/run/docker.sock:/var/run/docker.sock`을 추가하거나 DinD 사용(저장소 `docker-compose.yml`의 `--profile dind` 참조). 이미지에 Docker CLI가 포함되어 있으며, DinD에서 일반적인 Action이 동작합니다.

### 자동 설치 및 등록

UI의 "Quick Add Runner"에서 이름, 대상, 토큰을 입력하고 제출하면 설치 스크립트가 먼저 실행된 뒤 등록 및 시작됩니다. 실패 시:

```bash
docker exec runner-manager /app/scripts/install-runner.sh <name> [version]
```

또는 호스트에서 [actions-runner](https://github.com/actions/runner/releases)를 `runners/<name>/` 아래에 풀고, UI에서 제출하거나 해당 디렉터리에서 `./config.sh`를 수동 실행하세요.

### 컨테이너 모드 (Runner당 컨테이너)

각 Runner는 자체 컨테이너에서 실행됩니다. Manager는 호스트 Docker로 시작/중지하고, 컨테이너 내 Agent로부터 HTTP로 상태를 가져옵니다.

**방법 1: env만 사용 (전체 컨테이너 시 권장)**
config.yaml 수정 없이 사용. `cp .env.example .env` 후 예: `CONTAINER_MODE=true`, `VOLUME_HOST_PATH=<runners 호스트 절대 경로>`(예: `realpath runners`), `JOB_DOCKER_BACKEND=host-socket`, `CONTAINER_NETWORK=runner-net` 설정. `RUNNER_IMAGE`를 설정하지 않으면 Runner 이미지는 `MANAGER_IMAGE`에서 자동 유도(예: v1.0.1 → v1.0.1-runner). 마운트한 `config.yaml`과 `runners`는 여전히 `chown 1001:1001` 필요. 자세한 내용은 `.env.example`의 오버라이드 변수 참조.

**방법 2: config.yaml에서 활성화** (`config.yaml.example` 참조):

```yaml
runners:
  base_path: /app/runners
  container_mode: true
  container_image: ghcr.io/soulteary/runner-fleet:v1.0.0-runner
  container_network: runner-net
  agent_port: 8081
  job_docker_backend: dind   # dind | host-socket | none
  dind_host: runner-dind
  volume_host_path: /abs/path/on/host/to/runners
```

Runner 이미지: Manager와 동일한 이름에 `-runner` 태그(운영: 버전 예 v1.0.0-runner, 개발: main-runner), 또는 로컬 빌드: `docker build -f Dockerfile.runner -t ghcr.io/soulteary/runner-fleet:v1.0.0-runner .`. Manager는 호스트 Docker(`docker.sock` 마운트)를 사용해야 하며, `DOCKER_HOST`로 DinD를 사용하면 안 됩니다. Compose에서는 호스트 docker GID용 `group_add` 또는 `user: "0:0"`을 사용하세요. Runner 이름은 컨테이너 이름으로 정규화되며, 매핑 후 중복 시 충돌합니다.

### 문제 해결

- **compose down 후 Runner가 시작되지 않음**: 한 번 `docker network create runner-net` 실행. 계속 실패하면 UI에서 "Start"로 재생성하거나 `docker rm -f github-runner-<name>` 후 "Start".
- **root로 실행**: 마운트된 디렉터리는 프로세스 사용자가 쓸 수 있어야 함. root 사용 시 `RUNNER_ALLOW_RUNASROOT=1` 설정.
- **이전 Runner 이미지**: `docker rm -f github-runner-<name>`, 그 다음 UI에서 "Start"로 재생성.
- **status=unknown**: 상세 팝업에서 probe 확인; "Start/Stop"으로 자가 복구 시도.

### 이미지 로컬 빌드

```bash
docker build -t runner-manager .
docker build -f Dockerfile.runner -t ghcr.io/soulteary/runner-fleet:v1.0.0-runner .
```

Make: `make docker-build`, `make docker-run`, `make docker-stop`.

---

## 2. 설정

```bash
cp config.yaml.example config.yaml
```

| 필드 | 설명 | 기본값 |
|------|------|--------|
| `server.port` | HTTP 서버 포트 | `8080` |
| `server.addr` | 바인드 주소; 비우면 모든 인터페이스 | 비움 |
| `runners.base_path` | Runner 설치 디렉터리 루트 경로; **컨테이너에서는 `/app/runners`로 설정** | `./runners` |
| `runners.items` | 미리 정의된 Runner 목록 | Web UI에서도 추가 가능 |
| `runners.container_mode` | 컨테이너 모드 활성화 | `false` |
| `runners.container_image` | 컨테이너 모드에서 Runner 이미지(-runner 태그) | `ghcr.io/soulteary/runner-fleet:v1.0.0-runner` |
| `runners.container_network` | 컨테이너 모드에서 Runner 네트워크 | `runner-net` |
| `runners.agent_port` | 컨테이너 내 Agent 포트 | `8081` |
| `runners.job_docker_backend` | Job 내 Docker: `dind` / `host-socket` / `none` | `dind` |
| `runners.dind_host` | `job_docker_backend=dind`일 때 DinD 호스트명 | `runner-dind` |
| `runners.volume_host_path` | 컨테이너 모드에서 runners의 호스트 절대 경로(필수) | 비움 |

일부 필드는 환경 변수로 덮어쓸 수 있음(`MANAGER_PORT`, `CONTAINER_MODE`, `VOLUME_HOST_PATH`, `JOB_DOCKER_BACKEND` 등). 전체 컨테이너 시 `.env`만 수정하면 됨. `.env.example` 참조.

**검증**: 중복 이름 불가. 컨테이너 모드에서는 컨테이너 이름 충돌을 검사합니다. `job_docker_backend`는 `dind`/`host-socket`/`none`만 허용. 컨테이너 모드에서 컨테이너 `base_path` 사용 시 `volume_host_path` 필수. `job_docker_backend`를 생략하면 `dind`. 백엔드 변경 후 UI에서 Runner 재시작하세요.

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

**토큰 얻기**: Repo/조직 → Settings → Actions → Runners → New self-hosted runner, 토큰 복사(약 1시간 유효). Runner마다 새 토큰 필요.

**서비스에 추가**: UI "Quick Add Runner"에서 이름(고유), 대상 유형(org/repo), 대상, 토큰(선택, 설정 시 제출 시 자동 등록 및 시작 가능) 입력. GitHub에서 `./config.sh --url ... --token ...`을 "Parse from GitHub command"에 붙여넣고 "Parse & fill" 클릭 가능. 자동 등록은 GitHub.com 전용. GitHub Enterprise는 Runner 디렉터리에서 수동 `config.sh` 필요.

**Runner가 설치되지 않은 경우**: [GitHub Actions Runner](https://github.com/actions/runner/releases)에서 다운로드 후 `runners/<name>/`에 풀고, UI에 토큰 입력 또는 해당 디렉터리에서 `./config.sh` 실행. 컨테이너 배포 시 UI에서 토큰 제출 시 먼저 설치 후 등록. 컨테이너 모드는 먼저 Runner 이미지와 `volume_host_path` 설정 필요(위 컨테이너 모드 참조).

**등록 결과**: 해당 Runner 디렉터리의 `.registration_result.json`에 기록. **GitHub 표시 확인**(선택): Runner 디렉터리에 `.github_check_token`(PAT; 조직은 `admin:org`, 저장소는 `repo` 필요)을 두면 약 5분마다 확인하며 결과는 `.github_status.json`에 기록.

머신당 여러 Runner: 별도 하위 디렉터리 사용.

---

## 4. 보안 및 검증

**인증**: 기본값은 로그인 없음. 내부 네트워크 또는 localhost에서만 사용 권장. Basic Auth 활성화에는 환경 변수 `BASIC_AUTH_PASSWORD` 설정. `BASIC_AUTH_USER` 선택(기본 `admin`). `GET /health`를 제외한 모든 경로에 인증 필요. 비밀은 커밋하지 말고 `.env` 사용. 컨테이너: `-e BASIC_AUTH_PASSWORD=...` 또는 compose `env_file`.

**경로 및 고유성**: name/path에 `..`, `/`, `\` 포함 불가. 디렉터리는 `runners.base_path` 아래에 있어야 함. 중복 이름 불가. 편집 시 이름은 읽기 전용. 컨테이너 모드에서 이름은 컨테이너 이름으로 정규화되며, 매핑 후 중복 시 오류.

**민감한 파일**: config.yaml과 .env는 `.gitignore`에 있음. 각 Runner의 `.github_check_token`은 `chmod 600` 권장. 버전 관리 under 시 `.gitignore`에 `**/.github_check_token` 추가.

[← 프로젝트 홈으로](../../README.md)
