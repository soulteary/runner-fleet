## Changes

- Agent `/status`, `/start` and `/stop` now return HTTP 503 without dispatching a handler when credentials are missing, empty or unreadable. Previously a caller able to reach that Agent port could use these states for anonymous access. Manager Basic Auth protects a different endpoint. A valid injected `AGENT_TOKEN` is unaffected; jobs running as the Agent user remain inside the same trust boundary as their runner files.
- The Manager stops start/recreate and status calls when its persistent token cannot be obtained, preserving occupied token records. Invalid bearer tokens still return 401 and `/health` remains public. Token paths, formats, configuration fields and authenticated request formats are unchanged.
- GitHub deregistration now distinguishes an actual HTTP 404 from a transport error whose URL contains `404`. A lost DELETE response remains unconfirmed; the next attempt queries remote state again. This corrects false success reporting and does not establish whether a disconnected request committed remotely.
- Directory precheck reads one entry instead of enumerating and sorting the whole directory. On the same Linux arm64 setup, ten-run medians for 10,000 files changed from 3,711,058 to 43,669 ns/op and from 1,466,977.5 to 952 B/op (20,029 to 13 allocations). These are warm precheck measurements, with external host load uncontrolled; they do not measure HTTP throughput. [Full samples and conditions](https://github.com/soulteary/runner-fleet/blob/v1.9.4/docs/benchmarks/precheck.md).
- Linux PR and release checks now require at least 95% total production statement coverage. Tests cover actual entrypoints, HTTP, process signals, permissions, timeouts and storage/retry failures. No production Go code is excluded. Docker CLI failure outcomes use explicit test fixtures; no real GitHub runner is registered or deleted by these tests.

## Upgrade and rollback

Drain active jobs, upgrade the Manager first, then replace old Runner containers with `ghcr.io/soulteary/runner-fleet:v1.9.4-runner`. Preserve configuration and persistent runner volumes, including `.agent_token` and registration files. Recover matching credentials and permissions from trusted sources; do not delete an occupied token record or silently generate a new identity to hide storage failure. No data or token-format migration is required.

Upgrading only the Manager leaves an old running Agent unchanged. Restrict Agent network access while versions are mixed and finish replacing legacy Agents before resuming normal scheduling. Rolling back an Agent restores permissive legacy behavior; keep jobs paused and access restricted during rollback. [Detailed recovery and replacement order](https://github.com/soulteary/runner-fleet/blob/v1.9.4/docs/agent-auth-upgrade.md).

Existing logs and exported copies are not rewritten. If credentials were exposed in an earlier deployment, restrict access, apply retention or cleanup to all copies, and rotate exposed credentials. These are continuing recovery requirements, not a newly confirmed logging leak in this release. The Go and dependency versions introduced in v1.9.3 are retained. <!-- version-check-ignore: previous release baseline -->

## Linux validation

Go 1.27.3, non-root Linux arm64: 522 top-level tests / 792 named test and subtest results passed with the race detector and cache disabled; zero failures and zero skips. Exact coverage was 2178/2289 production statements (95.1507%). Linux amd64 CI passed the same suite with zero failures/skips and 2179/2289 statements (95.1944%). Formatting, vet, golangci-lint 2.15.0 and Linux amd64/arm64 builds passed. See the release workflows and attached quality reports for the released source.

## Downloads

- Manager: `docker pull ghcr.io/soulteary/runner-fleet:v1.9.4`
- Runner: `docker pull ghcr.io/soulteary/runner-fleet:v1.9.4-runner`
- Verify binary downloads with `checksums.txt`. Runner lifecycle support is Linux only (`amd64`, `arm64`); Darwin build attachments retain the existing distribution format.
