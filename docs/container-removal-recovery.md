# Recovering an unconfirmed container removal

A Manager API caller with delete access can request `DELETE /api/runners/:name`.
In v1.9.5, an unsuccessful `docker rm -f` was ignored: local files, PAT and <!-- version-check-ignore: affected baseline -->
configuration could be deleted even though the container remained alive. This
is a lifecycle reliability defect, not an authentication bypass.

The Manager now returns HTTP 500 before GitHub deregistration or local cleanup
if container removal cannot be confirmed. The configuration, installation files
and stored PAT remain available. A transport error can occur after Docker has
already removed the container; an error does not prove either outcome. Restore
Docker connectivity, inspect the original container identity, and retry the same
DELETE. An already absent container remains a successful, idempotent removal.
Do not change the runner name, delete its token or create a replacement identity
to work around this state.

No protocol fields or persistent record formats change. The failure response
changes from a false HTTP 200 to HTTP 500. Upgrade Managers before resuming delete
traffic; old Managers retain the previous behavior during a rolling upgrade.
No persistent volume migration is required. Preserve runner volumes and Manager
configuration/token volumes. Rolling back the Manager restores the old deletion
behavior, so keep deletion traffic paused if rolling back.

Local directory cleanup and PAT removal failures also return HTTP 500 and retain
the configuration record. Directory cleanup precedes PAT deletion. Filesystem
cleanup can partially succeed; inspect surviving files before retrying. A later
configuration save can fail after external operations and cleanup have committed.
That failure also returns HTTP 500: it does not roll back Docker or GitHub.
Verify those systems and retry against the existing configuration record; do not
register a replacement blindly.

This change does not make Docker, GitHub and filesystem operations one
transaction. Once container removal is confirmed, GitHub deregistration remains
best effort and is reported through `github_deregistered`; verify GitHub state
when it is false. Previously lost records are not reconstructed automatically.
Recover those records from trusted backups and inspect any surviving containers
before reusing their names. Running jobs may already have been interrupted.

The regression uses an explicit Docker CLI fault fixture. It covers both a daemon
failure before removal and a simulated committed removal whose acknowledgment is
lost, followed by retry against the same identity. It does not remove real GitHub
runners or claim to simulate filesystem power loss.
