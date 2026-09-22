# Security Policy

## Reporting a vulnerability

Report privately through GitHub's
[security advisory form](https://github.com/soulteary/runner-fleet/security/advisories/new).
It reaches the maintainer without the report being public first.

Please do not open a public issue for a vulnerability. If the advisory form is unavailable to you,
open an issue that says only that you have a security report and asks for a contact — no details.

Useful in a report: the version or image tag, whether the deployment uses container mode, which
`job_docker_backend` it runs, and what an attacker needs to reach (the network position, and
whether Basic Auth is enabled).

This is a small project with no SLA. Expect acknowledgement rather than a fix on a schedule.

## What this tool holds

Worth knowing when judging a deployment or a report. Each is documented in the
[User Guide](docs/guide.md) at the section named.

| Secret | Where | Notes |
|---|---|---|
| Runner registration credentials | `runners/<name>/.credentials_rsaparams`, written by `config.sh` | The RSA private key the runner authenticates to GitHub with. actions/runner sets no Unix permissions on it, so the **directory** mode is what protects it. New directories are created `0700`; ones created by older versions stay `0755` and are named by the startup self-check with the `chmod` to run |
| Optional PAT | `config/tokens/<name>`, mode `0600` | Used for the GitHub visibility check and to deregister a runner on delete. Org needs `admin:org`, repo needs `repo`. Kept outside the runner directory, which container mode mounts into the runner container |
| Agent token | `runners/<name>/.agent_token`, mode `0600` | Generated per runner by the Manager and injected as `AGENT_TOKEN`. Without it, any container on the same Docker network could call the Agent's `/start` and `/stop` |
| Basic Auth password | `BASIC_AUTH_PASSWORD` | Optional and **off by default**. Never passed to runner processes or jobs |

## Exposure worth understanding before deploying

None of these are bugs; they are what the tool is. They are listed because a deployment that does
not account for them is the more likely problem.

- **No authentication by default.** Without `BASIC_AUTH_PASSWORD` the UI and the whole API are
  open to anyone who can reach the port. Bind to localhost or an internal network, or set a
  password. See [4. Security and validation](docs/guide.md#4-security-and-validation).
- **The default mode is one trust domain.** Without `container_mode`, every runner is a process
  inside the Manager's container, running as the same user (UID 1001) with the same filesystem.
  A job on any runner can read every other runner's `.credentials_rsaparams` and `.agent_token`,
  read the PATs under `config/tokens/` — whose `0600` mode protects nothing when the job is the
  owner of the file — edit `config/config.yaml`, and signal the Manager. The stock
  `docker-compose.yml` also mounts the host Docker socket into that container and the image grants
  passwordless `sudo`, so a job there is as trusted as root on the host — the same exposure as
  `host-socket`, without the per-runner containers. Use container mode when runners serve different
  repositories or owners, or when any runner holds a PAT.
- **`job_docker_backend: host-socket` gives jobs the host.** A job can bind-mount any host path
  through the shared Docker socket. That is the point of the backend, and it means a workflow you
  run is as trusted as root on that machine. `dind` keeps jobs off the host's filesystem, but it is
  one privileged daemon shared by every runner: a job can list, exec into and remove containers
  that other jobs started there.
- **`runner-net` is a trust boundary.** The stock DinD service listens on port 2375 without TLS or
  authentication, so any container attached to `runner-net` controls that privileged daemon. Attach
  nothing else to it; the startup self-check lists unexpected members in container mode.
- **The stock `docker-compose.yml` mounts the host Docker socket into the Manager in both modes.**
  Container mode needs it to create runner containers; the default mode only uses it to give jobs
  Docker. Access to it is equivalent to root on the host. In the default mode, if jobs do not need
  Docker, remove the mount and the `group_add` entry.
- **Anyone who can add a runner can run code.** Adding a runner and pointing it at a repository
  you control is a normal use of the UI, so the UI's access boundary is the real boundary.
- **`/metrics` requires auth when Basic Auth is on; `/health` and `/ready` never do.** Probes
  carry no credentials, and neither probe reveals which check failed. See
  [5. Operations](docs/guide.md#5-operations).

## Supported versions

The latest release. Fixes go into a new release rather than being backported.
