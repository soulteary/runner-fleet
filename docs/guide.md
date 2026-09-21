# User Guide

**文档 / Docs:** [EN](README.md) · [中文](zh/) · [Français](fr/) · [Deutsch](de/) · [한국어](ko/) · [日本語](ja/)

![](../.github/assets/fleet.jpg)

Deployment, configuration, adding runners, and security are covered here. For contributor build and API details see [Development & Build](development.md).

---

## 1. Deployment (Docker)

- **Linux only**, `linux/amd64` and `linux/arm64`. Whether a runner is running is read from the process table through `/proc`; on any other OS every runner reports "not running", so start/stop and the self-heal sweep cannot work. Published images cover those two architectures.
- **The interface and its messages follow your language; the logs are fixed to English.** The UI and what the server says back — API messages and toasts — are chosen from `?lang=`, the language cookie, or `Accept-Language`. A script that sends no `Accept-Language` gets English. Logs are English by decision, not by omission: a log line has no request to follow, its reader is whoever operates the deployment, and grep, Loki queries and alert rules all break when the same event shows up in a different language each run. The startup self-check goes to the log as well, prefixed `[preflight …]`.
- Image is **Ubuntu**-based with .NET Core 6.0 dependencies; runs as **UID 1001**—host-mounted dirs must be writable by that user (e.g. `chown 1001:1001 config runners`).
- ~15 seconds after start, registered but stopped runners are auto-started; periodic check every 5 minutes.

### Use published image (recommended)

Production: use a specific version (e.g. v1.8.0). For development you can use the `main` tag.

```bash
docker pull ghcr.io/soulteary/runner-fleet:v1.8.0
```

### docker-compose quick start

The repo root has `docker-compose.yml`. Enable DinD only when using container mode and jobs need Docker with `job_docker_backend: dind`.

```bash
mkdir -p config runners && cp config.yaml.example config/config.yaml
# Edit config/config.yaml: set runners.base_path to /app/runners

sudo chown -R 1001:1001 config runners

docker network create runner-net 2>/dev/null || true
docker compose up -d
# If job_docker_backend: dind: docker compose --profile dind up -d
```

UI: http://localhost:8080. Auth details in [4. Security & validation](#4-security-and-validation).

### Run container (full args)

Mount the `config` directory (not the file `config/config.yaml` alone, or Docker may create an empty file if missing) and `runners`; port must match `server.port` in config (default 8080).

```bash
docker run -d --name runner-manager \
  -p 8080:8080 \
  -v $(pwd)/config:/app/config \
  -v $(pwd)/runners:/app/runners \
  ghcr.io/soulteary/runner-fleet:v1.8.0
```

Host dirs must be writable by UID 1001. Basic Auth: `-e BASIC_AUTH_PASSWORD=password`, `-e BASIC_AUTH_USER=admin`. For Docker in jobs add `-v /var/run/docker.sock:/var/run/docker.sock` plus `--group-add $(getent group docker | cut -d: -f3)` if the host docker GID is not 999 (the image ships a `docker` group at GID 999, overridable with build arg `DOCKER_GID`), or use DinD (see repo `docker-compose.yml` `--profile dind`). Both images ship the Docker CLI plus a command-line base layer aligned with GitHub-hosted runners: `scripts/apt-packages.txt` mirrors the `apt` package set from `actions/runner-images` (`toolset-2404.json`), so `git`, `unzip`, `jq`, `rsync`, `sudo`, `xvfb` and the rest are present. Language and platform SDKs are deliberately not included — use the `setup-*` actions or extend the image. Like hosted runners, both images give the job user passwordless `sudo`, so `sudo apt-get install -y …` works; build with `--build-arg ALLOW_SUDO=false` to drop it.

### Auto install & register

In the UI "Quick Add Runner" enter name, target, token and submit; the install script runs first, then register and start. On failure:

```bash
docker exec runner-manager /app/scripts/install-runner.sh <name> [version]
```

The script picks the architecture from `uname -m`, resolves the latest runner version from the GitHub API when none is given, and **always** verifies the SHA-256. If the official hash cannot be fetched (offline, or a mirror), pass it explicitly: `RUNNER_SHA256=<sha256> ... install-runner.sh <name> <version>`. A directory that already contains a runner is left alone unless `RUNNER_FORCE_REINSTALL=1` is set.

