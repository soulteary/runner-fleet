# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Security

- Write endpoints now reject requests the browser reports as cross-site. There was no CSRF defense of any kind: `AddRunnerRequest` and `UpdateRunnerRequest` carried `form` struct tags, so `POST /api/runners` accepted `application/x-www-form-urlencoded` — a CORS "simple request", sent without a preflight, with whatever Basic Auth credentials the browser had cached for this origin attached automatically. A page on any other origin could therefore add a runner, or stop one via `POST /api/runners/:name/stop`, simply by being opened by someone with the UI in their browser. `PUT` and `DELETE` always preflight and were never the exposed part; `POST` was, and the start/stop/recreate routes are all POST. With `BASIC_AUTH_PASSWORD` unset there is no authentication at all, so this only mattered where it was set — which is exactly the deployment that had taken the trouble to lock the UI down. The check reads `Sec-Fetch-Site` first (computed by the browser, so a reverse proxy rewriting `Host` cannot break it) and accepts only `same-origin`; `same-site` is refused, because a subdomain or another port is a different origin and this is an admin surface. Browsers too old to send it fall back to comparing `Origin` against `Host`. A request carrying **neither** header did not come from a browser — curl and CI scripts hold no cookie and no cached credentials, so they cannot be the attacker here — and passes, which keeps the API scriptable with no token or extra header anywhere. `TRUSTED_ORIGINS` (comma-separated) is the escape hatch for a proxy that rewrites `Host` badly enough to refuse your own requests. As a second layer the `form` tags are gone, so a form-encoded post binds to an empty struct and fails validation even if it got past the middleware; the Web UI already posted JSON, using `FormData` only to read fields out of the form element. The guard is tested through the real route table (`newEchoServer()`), not just as a function — a middleware that is correct but never mounted is the usual way this kind of protection fails.

### Fixed

- The five translated `development.md` files were a full release behind. The English original gained three sections in v1.5.1/v1.6.0 — how running state is determined, runner directory permissions, runner removal and GitHub — and an entire `## Tests` section, and not one of `zh`/`ja`/`ko`/`fr`/`de` followed: 197 lines against 106. Nothing failed, because a stale translation is still valid Markdown and still renders. The UI's i18n has had tests asserting the key sets match across all six languages since v1.6.0; the documentation half had nothing equivalent. All four sections are now translated, and `scripts/check-docs-structure.sh` compares the heading-level sequence of every `docs/<lang>/*.md` against its English original — heading text is supposed to differ, structure is not — as a job in `CI (Consistency)`, so the next skipped section fails the PR instead of going unnoticed for a release.
- The module path no longer contradicts the repository. `go.mod` declared `github.com/lab-dev/github-actions-runner-manager` while the repo lives at `github.com/soulteary/runner-fleet`, so `go install github.com/soulteary/runner-fleet/cmd/runner-manager@latest` could not resolve. Container deployments were unaffected, which is why it survived this long. All 27 files' imports move with it.

### Changed

- CI runs `go test -race`. Both CI workflows and both release workflows now use it, and `make test-race` runs locally what CI runs. The suite already passed under the race detector — this is a guard, not a fix — but nothing was stopping it from regressing, and this project's concurrency rests on conventions the type system does not enforce: the single-worker registration queue, the per-runner `runnerOps` lock, and the single-winner `os.Link` creation in `EnsureAgentToken`. A race there surfaces as an occasional wrong status in production, not as a test failure.
- Coverage of the paths that actually change things went from 66.8% to 77.3% overall. The gaps were not spread evenly: they sat almost entirely on the code with side effects, which is the code where an untested branch costs something. `internal/handler` 49.5% → 72.9%, `cmd/runner-agent` 45.8% → 69.2%, `cmd/runner-manager` 44.1% → 61.3%, `internal/runner` 70.0% → 75.9%. Newly covered: `runRegistrationJob` end to end against fake `config.sh` and `install-runner.sh` scripts, including each of the failure messages it enriches (duplicate name, expired/used token, running as root) and the `--name` / `--unattended` arguments whose absence caused the v1.5.1 bugs; `StopRunnerContainer` / `RemoveRunnerContainer` / `ContainerState` against a fake `docker` that can fail, covering "container is gone" versus "Docker is unreachable" — conflating those two would report a name as free and fail later at `docker create`; the Agent's token guard, which is the only thing standing between `/start`, `/stop` and any other container on the same Docker network, and had no tests at all; and `StopRunner` against a real process on Linux. `installRunnerScriptPath` became a `var` so a test can point it at a fake script, and the wiring inside `main()` moved into `newEchoServer()` — same reasoning as the v1.6.0 split, and what lets the CSRF guard be tested where it is mounted rather than only where it is defined.


