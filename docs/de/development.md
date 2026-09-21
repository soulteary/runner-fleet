# Entwicklung & Build

**文档 / Docs:** [EN](../development.md) · [中文](../zh/development.md) · [Français](../fr/development.md) · Deutsch · [한국어](../ko/development.md) · [日本語](../ja/development.md)

![](../../.github/assets/fleet.jpg)

Für Produktion Container-Bereitstellung verwenden; siehe [Benutzerhandbuch](guide.md). Dieses Dokument ist für Mitwirkende: lokaler Build und Debug.

## Anforderungen

- Go 1.27 (abgestimmt auf [go.mod](../../go.mod)).

## Architektur

Drei Prozesse. Wissenswert ist, welcher davon was besitzt.

```mermaid
flowchart LR
  GH["GitHub Actions"]
  subgraph host["Host"]
    M["<b>Manager</b><br/>runner-manager :8080"]
    SOCK[("docker.sock")]
    DIR[("runners/&lt;name&gt;/")]
    subgraph RC["Runner container — container mode only"]
      AG["<b>Agent</b><br/>runner-agent :8081"]
      RUN["run.sh → Runner.Listener"]
    end
  end
  M -->|"docker create / start / stop / rm"| SOCK
  SOCK -.->|"creates"| RC
  M -->|"HTTP + Bearer AGENT_TOKEN<br/>/status /start /stop"| AG
  AG -->|"spawns; reads /proc"| RUN
  M -->|"config, tokens, registration result"| DIR
  DIR -.->|"bind-mounted as /runner"| RC
  RUN -->|"long-polls for jobs"| GH
  M -.->|"optional PAT: listed? busy?"| GH
```

Die Beschriftungen im Diagramm bleiben in allen Übersetzungen englisch: es sind Prozessnamen, Pfade
und Endpunkte, und einen Bezeichner zu übersetzen macht ihn schwerer greppbar, nicht lesbarer.

**Der Manager orchestriert; er beherbergt keine Runner.** Im Containermodus ist jeder Runner ein
eigener Container, den der Manager über den Docker-Socket des Hosts anlegt — deshalb braucht der
Manager diesen Socket und darf nicht auf DinD zeigen. Im Standardmodus gibt es weder Agent noch
Runner-Container: die Runner-Prozesse laufen im Container des Managers selbst, und der Manager
liest `/proc` direkt.

