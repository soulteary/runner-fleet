# Benutzerhandbuch

**文档 / Docs:** [EN](../guide.md) · [中文](../zh/guide.md) · [Français](../fr/guide.md) · Deutsch · [한국어](../ko/guide.md) · [日本語](../ja/guide.md)

![](../../.github/assets/fleet.jpg)

Bereitstellung, Konfiguration, Hinzufügen von Runnern und Sicherheit werden hier behandelt. Für Build und API für Mitwirkende siehe [Entwicklung & Build](development.md).

---

## 1. Bereitstellung (Docker)

- Das Image basiert auf **Ubuntu** mit .NET Core 6.0-Abhängigkeiten; läuft unter **UID 1001** – gemountete Host-Verzeichnisse müssen für diesen Benutzer schreibbar sein (z. B. `chown 1001:1001 config runners`).
- Etwa 15 Sekunden nach dem Start werden registrierte, aber gestoppte Runner automatisch gestartet; periodische Prüfung alle 5 Minuten.

### Veröffentlichtes Image verwenden (empfohlen)

Produktion: konkrete Version verwenden (z. B. v1.4.0). Für Entwicklung kann der Tag `main` genutzt werden.

```bash
docker pull ghcr.io/soulteary/runner-fleet:v1.4.0
```

### docker-compose Schnellstart

Im Repo-Root liegt `docker-compose.yml`. DinD nur aktivieren, wenn Sie den Containermodus nutzen und Jobs Docker mit `job_docker_backend: dind` benötigen.

```bash
mkdir -p config && cp config.yaml.example config/config.yaml
# config/config.yaml bearbeiten: runners.base_path auf /app/runners setzen

chown 1001:1001 config runners
mkdir -p runners && chown 1001:1001 runners

docker network create runner-net 2>/dev/null || true
docker compose up -d
# Bei job_docker_backend: dind: docker compose --profile dind up -d
```

UI: http://localhost:8080. Auth-Details in [4. Sicherheit und Validierung](#4-sicherheit-und-validierung).

### Container starten (volle Parameter)

`config`-Verzeichnis (nicht die Datei `config/config.yaml` einzeln, sonst erzeugt Docker bei fehlender Datei eine leere Datei) und `runners` müssen gemountet werden; der Port muss mit `server.port` in der Config übereinstimmen (Standard 8080).

```bash
docker run -d --name runner-manager \
  -p 8080:8080 \
  -v $(pwd)/config:/app/config \
  -v $(pwd)/runners:/app/runners \
  ghcr.io/soulteary/runner-fleet:v1.4.0
```

Host-Verzeichnisse müssen für UID 1001 schreibbar sein. Basic Auth: `-e BASIC_AUTH_PASSWORD=password`, `-e BASIC_AUTH_USER=admin`. Für Docker in Jobs `-v /var/run/docker.sock:/var/run/docker.sock` hinzufügen, plus `--group-add $(getent group docker | cut -d: -f3)`, falls die Host-Docker-GID nicht 999 ist (das Image enthält eine `docker`-Gruppe mit GID 999, änderbar über Build-Arg `DOCKER_GID`) oder DinD nutzen (siehe Repo `docker-compose.yml`, `--profile dind`). Beide Images enthalten die Docker-CLI sowie eine an GitHub-gehostete Runner angeglichene Kommandozeilen-Basis: `scripts/apt-packages.txt` übernimmt das apt-Paketset aus `actions/runner-images` (`toolset-2404.json`), also sind `git`, `unzip`, `jq`, `rsync`, `sudo`, `xvfb` usw. vorhanden. Sprach- und Plattform-SDKs sind bewusst nicht enthalten — dafür die `setup-*`-Actions nutzen oder das Image erweitern. Wie bei gehosteten Runnern erhält der Job-Benutzer in beiden Images passwortloses `sudo`, sodass `sudo apt-get install -y …` funktioniert; mit `--build-arg ALLOW_SUDO=false` bauen, um es zu entfernen.

### Automatische Installation und Registrierung

In der UI „Quick Add Runner“ Name, Ziel, Token eingeben und absenden; zuerst läuft das Installationsskript, dann Registrierung und Start. Bei Fehlern:

```bash
docker exec runner-manager /app/scripts/install-runner.sh <name> [version]
```

Das Skript wählt die Architektur per `uname -m`, ermittelt ohne Versionsangabe die neueste Version über die GitHub-API und prüft **immer** die SHA-256. Ist der offizielle Hash nicht abrufbar (offline/Mirror), explizit übergeben: `RUNNER_SHA256=<sha256> ... install-runner.sh <name> <version>`. Ein Verzeichnis mit vorhandenem Runner wird übersprungen, außer `RUNNER_FORCE_REINSTALL=1` ist gesetzt.