## [1.6.0] - 2026-09-20

### Security

- Runner install directories are created private (0700) instead of world-traversable (0755). `config.sh` writes `.credentials_rsaparams` into that directory — the RSA private key the runner authenticates to GitHub with — and actions/runner sets no permissions on it: `ConfigurationStore` only marks the file Hidden, which is a Windows attribute, so on Linux the file takes the process umask and is typically 0644. Any local user on the host could therefore read a runner's key and impersonate it to GitHub: take its jobs and see the secrets passed to them. The project already wrote `.agent_token` as 0600 and told people to `chmod 600` their `.github_check_token`, so this was the one place the same care was missing. `MkdirAll` does not alter directories that already exist, so runners created by earlier versions keep their 0755; the startup self-test now names them with a ready-to-run `chmod 700`. They are reported rather than changed on the spot, because tightening a directory under a UID mismatch (Manager as root, container as app(1001)) would break a deployment that currently works. `base_path` itself stays traversable — it holds no credentials.
- The "runner added" message box escapes the API response before inserting it. Runner names only have to avoid `..`, `/` and `\`, so a name like `<img src=x onerror=…>` is accepted, stored in the config and echoed back in `message` and `install_dir`; both went into `innerHTML` raw, while the very next line escaped `data.output` and the status badge escaped its value. With no `BASIC_AUTH_PASSWORD` set there is no authentication at all, so the name did not have to come from the person who then sees it. A test now scans the whole template for any API field reaching `innerHTML` unescaped, rather than pinning the one line — the defect was precisely that the line next door was already doing it right.

### Fixed

- The Runner image builds again. `Publish image (Runner)` had failed on every push to `main` since the Agent started reading running state from the process table: that change put `internal/runnerproc` into `cmd/runner-agent`'s import graph, but `Dockerfile.runner`'s builder stage copies only `go.mod`, `go.sum` and `cmd/runner-agent`, so the build stopped at `no required module provides package .../internal/runnerproc`. Nothing caught it earlier because the one thing CI compiles — `go build ./cmd/runner-agent` — runs against the whole checkout, where that package is plainly there; only the narrowed image context lacks it, and no workflow built an image until the merge had already landed. The builder stage now copies all of `internal`, so the next package the Agent imports needs no matching edit here, and both CI workflows build their image's builder stage on every pull request — using the Dockerfile's own `COPY` list rather than a second copy of it, so the check cannot drift from what actually gets built.
- `runner.List(nil)` panicked. `ListWithLiveStatus` had a `cfg == nil` guard, but it ran *after* `List(cfg)`, which dereferences `cfg.Runners` immediately — so the guard was unreachable code. No caller passes nil today (both background loops only run once `config.Load` succeeded), which is why nothing had caught it; the new `cmd/runner-manager` tests did, on the first call.
- Removing a runner now deregisters it from GitHub instead of leaving it behind. Removal stopped the process or container, deleted the install directory and dropped the config entry — it never ran `config.sh remove` nor called the API, so GitHub kept an offline runner under that name forever. Adding a runner back under the same name then failed with `A runner exists with the same name`, and by that point the local credentials had been deleted, so there was nothing left to deregister with either. The runner is now deleted through `DELETE .../actions/runners/{id}` using the optional per-runner PAT the repository already supports (`.github_check_token`), **before** the install directory is removed, since that is where the token lives. Without a PAT there is no way to do it — GitHub requires a PAT or a fresh removal token, and `config.sh remove` needs the latter — so the response says so explicitly and names where to delete it by hand, rather than reporting a clean removal.
- A GitHub visibility check that could not reach an answer is no longer recorded as "not registered". Every failure path — network error, an expired token (401), insufficient scope or a rate limit (403), an invisible target (404), a malformed response — returned the same `false` as a genuine absence, so an expired PAT produced a confident "not on GitHub" and sent people chasing a registration problem that did not exist. The stored status is now three-valued and carries the reason, which the UI shows as "GitHub check failed" with the cause in the tooltip. The data model was already three-valued (`registered_on_github` is a nullable bool, and the detail dialog rendered three states) — only the producer could not express "unknown".
- The runner list showed **every** checked runner as present on GitHub, including the ones GitHub said were absent. The cell tested `{{if .RegisteredOnGitHub}}`, and `html/template` judges a pointer only by whether it is nil, so a pointer to `false` is true; the column was structurally incapable of rendering "not on GitHub". It now goes through explicit `GitHubYes` / `GitHubNo` / `GitHubUnknown` helpers. Together with the previous entry this had been compounding: a failed check was stored as `false` and then rendered as a green check mark.
- The visibility check now pages through the full runner list. It requested `per_page=100` and read only the first page (decoding `total_count` without using it), so in an organization with more than 100 runners everything past the first page was reported as not registered — the same false negative from a different direction.
- Target names are escaped per path segment before going into the API URL. `ValidateTarget` only rejects empty values and stray slashes, so a target such as `myorg?x=1` used to be pasted into the path raw and the request landed on a different endpoint.
- A running runner was never recognized as running, so the 5-minute sweep restarted every one of them on every pass. Running state was read from a pid file — `Runner.Listener.pid`, falling back to `.path` — and actions/runner writes neither: none of `run.sh`, `run-helper.sh.template` or `runsvc.sh` writes a pid anywhere (the pid only lives in a shell variable), and `.path` holds a PATH string, which `runsvc.sh` itself shows with `export PATH=$(cat .path)`. Parsing it as a pid always failed, so every registered runner read as "installed, not running" forever and the periodic `已定时拉起 runner: <name>` line repeated for all of them indefinitely. Running state now comes from the process table: `internal/runnerproc` scans `/proc` for a process whose argv claims that install directory (`<dir>/bin/Runner.Listener`, or a shell running `<dir>/run.sh` or `<dir>/run-helper.sh`). The supervising shell counts, because `run-helper.sh` sleeps 5 seconds on exit code 2 before `run.sh` relaunches the listener — a listener-only test would call the runner dead during every restart and start a second one. Matching is on the full path, so runners sharing a host never claim each other's processes, and a shell-interpreted script is only claimed when argv[0] is actually a shell, so `grep <dir>/run.sh` is not mistaken for a live runner. Detection needs `/proc` and is therefore Linux-only; elsewhere it reports "not running", which is what the pid-file version effectively did everywhere.
- The two background loops no longer decide from a view that cannot see the runners. In container mode the Manager and the runners are in different PID namespaces, so `runner.List`'s `Running` is false no matter how the local process check is implemented. Both the startup auto-start and the 5-minute sweep now go through `runner.ListWithLiveStatus`, which asks each container's Agent — the same source the UI already used. A probe failure maps to `status=unknown` instead of `installed`, so "registered but not running, so start it" cannot fire on a runner the Manager could not reach; restarting containers while Docker is unreachable makes an outage worse, not better. The two loops, which had drifted into identical copies, now share one `startIdleRunners`.
- Stopping a runner in default (non-container) mode always failed with `未找到 runner pid 文件或 pid 无效`, because it read the same absent pid file. `Stop` now signals the processes found for that install directory, supervisors before the listener so `run.sh`'s retry loop cannot relaunch a listener that was just terminated, and reports an error when nothing is running rather than silently succeeding.
- A runner that was never registered no longer reports as "registered, not running" in container mode. `ContainerRunnerStatus` hardcoded `installed` whenever there was no container to ask — absent or stopped — although its own contract was to stay consistent with the disk. The UI therefore showed a configured-but-unregistered runner as registered, and with the periodic sweep now reading live status it would have gone further and created a container and called `/start` for it; for a runner whose install directory does not exist at all, Docker would have created the bind-mount source itself, owned by root, which is exactly the ownership the Manager (UID 1001) cannot write into. With no container to ask, the status now falls back to what the disk says.
- The Agent's "already running, do not start again" guard never fired, for the same reason: it called the same broken check. Each sweep therefore started another `run.sh` inside the container. The new listener loses the session-creation race against the one already holding the session (`A session for this runner already exists`, exit code 5, which `run-helper.sh` treats as "no retry needed"), so processes did not accumulate and jobs were not cut short — but every runner forked a short-lived listener every 5 minutes for nothing. The guard now works, and the check-then-start pair is serialized so concurrent `/start` calls (periodic sweep, post-registration start, a UI click) cannot each pass it.

### Changed

- `cmd/runner-manager` had no tests at all. The wiring inside `main()` is now split into `basicAuthMiddleware`, `httpErrorHandler`, `registerRoutes`, `listenAddr` and `loadI18n` — same behaviour, just reachable from a test — and the package is covered at 44%: the auth middleware (rejects wrong user and wrong password, `/health` stays open, the username defaults to `admin` and is trimmed, and — named so it is impossible to miss — that an unset `BASIC_AUTH_PASSWORD` means no authentication middleware is mounted at all), the listen address, the error handler's JSON shape, the full route table, the template renderer including its name-fallback, and `startIdleRunners` against real processes.
- The six i18n files are now checked against each other. A key present in `en.json` and missing elsewhere renders as a blank in the UI without any error, and adding a string while forgetting one language is the easy mistake; tests assert the key sets match, that no value is blank, and that every key the template references exists. They pass today — this is a guard, not a fix.

### Upgrading

Two things this release deliberately leaves to you:

- Runner install directories created by earlier versions keep their `0755`. `MkdirAll` does
  not alter a directory that already exists, and tightening one under a UID mismatch would
  break a working deployment, so the startup self-test names each one with a ready-to-run
  `chmod 700` instead of changing it for you.
- Deregistering a runner from GitHub when you remove it needs the optional per-runner PAT
  (`.github_check_token` in that runner's install directory). Without one, removal still
  cleans up locally and the response names the runner to delete by hand under
  Settings → Actions → Runners — GitHub offers no other way.

## [1.5.1] - 2026-09-20

### Fixed

- Every runner in a deployment used to register under the **same** name, so GitHub only ever showed one. `config.sh` was called without `--name`, and its documented default is the machine hostname — but it runs inside the *Manager* container, so every runner in that deployment inherited the same hostname. GitHub therefore listed a single runner named after a container id (`dd014243d356`) instead of the names shown in the UI. Registration now passes the configured runner name. Upgrading does not retroactively fix an existing install: delete that container-id-named runner under the repository's or organization's Settings → Actions → Runners, then add the runners again.
- A duplicate registration no longer blocks every runner queued behind it. `config.sh` was also called without `--unattended`; on a name collision the runner prints `A runner exists with the same name` and, interactively, enters a retry loop (`Failed to replace the runner. Try again or ctrl-c to quit`). With no TTY attached that loop just burned the two-minute timeout, and because registration runs on a single sequential worker everything queued behind it waited too — adding three runners produced one success and not a single log line for the other two. `--unattended` makes a collision fail immediately, and the error now names where to delete the existing runner.

## [1.5.0] - 2026-09-20

### Fixed

- Starting or stopping a runner no longer dies with the browser request. The lifecycle context was derived from the HTTP request, so a page reload (the UI reloads 5 seconds after a runner is added, and the message box close button reloads too) cancelled the in-flight request, and `exec.CommandContext` turned that into a SIGKILL for the `docker` child — leaving `docker create 失败。输出: (无输出): signal: killed` in the log while the daemon may already have created the container, so the next start hit a name conflict. Start/stop now keep their timeout but drop the request's cancellation, matching what runner removal and the background registration worker already did. ([#18])
- The image publish workflows no longer fail over a build-cache export. All four build workflows (`Publish image (Manager)` / `(Runner)`, `Release (Manager)` / `(Runner)`) fire at once on a tag push and every one of them exported to `type=gha` without a `scope`, so all four landed in BuildKit's default `buildkit` scope and overwrote each other — one job's index replacing layers another was still writing, which surfaces as `error writing layer blob: not_found`. It took down `Publish image (Manager)` on the v1.5.0 tag *after* the image itself had been built and pushed, marking a completed release red. Each workflow now exports to its own scope, and `cache-to` carries `ignore-error=true`: a cache export is an accelerator, and losing it must never fail a job whose image already shipped.

### Added

- Runner containers are rebuilt when the config they were created with no longer matches. Image, network, mount directory and the in-job Docker backend are fixed at `docker create` time, so until now changing `job_docker_backend`, `container_image`, `container_network` or `volume_host_path` left existing containers untouched — a runner created under `host-socket` kept full access to the host daemon after a switch to `dind`, and nothing said so. The Manager now compares each container's actual create parameters against the config: a **stopped** container that drifted is removed and recreated on Start, and the UI marks the runner "config changed" with the exact difference in the tooltip (e.g. `job_docker_backend: → dind`). A **running** container is never recreated on its own — a job may be in flight — so the row offers a "Recreate" button, backed by `POST /api/runners/:name/recreate`. Rebuilding an image under the same tag is detected too, since the comparison includes the image ID. `runners.resources` keeps being applied to existing containers via `docker update` and does not trigger a rebuild. The network a container was created on is recorded as a label too, because `docker create --network` sets exactly one network while a container can be attached to more afterwards: asking only whether it is attached to the configured network would accept a secondary attachment and leave the original connection — the one the config change meant to drop — in place. Containers from older versions carry no label and fall back to `HostConfig.NetworkMode`, which is bounded the same way, since a false positive there converges after one labelled rebuild. The backend a container was created with is recorded as a container label, so a custom runner image that sets `ENV DOCKER_HOST` itself is not mistaken for one of ours; containers created by older versions carry no label and fall back to inference from `DOCKER_HOST` and the socket mount. That inference deliberately does not compare against the *current* `dind_host`: a container was created under whatever the config said then, and switching to `none` usually comes with changing or dropping `dind_host` in the same edit, so recognizing only the current value would miss a container still holding `DOCKER_HOST=tcp://old-dind:2375` — exactly the route `none` is meant to cut. Any `tcp://…:2375` is therefore treated as possibly ours. The cost is bounded: a pre-label container whose image sets its own `ENV DOCKER_HOST` to that shape is rebuilt once needlessly, after which it carries the label and is never inferred about again. ([#19], [#20])
- A container created before per-runner agent tokens existed (no `AGENT_TOKEN` injected, so its Agent accepts unauthenticated calls from anything on the same network) now counts as drifted and is recreated on the next start. The comparison asks only whether the container has a non-blank token, never whether it matches the current one — a rotated token is a runtime failure the probe already surfaces (the Agent answers 401), not a reason to delete a container. Blank counts as missing, on purpose: the Agent trims `AGENT_TOKEN` and falls through to the mounted `.agent_token` when it is empty, so an image declaring `ENV AGENT_TOKEN=` plus a token file the container UID cannot read leaves authentication off with no 401 to reveal it — and a presence-only test would let exactly that container skip the rebuild this entry is about. The status path creates the runner's token if it does not exist yet, rather than only reading it: a pre-token container that is **already running** at upgrade time has no token file, both auto-start loops skip containers that are already running, so nothing would ever create one — the comparison would keep reading an empty token, skip itself, and the runner would sit there unauthenticated with nothing on screen to say so. Token creation is single-winner (`O_EXCL`), since that status path is deliberately not behind the per-runner lock — a list request must not block on a `docker create`. Two callers creating a token at once would otherwise each generate one and the later write would win on disk while the earlier one was injected into the container, leaving the Agent on token A, the Manager reading token B, and every call answered 401 with a non-blank env that the drift check would never flag. The token is written to a private temporary file and published under its real name with `os.Link`, which refuses to overwrite: that makes creation single-winner *and* means the name is either absent or complete, never half-written. Nothing is ever unlinked to recover a file that looks empty — "it has been empty for N milliseconds, so its writer must be dead" does not hold when a writer can simply be descheduled or its write can stall, and unlinking it there hands the live writer an orphaned inode while someone else publishes a different token, which is the very split this is meant to prevent. A file that holds the name but yields nothing readable is reported, with what to do about it, rather than taken over. ([#20])
- Lifecycle operations are serialized per runner. Manager startup auto-start, the 5-minute periodic start, post-registration start and UI clicks can all land on the same runner at once; that used to leave a `container name is already in use` line in the log, and with rebuilds in the mix two interleaved calls could remove a container the other had just created. ([#20])
- Conflict pre-check when adding a runner. The name field asks `/api/runner-precheck` while you type and reports, before you submit: a runner of that name already in the config, a name that normalizes to a container name already taken, an install directory owned by another runner, a directory that already holds a registered runner (`.runner`), and a leftover container of that name on the host. Blocking findings come with a one-click suggested name and, where useful, a ready-to-run fix command. Warnings do not block: a non-empty directory that will be reused, and — when no registration token is filled in — adopting an already registered runner directory, which is a legitimate way to take an existing runner into the config. With a token that same directory is blocking, because `config.sh` refuses to configure it twice. ([#18])
- `examples/deploy/`: two copy-and-go deployments — `standalone/` (one Manager container with the runner processes inside, via `docker run` or Compose) and `fleet/` (container mode, one container per runner, image cache shared through the host daemon, toolchain and action caches baked into a runner image layer, build caches isolated per runner). The README compares both, documents which caches are shared versus isolated and why, and collects the deployment pitfalls (directory ownership, `VOLUME_HOST_PATH`, host-socket disk growth). ([#18])

### Changed

- `POST /api/runners` answers a name conflict with **409** plus `conflicts` and `suggested_name` instead of silently appending a random suffix — typing `droiddesk` no longer creates `droiddesk-ab12cd` without saying so. Send `auto_rename: true` to keep the old behaviour; the suggestion is now the predictable `name-2`, `name-3`, … The same checks also cover container-name collisions, install-directory collisions and host leftovers, which previously surfaced as a 500 while saving the config or as `docker create` failing later. ([#18])
- `docker-compose.yml` now pins the in-container listen port to `SERVER_PORT` (default 8080) and maps `MANAGER_PORT` to it. Previously a numeric `MANAGER_PORT` also overrode `server.port` while the published target stayed 8080, so `MANAGER_PORT=9000` silently produced an unreachable port. ([#18])
- Default image tags across the docs, `docker-compose.yml`, `.env.example`, `config.yaml.example`, `Makefile`, examples and `DefaultRunnerContainerImage()` now point at `v1.5.0`.

### Upgrading

Nothing to do by hand. Containers created by earlier versions no longer match what
this version would create — they carry no creation labels and, from before per-runner
agent tokens, no injected `AGENT_TOKEN` — so the Manager marks each one "config changed"
and names the difference in the tooltip. A **stopped** runner is rebuilt the next time it
starts; a **running** one is left alone, since a job may be in flight, and the row's
"Recreate" button applies it when you choose (it interrupts a running job).

Previous releases told you to `docker rm -f github-runner-<name>` before hitting Start to
pick up a new base image. That is no longer needed: rebuilding an image under the same tag
changes its ID, and the comparison is on image ID.

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

[Unreleased]: https://github.com/soulteary/runner-fleet/compare/v1.6.0...HEAD
[1.6.0]: https://github.com/soulteary/runner-fleet/compare/v1.5.1...v1.6.0
[1.5.1]: https://github.com/soulteary/runner-fleet/compare/v1.5.0...v1.5.1
[1.5.0]: https://github.com/soulteary/runner-fleet/compare/v1.4.0...v1.5.0
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
[#18]: https://github.com/soulteary/runner-fleet/pull/18
[#19]: https://github.com/soulteary/runner-fleet/pull/19
[#20]: https://github.com/soulteary/runner-fleet/pull/20
[#21]: https://github.com/soulteary/runner-fleet/pull/21
