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
go build -ldflags "-X main.Version=1.5.0" -o runner-manager ./cmd/runner-manager

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
| `/api/runners/:name` | DELETE | Remove a runner: stop it, deregister it from GitHub when a PAT is available, delete its install directory, drop it from the config. The response carries `github_deregistered` and a `message` that states what happened on the GitHub side. |
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

### How running state is determined

`internal/runnerproc` answers "is this runner's process alive?" by scanning `/proc` for a
process whose argv claims that install directory — `<dir>/bin/Runner.Listener`, or a shell
running `<dir>/run.sh` or `<dir>/run-helper.sh`.

It deliberately does **not** read a pid file, because actions/runner does not write one.
None of `run.sh`, `run-helper.sh.template` or `runsvc.sh` writes a pid anywhere; the pid
only ever lives in a shell variable. The `.path` file in an install directory holds a PATH
string, not a pid (`runsvc.sh` does `export PATH=$(cat .path)`). Reading either of those
names always failed, which made every runner report "registered but not running" forever.

The supervising shell counts as running even when `Runner.Listener` is momentarily gone:
`run-helper.sh` sleeps 5 seconds on exit code 2 and `run.sh` then relaunches the listener,
so a listener-only test would report the runner dead during every restart.

Two consequences for callers:

- `runner.List` is a disk-level view. In container mode its `Running` is always false —
  the Manager and the runners are in different PID namespaces. Use
  `runner.ListWithLiveStatus`, which asks each container's Agent, whenever the answer
  drives an action. It maps a probe failure to `status=unknown` rather than `installed`,
  so "registered but not running, so start it" cannot fire on a runner it could not reach.
- Detection needs `/proc`, so it is Linux-only; elsewhere it reports "not running".

### Runner directory permissions

A runner's install directory is created 0700. `config.sh` writes `.credentials_rsaparams`
there — the RSA private key the runner authenticates to GitHub with — and actions/runner
sets no Unix permissions on it (`ConfigurationStore` only sets the Windows Hidden attribute,
so the file follows the umask, usually 0644). The directory mode is therefore the only thing
standing between that key and other local users on the host.

`MkdirAll` leaves an existing directory's mode alone, so directories created by older
versions stay 0755. `Preflight` reports those with a ready-to-run `chmod 700` instead of
changing them: tightening a directory when the Manager and the container run as different
UIDs would break a working deployment, and that call belongs to a person.

`base_path` itself is left traversable — it holds no credentials, and it is the host mount
point in container mode.

This does not change what a job can reach. Under `job_docker_backend: host-socket` a job can
bind-mount any host path, which the deployment docs already warn about; the mode protects
against other local users, not against that.

### Runner removal and GitHub

`DELETE /api/runners/:name` removes a runner from this tool **and** from GitHub. The GitHub
side needs a credential, and the only one this tool ever has is the optional per-runner PAT
at `<runner dir>/.github_check_token` — the same file the visibility check uses. So
deregistration runs *before* the install directory is deleted, because that is where the
token lives, and skipping that order silently loses the ability to do it at all.

With no PAT the runner cannot be deregistered: GitHub wants a PAT or a fresh removal token,
and `config.sh remove` wants the latter. The response then says so and names where to delete
it by hand. It is worth saying rather than swallowing — a leftover runner makes the next
`config.sh --name <same name>` fail with `A runner exists with the same name`.

`registered_on_github` is a **nullable** bool: `true` / `false` are answers, `null` means no
answer was reached, and `github_check_error` carries why. Templates must not test it with
`{{if .RegisteredOnGitHub}}` — `html/template` judges a pointer by nil-ness alone, so a
pointer to `false` is true. Use the `GitHubYes` / `GitHubNo` / `GitHubUnknown` helpers on
`RunnerInfo`.

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
