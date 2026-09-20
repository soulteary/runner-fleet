# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Fixed

- Starting or stopping a runner no longer dies with the browser request. The lifecycle context was derived from the HTTP request, so a page reload (the UI reloads 5 seconds after a runner is added, and the message box close button reloads too) cancelled the in-flight request, and `exec.CommandContext` turned that into a SIGKILL for the `docker` child — leaving `docker create 失败。输出: (无输出): signal: killed` in the log while the daemon may already have created the container, so the next start hit a name conflict. Start/stop now keep their timeout but drop the request's cancellation, matching what runner removal and the background registration worker already did.

### Added

- Runner containers are rebuilt when the config they were created with no longer matches. Image, network, mount directory and the in-job Docker backend are fixed at `docker create` time, so until now changing `job_docker_backend`, `container_image`, `container_network` or `volume_host_path` left existing containers untouched — a runner created under `host-socket` kept full access to the host daemon after a switch to `dind`, and nothing said so. The Manager now compares each container's actual create parameters against the config: a **stopped** container that drifted is removed and recreated on Start, and the UI marks the runner "config changed" with the exact difference in the tooltip (e.g. `job_docker_backend: → dind`). A **running** container is never recreated on its own — a job may be in flight — so the row offers a "Recreate" button, backed by `POST /api/runners/:name/recreate`. Rebuilding an image under the same tag is detected too, since the comparison includes the image ID. `runners.resources` keeps being applied to existing containers via `docker update` and does not trigger a rebuild. The network a container was created on is recorded as a label too, because `docker create --network` sets exactly one network while a container can be attached to more afterwards: asking only whether it is attached to the configured network would accept a secondary attachment and leave the original connection — the one the config change meant to drop — in place. Containers from older versions carry no label and fall back to `HostConfig.NetworkMode`, which is bounded the same way, since a false positive there converges after one labelled rebuild. The backend a container was created with is recorded as a container label, so a custom runner image that sets `ENV DOCKER_HOST` itself is not mistaken for one of ours; containers created by older versions carry no label and fall back to inference from `DOCKER_HOST` and the socket mount. That inference deliberately does not compare against the *current* `dind_host`: a container was created under whatever the config said then, and switching to `none` usually comes with changing or dropping `dind_host` in the same edit, so recognizing only the current value would miss a container still holding `DOCKER_HOST=tcp://old-dind:2375` — exactly the route `none` is meant to cut. Any `tcp://…:2375` is therefore treated as possibly ours. The cost is bounded: a pre-label container whose image sets its own `ENV DOCKER_HOST` to that shape is rebuilt once needlessly, after which it carries the label and is never inferred about again.
- A container created before per-runner agent tokens existed (no `AGENT_TOKEN` injected, so its Agent accepts unauthenticated calls from anything on the same network) now counts as drifted and is recreated on the next start. The comparison asks only whether the container has a non-blank token, never whether it matches the current one — a rotated token is a runtime failure the probe already surfaces (the Agent answers 401), not a reason to delete a container. Blank counts as missing, on purpose: the Agent trims `AGENT_TOKEN` and falls through to the mounted `.agent_token` when it is empty, so an image declaring `ENV AGENT_TOKEN=` plus a token file the container UID cannot read leaves authentication off with no 401 to reveal it — and a presence-only test would let exactly that container skip the rebuild this entry is about. The status path creates the runner's token if it does not exist yet, rather than only reading it: a pre-token container that is **already running** at upgrade time has no token file, both auto-start loops skip containers that are already running, so nothing would ever create one — the comparison would keep reading an empty token, skip itself, and the runner would sit there unauthenticated with nothing on screen to say so. Token creation is single-winner (`O_EXCL`), since that status path is deliberately not behind the per-runner lock — a list request must not block on a `docker create`. Two callers creating a token at once would otherwise each generate one and the later write would win on disk while the earlier one was injected into the container, leaving the Agent on token A, the Manager reading token B, and every call answered 401 with a non-blank env that the drift check would never flag.
- Lifecycle operations are serialized per runner. Manager startup auto-start, the 5-minute periodic start, post-registration start and UI clicks can all land on the same runner at once; that used to leave a `container name is already in use` line in the log, and with rebuilds in the mix two interleaved calls could remove a container the other had just created.
- Conflict pre-check when adding a runner. The name field asks `/api/runner-precheck` while you type and reports, before you submit: a runner of that name already in the config, a name that normalizes to a container name already taken, an install directory owned by another runner, a directory that already holds a registered runner (`.runner`), and a leftover container of that name on the host. Blocking findings come with a one-click suggested name and, where useful, a ready-to-run fix command. Warnings do not block: a non-empty directory that will be reused, and — when no registration token is filled in — adopting an already registered runner directory, which is a legitimate way to take an existing runner into the config. With a token that same directory is blocking, because `config.sh` refuses to configure it twice.
- `examples/deploy/`: two copy-and-go deployments — `standalone/` (one Manager container with the runner processes inside, via `docker run` or Compose) and `fleet/` (container mode, one container per runner, image cache shared through the host daemon, toolchain and action caches baked into a runner image layer, build caches isolated per runner). The README compares both, documents which caches are shared versus isolated and why, and collects the deployment pitfalls (directory ownership, `VOLUME_HOST_PATH`, host-socket disk growth).

