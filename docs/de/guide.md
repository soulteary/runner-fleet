# Benutzerhandbuch

**文档 / Docs:** [EN](../guide.md) · [中文](../zh/guide.md) · [Français](../fr/guide.md) · Deutsch · [한국어](../ko/guide.md) · [日本語](../ja/guide.md)

![](../../.github/assets/fleet.jpg)

Bereitstellung, Konfiguration, Hinzufügen von Runnern und Sicherheit werden hier behandelt. Für Build und API für Mitwirkende siehe [Entwicklung & Build](development.md).

---

## 1. Bereitstellung (Docker)

- Das Image basiert auf **Ubuntu** mit .NET Core 6.0-Abhängigkeiten; läuft unter **UID 1001** – gemountete Host-Verzeichnisse müssen für diesen Benutzer schreibbar sein (z. B. `chown 1001:1001 config.yaml runners`).
- Etwa 15 Sekunden nach dem Start werden registrierte, aber gestoppte Runner automatisch gestartet; periodische Prüfung alle 5 Minuten.

### Veröffentlichtes Image verwenden (empfohlen)

```bash
docker pull ghcr.io/soulteary/runner-fleet:main
```

### docker-compose Schnellstart

Im Repo-Root liegt `docker-compose.yml`. DinD nur aktivieren, wenn Sie den Containermodus nutzen und Jobs Docker mit `job_docker_backend: dind` benötigen.

```bash
cp config.yaml.example config.yaml
# config.yaml bearbeiten: runners.base_path auf /app/runners setzen

chown 1001:1001 config.yaml
mkdir -p runners && chown 1001:1001 runners

docker network create runner-net 2>/dev/null || true
docker compose up -d
# Bei job_docker_backend: dind: docker compose --profile dind up -d
```

UI: http://localhost:8080. Auth-Details in [4. Sicherheit und Validierung](#4-sicherheit-und-validierung).

### Container starten (volle Parameter)

`config.yaml` und `runners` müssen gemountet werden; der Port muss mit `server.port` in der Config übereinstimmen (Standard 8080).

```bash
docker run -d --name runner-manager \
  -p 8080:8080 \
  -v $(pwd)/config.yaml:/app/config.yaml \
  -v $(pwd)/runners:/app/runners \
  ghcr.io/soulteary/runner-fleet:main
```

Host-Verzeichnisse müssen für UID 1001 schreibbar sein. Basic Auth: `-e BASIC_AUTH_PASSWORD=password`, `-e BASIC_AUTH_USER=admin`. Für Docker in Jobs `-v /var/run/docker.sock:/var/run/docker.sock` hinzufügen oder DinD nutzen (siehe Repo `docker-compose.yml`, `--profile dind`). Das Image enthält die Docker-CLI; gängige Actions funktionieren mit DinD.

### Automatische Installation und Registrierung

In der UI „Quick Add Runner“ Name, Ziel, Token eingeben und absenden; zuerst läuft das Installationsskript, dann Registrierung und Start. Bei Fehlern:

```bash
docker exec runner-manager /app/scripts/install-runner.sh <name> [version]
```

