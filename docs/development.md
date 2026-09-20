# Development & Build

**文档 / Docs:** [EN](README.md) · [中文](zh/) · [Français](fr/) · [Deutsch](de/) · [한국어](ko/) · [日本語](ja/)

![](../.github/assets/fleet.jpg)

For production use container deployment; see [User Guide](guide.md). This doc is for contributors: local build and debug.

## Requirements

- Go 1.26 (match [go.mod](../go.mod)).

## Build

```bash
# Build runner-manager binary
go build -o runner-manager ./cmd/runner-manager

# With version (for /version and debugging)
go build -ldflags "-X main.Version=1.4.0" -o runner-manager ./cmd/runner-manager

# Build Runner Agent only (container mode)
go build -o runner-agent ./cmd/runner-agent

# Or Make: make build / make build-agent / make build-all
```

Templates are embedded in the Manager binary (`cmd/runner-manager/templates/`); single binary, no need to ship `templates/`.

## Local run & debug

```bash
mkdir -p config && cp config.yaml.example config/config.yaml
go run ./cmd/runner-manager
# Or make run (build then run); custom config: ./runner-manager -config /path/to/config.yaml
```

Listens on `:8080`, http://localhost:8080. Basic Auth for debug: `BASIC_AUTH_PASSWORD=secret go run ./cmd/runner-manager`; see [User Guide – Security](guide.md#4-security-and-validation).

## CLI flags

- `-config <path>`: Config file path.
- `-version`: Print version and exit (inject at build with `-ldflags "-X main.Version=..."`).

## HTTP API

With Basic Auth, all requests except `/health` must include `Authorization: Basic <base64(user:password)>` in the header.

| Path | Method | Description |
|------|--------|-------------|
| `/health` | GET | Returns `{"status":"ok"}`; for Ingress/K8s probes; always unauthenticated. |
| `/version` | GET | Returns `{"version":"..."}`. |
| `/api/runners` | GET | Runner list. In container mode, on probe failure returns `status=unknown` with structured `probe` (`error/type/suggestion/check_command/fix_command`). |
| `/api/runners/:name` | GET | Single runner details. Same `probe` on probe failure in container mode. In container mode the response also carries `container_drift` when the container's create parameters no longer match the config. |
| `/api/runners/:name/start` | POST | Start runner. On probe failure still attempts start, returns structured `probe` in response. |
| `/api/runners/:name/stop` | POST | Stop runner. On probe failure still attempts stop, returns structured `probe` in response. |
| `/api/runners` | POST | Add a runner (optionally install and register). On a name conflict returns **409** with `conflicts` and `suggested_name` instead of silently renaming; send `auto_rename: true` for the old auto-suffix behaviour. |
| `/api/runners/:name/recreate` | POST | Remove and recreate the runner container with the current config (container mode only). Interrupts a job running on it — starting a stopped container already recreates it automatically when its create parameters drifted. |
| `/api/runner-precheck` | GET | Pre-flight a name before adding: `?name=&path=`. Returns `available`, a `suggested_name` and the `conflicts` found (`name_taken`, `container_name`, `install_dir`, `dir_registered`, `dir_adopt`, `dir_exists`, `container_exists`), each with `level` (`error`/`warn`), `message`, `detail` and an optional `fix_command`. Read-only; the Web UI calls it while you type. |

### Breaking change (upgrade note)

Legacy flat `probe_*` fields are removed; use the `probe` object: `probe.error`, `probe.type`, `probe.suggestion`, `probe.check_command`, `probe.fix_command`. `probe.type` values: `docker-access`, `agent-http`, `agent-connect`, `unknown`. Web UI can still "Start/Stop" for self-heal when `status=unknown`.

Example (probe failure):

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

## Makefile targets

- `make help`: List all targets.
- `make build`: Build Manager (with Version ldflags).
- `make build-agent`: Build Runner Agent (container mode).
- `make build-all`: Build Manager and Agent.
- `make test`: Run tests.
- `make run`: Build then run Manager.
- `make docker-build` / `make docker-run` / `make docker-stop`: Manager image build and run; see [User Guide](guide.md).
- `make docker-build-runner`: Build Runner image for container mode (`Dockerfile.runner`, default tag in `RUNNER_IMAGE`).
- `make docker-build-runner-example`: Build a custom Runner image example (`EXAMPLE=android|node`, see [`examples/runner-images/`](../examples/runner-images/)).
- `make clean`: Remove built binaries (runner-manager, runner-agent).

Container mode uses Agent from `cmd/runner-agent` and Runner image from `Dockerfile.runner`.


## Releasing

Version references in docs and examples must match the default image tag in `internal/config/config.go`. CI enforces this via `scripts/check-version-consistency.sh`; run it locally before opening a release PR:

```bash
sh scripts/check-version-consistency.sh
```

If a line legitimately cites an older version (release notes, upgrade instructions), append a `version-check-ignore` marker to that line to skip it.

[← Back to docs](README.md)
