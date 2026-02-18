# Development & Build

For production use container deployment; see [User Guide](guide.md). This doc is for contributors: local build and debug.

## Requirements

- Go 1.26 (match [go.mod](../go.mod)).

## Build

```bash
# Build runner-manager binary
go build -o runner-manager ./cmd/runner-manager

# With version (for /version and debugging)
go build -ldflags "-X main.Version=1.0.0" -o runner-manager ./cmd/runner-manager

# Build Runner Agent only (container mode)
go build -o runner-agent ./cmd/runner-agent

# Or Make: make build / make build-agent / make build-all
```

Templates are embedded in the Manager binary (`cmd/runner-manager/templates/`); single binary, no need to ship `templates/`.

## Local run & debug

```bash
cp config.yaml.example config.yaml
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
| `/api/runners/:name` | GET | Single runner details. Same `probe` on probe failure in container mode. |
| `/api/runners/:name/start` | POST | Start runner. On probe failure still attempts start, returns structured `probe` in response. |
| `/api/runners/:name/stop` | POST | Stop runner. On probe failure still attempts stop, returns structured `probe` in response. |

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
- `make clean`: Remove built binaries (runner-manager, runner-agent).

Container mode uses Agent from `cmd/runner-agent` and Runner image from `Dockerfile.runner`.

[← Back to docs](README.md)