Oder auf dem Host [actions-runner](https://github.com/actions/runner/releases) unter `runners/<name>/` entpacken, dann in der UI absenden oder `./config.sh` manuell ausführen.

### Containermodus (ein Runner pro Container)

Jeder Runner läuft in seinem eigenen Container; der Manager startet/stoppt über Host-Docker und holt den Status per HTTP vom Agent im Container.

In **config.yaml** aktivieren (siehe `config.yaml.example`):

```yaml
runners:
  base_path: /app/runners
  container_mode: true
  container_image: ghcr.io/soulteary/runner-fleet:main-runner
  container_network: runner-net
  agent_port: 8081
  job_docker_backend: dind   # dind | host-socket | none
  dind_host: runner-dind
  volume_host_path: /abs/path/on/host/to/runners
```

Runner-Image: gleicher Name wie Manager mit Tag `-runner`, oder lokal bauen: `docker build -f Dockerfile.runner -t ghcr.io/soulteary/runner-fleet:main-runner .`. Der Manager muss Host-Docker verwenden (Mount von `docker.sock`), nicht DinD über `DOCKER_HOST`; in Compose `group_add` für Host-Docker-GID oder `user: "0:0"` verwenden. Runner-Namen werden zu Containernamen normalisiert; Duplikate nach dem Mapping kollidieren.

### Fehlerbehebung

- **Runner startet nach compose down nicht**: Einmal `docker network create runner-net` ausführen. Bei anhaltendem Fehler in der UI „Start“ zum Neuerstellen nutzen oder `docker rm -f github-runner-<name>` dann „Start“.
- **Lauf als root**: Gemountete Verzeichnisse müssen für den Prozessbenutzer schreibbar sein; für root `RUNNER_ALLOW_RUNASROOT=1` setzen.
- **Altes Runner-Image**: `docker rm -f github-runner-<name>`, dann in der UI „Start“ zum Neuerstellen.
- **status=unknown**: Probe im Detail-Popup prüfen; „Start/Stop“ zur Selbstheilung versuchen.

### Images lokal bauen

```bash
docker build -t runner-manager .
docker build -f Dockerfile.runner -t ghcr.io/soulteary/runner-fleet:main-runner .
```

Make: `make docker-build`, `make docker-run`, `make docker-stop`.

---

## 2. Konfiguration

```bash
cp config.yaml.example config.yaml
```

| Feld | Beschreibung | Standard |
|------|--------------|----------|
| `server.port` | HTTP-Server-Port | `8080` |
| `server.addr` | Bind-Adresse; leer = alle Interfaces | leer |
| `runners.base_path` | Wurzelpfad der Runner-Installationsverzeichnisse; **in Container auf `/app/runners` setzen** | `./runners` |
| `runners.items` | Vordefinierte Runner-Liste | Kann auch über die Web-UI hinzugefügt werden |
| `runners.container_mode` | Containermodus aktivieren | `false` |
| `runners.container_image` | Runner-Image im Containermodus (Tag -runner) | `ghcr.io/soulteary/runner-fleet:main-runner` |
| `runners.container_network` | Netzwerk für Runner im Containermodus | `runner-net` |
| `runners.agent_port` | Agent-Port im Container | `8081` |
| `runners.job_docker_backend` | Docker in Jobs: `dind` / `host-socket` / `none` | `dind` |
| `runners.dind_host` | DinD-Hostname bei `job_docker_backend=dind` | `runner-dind` |
| `runners.volume_host_path` | Absoluter Host-Pfad zu runners im Containermodus (erforderlich) | leer |

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

Mehrere Runner pro Maschine: getrennte Unterverzeichnisse verwenden.

---

## 4. Sicherheit und Validierung

**Auth**: Standardmäßig keine Anmeldung; nur im internen Netz oder auf localhost verwenden. Umgebungsvariable `BASIC_AUTH_PASSWORD` setzen für Basic Auth; `BASIC_AUTH_USER` optional (Standard `admin`). Alle Routen außer `GET /health` erfordern Auth; keine Secrets committen – `.env` verwenden. Im Container: `-e BASIC_AUTH_PASSWORD=...` oder compose `env_file`.

**Pfade und Eindeutigkeit**: name/path dürfen nicht `..`, `/`, `\` enthalten; Verzeichnisse müssen unter `runners.base_path` liegen. Keine doppelten Namen; Name beim Bearbeiten schreibgeschützt. Im Containermodus werden Namen zu Containernamen normalisiert; Duplikate nach Mapping führen zu Fehler.

**Sensible Dateien**: config.yaml und .env stehen in `.gitignore`. Für `.github_check_token` jedes Runners `chmod 600` verwenden; `**/.github_check_token` zu `.gitignore` hinzufügen, wenn unter Versionskontrolle.

[← Zurück zur Projektstartseite](../../README.md)