### Changed

- `POST /api/runners` answers a name conflict with **409** plus `conflicts` and `suggested_name` instead of silently appending a random suffix — typing `droiddesk` no longer creates `droiddesk-ab12cd` without saying so. Send `auto_rename: true` to keep the old behaviour; the suggestion is now the predictable `name-2`, `name-3`, … The same checks also cover container-name collisions, install-directory collisions and host leftovers, which previously surfaced as a 500 while saving the config or as `docker create` failing later.
- `docker-compose.yml` now pins the in-container listen port to `SERVER_PORT` (default 8080) and maps `MANAGER_PORT` to it. Previously a numeric `MANAGER_PORT` also overrode `server.port` while the published target stayed 8080, so `MANAGER_PORT=9000` silently produced an unreachable port.

## [1.4.0] - 2026-09-18

### Added

- Custom Runner image workflow: extend the stock image with project toolchains (Android SDK, Node, …), with ready-to-use examples under `examples/runner-images/` and a `make docker-build-runner-example` target. Point a single runner at the image via `items[].container_image` and select it from the workflow with a label. ([#15])
- Startup preflight now checks every configured Runner image for `git` / `unzip` / `tar` / `curl` (one-shot container, no pull if the image is absent), listing misses and pointing at the extension examples instead of waiting for mid-Job `command not found`. ([#15])

### Changed

- CLI baseline aligned with GitHub-hosted `ubuntu-24.04`: `scripts/apt-packages.txt` now mirrors `actions/runner-images` `toolset-2404.json` apt sets (~77 packages, including `jq`, `rsync`, `sudo`, `xvfb`). Language and platform SDKs stay out on purpose — use `setup-*` actions or an extended image. ([#16])
- Both images grant the job user passwordless `sudo` (same as hosted runners); opt out with `--build-arg ALLOW_SUDO=false`. Preflight no longer treats `sudo` as a required binary — presence alone does not prove sudoers membership. ([#16])
- Default image tags across the docs, `docker-compose.yml`, `.env.example`, `config.yaml.example`, `Makefile`, examples and `DefaultRunnerContainerImage()` now point at `v1.4.0`.

### Upgrading

Recreate Runner containers so they pick up the expanded apt baseline and passwordless sudo:

```bash
docker rm -f github-runner-<name>   # then click "Start" in the UI to recreate
```

If you build custom images `FROM` this repo's runner tag, rebuild them against `v1.4.0-runner` so they inherit the new base layer.

## [1.3.0] - 2026-09-18

### Added

- Per-runner overrides for `container_image` and `job_docker_backend` under `runners.items[]`, falling back to the global values when unset. One machine can now serve projects with different toolchains — a Flutter + Android image for one runner, the default image for the rest — instead of forcing a single oversized image or a second Manager instance. ([#8])
- `runners.resources` caps runner containers via `docker create` (`cpus`, `memory`, `memory_swap`, `pids_limit`). Values are validated at load time rather than failing at container creation, and are also applied to pre-existing containers with `docker update` on start. Leaving it unset changes nothing. ([#9])
- Startup self-test: the Manager now checks the runners directory, Docker reachability, the container network, the runner image and the in-job Docker backend, logging each failure with a copy-pasteable fix. Nothing blocks startup. ([#10])
- CI enforces that version references in the docs and examples match the default image tag in `internal/config/config.go`, with a `version-check-ignore` marker for lines that legitimately cite older versions. ([#13])

### Fixed

- **Jobs failed with `git: command not found` and `Unable to locate executable file: unzip`.** The Manager image shipped only the .NET runner's runtime dependencies, so in default mode — where jobs run inside the Manager container — `actions/checkout` silently degraded to a tarball download with no `.git`, and `setup-gradle` could not unpack its distribution. `unzip` was missing from both images, so container mode hit the same wall. Both images now install from one shared manifest (`scripts/apt-packages.txt`) covering `git`, `unzip`, `zip`, `xz-utils`, `build-essential`, `gnupg`, `jq` and `openssh-client`; CI fails if either Dockerfile stops using it. ([#7])
- `install-runner.sh` hard-coded the `x64` architecture, so arm64 hosts installed an x86 runner. It now selects `x64` / `arm64` / `arm` from `uname -m` and fails on anything else. ([#12])
- `install-runner.sh` left the downloaded tarball behind whenever `RUNNERS_BASE_PATH` was relative — the repository's own local default — because the cleanup trap resolved its path a second time after `cd`. ([#12])

### Security

- The in-container Agent's `/status`, `/start` and `/stop` endpoints had no authentication, so any container on the shared Docker network could stop another runner. The Manager now issues a random per-runner token, injects it as `AGENT_TOKEN` at container creation and sends it as `Authorization: Bearer`; the Agent rejects unauthenticated control requests with a constant-time comparison. `/health` stays open for the container HEALTHCHECK. Containers created before this release keep working unauthenticated until recreated. ([#11])
- `install-runner.sh` verified the tarball checksum only for one hard-coded version and silently skipped verification for every other — worse than not verifying, since nothing told the operator. Verification is now mandatory: the hash comes from `RUNNER_SHA256`, the release asset's `digest`, the release notes, or the pinned fallback, and the script fails with instructions when none is available. `curl -f` also stops an HTTP error page from being written out as a tarball. ([#12])

### Upgrading

Runner containers created by an earlier version must be recreated before the Agent authentication and the resource limits take full effect:

```bash
docker rm -f github-runner-<name>   # then click "Start" in the UI to recreate
```

Resource limits alone are applied to existing containers on start via `docker update`; clearing them requires recreating the container.

## [1.2.0] - 2026-09-18

### Added

- `runners.docker_gid` config option, overridable with the `DOCKER_GID` environment variable, setting the docker group GID added to runner containers. When left unset the Manager auto-detects it by stat'ing `/var/run/docker.sock`, so most deployments need no configuration at all. ([#4])
- Both images now ship a `docker` group that the `app` user belongs to, with a `DOCKER_GID` build arg (default `999`). This covers setups that mount `docker.sock` into a plain `docker run` rather than going through the Manager. ([#4])

### Fixed

- **`job_docker_backend: host-socket`: `docker` inside jobs failed with `permission denied while trying to connect to the Docker daemon socket`.** The host socket was mounted into the runner container, but the container's `app` user (UID 1001) was not in the socket's group. The Manager now passes `--group-add <docker GID>` when creating runner containers; if the GID cannot be determined, the flag is omitted and behaviour is unchanged. ([#4], [#5])

### Changed

- Default image tags across the docs, `docker-compose.yml`, `.env.example`, `config.yaml.example`, `Makefile` and `DefaultRunnerContainerImage()` now point at `v1.2.0`. They had been left at `v1.0.0` since that release, so `docker compose up -d` pulled a three-releases-old Manager by default.
- The `docker compose` `runner-manager` service passes `DOCKER_GID` through to the Manager, in addition to using it for `group_add`.

### Upgrading

Runner containers created by an earlier version must be recreated before `--group-add` takes effect:

```bash
docker rm -f github-runner-<name>   # then click "Start" in the UI to recreate
```

## [1.1.1] - 2026-02-25

### Fixed

- Container mode: a failed status probe no longer blocks starting a runner. `StartRunner` now falls back to the on-disk status, so a runner that is registered on disk can still be started when the container probe reports `unknown`. ([#2])

## [1.1.0] - 2026-02-18

### Added

- Full-container deployment driven entirely from `.env`: `CONTAINER_MODE`, `VOLUME_HOST_PATH`, `JOB_DOCKER_BACKEND`, `CONTAINER_NETWORK`, `RUNNER_IMAGE`, `RUNNERS_BASE_PATH`, `SERVER_PORT` and `SERVER_ADDR` override `config/config.yaml`, with no need to edit the config file.
- `config/config.yaml` is generated from defaults plus environment variables on first start when the file does not exist.
- Runner image ships `build-essential`, `git` and `gnupg` for common GitHub Actions.

### Changed

- Config path moved to `config/config.yaml`; mount the `config` directory rather than the single file, so Docker cannot create an empty file when the host has none.

## [1.0.1] - 2026-02-18

### Changed

- The default runner image resolves to a stable version tag instead of `main-runner`. `DefaultRunnerContainerImage()` derives it from `MANAGER_IMAGE` (`image:tag` → `image:tag-runner`), falling back to `FLEET_IMAGE_TAG` and then to the released version.

## [1.0.0] - 2026-02-18

Initial release.

- Web UI (Golang + Echo) to view, add, edit, delete, register and start/stop multiple self-hosted GitHub Actions runners on one machine.
- YAML-only config, no database.
- Auto install and register from the UI, including parsing a pasted `./config.sh --url ... --token ...`.
- Container mode: one runner per container, with an in-container Agent the Manager drives over HTTP.
- In-job Docker via DinD or the host socket, plus Docker/`docker-compose` deployment out of the box.
- Self-heal and structured probes (error type, check/fix commands) for troubleshooting.
- Optional Basic Auth, optional PAT-based verification against GitHub's runner list, and a multi-language UI.

[Unreleased]: https://github.com/soulteary/runner-fleet/compare/v1.4.0...HEAD
[1.4.0]: https://github.com/soulteary/runner-fleet/compare/v1.3.0...v1.4.0
[1.3.0]: https://github.com/soulteary/runner-fleet/compare/v1.2.0...v1.3.0
[1.2.0]: https://github.com/soulteary/runner-fleet/compare/v1.1.1...v1.2.0
[1.1.1]: https://github.com/soulteary/runner-fleet/compare/v1.1.0...v1.1.1
[1.1.0]: https://github.com/soulteary/runner-fleet/compare/v1.0.1...v1.1.0
[1.0.1]: https://github.com/soulteary/runner-fleet/compare/v1.0.0...v1.0.1
[1.0.0]: https://github.com/soulteary/runner-fleet/releases/tag/v1.0.0
[#2]: https://github.com/soulteary/runner-fleet/pull/2
[#4]: https://github.com/soulteary/runner-fleet/issues/4
[#5]: https://github.com/soulteary/runner-fleet/pull/5
[#7]: https://github.com/soulteary/runner-fleet/pull/7
[#8]: https://github.com/soulteary/runner-fleet/pull/8
[#9]: https://github.com/soulteary/runner-fleet/pull/9
[#10]: https://github.com/soulteary/runner-fleet/pull/10
[#11]: https://github.com/soulteary/runner-fleet/pull/11
[#12]: https://github.com/soulteary/runner-fleet/pull/12
[#13]: https://github.com/soulteary/runner-fleet/pull/13
[#15]: https://github.com/soulteary/runner-fleet/pull/15
[#16]: https://github.com/soulteary/runner-fleet/pull/16
