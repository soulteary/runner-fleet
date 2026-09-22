# Benutzerhandbuch

**文档 / Docs:** [EN](../guide.md) · [中文](../zh/guide.md) · [Français](../fr/guide.md) · Deutsch · [한국어](../ko/guide.md) · [日本語](../ja/guide.md)

![](../../.github/assets/fleet.jpg)

Bereitstellung, Konfiguration, Hinzufügen von Runnern und Sicherheit werden hier behandelt. Für Build und API für Mitwirkende siehe [Entwicklung & Build](development.md).

---

## 1. Bereitstellung (Docker)

- **Nur Linux**, `linux/amd64` und `linux/arm64`. Ob ein Runner läuft, wird über `/proc` aus der Prozesstabelle gelesen; auf jedem anderen Betriebssystem meldet sich jeder Runner als „läuft nicht", womit Start/Stopp und der Selbstheilungslauf nicht funktionieren können. Die veröffentlichten Images decken diese beiden Architekturen ab.
- **Oberfläche und Meldungen folgen Ihrer Sprache; die Logs sind auf Englisch festgelegt.** Die UI und das, was der Server zurückgibt — API-Meldungen und Toasts — werden aus `?lang=`, dem Sprach-Cookie oder `Accept-Language` ermittelt; ein Skript ohne `Accept-Language` erhält Englisch. Englische Logs sind eine Entscheidung, kein Versäumnis: Eine Logzeile hat keine Anfrage, der sie folgen könnte, ihr Leser ist der Betrieb, und grep, Loki-Abfragen und Alarmregeln brechen, sobald dasselbe Ereignis jedes Mal in einer anderen Sprache erscheint. Der Startup-Selbsttest geht ebenfalls ins Log, mit dem Präfix `[preflight …]`.
- Das Image basiert auf **Ubuntu** mit .NET Core 6.0-Abhängigkeiten; läuft unter **UID 1001** – gemountete Host-Verzeichnisse müssen für diesen Benutzer schreibbar sein (z. B. `chown 1001:1001 config runners`).
- Etwa 15 Sekunden nach dem Start werden registrierte, aber gestoppte Runner automatisch gestartet; periodische Prüfung alle 5 Minuten.

### Veröffentlichtes Image verwenden (empfohlen)

Produktion: konkrete Version verwenden (z. B. v1.8.0). Für Entwicklung kann der Tag `main` genutzt werden.

```bash
docker pull ghcr.io/soulteary/runner-fleet:v1.8.0
```

### docker-compose Schnellstart

Im Repo-Root liegt `docker-compose.yml`. DinD nur aktivieren, wenn Sie den Containermodus nutzen und Jobs Docker mit `job_docker_backend: dind` benötigen.

```bash
mkdir -p config runners && cp config.yaml.example config/config.yaml
# config/config.yaml bearbeiten: runners.base_path auf /app/runners setzen

sudo chown -R 1001:1001 config runners

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
  ghcr.io/soulteary/runner-fleet:v1.8.0
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
config/config.yaml muss nicht geändert werden. `cp .env.example .env` und z. B. setzen: `CONTAINER_MODE=true`, `VOLUME_HOST_PATH=<absoluter Host-Pfad zu runners>` (z. B. `realpath runners`), `JOB_DOCKER_BACKEND=host-socket`, `CONTAINER_NETWORK=runner-net`. Wenn Sie `config/config.yaml` nicht anlegen, wird die Datei beim ersten Start aus diesen Umgebungsvariablen erzeugt. Wenn `RUNNER_IMAGE` nicht gesetzt ist, wird das Runner-Image aus `MANAGER_IMAGE` abgeleitet (z. B. `v1.8.0` → `v1.8.0-runner`). Gemountete `config` und `runners` benötigen weiterhin `chown 1001:1001`. Siehe `.env.example` für alle Override-Variablen.

**Option 2: In config/config.yaml aktivieren** (siehe `config.yaml.example`):

```yaml
runners:
  base_path: /app/runners
  container_mode: true
  container_image: ghcr.io/soulteary/runner-fleet:v1.8.0-runner
  container_network: runner-net
  agent_port: 8081
  job_docker_backend: dind   # dind | host-socket | none
  dind_host: runner-dind
  volume_host_path: /abs/path/on/host/to/runners