Or on the host extract [actions-runner](https://github.com/actions/runner/releases) under `runners/<name>/`, then submit in the UI or run `./config.sh` manually.

### Container mode (runner per container)

Each runner runs in its own container; Manager starts/stops via host Docker and gets status over HTTP from the in-container Agent.

**Option 1: Env only (recommended for full-container)**
No need to edit config/config.yaml. Copy `cp .env.example .env` and set e.g. `CONTAINER_MODE=true`, `VOLUME_HOST_PATH=<host absolute path to runners>` (e.g. `realpath runners`), `JOB_DOCKER_BACKEND=host-socket`, `CONTAINER_NETWORK=runner-net`. If you do not create `config/config.yaml`, the program will generate it on first start from these env vars. If `RUNNER_IMAGE` is unset, the runner image is derived from `MANAGER_IMAGE` (e.g. `v1.8.0` → `v1.8.0-runner`). Mounted `config` and `runners` still need `chown 1001:1001`. See `.env.example` for all override variables.

**Option 2: Enable in config/config.yaml** (see `config.yaml.example`):

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

Runner image: same name as Manager with `-runner` tag (production: use a version tag e.g. v1.8.0-runner; dev: main-runner), or build locally: `docker build -f Dockerfile.runner -t ghcr.io/soulteary/runner-fleet:v1.8.0-runner .`. Manager must use host Docker (mount `docker.sock`), not DinD via `DOCKER_HOST`; in Compose use `group_add` for host docker GID or `user: "0:0"`. For `job_docker_backend: host-socket`, the Manager passes `--group-add <host docker GID>` to the runner container (auto-detected from `docker.sock`, override with `runners.docker_gid` / `DOCKER_GID`); the image also ships a `docker` group (build arg `DOCKER_GID`, default 999). Runner names are normalized to container names; duplicates after mapping will conflict.

**Extending the runner image**: GitHub-hosted runners bundle toolchains (Android SDK, Node, Python…) that self-hosted runners do not. Workflows written for `ubuntu-24.04` often rely on this implicitly and fail after the move — `SDK location not found`, `node: command not found`. Layer your toolchain on top of this repo's runner image; ready-to-use examples and the four rules that matter (chown to UID 1001, bake env vars into the image, passwordless sudo is inherited, warm caches after `USER app`) are in [`examples/runner-images/`](../examples/runner-images/). Point a single runner at it with `items[].container_image` and select it from the workflow with a label.

**Config changes and container rebuilds**: image, network, mount directory and the in-job Docker backend are fixed at `docker create` time — an existing container keeps whatever it was created with, so changing the config alone never reaches it. The Manager compares each container's actual create parameters against the current config. A **stopped** container that no longer matches is removed and recreated the next time it is started — whether you hit Start or the Manager auto-starts it; the list marks the runner "config changed" and the tooltip names the difference (e.g. `job_docker_backend: → dind`). A **running** container is left alone — a job may be in flight — so use the row's "Recreate" button (`POST /api/runners/:name/recreate`, interrupts a running job) or stop it and start it again when idle. Rebuilding an image under the same tag counts too: the comparison is on image ID, not just the reference.

**Ready-made deployment examples**: [`examples/deploy/`](../examples/deploy/) holds two copy-and-go setups — `standalone/` (one Manager container with the runner processes inside it; `docker run` or Compose) and `fleet/` (container mode: one container per runner, image cache shared through the host daemon, toolchain and action caches baked into a layer of the runner image, build caches isolated per runner). Its README compares the two, spells out which caches are shared and which are isolated, and collects the deployment pitfalls (directory ownership, `VOLUME_HOST_PATH`, disk growth under host-socket).

### Troubleshooting

- **Anything not working**: Check the startup self-test first — `docker compose logs runner-manager | grep '\[preflight'`. It reports the runners directory, Docker reachability, network, runner image and in-job Docker backend at boot, each failing item with a copy-pasteable fix.
- **Runner won't start after compose down**: Run `docker network create runner-net` once. If it still fails, use "Start" in the UI to recreate, or `docker rm -f github-runner-<name>` then "Start".
- **Running as root**: Mounted dirs must be writable by the process user; for root set `RUNNER_ALLOW_RUNASROOT=1`.
- **`permission denied` on docker.sock in jobs**: With `job_docker_backend: host-socket` the container user (UID 1001) must be in the socket's group. The Manager adds `--group-add` with the detected host docker GID when creating the container; a container created without the right GID is flagged as drifted and recreated the next time it is started (a running one shows "config changed" with a Recreate button). If detection fails, set `runners.docker_gid` (or `DOCKER_GID` in `.env`) to `getent group docker | cut -d: -f3`.
- **`command not found` or a missing SDK inside jobs**: Self-hosted runners do not bundle what GitHub-hosted ones do. Check the startup self-test (`docker compose logs runner-manager | grep '\[preflight'`) — it reports which of `git`/`unzip`/`tar`/`curl` each configured runner image lacks. For language and platform SDKs, extend the image; see [`examples/runner-images/`](../examples/runner-images/).
- **Old runner image**: Pull or rebuild it, then start the runner — the Manager notices the image changed (by reference and by image ID, so rebuilding the same tag counts) and recreates the container. A running container is not touched; use the row's "Recreate" button when you can afford to interrupt its job.
- **Log repeats `已定时拉起 runner: <name>` every 5 minutes, and no runner is ever shown as running**: fixed in this version — upgrade is enough, nothing needs re-registering. Running state used to come from a pid file (`Runner.Listener.pid`, falling back to `.path`), and actions/runner writes neither: none of its start scripts writes a pid file, and `.path` holds a PATH string. Every runner therefore read as "registered but not running", so the 5-minute sweep started each of them again on every pass. It is now read from the process table, and in container mode from the Agent inside each container — the Manager cannot see another container's processes. Same root cause: in default (non-container) mode "Stop" always failed with `未找到 runner pid 文件或 pid 无效`.
- **A runner you deleted is still listed on GitHub, and adding it back under the same name fails**: deletion now also deregisters it from GitHub, but only when that runner directory holds a `.github_check_token` (the optional PAT — organization needs `admin:org`, repository needs `repo`). Without one there is no credential to do it with, and the delete response says so and points at Settings → Actions → Runners. Runners deleted by older versions were never deregistered; remove those by hand.
- **A runner shows "GitHub check failed"**: the visibility check reached GitHub but could not get an answer — hover for the cause (expired token, insufficient scope, rate limit, target not visible). This is distinct from "Not on GitHub", which means GitHub answered and the runner was not in the list. Older versions reported both as the latter.
- **status=unknown**: Check the probe in the detail popup; try "Start/Stop" to self-heal.

### Build images locally

```bash
docker build -t runner-manager .
docker build -f Dockerfile.runner -t ghcr.io/soulteary/runner-fleet:v1.8.0-runner .
```

Make: `make docker-build`, `make docker-run`, `make docker-stop`.

---

## 2. Configuration

```bash
mkdir -p config && cp config.yaml.example config/config.yaml
```

| Field | Description | Default |
|-------|-------------|---------|
| `server.port` | HTTP server port | `8080` |
| `server.addr` | Bind address; empty = all interfaces | empty |
| `runners.base_path` | Root path for runner install dirs; **set to `/app/runners` in container** | `./runners` |
| `runners.items` | Predefined runner list | Can also add via Web UI |
| `runners.container_mode` | Enable container mode | `false` |
| `runners.container_image` | Runner image in container mode (tag with -runner) | `ghcr.io/soulteary/runner-fleet:v1.8.0-runner` |
| `runners.container_network` | Network for runners in container mode | `runner-net` |
| `runners.agent_port` | In-container Agent port | `8081` |
| `runners.job_docker_backend` | Docker in jobs: `dind` / `host-socket` / `none` | `dind` |
| `runners.dind_host` | DinD hostname when `job_docker_backend=dind` | `runner-dind` |
| `runners.docker_gid` | Host docker group GID added to runner containers when `job_docker_backend=host-socket`; empty or `0` detects it from `docker.sock` | empty (auto-detect) |
| `runners.volume_host_path` | Host absolute path to runners in container mode (required) | empty |
| `runners.items[].name` | Display name; also the install directory name and, in container mode, the container name. Unique, and read-only once created | required |
| `runners.items[].path` | Subdirectory under `base_path`; empty uses `name` | empty (= `name`) |
| `runners.items[].target_type` | `org` or `repo` | required |
| `runners.items[].target` | Org name, or `owner/repo` | required |
| `runners.items[].labels` | Custom labels; a workflow's `runs-on` selects on them | empty |
| `runners.items[].container_image` | Per-runner image override (container mode); falls back to the global value | empty |
| `runners.items[].job_docker_backend` | Per-runner Docker backend override (container mode); falls back to the global value | empty |
| `runners.resources` | Resource limits for runner containers (`cpus` / `memory` / `memory_swap` / `pids_limit`), passed to `docker create` and applied to existing containers via `docker update` on start | empty (unlimited) |

Every field above that a container deployment needs to change can also come from the environment, so a full-container setup is `.env` only — see [Environment variables](#environment-variables) below.

### Environment variables

Read once at startup, so changing one needs a Manager restart. Where two names map to the same
setting they are read in the order listed, so the **second** one wins if both are set.

| Variable | Overrides | Default / note |
|---|---|---|
| `MANAGER_PORT`, `SERVER_PORT` | `server.port` | `8080`. Compose pins `SERVER_PORT` inside the container and maps `MANAGER_PORT` to it, so the published port can differ from the listening one |
| `SERVER_ADDR` | `server.addr` | empty (all interfaces) |
| `RUNNERS_BASE_PATH` | `runners.base_path` | `./runners`; `/app/runners` in the image |
| `CONTAINER_MODE` | `runners.container_mode` | `false`. Only `true` and `1` are read: this variable turns container mode **on, never off**, so one stray value cannot silently change a deployment's shape |
| `RUNNER_IMAGE`, `CONTAINER_IMAGE` | `runners.container_image` | Unset: derived from `MANAGER_IMAGE` (`:v1.8.0` → `:v1.8.0-runner`), else from `FLEET_IMAGE_TAG` |
| `CONTAINER_NETWORK` | `runners.container_network` | `runner-net` |
| `VOLUME_HOST_PATH`, `RUNNERS_VOLUME_HOST_PATH` | `runners.volume_host_path` | empty; required in container mode |
| `JOB_DOCKER_BACKEND` | `runners.job_docker_backend` | `dind` |
| `DOCKER_GID` | `runners.docker_gid` | empty = detect from `docker.sock` |

These have no config-file counterpart:

| Variable | What it does | Default |
|---|---|---|
| `BASIC_AUTH_PASSWORD` | Setting it enables Basic Auth | empty (no auth) |
| `BASIC_AUTH_USER` | Basic Auth user name | `admin` |
| `TRUSTED_ORIGINS` | Comma-separated origins exempt from the cross-site check; see [4. Security and validation](#4-security-and-validation) | empty |
| `LOG_LEVEL` | `trace` / `debug` / `info` / `warn` / `error` | `info` |
| `LOG_FORMAT` | `console` for people, `json` for ELK or Loki; an unrecognized value falls back to `console` rather than failing startup | `console` |
| `DOCKER_HOST` | Which Docker daemon the **Manager itself** uses. Container mode needs the host socket — pointing this at DinD breaks runner creation | `unix:///var/run/docker.sock` |
| `MANAGER_IMAGE` | Which Manager image Compose pulls; the runner image is derived from it | the release tag |
| `FLEET_IMAGE_TAG` | Tag for the default runner image when nothing else decides it | `v1.8.0` |

`scripts/install-runner.sh` additionally reads `RUNNER_VERSION`, `RUNNER_SHA256` and
`RUNNER_FORCE_REINSTALL` — see [Auto install & register](#auto-install--register).

Inside a runner container the Agent reads `AGENT_TOKEN`, `AGENT_PORT` and `RUNNER_INSTALL_DIR`.
The Manager sets all three when it creates the container; setting them by hand is not part of
any normal deployment.

**Validation**: No duplicate names; container mode checks for container name conflicts. `job_docker_backend` only allows `dind`/`host-socket`/`none`; in container mode with container `base_path`, `volume_host_path` is required. Omitted `job_docker_backend` defaults to `dind`. After changing the backend, a **stopped** container is rebuilt on its next start, while a **running** one is marked "config changed" and the row's "Recreate" button applies it immediately — see "Config changes and container rebuilds" above.

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

**Registration result**: Written to `.registration_result.json` in that runner dir. **GitHub visibility check** (optional): Put `.github_check_token` (PAT; org needs `admin:org`, repo needs `repo`) in the runner dir; checked ~every 5 minutes, result in `.github_status.json`. The same check also records whether GitHub says the runner is **running a job**, shown as a “Busy” badge in the list and as its own row in the config dialog. It shares that ~5-minute cadence, so it can lag by up to 5 minutes; without the PAT it stays “unknown” rather than being reported as idle. The list’s “Registered” and “GitHub ✓” both link to that target’s Runners settings page on GitHub.

**Name conflict check**: While you type a name, the form asks `/api/runner-precheck` and shows what would go wrong before you submit — a runner with that name already in the config, a name that normalizes to a container name already in use, an install directory taken by another runner, a leftover directory that already holds a registered runner (`.runner`), or a leftover container of that name on the host. Blocking findings are shown in red with a one-click suggested name; warnings (a non-empty directory that will be reused) let you continue. Submitting anyway is rejected server-side with **409** plus the same conflicts — the old behaviour of silently appending a random suffix is gone (send `auto_rename: true` if you want it back).

**Deleting a runner**: it is stopped, deregistered from GitHub when that runner's directory holds a PAT, and **its install directory is deleted** — only when that directory is under `runners.base_path`, so a misconfigured path cannot take a system directory with it. `_work` and any caches under it go too, so there is nothing to recover afterwards.

Multiple runners per machine: use separate subdirs.

---

## 4. Security and validation

**Auth**: No login by default; use only on internal network or localhost. Set env `BASIC_AUTH_PASSWORD` to enable Basic Auth; `BASIC_AUTH_USER` optional (default `admin`). All routes except `GET /health` and `GET /ready` require auth; do not commit secrets—use `.env`. In container: `-e BASIC_AUTH_PASSWORD=...` or compose `env_file`.

**Cross-site requests**: write endpoints reject anything the browser reports as cross-site, so a page on another origin cannot drive this API with your cached Basic Auth credentials. Nothing to configure. If a reverse proxy rewrites `Host` so that your own requests are refused, list the browser-visible origins in `TRUSTED_ORIGINS` (comma-separated). Non-browser callers (curl, CI scripts) are unaffected — they carry no cached credentials, so they cannot be the attacker here. See [Development](development.md) for the exact rule.

**Paths & uniqueness**: name/path must not contain `..`, `/`, `\`; dirs must be under `runners.base_path`. No duplicate names; name is read-only when editing. In container mode names are normalized to container names; duplicates after mapping will error.

**Agent auth** (container mode): the Manager writes a random per-runner token to `<runner dir>/.agent_token` (mode 0600), injects it into the container as `AGENT_TOKEN`, and sends it as `Authorization: Bearer` on every Agent call. The env var is what the Agent reads, so this does not depend on the Manager and the Agent sharing a UID; the file is the Manager's persistent copy so a Manager restart does not require recreating containers. The Agent rejects `/status`, `/start` and `/stop` without it, so other containers on the same network can no longer control runners; `/health` stays open for the container HEALTHCHECK. Containers created before this change have no token injected and keep working unauthenticated; they are now flagged as drifted and recreated the next time they start (a running one shows "config changed" with a Recreate button).

**Sensitive files**: config/config.yaml and .env are in `.gitignore`. For each runner's `.github_check_token` use `chmod 600`; add `**/.github_check_token` to `.gitignore` if under version control.

**Runner directory permissions**: each runner's install directory is created 0700. `config.sh` writes `.credentials_rsaparams` there — the RSA private key the runner authenticates to GitHub with — and actions/runner sets no Unix permissions on it, so the directory mode is what keeps other local users on the host from reading it and impersonating that runner. Directories created by earlier versions are still 0755; the startup self-test names them (`docker compose logs runner-manager | grep '\[preflight'`) with the exact `chmod 700` to run. It does not change them for you: under a UID mismatch (Manager as root, container as app(1001)) tightening a directory breaks a deployment that currently works, so look before you run it.

---

## 5. Operations

### Probes

| Path | Purpose |
|---|---|
| `GET /health` | Liveness. 200 for as long as the process is up, with no dependency checks — it stays 200 when the config is broken or the mount is unusable. For a K8s `livenessProbe`: a restart is the right answer to a hung process, and the wrong one to a bad config |
| `GET /ready` | Readiness. 503 when the config cannot be loaded, or when `runners.base_path` is missing or not writable — it write-probes, so it catches the directory-ownership mistake that `/health` cannot see. For a K8s `readinessProbe`, and the one to check after changing a deployment |

Both stay unauthenticated when Basic Auth is on, because a probe carries no credentials.
Neither reports *which* check failed; that is in the log.

### Metrics

`GET /metrics` serves Prometheus text — per-route call volume and latency. The `path` label is
Echo's route template (`/api/runners/:name`), not the request URL, so a fleet of runners does
not become a fleet of label values.

Unlike the probes, `/metrics` **requires auth** when Basic Auth is enabled: it exposes the call
volume of every endpoint, which is operational data with no reason to be more public than
`/api`. Configure the scrape job for it:

```yaml
scrape_configs:
  - job_name: runner-fleet
    static_configs:
      - targets: ['runner-manager:8080']
    basic_auth:
      username: admin
      password: <BASIC_AUTH_PASSWORD>
```

### Upgrading

```bash
docker compose pull && docker compose up -d
```

Nothing needs re-registering: runner identity lives in each runner's install directory, which the
upgrade does not touch.

In container mode the runner containers still exist with the **old** runner image, because image,
network, mount directory and the rest are fixed at `docker create` time. The Manager notices and
repairs that on its own — a **stopped** runner is rebuilt on its next start, a **running** one is
flagged "config changed" and rebuilt when you hit Recreate or stop and start it while idle. Two
details decide whether the new image is actually there to build from:

- A **version tag** pulls itself: after a version bump the new `-runner` tag is not present
  locally, so `docker create` fetches it.
- A **mutable tag** (`:main`, or the same version tag rebuilt) already resolves locally, so
  `docker create` reuses the stale image. Pull it yourself first — `docker pull <runner image>`.
  Drift is compared on image **ID** as well as reference, so once the pull lands the rebuild
  happens as usual.

`runners.resources` is the one setting that reaches an existing container without a rebuild: the
Manager applies it with `docker update` on start, so upgrading into a version that supports limits
does not require recreating everything.

Read the [Changelog](../CHANGELOG.md) for the release you are jumping to — breaking changes and
anything needing a manual step are called out there.

### What to back up

The README says the config is your backup. That is true of *configuration* and not of *identity*:
a runner's credentials live in its install directory, and without them a restored deployment has
to be re-registered by hand.

Back up `config/config.yaml` and each `runners/<name>/` directory, excluding `_work/`.

| In `runners/<name>/` | Written by | If you lose it |
|---|---|---|
| `.runner`, `.credentials_rsaparams` and the other files `config.sh` wrote | actions/runner | The runner is gone. Re-register it, and delete the stale entry on GitHub first — a new registration under the same name fails while the old one is listed |
| `.agent_token` | Manager (mode `0600`) | Regenerated; the container is flagged as drifted and rebuilt on its next start |
| `.github_check_token` | You, optionally | The visibility check stops, and deleting the runner can no longer deregister it from GitHub |
| `.registration_result.json`, `.github_status.json` | Manager | Cosmetic — both are rebuilt by the next registration or check |
| `_work/` | The jobs | Nothing worth keeping. It is checkouts and build output, it is the largest thing on disk, and it grows |

Ownership matters on restore: everything must end up owned by UID 1001, the same
`sudo chown -R 1001:1001 config runners` as the first install. `GET /ready` write-probes the
runners directory, so it is the fastest way to confirm a restore is actually usable.

### Behind a reverse proxy

The Manager speaks plain HTTP and has no TLS of its own, so the proxy terminates TLS. This matters
more than usual here: Basic Auth sends the password on every request, and without TLS it crosses
the network in the clear on each one.

The cross-site check does not need configuring. It reads `Sec-Fetch-Site`, which the browser
computes locally, so a proxy rewriting `Host` cannot break it. `TRUSTED_ORIGINS` is the escape
hatch for the case where it does — list the origins as the browser's address bar shows them,
comma-separated, and keep the list to origins you control. See
[4. Security and validation](#4-security-and-validation).

Point the proxy's health check at `/health` and its readiness gate, if it has one, at `/ready`.
Both stay unauthenticated. Do not expose `/metrics` publicly: with Basic Auth on it requires
credentials, and with Basic Auth off it is as open as everything else.

### Version and logs

`GET /version` returns the version and nothing else. Build details are deliberately not there:
`go_version` would let anyone match a published Go runtime CVE to the exact runtime you are
running. `runner-manager -version` prints the commit, build date, Go version and platform —
that one runs on the host and is not exposed.

`LOG_LEVEL` and `LOG_FORMAT` control the log; set `LOG_FORMAT=json` when shipping to ELK or
Loki. The startup self-check writes one line per item, each prefixed `[preflight …]` — that
prefix is the grep anchor used throughout [Troubleshooting](#troubleshooting).

[← Back to project home](../README.md)
