# Agent authentication upgrade

The Agent now fails closed. If neither a nonempty `AGENT_TOKEN` nor a readable,
nonempty `<RUNNER_INSTALL_DIR>/.agent_token` is available, `/status`, `/start` and
`/stop` return HTTP 503 without invoking the handler. Invalid bearer credentials
still return 401; `/health` remains public. Environment credentials retain priority
and token files are still reread on each request. No protocol or configuration
field was added, and the token format and persistent path are unchanged.

Previously these missing-credential states enabled anonymous access. A caller
able to reach the Agent port could inspect status and start or stop its runner.
Manager Basic Auth does not protect that separate port. This does not establish
isolation from jobs running as the Agent user: such jobs can already read their
runner's files. A valid injected token is unaffected by this failure path.

The Manager now stops start/recreate before Docker operations if token creation
or retrieval fails. Status becomes unknown instead of issuing an anonymous
request when an existing container's persistent token cannot be obtained. Existing
records are preserved, including empty files; retries never replace an occupied
token path to hide a failure. Docker stop/remove remains available for recovery.

## Upgrade and recovery

1. Stop assigning new jobs to affected runners and wait for active jobs to finish.
   Restrict the Agent port and runner network to trusted callers during replacement.
2. Back up the configuration and runner volumes with restricted access. Preserve
   the existing `.agent_token`, registration files and Manager token directory.
   Mount runner directories persistently and ensure the Manager can read existing
   tokens and create files in those directories. The Agent can use its injected
   environment token even when its UID cannot read the Manager-owned 0600 file.
3. Upgrade the Manager first. Check per-runner status and repair storage errors
   before replacing Agents. If a token is empty or unreadable, recover the matching
   token from a trusted backup or the existing container's protected configuration
   through a secure administrative workflow; do not print it into logs or tickets.
   Do not delete the file or silently generate a different identity. If no matching
   token can be recovered, keep the runner stopped for explicit credential recovery.
4. Replace each drained runner container with the upgraded Agent through the
   Manager's recreate operation. Changing only the Manager image leaves running
   Agents unchanged. Verify authorized status/start/stop, anonymous rejection and
   `/health`, then resume scheduling for that runner.

An old Agent can still allow anonymous requests while the fleet is mixed; upgrading
only the Manager does not close that endpoint. An old Manager can attempt anonymous
calls when storage fails; a new Agent rejects them. Do not leave mixed versions as
a permanent deployment. Roll back only with jobs paused and network access restricted:
rolling back the Agent restores the permissive behavior. There is no data migration,
and unchanged token records remain readable by older versions.

This change does not rewrite historical logs and introduces no credential logging.
If an earlier deployment exposed credentials, restrict log and exported-copy access,
apply the retention or cleanup policy, and rotate exposed credentials. Those are
recovery requirements for an existing exposure, not evidence of a new log leak here.
