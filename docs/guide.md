# User Guide

**文档 / Docs:** [EN](README.md) · [中文](zh/) · [Français](fr/) · [Deutsch](de/) · [한국어](ko/) · [日本語](ja/)

![](../.github/assets/fleet.jpg)

Deployment, configuration, adding runners, and security are covered here. For contributor build and API details see [Development & Build](development.md).

---

## 1. Deployment (Docker)

- Image is **Ubuntu**-based with .NET Core 6.0 dependencies; runs as **UID 1001**—host-mounted dirs must be writable by that user (e.g. `chown 1001:1001 config.yaml runners`).
- ~15 seconds after start, registered but stopped runners are auto-started; periodic check every 5 minutes.

### Use published image (recommended)

Production: use a specific version (e.g. v1.0.0). For development you can use the `main` tag.

```bash
docker pull ghcr.io/soulteary/runner-fleet:v1.0.0
```

### docker-compose quick start

The repo root has `docker-compose.yml`. Enable DinD only when using container mode and jobs need Docker with `job_docker_backend: dind`.

```bash
cp config.yaml.example config.yaml
# Edit config.yaml: set runners.base_path to /app/runners

chown 1001:1001 config.yaml
mkdir -p runners && chown 1001:1001 runners

docker network create runner-net 2>/dev/null || true
docker compose up -d
# If job_docker_backend: dind: docker compose --profile dind up -d
```

UI: http://localhost:8080. Auth details in [4. Security & validation](#4-security-and-validation).

### Run container (full args)

Mount `config.yaml` and `runners`; port must match `server.port` in config (default 8080).

```bash
docker run -d --name runner-manager \
  -p 8080:8080 \
  -v $(pwd)/config.yaml:/app/config.yaml \
  -v $(pwd)/runners:/app/runners \
  ghcr.io/soulteary/runner-fleet:v1.0.0
```

Host dirs must be writable by UID 1001. Basic Auth: `-e BASIC_AUTH_PASSWORD=password`, `-e BASIC_AUTH_USER=admin`. For Docker in jobs add `-v /var/run/docker.sock:/var/run/docker.sock`, or use DinD (see repo `docker-compose.yml` `--profile dind`). Image includes Docker CLI; common Actions work with DinD.

### Auto install & register

In the UI "Quick Add Runner" enter name, target, token and submit; the install script runs first, then register and start. On failure:

```bash
docker exec runner-manager /app/scripts/install-runner.sh <name> [version]
```

