## Changes

Fix the Manager and Runner container builds for Linux arm64. In v1.9.4 the arm64 image manifests were correct, but their embedded Go executables targeted amd64 because Dockerfile defaults overrode BuildKit's automatic platform arguments. Without amd64 emulation these containers cannot start; with emulation they run the wrong architecture. Standalone v1.9.4 download binaries are unaffected. Both Dockerfiles now preserve BuildKit's build and target platforms. CI builds the actual production builder stages for linux/amd64 and linux/arm64 and verifies their ELF machine fields. <!-- version-check-ignore: affected release or continuing upgrade requirement -->

This is a packaging and reliability correction, not a newly confirmed authentication bypass. No protocol, authentication format, diagnostic field or stored-record schema changes. BuildKit is required for multi-platform image builds. The native builder cross-compiles each target without emulating the Go compiler.

## Upgrade and rollback

Replace v1.9.4 Manager and Runner images with v1.9.5 images, including cached custom Runner base images. Drain active jobs before replacing containers. Preserve configuration, persistent runner volumes, `.agent_token` and registration files. No data migration is required. Do not delete token records or rotate identity to conceal storage failures. Avoid rolling back arm64 containers to v1.9.4 images. <!-- version-check-ignore: affected release or continuing upgrade requirement -->

The Agent authentication recovery requirements introduced in v1.9.4 continue: upgrade the Manager first, restrict Agent access during mixed-version operation, then replace all legacy Agents before resuming normal scheduling. Rolling back to an Agent predating that fix restores permissive legacy behavior. [Recovery and replacement order](https://github.com/soulteary/runner-fleet/blob/v1.9.5/docs/agent-auth-upgrade.md). <!-- version-check-ignore: affected release or continuing upgrade requirement -->

Existing logs and exported copies are not rewritten. If credentials were exposed in an earlier deployment, restrict access, apply retention or cleanup to all copies, and rotate exposed credentials. These are continuing operational requirements, not a new logging finding. Published v1.9.4 tags and attachments remain unchanged for traceability. <!-- version-check-ignore: affected release or continuing upgrade requirement -->

## Linux validation

The new regression test fails before the fix with ELF machine 62 for arm64 (expected 183), then passes for both image builders on amd64 and arm64. Full Linux tests, race detection and the exact 95% total production statement coverage gate run in PR and release CI; production Go code is not excluded. No new application performance claim is made. The directory precheck improvement and its benchmark remain documented in [the existing benchmark](https://github.com/soulteary/runner-fleet/blob/v1.9.5/docs/benchmarks/precheck.md).

## Downloads

- Manager: `docker pull ghcr.io/soulteary/runner-fleet:v1.9.5`
- Runner: `docker pull ghcr.io/soulteary/runner-fleet:v1.9.5-runner`
- Verify standalone downloads with `checksums.txt`. Runner lifecycle support remains Linux amd64/arm64. Darwin attachments retain the existing distribution format.
