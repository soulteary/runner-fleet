## Changes

Runner deletion now stops when container removal cannot be confirmed. In v1.9.5, a caller with deletion access could receive HTTP 200 after Docker failed, while local configuration, installation files and the stored PAT were removed. A container could remain running without its management records. This is a lifecycle reliability defect, not an authentication bypass. <!-- version-check-ignore: affected baseline -->

Local directory or PAT cleanup errors also return HTTP 500 and retain the configuration record. Installation cleanup precedes PAT deletion. The same runner identity can be inspected and retried after an uncertain response. Tests cover a simulated committed Docker removal with a lost acknowledgment, real Linux permission failures, and cleanup committed before a failed configuration save followed by retry. They do not remove real GitHub runners.

Repeated credential-unavailable warnings now check the existing cache before constructing a candidate entry. Ten samples in the same Linux arm64 setup measured medians of 200.2 to 80.62 ns/op, 32 to 0 B/op, and 2 to 0 allocations. The one-warning-per-directory behavior is unchanged and tested with 64 concurrent callers and the race detector. This is a cached failure/status-polling microbenchmark, not an HTTP throughput result. External host load was uncontrolled. [Raw samples and conditions](https://github.com/soulteary/runner-fleet/blob/v1.9.6/docs/benchmarks/credential-warning.md).

The six-language guides now point to the recovery procedure and correct an old deletion paragraph that still described the historical runner-directory PAT location. PAT storage remains in the Manager's configuration volume; this documentation correction introduces no new migration.

## Compatibility and recovery

No API JSON fields, authentication format, configuration keys or persistent schemas change. The new localized `api.remove_failed` message describes deletion failures; affected failures change from false success to HTTP 500. Existing success behavior and GitHub deregistration's `github_deregistered` result remain unchanged.

Pause deletion traffic while upgrading Managers. Mixed old Managers retain the previous behavior. Preserve configuration, PAT and runner volumes. No volume migration is required. Previously deleted management records are not reconstructed automatically: restore them from trusted backups and inspect surviving containers before reusing names. Do not delete token records or create replacement identities to conceal uncertain completion.

Docker, GitHub, the filesystem and configuration storage are not one transaction. An HTTP 500 can follow a successful remote action or partial local cleanup. Verify the original resources and retry against the retained configuration record. GitHub deregistration remains best effort; inspect GitHub when `github_deregistered` is false. [Recovery details](https://github.com/soulteary/runner-fleet/blob/v1.9.6/docs/container-removal-recovery.md).

Rolling back a Manager restores the previous deletion behavior, so keep deletion traffic paused during rollback. The earlier Agent authentication upgrade/replacement order and identity-preservation requirements continue; they are not new changes in this release. Existing logs and exported copies are not rewritten. If credentials were exposed previously, restrict access, apply retention or cleanup to all copies and rotate exposed credentials; no new logging leak is claimed.

## Linux validation

Go 1.27.3, non-root Linux amd64 CI: 527 top-level tests / 799 named test and subtest results passed, zero failures and zero skips, with the race detector and cache disabled. Exact coverage was 2188/2293 production statements (95.4208%); no production code is excluded and the gate remains at 95%. Formatting, vet, lint, documentation consistency and both Linux image builder architectures passed. See the release workflows for results on the signed release source.

Both production image builder stages are checked for Linux amd64 and arm64 ELF payloads. Release verification checks attachment hashes, binary source/architecture metadata, OCI source/subject bindings and isolated runtime authentication. Darwin attachments retain the existing distribution format; Runner lifecycle support remains Linux amd64/arm64.

## Downloads

- Manager: `docker pull ghcr.io/soulteary/runner-fleet:v1.9.6`
- Runner: `docker pull ghcr.io/soulteary/runner-fleet:v1.9.6-runner`
- Verify standalone downloads with `checksums.txt`.
