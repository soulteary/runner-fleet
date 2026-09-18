# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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

[Unreleased]: https://github.com/soulteary/runner-fleet/compare/v1.2.0...HEAD
[1.2.0]: https://github.com/soulteary/runner-fleet/compare/v1.1.1...v1.2.0
[1.1.1]: https://github.com/soulteary/runner-fleet/compare/v1.1.0...v1.1.1
[1.1.0]: https://github.com/soulteary/runner-fleet/compare/v1.0.1...v1.1.0
[1.0.1]: https://github.com/soulteary/runner-fleet/compare/v1.0.0...v1.0.1
[1.0.0]: https://github.com/soulteary/runner-fleet/releases/tag/v1.0.0
[#2]: https://github.com/soulteary/runner-fleet/pull/2
[#4]: https://github.com/soulteary/runner-fleet/issues/4
[#5]: https://github.com/soulteary/runner-fleet/pull/5
