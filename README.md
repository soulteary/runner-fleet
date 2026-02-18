# Runner Fleet - GitHub Actions Runner Manager

**文档 (Docs):** [中文](docs/zh/) · [Français](docs/fr/) · [Deutsch](docs/de/) · [한국어](docs/ko/) · [日本語](docs/ja/)

![](.github/assets/fleet.jpg)

HTTP management UI built with Golang Echo to view and manage multiple self-hosted GitHub Actions Runners on one machine. YAML-based config, no database required.

## Highlights

- **Zero database**: YAML-only config, no external deps; config is your backup and easy to version.
- **Web one-stop**: Add, register, start/stop, edit, and view status in the UI—no SSH or manual `config.sh`.
- **Auto install & register**: In "Quick Add" enter a token to auto-download the runner, register, and start; paste `./config.sh --url ... --token ...` from GitHub to parse and fill the form.
- **Container-first**: Docker / docker-compose out of the box; DinD and host-socket for in-job Docker; optional **container mode** (one runner per container) with Manager controlling lifecycle and status.
- **Self-heal & troubleshoot**: ~15s after start, registered but stopped runners are started; periodic check every 5 minutes; in container mode, `status=unknown` shows a structured probe (error type, check/fix commands) for copy-paste troubleshooting or start/stop self-heal.
- **Observable**: Registration result is written and shown in the UI; optional PAT (`.github_check_token`) to periodically verify runners appear in GitHub's list, synced to the UI.

## Features

- **View**: List all runners, status (installed/unregistered/missing dir), running or not; view full config per runner.
- **Edit**: Change subpath, target type, target, labels (name is read-only).
- **Quick Add**: Name + target (org/repo) + optional token; one-click add and optional auto-register.
- **Delete**: Remove from config (does not delete disk).
- **Start/Stop**: Start or stop registered runners.
- **Container mode** (optional): One runner per container; Manager starts/stops via Docker; runner image tag uses `-runner` suffix.

## Quick start

```bash
cp config.yaml.example config.yaml
# Edit config.yaml: set runners.base_path to /app/runners
# On host: mkdir -p runners && chown 1001:1001 config.yaml runners

docker network create runner-net 2>/dev/null || true
docker compose up -d
```

Open http://localhost:8080. For more options (docker run, DinD, container mode) see the [User Guide](docs/guide.md). Health: `GET /health`; version: `GET /version`.

## Use cases

- **Personal / team**: One machine as self-hosted runners for multiple repos or orgs; manage via Web UI, no need to remember CLI.
- **Internal CI**: Deploy on internal network; use DinD (isolated) or host-socket (shared with host) when jobs need Docker; runners recover after Manager or DinD restart.
- **Isolation & traceability**: Container mode gives one container per runner with clear boundaries; combine with registration result and GitHub visibility check to verify runners.

## Documentation

- **[User Guide](docs/guide.md)** — Deployment (Docker/docker-compose), config, adding runners, security & troubleshooting
- **[Development & Build](docs/development.md)** — Go build, local debug, HTTP API, Makefile

## Other

CI / images / releases: [.github/workflows](.github/workflows).

MIT License — see [LICENSE](LICENSE).