**Der Status überquert eine Prozessgrenze, also läuft er über HTTP.** Manager und Runner-Container
liegen in verschiedenen PID-Namespaces; der Manager sieht die Prozesse des Runners nicht und fragt
deshalb den Agent, der sein eigenes `/proc` liest. Dieser Aufruf trägt ein Bearer-Token pro Runner,
denn jeder Container im selben Docker-Netz erreicht `/start` und `/stop` des Agents. Schlägt der
Aufruf fehl, lautet die Antwort `unknown`, nie `installed`: „registriert, läuft aber nicht, also
starten" darf nicht auf einem Runner greifen, den der Manager gar nicht erreichen konnte. Siehe
[Wie der Laufzustand ermittelt wird](#wie-der-laufzustand-ermittelt-wird).

**Neun Dinge werden im Moment des `docker create` festgelegt** und ändern sich danach nicht mehr:
Containername, Image, Netzwerk, Mount-Verzeichnis, In-Job-Docker-Backend, DinD-Host, Docker-GID,
Agent-Token und die Ressourcenlimits. `docker start` startet nur neu, was bereits gebaut wurde —
die Konfiguration allein zu ändern erreicht einen bestehenden Container also nie. Genau dafür gibt
es die Drift-Erkennung: Der Manager vergleicht die tatsächlichen Erzeugungsparameter jedes
Containers mit der aktuellen Konfiguration, baut einen **gestoppten** beim nächsten Start neu und
markiert einen **laufenden**, statt einen Job zu unterbrechen. Das Image wird per ID und nicht nur
per Referenz verglichen, ein Neubau desselben Tags zählt also ebenfalls.

## Build

```bash
# runner-manager-Binary bauen
go build -o runner-manager ./cmd/runner-manager

# Mit Version (für /version und Debug)
go build -ldflags "-X main.Version=1.7.1" -o runner-manager ./cmd/runner-manager

# Nur Runner Agent bauen (Containermodus)
go build -o runner-agent ./cmd/runner-agent

# Oder Make: make build / make build-agent / make build-all
```

Templates sind im Manager-Binary eingebettet (`cmd/runner-manager/templates/`); einzelnes Binary, kein separates `templates/` nötig.

## Lokal ausführen und debuggen

```bash
mkdir -p config && cp config.yaml.example config/config.yaml
go run ./cmd/runner-manager
# Oder make run (build dann run); eigene Config: ./runner-manager -config /path/to/config.yaml
```

Lauscht auf `:8080`, http://localhost:8080. Basic Auth zum Debuggen: `BASIC_AUTH_PASSWORD=secret go run ./cmd/runner-manager`; siehe [Benutzerhandbuch – Sicherheit](guide.md#4-sicherheit-und-validierung).

## CLI-Flags

- `-config <path>`: Pfad zur Konfigurationsdatei.
- `-version`: Version ausgeben und beenden (beim Build mit `-ldflags "-X main.Version=..."` injizieren).

## HTTP-API

Mit Basic Auth müssen alle Anfragen außer `/health` und `/ready` den Header `Authorization: Basic <base64(user:password)>` enthalten.

| Pfad | Methode | Beschreibung |
|------|---------|--------------|
| `/health` | GET | Liveness. Gibt `{"status":"ok","service":"runner-fleet"}` zurück; ohne Abhängigkeitsprüfungen immer 200, solange der Prozess läuft; immer unauthentifiziert. |
| `/ready` | GET | Readiness. Gleiche Body-Form; 503, wenn die Konfiguration nicht geladen werden kann oder das Runner-Basisverzeichnis fehlt bzw. nicht beschreibbar ist. Für K8s `readinessProbe`; ebenfalls unauthentifiziert und nennt nie die fehlgeschlagene Prüfung. |
| `/version` | GET | Gibt `{"version":"..."}` zurück. |
| `/metrics` | GET | Prometheus-Metriken (Anzahl und Latenz der Anfragen). Das Label `path` verwendet die Echo-Routenvorlage (`/api/runners/:name`), nicht die Anfrage-URL. **Erfordert Auth**, wenn Basic Auth aktiviert ist — `basic_auth` im Scrape-Job konfigurieren. |
| `/api/runners` | GET | Runner-Liste. Im Containermodus bei Probe-Fehler `status=unknown` mit strukturiertem `probe` (`error/type/suggestion/check_command/fix_command`). |
| `/api/runners/:name` | GET | Einzelner Runner. Gleiches `probe` bei Probe-Fehler im Containermodus. |
| `/api/runners/:name/start` | POST | Runner starten. Bei Probe-Fehler startet trotzdem, gibt strukturiertes `probe` in der Antwort zurück. |
| `/api/runners/:name/stop` | POST | Runner stoppen. Bei Probe-Fehler stoppt trotzdem, gibt strukturiertes `probe` in der Antwort zurück. |
| `/api/runners` | POST | Runner hinzufügen (optional installieren und registrieren). Bei einem Namenskonflikt kommt **409** mit `conflicts` und `suggested_name`, statt still umzubenennen; für das alte Verhalten `auto_rename: true` senden. |
| `/api/runners/:name` | DELETE | Entfernt einen Runner: stoppt ihn, meldet ihn bei vorhandenem PAT von GitHub ab, löscht sein Installationsverzeichnis, nimmt ihn aus der Konfiguration. Die Antwort enthält `github_deregistered` und eine `message`, die sagt, was auf der GitHub-Seite passiert ist. |
| `/api/runners/:name/recreate` | POST | Entfernt den Runner-Container und erstellt ihn mit der aktuellen Konfiguration neu (nur Container-Modus). Ein laufender Job wird abgebrochen; ein gestoppter Container wird beim Start ohnehin automatisch neu erstellt, wenn seine Erstellungsparameter abweichen. |
| `/api/runner-precheck` | GET | Namensprüfung vor dem Hinzufügen: `?name=&path=`. Liefert `available`, einen `suggested_name` und die gefundenen `conflicts` (`name_taken`, `container_name`, `install_dir`, `dir_registered`, `dir_adopt`, `dir_exists`, `container_exists`) mit je `level` (`error`/`warn`), `message`, `detail` und optionalem `fix_command`. Nur lesend; die Web-UI ruft sie während der Eingabe auf. |
| `/api/runner-rows` | GET | Rendert nur den `<tbody>` der Liste, aus demselben Template-Fragment wie der erste Seitenaufbau. Die Web-UI fragt ihn ab, um die Liste an Ort und Stelle zu aktualisieren. |
| `/static/*` | GET, HEAD | Eingebettetes Stylesheet und Skript (`//go:embed`). Die URLs tragen einen Inhalts-Fingerabdruck (`?v=<hash>`): Anfragen mit Fingerabdruck werden lange gecacht, solche ohne revalidieren. `HEAD` ist neben `GET` registriert, damit eine Probe oder ein Proxy keinen 405 bekommt. |

### Site-übergreifende Anfragen (CSRF)

Schreibende Endpunkte (`POST`, `PUT`, `DELETE`) weisen Anfragen ab, die der Browser als
site-übergreifend meldet. Ohne das konnte eine Seite beliebiger anderer Herkunft ein Formular an
`POST /api/runners` schicken: Das ist im CORS-Sinne eine „einfache Anfrage“, wird also ohne
Preflight abgesendet, und der Browser hängt die für diese Herkunft zwischengespeicherten
Basic-Auth-Zugangsdaten automatisch an. Das Öffnen einer bösartigen Seite genügte damit, um
einen Runner anzulegen oder zu stoppen. `PUT` und `DELETE` lösen immer einen Preflight aus und
waren nie der offene Teil — `POST` war es, und
`/api/runners/:name/{start,stop,recreate}` sind sämtlich POST.

Die Prüfung liest zuerst `Sec-Fetch-Site`: Der Browser berechnet den Wert lokal, ein Reverse
Proxy, der `Host` umschreibt, kann ihn also nicht kaputt machen (Chrome 76+, Firefox 90+,
Safari 16.4+). Nur `same-origin` kommt durch; `same-site` nicht, denn eine Subdomain oder ein
anderer Port ist eine andere Herkunft, und dies ist eine Administrationsoberfläche. Browser, die
zu alt sind, um den Header zu senden, fallen auf den Vergleich von `Origin` mit dem `Host` der
Anfrage zurück — `Origin` senden alle aktuellen Browser bei einem site-übergreifenden POST.

Eine Anfrage **ohne beide** Header stammt nicht von einem Browser (curl, CI-Skripte). Sie hält
weder ein Cookie noch zwischengespeicherte Basic-Auth-Daten, kann also kein CSRF-Angriff sein,
und sie kommt durch — die API bleibt skriptbar, und nirgends ist ein Token oder ein zusätzlicher
Header nötig.

Zwei Folgen:

- **Anfrage-Bodies sind ausschließlich JSON.** `AddRunnerRequest` und `UpdateRunnerRequest`
  tragen keine `form`-Tags; eine formularkodierte Anfrage bindet daher an eine leere Struktur
  und scheitert an der Pflichtfeldprüfung, selbst wenn sie an der Middleware vorbeikäme. Die
  Weboberfläche sendet ohnehin JSON — ihr `FormData` dient nur dazu, die Felder aus dem
  Formularelement auszulesen.
- **`TRUSTED_ORIGINS` ist der Notausgang.** Schreibt ein Reverse Proxy `Host` so um, dass die
  eigenen Anfragen abgewiesen werden, tragen Sie dort die im Browser sichtbaren Herkünfte
  kommagetrennt ein (`https://ci.example.com`). Sie werden unabhängig davon zugelassen, was
  `Sec-Fetch-Site` sagt — nehmen Sie also nur Herkünfte auf, die Sie selbst kontrollieren.

### Breaking Change (Upgrade-Hinweis)

Alte flache Felder `probe_*` sind entfernt; Objekt `probe` verwenden: `probe.error`, `probe.type`, `probe.suggestion`, `probe.check_command`, `probe.fix_command`. Werte von `probe.type`: `docker-access`, `agent-http`, `agent-connect`, `unknown`. Die Web-UI kann bei `status=unknown` weiter „Start/Stop“ zur Selbstheilung nutzen.

Beispiel (Probe-Fehler):

```json
{
  "name": "runner-a",
  "status": "unknown",
  "probe": {
    "error": "agent returned 502: bad gateway",
    "type": "agent-http",
    "suggestion": "Check runner container logs and Agent + /runner process state",
    "check_command": "docker ps -a | rg \"github-runner-\" && docker logs --tail=200 <runner_container_name>",
    "fix_command": "docker restart <runner_container_name>"
  }
}
```

### Wie der Laufzustand ermittelt wird

`internal/runnerproc` beantwortet „läuft der Prozess dieses Runners noch?“, indem es `/proc`
nach einem Prozess durchsucht, dessen argv dieses Installationsverzeichnis beansprucht —
`<dir>/bin/Runner.Listener` oder eine Shell, die `<dir>/run.sh` bzw. `<dir>/run-helper.sh`
ausführt.

Eine pid-Datei liest es bewusst **nicht**, denn actions/runner schreibt keine. Weder `run.sh`
noch `run-helper.sh.template` noch `runsvc.sh` schreiben irgendwo eine pid; die pid existiert
nur in einer Shell-Variablen. Die Datei `.path` in einem Installationsverzeichnis enthält einen
PATH-String, keine pid (`runsvc.sh` macht selbst `export PATH=$(cat .path)`). Beide Namen zu
lesen schlug also immer fehl, wodurch jeder Runner dauerhaft „registriert, läuft nicht“
meldete.

Die überwachende Shell zählt als laufend, auch wenn `Runner.Listener` gerade fehlt:
`run-helper.sh` schläft bei Exit-Code 2 fünf Sekunden, danach startet `run.sh` den Listener neu.
Eine Prüfung allein auf den Listener würde den Runner also bei jedem Neustart für tot erklären.

Zwei Konsequenzen für Aufrufer:

- `runner.List` ist eine Sicht auf Platten-Ebene. Im Containermodus ist ihr `Running` immer
  false — Manager und Runner liegen in verschiedenen PID-Namespaces. Verwenden Sie
  `runner.ListWithLiveStatus`, das den Agent jedes Containers fragt, sobald die Antwort eine
  Aktion auslöst. Es bildet einen Sondierungsfehler auf `status=unknown` statt auf `installed`
  ab, damit „registriert, läuft nicht, also starten“ nicht bei einem Runner greift, den es gar
  nicht erreichen konnte.
- Die Erkennung braucht `/proc` und ist daher Linux-only; anderswo meldet sie „läuft nicht“.

### Berechtigungen des Runner-Verzeichnisses

Das Installationsverzeichnis eines Runners wird mit 0700 angelegt. `config.sh` schreibt dort
`.credentials_rsaparams` hinein — den privaten RSA-Schlüssel, mit dem sich der Runner gegenüber
GitHub authentifiziert — und actions/runner setzt darauf keinerlei Unix-Rechte
(`ConfigurationStore` setzt nur das Windows-Attribut Hidden, die Datei folgt also der umask,
üblicherweise 0644). Der Modus des Verzeichnisses ist damit das Einzige, was zwischen diesem
Schlüssel und den übrigen lokalen Benutzern des Hosts steht.

`MkdirAll` lässt den Modus eines bestehenden Verzeichnisses unangetastet; von älteren Versionen
angelegte Verzeichnisse bleiben also bei 0755. `Preflight` meldet diese mit einem sofort
ausführbaren `chmod 700`, statt sie selbst zu ändern: Ein Verzeichnis enger zu ziehen, während
Manager und Container unter verschiedenen UIDs laufen, würde ein funktionierendes Deployment
zerstören — diese Entscheidung gehört einem Menschen.

`base_path` selbst bleibt durchquerbar — dort liegen keine Zugangsdaten, und im Containermodus
ist es der Mountpunkt auf dem Host.

Das ändert nichts daran, was ein Job erreichen kann. Unter `job_docker_backend: host-socket`
kann ein Job jeden beliebigen Host-Pfad einbinden, wovor die Deployment-Dokumentation bereits
warnt; der Modus schützt vor anderen lokalen Benutzern, nicht davor.

### Entfernen eines Runners und GitHub

`DELETE /api/runners/:name` entfernt einen Runner aus diesem Werkzeug **und** aus GitHub. Die
GitHub-Seite braucht Zugangsdaten, und die einzigen, die dieses Werkzeug je besitzt, sind das
optionale, pro Runner hinterlegte PAT in `<Runner-Verzeichnis>/.github_check_token` — dieselbe
Datei, die auch die Sichtbarkeitsprüfung verwendet. Die Abmeldung läuft deshalb *vor* dem
Löschen des Installationsverzeichnisses, denn dort wohnt das Token; diese Reihenfolge zu
missachten heißt, die Möglichkeit dazu stillschweigend zu verlieren.

Ohne PAT lässt sich der Runner nicht abmelden: GitHub will ein PAT oder einen frischen Removal
Token, und `config.sh remove` will Letzteren. Die Antwort sagt das dann ausdrücklich und nennt,
wo er von Hand zu löschen ist. Das zu sagen lohnt sich, statt es zu schlucken — ein
zurückgelassener Runner lässt das nächste `config.sh --name <gleicher Name>` an
`A runner exists with the same name` scheitern.

`registered_on_github` ist ein **nullable** Bool: `true` / `false` sind Antworten, `null`
bedeutet, dass keine Antwort zustande kam, und `github_check_error` trägt den Grund. Templates
dürfen es nicht mit `{{if .RegisteredOnGitHub}}` prüfen — `html/template` beurteilt einen
Zeiger allein danach, ob er nil ist, ein Zeiger auf `false` ist also wahr. Verwenden Sie die
Helfer `GitHubYes` / `GitHubNo` / `GitHubUnknown` auf `RunnerInfo`.

## Makefile-Ziele

- `make help`: Alle Ziele anzeigen.
- `make build`: Manager bauen (mit Version-ldflags).
- `make build-agent`: Runner Agent bauen (Containermodus).
- `make build-all`: Manager und Agent bauen.
- `make test`: Tests ausführen.
- `make test-race`: Tests mit Race-Detector ausführen (das, was die CI tut).
- `make lint`: golangci-lint über `./...` ausführen — derselbe Schritt wie Lint im Test-Job der CI.
- `make check`: Alles, was die CI prüft, in einem Ziel — gofmt, vet, lint, `-race`-Tests und beide Konsistenzprüfungen. Vor dem Push ausführen.
- `make run`: Manager bauen und ausführen.
- `make docker-build` / `make docker-run` / `make docker-stop`: Manager-Image bauen und ausführen; siehe [Benutzerhandbuch](guide.md).
- `make docker-build-runner`: Runner-Image für Containermodus bauen (`Dockerfile.runner`, Standard-Tag in `RUNNER_IMAGE`).
- `make docker-build-runner-example`: Beispiel für ein angepasstes Runner-Image bauen (`EXAMPLE=android|node`, siehe [`examples/runner-images/`](../../examples/runner-images/)).
- `make clean`: Gebaute Binaries entfernen (runner-manager, runner-agent).

Containermodus nutzt Agent aus `cmd/runner-agent` und Runner-Image aus `Dockerfile.runner`.

## Tests

`go test ./...` oder `make test-race` für das, was die CI tatsächlich ausführt. Sämtliche CI-
und Release-Workflows starten `go test -race`, damit ein Data Race den Build scheitern lässt,
statt später als gelegentlich falscher Zustand in der Produktion aufzutauchen — die Nebenläufigkeit
dieses Projekts (die Registrierungs-Queue mit einem einzigen Worker, das `runnerOps`-Lock je
Runner, die Erzeugung mit einem einzigen Gewinner in `EnsureAgentToken`) beruht auf Konventionen,
die das Typsystem nicht erzwingt. Das golangci-lint des Repos läuft in der CI; `errcheck` und
`staticcheck` über `go run` decken das meiste davon ab, falls Sie es lokal nicht ausführen können.

Ein paar Konventionen, die vor Erweiterungen der Suite hilfreich sind:

- **Die Prozesserkennung wird gegen echte Prozesse geprüft**, nicht nur gegen ein gefälschtes
  `/proc` (`internal/runnerproc`, `internal/runner`, `cmd/runner-agent`). Ein blockierendes
  Wegwerf-`run.sh` vertritt den Runner; es darf kein `exec` machen, sonst wird die Shell ersetzt
  und das argv passt nicht mehr zu dem, wonach `internal/runnerproc` sucht.
- **Die Tests von `cmd/runner-manager` erreichen die Verdrahtung über die Funktionen, die
  `main()` aufruft** (`basicAuthMiddleware`, `httpErrorHandler`, `registerRoutes`, `listenAddr`,
  `loadI18n`). Eine neue Route bedeutet, `TestRegisterRoutes_AllEndpointsPresent` mitzuziehen,
  der die exakte Menge festschreibt — der Anlass, über Authentifizierung für die neue Route
  nachzudenken.
- **Middleware wird über `newEchoServer()` getestet, nicht nur für sich allein.** Eine Absicherung,
  die korrekt ist, aber nie eingehängt wurde, ist die typische Art, wie so etwas versagt; deshalb
  geht `TestCSRFGuardIsMountedOnWriteRoutes` durch die echte Routing-Tabelle. Eine neue
  schreibende Route gehört dort in `writeRoutes`.
- **Die i18n-Dateien werden gegeneinander geprüft.** Ein Schlüssel, der in `en.json` steht und
  anderswo fehlt, erscheint stillschweigend als Leerstelle; die Tests sichern zu, dass die
  Schlüsselmengen über alle sechs Sprachen übereinstimmen, dass kein Wert leer ist und dass jeder
  vom Template referenzierte Schlüssel existiert.
- **Template-Fehler bekommen Tests auf Template-Ebene.** Zwei frühere Bugs saßen in `index.html`
  statt in Go — ein `{{if}}` auf einem `*bool`, das einen Zeiger auf `false` als wahr las, und
  eine `innerHTML`-Zuweisung, die `escapeHtml` übersprang. Beide werden abgedeckt, indem das echte
  Template gerendert oder durchsucht wird, denn ein Test des Go-Helfers allein hätte keinen von
  beiden bemerkt.

- **Dokumentation wird wie Code getestet.** `internal/docsconsistency` enthält nur Tests und
  keinen Laufzeitcode. Sie vergleichen jede Übersetzung mit dem englischen Original
  *unterhalb* der von ci-recipes geprüften Überschriftenebene — Tabellenzeilen, Codeblöcke,
  Listenpunkte, aufgelöste Linkziele — denn die tatsächlich aufgetretene Abweichung war eine
  Tabellenzeile, keine Überschrift. Sie prüfen außerdem, dass aus Nicht-Markdown-Dateien
  referenzierte Pfade existieren, dass die Marke, die die Fehlerbehebungs-Doku zu `grep`
  empfiehlt, die vom Code geloggte ist, und dass jede README unter `examples/` eine
  deklarierte Sprachpolitik hat.

## Release

Versionsangaben in Doku und Beispielen müssen dem Standard-Image-Tag in `internal/config/config.go` entsprechen. CI prüft das über `ci-recipes runner-fleet check-version-consistency`; lokal vor einer Release-PR ausführen:

```bash
ci-recipes runner-fleet check-version-consistency
```

Beide Prüfungen kommen aus [soulteary/ci-recipes](https://github.com/soulteary/ci-recipes), das die
CI-Shell einzelner Repositories durch eine getestete Go-Binary ersetzt; dieses Repository liefert nur
`scripts/ci-recipes.conf`. Installiere die in der CI gepinnte Version:

```bash
make install-ci-recipes
```

Der Pin steht nur in `.github/workflows/ci-consistency.yml`; das Makefile liest ihn von dort.

Zeilen, die bewusst eine ältere Version nennen (Release Notes, Upgrade-Hinweise), mit dem Marker `version-check-ignore` versehen.

Für die Übersetzungen gibt es dieselbe Absicherung. `ci-recipes runner-fleet check-docs-structure` vergleicht
die Abfolge der Überschriftenebenen jeder `docs/<lang>/*.md` mit dem englischen Original — der
Text der Überschriften soll sich unterscheiden, die Struktur nicht — sodass ein im Englischen
ergänzter Abschnitt, der in den fünf Übersetzungen fehlt, die PR scheitern lässt, statt
unbemerkt zu bleiben:

```bash
ci-recipes runner-fleet check-docs-structure
```

Zwei Prüfungen laufen zum Release-Zeitpunkt statt bei jedem PR.
`.github/actions/check-release-version` läuft auf einem `v*.*.*`-Tag und lehnt ihn ab, wenn die
Basisversion in `internal/config/config.go` nicht dem Tag entspricht oder `CHANGELOG.md` keinen
passenden `## [X.Y.Z]`-Abschnitt samt Linkdefinition hat. `check-version-consistency` kann das
nicht sehen: Sie nimmt diese Basis als Wahrheit, also besteht ein Repository, das intern
konsistent, aber einen Patch hinter dem veröffentlichten Release ist — genau so ging ein Release
hinaus, auf das nichts im Baum zeigte. Der Job `Quick start runs` in `CI (Consistency)` führt den
Schnellstart-Block des Handbuchs gegen ein lokal gebautes Image aus und fragt dann `GET /ready`
ab, denn ein dokumentiertes Vorgehen, das niemand ausführt, ist ein Vorgehen, von dem niemand
weiß, dass es kaputt ist.

Neben dieser Datei liegen zwei Notizen für Maintainer, bewusst ohne Übersetzung, denn beide
halten eine Build-Entscheidung fest und kein Produktverhalten:
[CI-Shell nach ci-recipes migrieren](../ci-recipes-migration.md) ist das Audit hinter dem Umzug —
welche Shell-Flächen dorthin gehörten, die vier Defekte, die die Portierung behob, und was sie
kostete — und [der Plan zur Dokumentationsverbesserung](../docs-improvement-plan.md) ist das
Audit, aus dem diese Prüfungen hervorgingen, samt Stand und offenen Punkten.

[← Zurück zur Dokumentation](README.md)