Oder auf dem Host [actions-runner](https://github.com/actions/runner/releases) unter `runners/<name>/` entpacken, dann in der UI absenden oder `./config.sh` manuell ausführen.

### Containermodus (ein Runner pro Container)

Jeder Runner läuft in seinem eigenen Container; der Manager startet/stoppt über Host-Docker und holt den Status per HTTP vom Agent im Container.

**Option 1: Nur Env (empfohlen für Full-Container)**
config/config.yaml muss nicht geändert werden. `cp .env.example .env` und z. B. setzen: `CONTAINER_MODE=true`, `VOLUME_HOST_PATH=<absoluter Host-Pfad zu runners>` (z. B. `realpath runners`), `JOB_DOCKER_BACKEND=host-socket`, `CONTAINER_NETWORK=runner-net`. Wenn Sie `config/config.yaml` nicht anlegen, wird die Datei beim ersten Start aus diesen Umgebungsvariablen erzeugt. Wenn `RUNNER_IMAGE` nicht gesetzt ist, wird das Runner-Image aus `MANAGER_IMAGE` abgeleitet (z. B. `v1.4.0` → `v1.4.0-runner`). Gemountete `config` und `runners` benötigen weiterhin `chown 1001:1001`. Siehe `.env.example` für alle Override-Variablen.

**Option 2: In config/config.yaml aktivieren** (siehe `config.yaml.example`):

```yaml
runners:
  base_path: /app/runners
  container_mode: true
  container_image: ghcr.io/soulteary/runner-fleet:v1.4.0-runner
  container_network: runner-net
  agent_port: 8081
  job_docker_backend: dind   # dind | host-socket | none
  dind_host: runner-dind
  volume_host_path: /abs/path/on/host/to/runners
```

Runner-Image: gleicher Name wie Manager mit Tag `-runner` (Produktion: Version z. B. v1.4.0-runner; Entwicklung: main-runner), oder lokal bauen: `docker build -f Dockerfile.runner -t ghcr.io/soulteary/runner-fleet:v1.4.0-runner .`. Der Manager muss Host-Docker verwenden (Mount von `docker.sock`), nicht DinD über `DOCKER_HOST`; in Compose `group_add` für Host-Docker-GID oder `user: "0:0"` verwenden. Bei `job_docker_backend: host-socket` übergibt der Manager `--group-add <Host-Docker-GID>` an den Runner-Container (automatisch aus `docker.sock` erkannt, überschreibbar via `runners.docker_gid` / `DOCKER_GID`); das Image enthält zudem eine `docker`-Gruppe (Build-Arg `DOCKER_GID`, Standard 999). Runner-Namen werden zu Containernamen normalisiert; Duplikate nach dem Mapping kollidieren.

**Runner-Image erweitern**: GitHub-gehostete Runner bringen Toolchains mit (Android SDK, Node, Python …), selbst gehostete nicht. Für `ubuntu-24.04` geschriebene Workflows setzen das oft implizit voraus und scheitern nach dem Umzug. Legen Sie Ihre Toolchain über das Runner-Image dieses Repos — fertige Beispiele und die vier wichtigen Regeln (chown auf UID 1001, Umgebungsvariablen ins Image, passwortloses sudo wird geerbt, Warmlauf nach `USER app`) stehen in [`examples/runner-images/`](../../examples/runner-images/). Mit `items[].container_image` nutzt es nur ein bestimmter Runner; im Workflow per Label auswählen.

**Fertige Deployment-Beispiele**: [`examples/deploy/`](../../examples/deploy/) enthält zwei sofort kopierbare Setups — `standalone/` (ein Manager-Container, die Runner-Prozesse laufen darin; per `docker run` oder Compose) und `fleet/` (Container-Modus: ein Container je Runner, Image-Cache über den gemeinsamen Host-Daemon, Toolchain- und Action-Caches in einer Schicht des Runner-Images, Build-Caches je Runner getrennt). Die README vergleicht beide, erklärt welche Caches geteilt und welche isoliert sind, und sammelt die typischen Stolperstellen (Verzeichnis-Eigentümer, `VOLUME_HOST_PATH`, wachsender Speicherbedarf unter host-socket).

### Fehlerbehebung

- **Wenn etwas nicht läuft, zuerst den Startup-Selbsttest ansehen**: `docker compose logs runner-manager | grep 自检`. Beim Start werden runners-Verzeichnis, Docker-Erreichbarkeit, Netzwerk, Runner-Image und Job-Docker-Backend geprüft; fehlgeschlagene Punkte nennen direkt den Fix.
- **Runner startet nach compose down nicht**: Einmal `docker network create runner-net` ausführen. Bei anhaltendem Fehler in der UI „Start“ zum Neuerstellen nutzen oder `docker rm -f github-runner-<name>` dann „Start“.
- **Lauf als root**: Gemountete Verzeichnisse müssen für den Prozessbenutzer schreibbar sein; für root `RUNNER_ALLOW_RUNASROOT=1` setzen.
- **`permission denied` auf docker.sock in Jobs**: Bei `job_docker_backend: host-socket` muss der Container-Benutzer (UID 1001) in der Gruppe des Sockets sein. Der Manager hängt beim Erstellen `--group-add` mit der erkannten Host-Docker-GID an; nach dem Upgrade den Runner-Container neu erstellen (`docker rm -f github-runner-<name>`, dann „Start“). Schlägt die Erkennung fehl, `runners.docker_gid` (oder `DOCKER_GID` in `.env`) auf `getent group docker | cut -d: -f3` setzen.
- **`command not found` oder fehlendes SDK im Job**: Selbst gehostete Runner bringen nicht mit, was GitHub-gehostete mitbringen. Zuerst den Startup-Selbsttest ansehen (`docker compose logs runner-manager | grep 自检`): Er nennt, welche von `git`/`unzip`/`tar`/`curl` je Runner-Image fehlen. Sprach- und Plattform-SDKs per eigenem Image ergänzen, siehe [`examples/runner-images/`](../../examples/runner-images/).
- **Altes Runner-Image**: `docker rm -f github-runner-<name>`, dann in der UI „Start“ zum Neuerstellen.
- **status=unknown**: Probe im Detail-Popup prüfen; „Start/Stop“ zur Selbstheilung versuchen.

### Images lokal bauen

```bash
docker build -t runner-manager .
docker build -f Dockerfile.runner -t ghcr.io/soulteary/runner-fleet:v1.4.0-runner .
```

Make: `make docker-build`, `make docker-run`, `make docker-stop`.

---

## 2. Konfiguration

```bash
mkdir -p config && cp config.yaml.example config/config.yaml
```

| Feld | Beschreibung | Standard |
|------|--------------|----------|
| `server.port` | HTTP-Server-Port | `8080` |
| `server.addr` | Bind-Adresse; leer = alle Interfaces | leer |
| `runners.base_path` | Wurzelpfad der Runner-Installationsverzeichnisse; **in Container auf `/app/runners` setzen** | `./runners` |
| `runners.items` | Vordefinierte Runner-Liste | Kann auch über die Web-UI hinzugefügt werden |
| `runners.container_mode` | Containermodus aktivieren | `false` |
| `runners.container_image` | Runner-Image im Containermodus (Tag -runner) | `ghcr.io/soulteary/runner-fleet:v1.4.0-runner` |
| `runners.container_network` | Netzwerk für Runner im Containermodus | `runner-net` |
| `runners.agent_port` | Agent-Port im Container | `8081` |
| `runners.job_docker_backend` | Docker in Jobs: `dind` / `host-socket` / `none` | `dind` |
| `runners.dind_host` | DinD-Hostname bei `job_docker_backend=dind` | `runner-dind` |
| `runners.volume_host_path` | Absoluter Host-Pfad zu runners im Containermodus (erforderlich) | leer |
| `runners.items[].container_image` | Image-Override pro Runner (Containermodus); leer = globaler Wert | leer |
| `runners.items[].job_docker_backend` | Docker-Backend-Override pro Runner (Containermodus); leer = globaler Wert | leer |
| `runners.resources` | Ressourcenlimits der Runner-Container (`cpus` / `memory` / `memory_swap` / `pids_limit`), an `docker create` durchgereicht und beim Start via `docker update` auch auf bestehende Container angewendet | leer (unbegrenzt) |

Einige Felder können per Umgebungsvariable überschrieben werden (`MANAGER_PORT`, `CONTAINER_MODE`, `VOLUME_HOST_PATH`, `JOB_DOCKER_BACKEND` usw.), sodass Full-Container nur über `.env` läuft; siehe `.env.example`.

**Validierung**: Keine doppelten Namen; Containermodus prüft auf Container-Namenskonflikte. `job_docker_backend` erlaubt nur `dind`/`host-socket`/`none`; im Containermodus mit Container-`base_path` ist `volume_host_path` erforderlich. Fehlendes `job_docker_backend` bedeutet `dind`; nach Backend-Änderung Runner in der UI neu starten.

Beispiel:

```yaml
server:
  port: 8080
  addr: 0.0.0.0
runners:
  base_path: /app/runners
  items: []
```

---

## 3. Runner hinzufügen

**Token besorgen**: Repo/Org → Settings → Actions → Runners → New self-hosted runner, Token kopieren (ca. 1 Stunde gültig). Jeder Runner braucht einen neuen Token.

**Im Service hinzufügen**: In der UI „Quick Add Runner“ Name (eindeutig), Zieltyp (org/repo), Ziel, Token (optional; wenn gesetzt, kann Absenden automatisch registrieren und starten) eingeben. Sie können `./config.sh --url ... --token ...` von GitHub in „Parse from GitHub command“ einfügen und „Parse & fill“ klicken. Auto-Registrierung nur für GitHub.com; GitHub Enterprise erfordert manuelles `config.sh` im Runner-Verzeichnis.

**Wenn Runner nicht installiert**: Von [GitHub Actions Runner](https://github.com/actions/runner/releases) herunterladen, unter `runners/<name>/` entpacken, dann Token in der UI eingeben oder `./config.sh` dort ausführen. Bei Container-Deploy löst das Absenden eines Tokens in der UI zuerst Installation, dann Registrierung aus; Containermodus erfordert zuerst Runner-Image und `volume_host_path` (siehe Containermodus oben).

**Registrierungsergebnis**: Wird in `.registration_result.json` im Runner-Verzeichnis geschrieben. **GitHub-Sichtbarkeitsprüfung** (optional): `.github_check_token` (PAT; Org braucht `admin:org`, Repo braucht `repo`) ins Runner-Verzeichnis legen; wird ca. alle 5 Minuten geprüft, Ergebnis in `.github_status.json`.

**Namenskonflikt-Prüfung**: Während der Eingabe fragt das Formular `/api/runner-precheck` und zeigt vor dem Absenden, was schiefgehen würde — ein Runner dieses Namens existiert bereits, der Name ergibt denselben Containernamen wie ein anderer, das Installationsverzeichnis ist belegt, ein übrig gebliebenes Verzeichnis enthält bereits einen registrierten Runner (`.runner`), oder auf dem Host existiert noch ein Container dieses Namens. Blockierendes wird rot angezeigt, mit einem Vorschlagsnamen zum Übernehmen; Warnungen (ein nicht leeres Verzeichnis wird weiterverwendet) lassen sich übergehen. Trotzdem Absenden lehnt der Server mit **409** und denselben Konflikten ab — das frühere stille Anhängen eines Zufallssuffixes entfällt (bei Bedarf `auto_rename: true`).

Mehrere Runner pro Maschine: getrennte Unterverzeichnisse verwenden.

---

## 4. Sicherheit und Validierung

**Auth**: Standardmäßig keine Anmeldung; nur im internen Netz oder auf localhost verwenden. Umgebungsvariable `BASIC_AUTH_PASSWORD` setzen für Basic Auth; `BASIC_AUTH_USER` optional (Standard `admin`). Alle Routen außer `GET /health` erfordern Auth; keine Secrets committen – `.env` verwenden. Im Container: `-e BASIC_AUTH_PASSWORD=...` oder compose `env_file`.

**Pfade und Eindeutigkeit**: name/path dürfen nicht `..`, `/`, `\` enthalten; Verzeichnisse müssen unter `runners.base_path` liegen. Keine doppelten Namen; Name beim Bearbeiten schreibgeschützt. Im Containermodus werden Namen zu Containernamen normalisiert; Duplikate nach Mapping führen zu Fehler.

**Agent-Authentifizierung** (Containermodus): Der Manager schreibt pro Runner ein zufälliges Token nach `<Runner-Verzeichnis>/.agent_token` (Modus 0600), injiziert es beim Erstellen als `AGENT_TOKEN` in den Container und sendet es bei jedem Agent-Aufruf als `Authorization: Bearer`. Der Agent liest die Umgebungsvariable, daher hängt dies nicht von übereinstimmenden UIDs ab; die Datei ist die persistente Kopie des Managers. Der Agent lehnt `/status`, `/start` und `/stop` ohne Token ab; `/health` bleibt für den HEALTHCHECK offen. Vorher erstellte Container haben kein Token und laufen weiter ohne Prüfung — zum Aktivieren neu erstellen.

**Sensible Dateien**: config/config.yaml und .env stehen in `.gitignore`. Für `.github_check_token` jedes Runners `chmod 600` verwenden; `**/.github_check_token` zu `.gitignore` hinzufügen, wenn unter Versionskontrolle.

[← Zurück zur Projektstartseite](../../README.md)