```

Runner-Image: gleicher Name wie Manager mit Tag `-runner` (Produktion: Version z. B. v1.8.0-runner; Entwicklung: main-runner), oder lokal bauen: `docker build -f Dockerfile.runner -t ghcr.io/soulteary/runner-fleet:v1.8.0-runner .`. Der Manager muss Host-Docker verwenden (Mount von `docker.sock`), nicht DinD über `DOCKER_HOST`; in Compose `group_add` für Host-Docker-GID oder `user: "0:0"` verwenden. Bei `job_docker_backend: host-socket` übergibt der Manager `--group-add <Host-Docker-GID>` an den Runner-Container (automatisch aus `docker.sock` erkannt, überschreibbar via `runners.docker_gid` / `DOCKER_GID`); das Image enthält zudem eine `docker`-Gruppe (Build-Arg `DOCKER_GID`, Standard 999). Runner-Namen werden zu Containernamen normalisiert; Duplikate nach dem Mapping kollidieren.

**Runner-Image erweitern**: GitHub-gehostete Runner bringen Toolchains mit (Android SDK, Node, Python …), selbst gehostete nicht. Für `ubuntu-24.04` geschriebene Workflows setzen das oft implizit voraus und scheitern nach dem Umzug. Legen Sie Ihre Toolchain über das Runner-Image dieses Repos — fertige Beispiele und die vier wichtigen Regeln (chown auf UID 1001, Umgebungsvariablen ins Image, passwortloses sudo wird geerbt, Warmlauf nach `USER app`) stehen in [`examples/runner-images/`](../../examples/runner-images/). Mit `items[].container_image` nutzt es nur ein bestimmter Runner; im Workflow per Label auswählen.

**Konfigurationsänderungen und Neuaufbau von Containern**: Image, Netzwerk, Mount-Verzeichnis und das Docker-Backend im Job stehen mit `docker create` fest; ein vorhandener Container behält, womit er erstellt wurde — eine Konfigurationsänderung allein erreicht ihn nicht. Der Manager vergleicht die tatsächlichen Erstellungsparameter jedes Containers mit der aktuellen Konfiguration. Ein **gestoppter** Container, der nicht mehr passt, wird beim nächsten Start entfernt und neu erstellt — ob per Klick auf „Start“ oder durch den automatischen Start des Managers; in der Liste trägt der Runner dann „Konfig geändert“, und der Tooltip nennt den Unterschied (z. B. `job_docker_backend: → dind`). Ein **laufender** Container bleibt unangetastet — möglicherweise läuft gerade ein Job. Für sofortige Wirkung die Schaltfläche „Neu erstellen“ in der Zeile nutzen (`POST /api/runners/:name/recreate`, bricht einen laufenden Job ab) oder ihn im Leerlauf stoppen und wieder starten. Ein Neubau desselben Tags zählt ebenfalls: verglichen wird die Image-ID, nicht nur die Referenz.

**Fertige Deployment-Beispiele**: [`examples/deploy/`](../../examples/deploy/) enthält zwei sofort kopierbare Setups — `standalone/` (ein Manager-Container, die Runner-Prozesse laufen darin; per `docker run` oder Compose) und `fleet/` (Container-Modus: ein Container je Runner, Image-Cache über den gemeinsamen Host-Daemon, Toolchain- und Action-Caches in einer Schicht des Runner-Images, Build-Caches je Runner getrennt). Die README vergleicht beide, erklärt welche Caches geteilt und welche isoliert sind, und sammelt die typischen Stolperstellen (Verzeichnis-Eigentümer, `VOLUME_HOST_PATH`, wachsender Speicherbedarf unter host-socket).

### Fehlerbehebung

- **Wenn etwas nicht läuft, zuerst den Startup-Selbsttest ansehen**: `docker compose logs runner-manager | grep '\[preflight'`. Beim Start werden runners-Verzeichnis, Docker-Erreichbarkeit, Netzwerk, Runner-Image und Job-Docker-Backend geprüft; fehlgeschlagene Punkte nennen direkt den Fix.
- **Runner startet nach compose down nicht**: Einmal `docker network create runner-net` ausführen. Bei anhaltendem Fehler in der UI „Start“ zum Neuerstellen nutzen oder `docker rm -f github-runner-<name>` dann „Start“.
- **Lauf als root**: Gemountete Verzeichnisse müssen für den Prozessbenutzer schreibbar sein; für root `RUNNER_ALLOW_RUNASROOT=1` setzen.
- **`permission denied` auf docker.sock in Jobs**: Bei `job_docker_backend: host-socket` muss der Container-Benutzer (UID 1001) in der Gruppe des Sockets sein. Der Manager hängt beim Erstellen `--group-add` mit der erkannten Host-Docker-GID an; ein Container mit nicht mehr passender GID gilt als abweichend und wird beim nächsten Start neu erstellt (bei einem laufenden erscheint „Konfig geändert“ mit der Schaltfläche „Neu erstellen“). Schlägt die Erkennung fehl, `runners.docker_gid` (oder `DOCKER_GID` in `.env`) auf `getent group docker | cut -d: -f3` setzen.
- **`command not found` oder fehlendes SDK im Job**: Selbst gehostete Runner bringen nicht mit, was GitHub-gehostete mitbringen. Zuerst den Startup-Selbsttest ansehen (`docker compose logs runner-manager | grep '\[preflight'`): Er nennt, welche von `git`/`unzip`/`tar`/`curl` je Runner-Image fehlen. Sprach- und Plattform-SDKs per eigenem Image ergänzen, siehe [`examples/runner-images/`](../../examples/runner-images/).
- **Altes Runner-Image**: Neu ziehen oder neu bauen und den Runner starten — der Manager erkennt das geänderte Image (über Referenz und Image-ID, ein Neubau desselben Tags zählt also auch) und erstellt den Container neu. Ein laufender Container bleibt unangetastet; nutzen Sie „Neu erstellen“ in der Zeile, wenn der laufende Job unterbrochen werden darf.
- **Im Log erscheint alle 5 Minuten `已定时拉起 runner: <Name>`, und kein Runner wird je als laufend angezeigt**: in dieser Version behoben — ein Upgrade genügt, kein Runner muss neu registriert werden. Der Laufzustand kam bisher aus einer pid-Datei (`Runner.Listener.pid`, ersatzweise `.path`), und actions/runner schreibt beide nicht: keines seiner Startskripte legt eine pid-Datei an, und `.path` enthält einen PATH-String. Damit las sich jeder Runner als „registriert, aber nicht laufend“, und der 5-Minuten-Durchlauf startete jeden von ihnen in jeder Runde erneut. Der Zustand wird jetzt aus der Prozesstabelle gelesen, im Container-Modus vom Agent im jeweiligen Container — der Manager sieht die Prozesse anderer Container nicht. Gleiche Ursache: im Standardmodus (ohne Container) scheiterte „Stopp“ stets mit `未找到 runner pid 文件或 pid 无效`.
- **Ein in der Oberfläche gelöschter Runner steht weiter auf GitHub, und ein erneutes Anlegen unter demselben Namen scheitert**: Das Löschen meldet den Runner jetzt auch bei GitHub ab — allerdings nur, wenn in `config/tokens/<Runner-Name>` das optionale PAT liegt (Organisation braucht `admin:org`, Repository `repo`). Ohne dieses Token fehlt die Berechtigung dafür; die Antwort auf das Löschen sagt das und verweist auf Settings → Actions → Runners. Von älteren Versionen gelöschte Runner wurden nie abgemeldet und müssen von Hand entfernt werden.
- **Ein Runner zeigt „GitHub-Abfrage fehlgeschlagen“**: Die Abfrage lief, lieferte aber keine Antwort — der Grund steht im Tooltip (abgelaufenes Token, fehlende Berechtigung, Rate Limit, Ziel nicht sichtbar). Das ist etwas anderes als „GitHub: nicht vorhanden“, wo GitHub geantwortet hat und der Runner nicht in der Liste stand. Ältere Versionen meldeten beides als Letzteres.
- **status=unknown**: Probe im Detail-Popup prüfen; „Start/Stop“ zur Selbstheilung versuchen.

### Images lokal bauen

```bash
docker build -t runner-manager .
docker build -f Dockerfile.runner -t ghcr.io/soulteary/runner-fleet:v1.8.0-runner .
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
| `runners.container_image` | Runner-Image im Containermodus (Tag -runner) | `ghcr.io/soulteary/runner-fleet:v1.8.0-runner` |
| `runners.container_network` | Netzwerk für Runner im Containermodus | `runner-net` |
| `runners.agent_port` | Agent-Port im Container | `8081` |
| `runners.job_docker_backend` | Docker in Jobs: `dind` / `host-socket` / `none` | `dind` |
| `runners.dind_host` | DinD-Hostname bei `job_docker_backend=dind` | `runner-dind` |
| `runners.docker_gid` | Docker-Gruppen-GID des Hosts, die Runner-Containern bei `job_docker_backend=host-socket` hinzugefügt wird; leer oder `0` ermittelt sie aus `docker.sock` | leer (automatisch) |
| `runners.volume_host_path` | Absoluter Host-Pfad zu runners im Containermodus (erforderlich) | leer |
| `runners.items[].name` | Anzeigename; zugleich Name des Installationsverzeichnisses und im Containermodus des Containers. Eindeutig und nach dem Anlegen nicht mehr änderbar | erforderlich |
| `runners.items[].path` | Unterverzeichnis unter `base_path`; leer verwendet `name` | leer (= `name`) |
| `runners.items[].target_type` | `org` oder `repo` | erforderlich |
| `runners.items[].target` | Organisationsname oder `owner/repo` | erforderlich |
| `runners.items[].labels` | Eigene Labels; das `runs-on` eines Workflows wählt danach aus | leer |
| `runners.items[].container_image` | Image-Override pro Runner (Containermodus); leer = globaler Wert | leer |
| `runners.items[].job_docker_backend` | Docker-Backend-Override pro Runner (Containermodus); leer = globaler Wert | leer |
| `runners.resources` | Ressourcenlimits der Runner-Container (`cpus` / `memory` / `memory_swap` / `pids_limit`), an `docker create` durchgereicht und beim Start via `docker update` auch auf bestehende Container angewendet | leer (unbegrenzt) |

