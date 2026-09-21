# Runner Fleet - GitHub Actions Runner Manager

**文档 (Docs):** [中文](docs/zh/) · [Français](docs/fr/) · [Deutsch](docs/de/) · [한국어](docs/ko/) · [日本語](docs/ja/)

![](.github/assets/fleet.jpg)

HTTP management UI built with Golang Echo to view and manage multiple self-hosted GitHub Actions Runners on one machine. YAML-based config, no database required.

**Linux only** (`linux/amd64`, `linux/arm64`): runner liveness is read from `/proc`. The UI is translated into six languages, but server-side messages and logs are currently Chinese only — see the [User Guide](docs/guide.md#1-deployment-docker).

![](.github/assets/preview.jpg)

## Highlights

- **Zero database**: YAML-only config, no external deps; config is your backup and easy to version.
- **Web one-stop**: Add, register, start/stop, edit, and view status in the UI—no SSH or manual `config.sh`.
- **Auto install & register**: In "Quick Add" enter a token to auto-download the runner, register, and start; paste `./config.sh --url ... --token ...` from GitHub to parse and fill the form.
- **Container-first**: Docker / docker-compose out of the box; DinD and host-socket for in-job Docker; optional **container mode** (one runner per container) with Manager controlling lifecycle and status.
- **Config drift is repaired, not just reported**: image, network, mount directory, in-job Docker backend and the agent token are all fixed at `docker create` time, so editing the config never reached an existing container. The Manager compares each container against the current config: a **stopped** runner is rebuilt on its next start, a **running** one is flagged "config changed" with the exact difference and rebuilt when you say so.
- **Hosted-CLI baseline + custom images**: stock images align the CLI layer with GitHub-hosted `ubuntu-24.04`; extend with Android/Node (etc.) via [`examples/runner-images/`](examples/runner-images/) and per-runner `container_image`.
- **Self-heal & troubleshoot**: ~15s after start, registered but stopped runners are started; periodic check every 5 minutes; in container mode, `status=unknown` shows a structured probe (error type, check/fix commands) for copy-paste troubleshooting or start/stop self-heal.
- **Observable**: Registration result is written and shown in the UI; optional PAT (`.github_check_token`) to periodically verify runners appear in GitHub's list, synced to the UI.

## Features

- **View**: List all runners, status (installed/unregistered/missing dir), running or not; view full config per runner.
- **Edit**: Change subpath, target type, target, labels (name is read-only).
- **Quick Add**: Name + target (org/repo) + optional token; one-click add and optional auto-register. Conflicts (name taken, container-name collision, install directory in use, leftover container on the host) are reported while you type, with a suggested name.
- **Delete**: Remove from config (does not delete disk).
- **Start/Stop**: Start or stop registered runners.
- **Container mode** (optional): One runner per container; Manager starts/stops via Docker; runner image tag uses `-runner` suffix.
- **Recreate container** (container mode): Rebuild a runner's container with the current config; the list flags runners whose container no longer matches, with the difference in the tooltip.

## Quick start

```bash
mkdir -p config runners && cp config.yaml.example config/config.yaml
# Edit config/config.yaml: set runners.base_path to /app/runners
sudo chown -R 1001:1001 config runners

docker network create runner-net 2>/dev/null || true
docker compose up -d
```

Open http://localhost:8080. The default image tag is the stable release (e.g. v1.7.1). For more options (docker run, DinD, container mode, using `main` or other tags) see the [User Guide](docs/guide.md). Probes, Prometheus metrics and logging: [Operations](docs/guide.md#5-operations) — `GET /health` (liveness), `GET /ready` (readiness), `GET /metrics`, `GET /version`.

Two copy-and-go deployments live in [`examples/deploy/`](examples/deploy/): `standalone/` (single container, runner processes inside the Manager — `docker run` or Compose) and `fleet/` (one container per runner, image and toolchain caches shared, build caches isolated).

## Use cases

- **Personal / team**: One machine as self-hosted runners for multiple repos or orgs; manage via Web UI, no need to remember CLI.
- **Internal CI**: Deploy on internal network; use DinD (isolated) or host-socket (shared with host) when jobs need Docker; runners recover after Manager or DinD restart.
- **Isolation & traceability**: Container mode gives one container per runner with clear boundaries; combine with registration result and GitHub visibility check to verify runners.

## Documentation

- **[User Guide](docs/guide.md)** — Deployment (Docker/docker-compose), config, adding runners, security & troubleshooting
- **[Deployment examples](examples/deploy/)** — Single-container and multi-container setups, what each cache shares or isolates, deployment pitfalls
- **[Development & Build](docs/development.md)** — Go build, local debug, HTTP API, Makefile
- **[Changelog](CHANGELOG.md)** — Release history and upgrade notes

## Other

CI / images / releases: [.github/workflows](.github/workflows).

MIT License — see [LICENSE](LICENSE).
