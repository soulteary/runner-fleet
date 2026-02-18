# 개발 및 빌드

**文档 / Docs:** [EN](../development.md) · [中文](../zh/development.md) · [Français](../fr/development.md) · [Deutsch](../de/development.md) · 한국어 · [日本語](../ja/development.md)

![](../../.github/assets/fleet.jpg)

프로덕션은 컨테이너 배포를 사용하세요. [사용 가이드](guide.md) 참조. 이 문서는 기여자용: 로컬 빌드 및 디버그입니다.

## 요구 사항

- Go 1.26 ([go.mod](../../go.mod)과 일치).

## 빌드

```bash
# runner-manager 바이너리 빌드
go build -o runner-manager ./cmd/runner-manager

# 버전 포함 (/version 및 디버깅용)
go build -ldflags "-X main.Version=1.0.0" -o runner-manager ./cmd/runner-manager

# Runner Agent만 빌드 (컨테이너 모드)
go build -o runner-agent ./cmd/runner-agent

# 또는 Make: make build / make build-agent / make build-all
```

템플릿은 Manager 바이너리에 임베드됩니다(`cmd/runner-manager/templates/`). 단일 바이너리로 `templates/` 디렉터리 없이 배포 가능합니다.

## 로컬 실행 및 디버그

```bash
cp config.yaml.example config.yaml
go run ./cmd/runner-manager
# 또는 make run (빌드 후 실행); 사용자 설정: ./runner-manager -config /path/to/config.yaml
```

`:8080`에서 수신, http://localhost:8080. 디버그용 Basic Auth: `BASIC_AUTH_PASSWORD=secret go run ./cmd/runner-manager`. [사용 가이드 – 보안](guide.md#4-보안-및-검증) 참조.

## CLI 플래그

- `-config <path>`: 설정 파일 경로.
- `-version`: 버전 출력 후 종료(빌드 시 `-ldflags "-X main.Version=..."`로 주입).

## HTTP API

Basic Auth 사용 시 `/health`를 제외한 모든 요청에 Header `Authorization: Basic <base64(user:password)>`가 필요합니다.

| 경로 | 메서드 | 설명 |
|------|--------|------|
| `/health` | GET | `{"status":"ok"}` 반환. Ingress/K8s 프로브용. 항상 인증 없음. |
| `/version` | GET | `{"version":"..."}` 반환. |
| `/api/runners` | GET | Runner 목록. 컨테이너 모드에서 probe 실패 시 `status=unknown`과 구조화된 `probe`(`error/type/suggestion/check_command/fix_command`) 반환. |
| `/api/runners/:name` | GET | 단일 Runner 상세. 컨테이너 모드에서 probe 실패 시 동일한 `probe`. |
| `/api/runners/:name/start` | POST | Runner 시작. probe 실패 시에도 시작 시도, 응답에 구조화된 `probe` 반환. |
| `/api/runners/:name/stop` | POST | Runner 중지. probe 실패 시에도 중지 시도, 응답에 구조화된 `probe` 반환. |

### 호환성 변경 (업그레이드 참고)

이전 평면 필드 `probe_*`는 제거되었습니다. `probe` 객체 사용: `probe.error`, `probe.type`, `probe.suggestion`, `probe.check_command`, `probe.fix_command`. `probe.type` 값: `docker-access`, `agent-http`, `agent-connect`, `unknown`. Web UI는 `status=unknown`일 때 "Start/Stop"으로 자가 복구 가능.

예시 (probe 실패):

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

## Makefile 타깃

- `make help`: 모든 타깃 표시.
- `make build`: Manager 빌드(Version ldflags 포함).
- `make build-agent`: Runner Agent 빌드(컨테이너 모드).
- `make build-all`: Manager와 Agent 빌드.
- `make test`: 테스트 실행.
- `make run`: Manager 빌드 후 실행.
- `make docker-build` / `make docker-run` / `make docker-stop`: Manager 이미지 빌드 및 실행. [사용 가이드](guide.md) 참조.
- `make docker-build-runner`: 컨테이너 모드용 Runner 이미지 빌드(`Dockerfile.runner`, 기본 태그는 `RUNNER_IMAGE`).
- `make clean`: 빌드된 바이너리 제거(runner-manager, runner-agent).

컨테이너 모드는 `cmd/runner-agent`의 Agent와 `Dockerfile.runner`의 Runner 이미지를 사용합니다.

[← 문서로 돌아가기](README.md)