Jedes Feld oben, das eine Container-Bereitstellung ändern muss, kann auch aus der Umgebung kommen, sodass ein Vollcontainer-Setup nur `.env` berührt — siehe [Umgebungsvariablen](#umgebungsvariablen) unten.

### Umgebungsvariablen

Werden einmal beim Start gelesen; eine Änderung erfordert einen Neustart des Managers. Wo zwei
Namen dieselbe Einstellung betreffen, werden sie in der Reihenfolge der Tabelle gelesen — sind
beide gesetzt, gewinnt also die **zweite**.

| Variable | Überschreibt | Standard / Hinweis |
|---|---|---|
| `MANAGER_PORT`, `SERVER_PORT` | `server.port` | `8080`. Compose fixiert `SERVER_PORT` im Container und bildet `MANAGER_PORT` darauf ab, sodass der veröffentlichte Port vom Lauschport abweichen darf |
| `SERVER_ADDR` | `server.addr` | leer (alle Schnittstellen) |
| `RUNNERS_BASE_PATH` | `runners.base_path` | `./runners`; im Image `/app/runners` |
| `CONTAINER_MODE` | `runners.container_mode` | `false`. Nur `true` und `1` werden gelesen: diese Variable schaltet den Containermodus **ein, niemals aus**, damit ein versehentlicher Wert die Form einer Bereitstellung nicht stillschweigend ändert |
| `RUNNER_IMAGE`, `CONTAINER_IMAGE` | `runners.container_image` | Nicht gesetzt: abgeleitet aus `MANAGER_IMAGE` (`:v1.8.0` → `:v1.8.0-runner`), sonst aus `FLEET_IMAGE_TAG` |
| `CONTAINER_NETWORK` | `runners.container_network` | `runner-net` |
| `VOLUME_HOST_PATH`, `RUNNERS_VOLUME_HOST_PATH` | `runners.volume_host_path` | leer; im Containermodus erforderlich |
| `JOB_DOCKER_BACKEND` | `runners.job_docker_backend` | `dind` |
| `DOCKER_GID` | `runners.docker_gid` | leer = aus `docker.sock` ermitteln |

Diese haben keine Entsprechung in der Konfigurationsdatei:

| Variable | Wirkung | Standard |
|---|---|---|
| `BASIC_AUTH_PASSWORD` | Setzen aktiviert Basic Auth | leer (keine Auth) |
| `BASIC_AUTH_USER` | Benutzername für Basic Auth | `admin` |
| `TRUSTED_ORIGINS` | Kommagetrennte Ursprünge, die von der Cross-Site-Prüfung ausgenommen sind; siehe [4. Sicherheit und Validierung](#4-sicherheit-und-validierung) | leer |
| `LOG_LEVEL` | `trace` / `debug` / `info` / `warn` / `error` | `info` |
| `LOG_FORMAT` | `console` für Menschen, `json` für ELK oder Loki; ein unbekannter Wert fällt auf `console` zurück, statt den Start zu verhindern | `console` |
| `DOCKER_HOST` | Welchen Docker-Daemon der **Manager selbst** nutzt. Der Containermodus braucht den Host-Socket — auf DinD gerichtet scheitert das Anlegen von Runnern | `unix:///var/run/docker.sock` |
| `MANAGER_IMAGE` | Welches Manager-Image Compose zieht; davon wird das Runner-Image abgeleitet | der Release-Tag |
| `FLEET_IMAGE_TAG` | Tag des Standard-Runner-Images, wenn nichts anderes ihn bestimmt | `v1.8.0` |

`scripts/install-runner.sh` liest zusätzlich `RUNNER_VERSION`, `RUNNER_SHA256` und
`RUNNER_FORCE_REINSTALL` — siehe [Automatische Installation und Registrierung](#automatische-installation-und-registrierung).

Im Runner-Container liest der Agent `AGENT_TOKEN`, `AGENT_PORT` und `RUNNER_INSTALL_DIR`. Alle
drei setzt der Manager beim Anlegen des Containers; sie von Hand zu setzen gehört zu keiner
normalen Bereitstellung.


**Validierung**: Keine doppelten Namen; Containermodus prüft auf Container-Namenskonflikte. `job_docker_backend` erlaubt nur `dind`/`host-socket`/`none`; im Containermodus mit Container-`base_path` ist `volume_host_path` erforderlich. Fehlendes `job_docker_backend` bedeutet `dind`. Nach einer Backend-Änderung wird ein **gestoppter** Container beim nächsten Start neu aufgebaut, ein **laufender** wird als "Konfiguration geändert" markiert und die Schaltfläche "Neu erstellen" in der Zeile wendet die Änderung sofort an — siehe oben "Konfigurationsänderungen und Neuaufbau von Containern".

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

**Registrierungsergebnis**: Wird in `.registration_result.json` im Runner-Verzeichnis geschrieben. **GitHub-Sichtbarkeitsprüfung** (optional): Das PAT nach `config/tokens/<Runner-Name>` legen (Modus 0600; Org braucht `admin:org`, Repo braucht `repo`); wird ca. alle 5 Minuten geprüft, Ergebnis in `.github_status.json`. Ein PAT, das eine frühere Version im Runner-Verzeichnis hinterlassen hat, wird automatisch dorthin verschoben und aus dem Runner-Verzeichnis entfernt — dieses Verzeichnis wird in den Container des Runners gemountet, wo Jobs es lesen könnten. Dieselbe Prüfung erfasst auch, ob GitHub den Runner als **Job-ausführend** meldet; in der Liste erscheint dann ein „Beschäftigt“-Badge, im Konfigurationsdialog eine eigene Zeile. Sie teilt sich den ~5-Minuten-Takt und kann daher bis zu 5 Minuten nachhinken; ohne PAT bleibt der Wert „unbekannt“ statt als inaktiv gemeldet zu werden. „Registriert“ und „GitHub ✓“ in der Liste verlinken beide auf die Runners-Einstellungsseite des Ziels.

**Namenskonflikt-Prüfung**: Während der Eingabe fragt das Formular `/api/runner-precheck` und zeigt vor dem Absenden, was schiefgehen würde — ein Runner dieses Namens existiert bereits, der Name ergibt denselben Containernamen wie ein anderer, das Installationsverzeichnis ist belegt, ein übrig gebliebenes Verzeichnis enthält bereits einen registrierten Runner (`.runner`), oder auf dem Host existiert noch ein Container dieses Namens. Blockierendes wird rot angezeigt, mit einem Vorschlagsnamen zum Übernehmen; Warnungen (ein nicht leeres Verzeichnis wird weiterverwendet) lassen sich übergehen. Trotzdem Absenden lehnt der Server mit **409** und denselben Konflikten ab — das frühere stille Anhängen eines Zufallssuffixes entfällt (bei Bedarf `auto_rename: true`).

**Einen Runner löschen**: Er wird gestoppt, bei vorhandenem PAT in seinem Verzeichnis von GitHub abgemeldet, und **sein Installationsverzeichnis wird gelöscht** — nur wenn dieses Verzeichnis unter `runners.base_path` liegt, damit ein falsch konfigurierter Pfad kein Systemverzeichnis mitnimmt. `_work` und die darin liegenden Caches gehen ebenfalls verloren; danach ist nichts wiederherstellbar.

Mehrere Runner pro Maschine: getrennte Unterverzeichnisse verwenden.

---

## 4. Sicherheit und Validierung

**Auth**: Standardmäßig keine Anmeldung; nur im internen Netz oder auf localhost verwenden. Umgebungsvariable `BASIC_AUTH_PASSWORD` setzen für Basic Auth; `BASIC_AUTH_USER` optional (Standard `admin`). Alle Routen außer `GET /health` und `GET /ready` erfordern Auth; keine Secrets committen – `.env` verwenden. Im Container: `-e BASIC_AUTH_PASSWORD=...` oder compose `env_file`.

**Site-übergreifende Anfragen**: Schreibende Endpunkte weisen alles ab, was der Browser als site-übergreifend meldet; eine Seite anderer Herkunft kann diese API also nicht mit Ihren zwischengespeicherten Basic-Auth-Daten steuern. Nichts zu konfigurieren. Schreibt ein Reverse Proxy `Host` so um, dass die eigenen Anfragen abgewiesen werden, tragen Sie die im Browser sichtbaren Herkünfte kommagetrennt in `TRUSTED_ORIGINS` ein. Nicht-Browser-Aufrufer (curl, CI-Skripte) sind nicht betroffen — sie führen keine zwischengespeicherten Zugangsdaten mit und kommen als Angreifer hier nicht in Frage. Die genaue Regel steht in der [Entwicklungsdoku](development.md).

**Pfade und Eindeutigkeit**: name/path dürfen nicht `..`, `/`, `\` enthalten; Verzeichnisse müssen unter `runners.base_path` liegen. Keine doppelten Namen; Name beim Bearbeiten schreibgeschützt. Im Containermodus werden Namen zu Containernamen normalisiert; Duplikate nach Mapping führen zu Fehler.

**Agent-Authentifizierung** (Containermodus): Der Manager schreibt pro Runner ein zufälliges Token nach `<Runner-Verzeichnis>/.agent_token` (Modus 0600), injiziert es beim Erstellen als `AGENT_TOKEN` in den Container und sendet es bei jedem Agent-Aufruf als `Authorization: Bearer`. Der Agent liest die Umgebungsvariable, daher hängt dies nicht von übereinstimmenden UIDs ab; die Datei ist die persistente Kopie des Managers. Der Agent lehnt `/status`, `/start` und `/stop` ohne Token ab; `/health` bleibt für den HEALTHCHECK offen. Vorher erstellte Container haben kein Token und laufen weiter ohne Prüfung; sie gelten jetzt als abweichend und werden beim nächsten Start automatisch neu erstellt (bei einem laufenden hilft „Neu erstellen“).

**Sensible Dateien**: config/config.yaml, .env und `config/tokens/` stehen in `.gitignore`. Das optionale PAT jedes Runners liegt in `config/tokens/<Runner-Name>`, das der Manager mit 0700 und die Datei mit 0600 anlegt — nicht im Runner-Verzeichnis ablegen, das der Container-Modus in den Container des Runners mountet, wo Jobs es lesen könnten.

**Berechtigungen der Runner-Verzeichnisse**: Das Installationsverzeichnis jedes Runners wird mit 0700 angelegt. `config.sh` schreibt dort `.credentials_rsaparams` hinein — den privaten RSA-Schlüssel, mit dem sich der Runner bei GitHub ausweist — und actions/runner setzt für diese Dateien keine Unix-Rechte. Der Modus des Verzeichnisses ist damit das Einzige, was andere lokale Benutzer auf dem Host davon abhält, den Schlüssel zu lesen und den Runner zu übernehmen. **Von älteren Versionen angelegte Verzeichnisse sind weiterhin 0755.** Der Startup-Selbsttest (`docker compose logs runner-manager | grep '\[preflight'`) benennt sie und gibt das passende `chmod 700` aus. Er ändert nichts von selbst: Bei abweichenden UIDs (Manager als root, Container als app(1001)) würde das Verschärfen eine laufende Installation zerstören — bitte erst prüfen.

---

## 5. Betrieb

### Probes

| Pfad | Zweck |
|---|---|
| `GET /health` | Liveness. 200, solange der Prozess läuft, ohne jede Abhängigkeitsprüfung — bleibt 200, auch wenn die Konfiguration kaputt oder der Mount unbrauchbar ist. Für einen K8s-`livenessProbe`: ein Neustart ist die richtige Antwort auf einen hängenden Prozess und die falsche auf eine fehlerhafte Konfiguration |
| `GET /ready` | Readiness. 503, wenn die Konfiguration nicht geladen werden kann oder `runners.base_path` fehlt bzw. nicht beschreibbar ist — es schreibt tatsächlich eine Testdatei und erkennt damit den Verzeichnis-Eigentümerfehler, den `/health` nicht sieht. Für einen K8s-`readinessProbe` und der richtige Endpunkt nach einer Änderung an der Bereitstellung |

Beide bleiben ohne Authentifizierung, auch wenn Basic Auth aktiv ist, denn ein Probe trägt keine
Zugangsdaten. Keiner von beiden nennt, *welche* Prüfung fehlschlug; das steht im Log.

### Metriken

`GET /metrics` liefert Prometheus-Text — Aufrufvolumen und Latenz je Route. Das Label `path` ist
Echos Routenvorlage (`/api/runners/:name`), nicht die Anfrage-URL, sodass aus einer Flotte von
Runnern keine Flotte von Labelwerten wird.

Anders als die Probes **erfordert** `/metrics` Authentifizierung, sobald Basic Auth aktiv ist: es
legt das Aufrufvolumen jedes Endpunkts offen — Betriebsdaten, die keinen Grund haben,
öffentlicher zu sein als `/api`. Den Scrape-Job entsprechend konfigurieren:

```yaml
scrape_configs:
  - job_name: runner-fleet
    static_configs:
      - targets: ['runner-manager:8080']
    basic_auth:
      username: admin
      password: <BASIC_AUTH_PASSWORD>
```

### Upgrade

```bash
docker compose pull && docker compose up -d
```

Kein Runner muss neu registriert werden: seine Identität liegt in seinem Installationsverzeichnis, das ein Upgrade nicht anfasst.

Im Containermodus existieren die Runner-Container weiterhin mit dem **alten** Runner-Image, denn
Image, Netzwerk, Mount-Verzeichnis und der Rest werden im Moment des `docker create` festgelegt.
Der Manager bemerkt das und repariert es selbst: ein **gestoppter** Runner wird beim nächsten Start
neu gebaut, ein **laufender** als „Konfiguration geändert" markiert und neu gebaut, sobald Sie auf
Neu erstellen klicken oder ihn im Leerlauf stoppen und starten. Zwei Details entscheiden, ob das
neue Image überhaupt da ist, um daraus zu bauen:

- Ein **Versions-Tag** holt sich selbst: nach einem Versionssprung liegt das neue `-runner`-Tag
  nicht lokal vor, also lädt `docker create` es.
- Ein **veränderliches Tag** (`:main`, oder dasselbe Versions-Tag neu gebaut) löst lokal bereits auf,
  also verwendet `docker create` das veraltete Image weiter. Ziehen Sie es vorher selbst —
  `docker pull <Runner-Image>`. Drift wird über die Image-**ID** und nicht nur über die Referenz
  verglichen, nach dem Pull läuft der Neubau also wie gewohnt.

`runners.resources` ist die einzige Einstellung, die einen bestehenden Container ohne Neubau
erreicht: der Manager wendet sie beim Start mit `docker update` an, ein Upgrade auf eine Version
mit Limits erzwingt also kein Neuanlegen aller Container.

Lesen Sie das [Changelog](../../CHANGELOG.md) der Zielversion — Breaking Changes und alles, was einen manuellen Schritt braucht, steht dort.

### Was zu sichern ist

Das README sagt, die Konfiguration sei Ihr Backup. Das gilt für die *Konfiguration*, nicht für die
*Identität*: die Zugangsdaten eines Runners liegen in seinem Installationsverzeichnis, und ohne sie
muss ein wiederhergestelltes Deployment von Hand neu registriert werden.

Sichern Sie das Verzeichnis `config/` (`config.yaml` sowie `tokens/`) und jedes Verzeichnis `runners/<Name>/`, ohne `_work/`.

| In `runners/<Name>/` | Geschrieben von | Wenn es verloren geht |
|---|---|---|
| `.runner`, `.credentials_rsaparams` und die übrigen von `config.sh` geschriebenen Dateien | actions/runner | Der Runner ist weg. Neu registrieren — und den veralteten Eintrag vorher auf GitHub löschen, denn eine neue Registrierung unter demselben Namen scheitert, solange der alte gelistet ist |
| `.agent_token` | Manager (Modus `0600`) | Wird neu erzeugt; der Container gilt als abgewichen und wird beim nächsten Start neu gebaut |
| `config/tokens/<Name>` (außerhalb des Runner-Verzeichnisses) | Sie, optional | Die Sichtbarkeitsprüfung endet, und beim Löschen kann der Runner nicht mehr von GitHub abgemeldet werden |
| `.registration_result.json`, `.github_status.json` | Manager | Kosmetisch — beide entstehen bei der nächsten Registrierung oder Prüfung neu |
| `_work/` | Die Jobs | Nichts Erhaltenswertes. Checkouts und Build-Ausgaben, der größte Posten auf der Platte, und er wächst |

Beim Wiederherstellen zählt der Eigentümer: alles muss am Ende UID 1001 gehören, dasselbe
`sudo chown -R 1001:1001 config runners` wie bei der Erstinstallation. `GET /ready` schreibt
tatsächlich eine Testdatei in das runners-Verzeichnis und ist damit der schnellste Weg zu
bestätigen, dass eine Wiederherstellung wirklich brauchbar ist.

### Hinter einem Reverse Proxy

Der Manager spricht einfaches HTTP und bringt kein eigenes TLS mit; der Proxy terminiert TLS. Das
wiegt hier schwerer als sonst: Basic Auth sendet das Passwort bei jeder Anfrage, und ohne TLS geht
es bei jeder einzelnen im Klartext über das Netz.

Die Cross-Site-Prüfung braucht keine Konfiguration. Sie liest `Sec-Fetch-Site`, das der Browser
lokal berechnet — ein Proxy, der `Host` umschreibt, kann sie nicht brechen. `TRUSTED_ORIGINS` ist
der Notausgang für den Fall, dass es doch passiert: tragen Sie die Ursprünge so ein, wie die
Adresszeile des Browsers sie zeigt, kommagetrennt, und beschränken Sie die Liste auf Ursprünge, die
Sie kontrollieren. Siehe [4. Sicherheit und Validierung](#4-sicherheit-und-validierung).

Richten Sie den Health-Check des Proxys auf `/health` und, falls vorhanden, sein Readiness-Gate auf
`/ready`; beide bleiben ohne Authentifizierung. Exponieren Sie `/metrics` nicht öffentlich: mit
Basic Auth verlangt es Zugangsdaten, ohne Basic Auth ist es so offen wie alles andere.

### Version und Logs

`GET /version` gibt die Version zurück und sonst nichts. Build-Details fehlen bewusst:
`go_version` ließe jeden eine veröffentlichte CVE der Go-Laufzeit der exakten Laufzeit zuordnen,
die Sie betreiben. `runner-manager -version` gibt Commit, Build-Datum, Go-Version und Plattform
aus — das läuft auf dem Host und ist nicht exponiert.

`LOG_LEVEL` und `LOG_FORMAT` steuern das Log; setzen Sie `LOG_FORMAT=json`, wenn es nach ELK oder
Loki geht. Der Startup-Selbsttest schreibt eine Zeile je Punkt, jeweils mit dem Präfix
`[preflight …]` — dieses Präfix ist der Grep-Anker, der in der gesamten
[Fehlerbehebung](#fehlerbehebung) verwendet wird.

[← Zurück zur Projektstartseite](../../README.md)
