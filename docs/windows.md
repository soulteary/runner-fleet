# Native Windows services

Target platforms: Windows 11 / Windows Server 2022 x64, with GitHub.com repository and organization runners. The Manager and runners use Windows services; WSL and Docker are not required.

## Install and start

Extract the Windows x64 release ZIP and run in **Administrator PowerShell**:

```powershell
.\runner-manager.exe service install
.\runner-manager.exe service start
Start-Process http://127.0.0.1:8080
```

Installation copies the executable to `%ProgramFiles%\RunnerFleet\runner-manager.exe` and registers the automatic `RunnerFleet` service as `NetworkService`. Default data locations:

```text
%ProgramData%\RunnerFleet\config.yaml
%ProgramData%\RunnerFleet\runners\<name>\
%ProgramData%\RunnerFleet\tokens\
%ProgramData%\RunnerFleet\logs\manager-YYYY-MM-DD.log
```

For custom configuration, copy `config.windows.yaml.example` to a dedicated directory and use `service install -config "D:\RunnerFleet\config.yaml"`. Installation fixes absolute paths; relative `base_path` resolves against the configuration directory. System/administrator ACLs protect the configuration, runner base and executable directories, so use dedicated directories. Logs remain under the default ProgramData directory.

## Add from the UI

On GitHub, open Settings → Actions → Runners → New self-hosted runner and choose Windows/x64. Paste `.\config.cmd --url https://github.com/owner/repo --token TOKEN` into the Manager, parse it, confirm the name/labels, and add. Pasted text is data and is never executed as a shell script.

The Manager downloads the official Windows ZIP, verifies its official SHA256, extracts it and registers an unattended service using `NetworkService`. It reads the official `.service` marker and verifies the service executable path before control operations.

Start persists `desired_state: running` and enables automatic startup/recovery. Stop persists `stopped`, disables startup/recovery and waits for the service to stop; this survives reboot. Manager restart or upgrade leaves runner services running. Unknown status does not trigger automatic creation or restart.

Use a fresh GitHub token after registration failure. If registration succeeded but service setup failed, Start repairs the service without registering again. Removal stops and uninstalls the local service and associated permission group before deleting files. Without a usable PAT, manually delete the remaining GitHub entry as indicated by the deletion result.

## Build environment and maintenance

Preinstall MSVC, Windows SDK, Git and other tools for `NetworkService`. Personal profile settings, credentials and mapped drives are not shared automatically. The Manager uses LocalSystem and runners use the lower-privilege NetworkService account. If upgrading from a LocalSystem runner, finish its job, then Stop and Start it to switch accounts. All runners share NetworkService; there is no per-runner account isolation. The UI binds to loopback by default; configure the bind address and existing authentication before remote access.

```powershell
.\runner-manager.exe service stop
.\runner-manager.exe service start
.\runner-manager.exe service uninstall
```

Uninstall removes only the Manager service, preserving files, configuration and runner services. To upgrade, stop the Manager, replace its installed executable, then start it again. Runner jobs continue running.

## Use your Windows proxy

During `service install`, the Manager captures proxy environment variables, or otherwise the installing user's enabled manual Windows system proxy (such as Clash/v2ray). It saves only proxy settings in `proxy.env` next to `config.yaml`, protected by the Manager directory ACL. The service loads this snapshot on startup; it does not continuously watch your desktop settings. Install from an elevated terminal under the same user who enabled the proxy. PAC scripts and SOCKS-only settings are not imported; use an HTTP/mixed port or edit `proxy.env` manually.

For an existing installation or a changed proxy port, run the following in Administrator PowerShell, then Stop and Start each affected runner in the UI after its job finishes. Use the same `-config` path as installation for a custom configuration:

```powershell
.\runner-manager.exe service proxy-import
.\runner-manager.exe service stop
.\runner-manager.exe service start
```

New runners receive the proxy automatically before registration. Existing runners receive it when started; running jobs are never restarted automatically. The proxy application must be running and its local port accessible to NetworkService, including after Windows restarts. An existing LocalSystem installation without `proxy.env` needs the import command above.

To override a runner manually, Stop it and edit `<base_path>\<runner>\.env`, adding entries outside the `Runner Fleet managed proxy` block, then Start it:

```text
http_proxy=http://127.0.0.1:7890
https_proxy=http://127.0.0.1:7890
no_proxy=localhost,127.0.0.1
```

Manual entries override inherited values and other `.env` settings are preserved. The same format is supported in the Manager's `proxy.env`; restart the Manager after editing it. Proxy credentials are visible to jobs using that proxy. Installing a proxy service, forwarding personal authentication, and configuring each build tool's own proxy are outside this import feature.

## Acceptance on Windows

Windows CI is configured to check builds, simulated lifecycle failures, two native SCM service instances, and Manager service installation/health. Real GitHub credentials and reboot acceptance require a target Windows machine:

- Register two runners with valid tokens, verify GitHub online status and run a real build on each.
- Stop one runner, verify the other continues, wait for reconciliation, then reboot and verify the stopped runner stays stopped.
- Restart the Manager during a long job and verify the job continues.
- Verify crash recovery and service repair without duplicate GitHub registration.
- Remove a runner and verify local service/group/directory cleanup and the reported GitHub deregistration result.

## Development checks

The backend is built into Windows binaries only; existing Linux coverage checks measure Linux production code. Test the installer and simulated controller on Linux/macOS with:

```bash
go test -race -tags windowsrunner_portable ./internal/windowsrunner
node --test scripts/test-command-parser.cjs
```

Run native SCM tests in an elevated Windows session:

```powershell
$env:RUNNER_FLEET_SCM_TESTS = "1"
go test -race ./internal/windowsrunner
```
