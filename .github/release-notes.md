## Changes

This maintenance release rebuilds the Manager and Runner with Go 1.27.4 and updates kit dependencies. `go.mod` requires Go 1.27.4 and both container images are built from `golang:1.27.4-bookworm`. Go 1.27.4 contains security fixes to cgo, the go command, the compiler, the linker and `net/http` (GO-2026-6859). No vulnerability specific to Runner Fleet is claimed; the release picks up the upstream toolchain and standard library fixes.

Updated direct dependencies: cli-kit v1.9.4, docker-kit v1.0.3, health-kit/v4 v4.0.7, i18n-kit/v4 v4.0.6, logger-kit/v3 v3.0.6, metrics-kit/v3 v3.0.6, preflight-kit v1.0.3, procfind-kit v1.0.3, secure-kit/v2 v2.1.5 and version-kit/v4 v4.0.5. Indirect requirements move to golang.org/x/crypto v0.59.0, x/sys v0.50.0 and x/text v0.44.0. <!-- version-check-ignore: dependency versions -->

The six-language development guides now state Go 1.27.4, and the default image tag in code, examples and documentation is v1.9.7. Go quality reports were regenerated. There are no functional changes to the Manager, the Agent or the web UI.

## Compatibility and recovery

No API JSON fields, authentication format, configuration keys or persistent schemas change relative to v1.9.6. Preserve configuration, PAT and runner volumes; no volume migration is required. Replace both the Manager and Runner images, and rebuild custom Runner images that use the published Runner image as their base. Drain active jobs before replacing Runner containers. <!-- version-check-ignore: previous release -->

Rolling back to v1.9.6 requires no data changes and restores binaries built with Go 1.27.3. The deletion recovery behavior introduced in v1.9.6 and the earlier Agent authentication upgrade/replacement order continue unchanged; they are not new in this release. [Recovery details](https://github.com/soulteary/runner-fleet/blob/v1.9.7/docs/container-removal-recovery.md). <!-- version-check-ignore: previous release -->

## Linux validation

Release CI runs the full test suite with the race detector and the 95% production statement coverage gate on Linux amd64 with Go 1.27.4, and builds both images for Linux amd64 and arm64. This note does not record test counts; see the release workflows for results on the signed release source.

Runner lifecycle support remains Linux amd64/arm64. Darwin attachments retain the existing distribution format.

## Downloads

- Manager: `docker pull ghcr.io/soulteary/runner-fleet:v1.9.7`
- Runner: `docker pull ghcr.io/soulteary/runner-fleet:v1.9.7-runner`
- Verify standalone downloads with `checksums.txt`.