Or on the host extract [actions-runner](https://github.com/actions/runner/releases) under `runners/<name>/`, then submit in the UI or run `./config.sh` manually.

### Container mode (runner per container)

Each runner runs in its own container; Manager starts/stops via host Docker and gets status over HTTP from the in-container Agent.

**Option 1: Env only (recommended for full-container)**
No need to edit config.yaml. Copy `cp .env.example .env` and set e.g. `CONTAINER_MODE=true`, `VOLUME_HOST_PATH=<host absolute path to runners>` (e.g. `realpath runners`), `JOB_DOCKER_BACKEND=host-socket`, `CONTAINER_NETWORK=runner-net`. If `RUNNER_IMAGE` is unset, the runner image is derived from `MANAGER_IMAGE` (e.g. `v1.0.1` → `v1.0.1-runner`). Mounted `config.yaml` and `runners` still need `chown 1001:1001`. See `.env.example` for all override variables.

**Option 2: Enable in config.yaml** (see `config.yaml.example`):

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

Runner image: same name as Manager with `-runner` tag (production: use a version tag e.g. v1.0.0-runner; dev: main-runner), or build locally: `docker build -f Dockerfile.runner -t ghcr.io/soulteary/runner-fleet:v1.0.0-runner .`. Manager must use host Docker (mount `docker.sock`), not DinD via `DOCKER_HOST`; in Compose use `group_add` for host docker GID or `user: "0:0"`. Runner names are normalized to container names; duplicates after mapping will conflict.

### Troubleshooting

- **Runner won't start after compose down**: Run `docker network create runner-net` once. If it still fails, use "Start" in the UI to recreate, or `docker rm -f github-runner-<name>` then "Start".
- **Running as root**: Mounted dirs must be writable by the process user; for root set `RUNNER_ALLOW_RUNASROOT=1`.
- **Old runner image**: `docker rm -f github-runner-<name>`, then "Start" in the UI to recreate.
- **status=unknown**: Check the probe in the detail popup; try "Start/Stop" to self-heal.

### Build images locally

```bash
docker build -t runner-manager .
docker build -f Dockerfile.runner -t ghcr.io/soulteary/runner-fleet:v1.0.0-runner .
```

Make: `make docker-build`, `make docker-run`, `make docker-stop`.

---

## 2. Configuration

```bash
cp config.yaml.example config.yaml
```

| Field | Description | Default |
|-------|-------------|---------|
| `server.port` | HTTP server port | `8080` |
| `server.addr` | Bind address; empty = all interfaces | empty |
| `runners.base_path` | Root path for runner install dirs; **set to `/app/runners` in container** | `./runners` |
| `runners.items` | Predefined runner list | Can also add via Web UI |
| `runners.container_mode` | Enable container mode | `false` |
| `runners.container_image` | Runner image in container mode (tag with -runner) | `ghcr.io/soulteary/runner-fleet:v1.0.0-runner` |
| `runners.container_network` | Network for runners in container mode | `runner-net` |
| `runners.agent_port` | In-container Agent port | `8081` |
| `runners.job_docker_backend` | Docker in jobs: `dind` / `host-socket` / `none` | `dind` |
| `runners.dind_host` | DinD hostname when `job_docker_backend=dind` | `runner-dind` |
| `runners.volume_host_path` | Host absolute path to runners in container mode (required) | empty |

Some fields above can be overridden by environment variables (e.g. `MANAGER_PORT`, `CONTAINER_MODE`, `VOLUME_HOST_PATH`, `JOB_DOCKER_BACKEND`), so you can run full-container with only `.env` changes; see `.env.example`.

**Validation**: No duplicate names; container mode checks for container name conflicts. `job_docker_backend` only allows `dind`/`host-socket`/`none`; in container mode with container `base_path`, `volume_host_path` is required. Omitted `job_docker_backend` defaults to `dind`; after changing backend, restart runners from the UI.

Example:

```yaml
server:
  port: 8080
  addr: 0.0.0.0
runners:
  base_path: /app/runners
  items: []
```

---

## 3. Adding Runners

**Get token**: Repo/org → Settings → Actions → Runners → New self-hosted runner, copy token (~1 hour valid). Each runner needs a new token.

**Add in service**: In the UI "Quick Add Runner" enter name (unique), target type (org/repo), target, token (optional; if set, submit can auto-register and start). You can paste `./config.sh --url ... --token ...` from GitHub into "Parse from GitHub command" and click "Parse & fill". Auto-register is for GitHub.com only; GitHub Enterprise requires manual `config.sh` in the runner dir.

**When runner not installed**: Download from [GitHub Actions Runner](https://github.com/actions/runner/releases), extract to `runners/<name>/`, then enter token in the UI or run `./config.sh` there. With container deploy, submitting a token in the UI triggers install then register; container mode needs Runner image and `volume_host_path` configured first (see container mode above).

**Registration result**: Written to `.registration_result.json` in that runner dir. **GitHub visibility check** (optional): Put `.github_check_token` (PAT; org needs `admin:org`, repo needs `repo`) in the runner dir; checked ~every 5 minutes, result in `.github_status.json`.

Multiple runners per machine: use separate subdirs.

---

## 4. Security and validation

**Auth**: No login by default; use only on internal network or localhost. Set env `BASIC_AUTH_PASSWORD` to enable Basic Auth; `BASIC_AUTH_USER` optional (default `admin`). All routes except `GET /health` require auth; do not commit secrets—use `.env`. In container: `-e BASIC_AUTH_PASSWORD=...` or compose `env_file`.

**Paths & uniqueness**: name/path must not contain `..`, `/`, `\`; dirs must be under `runners.base_path`. No duplicate names; name is read-only when editing. In container mode names are normalized to container names; duplicates after mapping will error.

**Sensitive files**: config.yaml and .env are in `.gitignore`. For each runner's `.github_check_token` use `chmod 600`; add `**/.github_check_token` to `.gitignore` if under version control.

[← Back to project home](../README.md)
