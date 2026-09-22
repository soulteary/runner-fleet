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

- **Do not point runners at public repositories.** Anyone can open a pull request against a public
  repository, and that pull request's workflow runs on your machine unless the repository's
  **Approval for running fork pull request workflows from contributors** setting (Settings → Actions
  → General) holds it back — and its default only holds back first-time contributors, so one merged
  typo clears the bar. GitHub's own guidance is to use self-hosted runners with private repositories
  only: [Hardening for self-hosted runners](https://docs.github.com/en/actions/reference/security/secure-use#hardening-for-self-hosted-runners).
  Runners here are also **persistent** — registered without `--ephemeral`, not JIT — so `_work`, the
  tool caches under it, `$HOME` caches and any process a job leaves behind carry over to the next job;
  one malicious job can tamper with every job after it.
- **No authentication by default.** Without `BASIC_AUTH_PASSWORD` the UI and the whole API are
  open to anyone who can reach the port. Bind to localhost or an internal network, or set a
  password. See [4. Security and validation](docs/guide.md#4-security-and-validation).
- **`job_docker_backend: host-socket` gives jobs the host.** A job can bind-mount any host path
  through the shared Docker socket. That is the point of the backend, and it means a workflow you
  run is as trusted as root on that machine. `dind` isolates instead.
- **The Manager needs the host Docker socket in container mode.** Access to it is equivalent to
  root on the host.
- **Anyone who can add a runner can run code.** Adding a runner and pointing it at a repository
  you control is a normal use of the UI, so the UI's access boundary is the real boundary.
- **`/metrics` requires auth when Basic Auth is on; `/health` and `/ready` never do.** Probes
  carry no credentials, and neither probe reveals which check failed. See
  [5. Operations](docs/guide.md#5-operations).

## Supported versions

The latest release. Fixes go into a new release rather than being backported.
