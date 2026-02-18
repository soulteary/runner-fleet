# Entwicklung & Build

**文档 / Docs:** [EN](../development.md) · [中文](../zh/development.md) · [Français](../fr/development.md) · Deutsch · [한국어](../ko/development.md) · [日本語](../ja/development.md)

Für Produktion Container-Bereitstellung verwenden; siehe [Benutzerhandbuch](guide.md). Dieses Dokument ist für Mitwirkende: lokaler Build und Debug.

## Anforderungen

- Go 1.26 (abgestimmt auf [go.mod](../../go.mod)).

## Build

```bash
# runner-manager-Binary bauen
go build -o runner-manager ./cmd/runner-manager

# Mit Version (für /version und Debug)
go build -ldflags "-X main.Version=1.0.0" -o runner-manager ./cmd/runner-manager

# Nur Runner Agent bauen (Containermodus)
go build -o runner-agent ./cmd/runner-agent

# Oder Make: make build / make build-agent / make build-all
```

Templates sind im Manager-Binary eingebettet (`cmd/runner-manager/templates/`); einzelnes Binary, kein separates `templates/` nötig.

## Lokal ausführen und debuggen

```bash
cp config.yaml.example config.yaml
go run ./cmd/runner-manager
# Oder make run (build dann run); eigene Config: ./runner-manager -config /path/to/config.yaml
```

Lauscht auf `:8080`, http://localhost:8080. Basic Auth zum Debuggen: `BASIC_AUTH_PASSWORD=secret go run ./cmd/runner-manager`; siehe [Benutzerhandbuch – Sicherheit](guide.md#4-sicherheit-und-validierung).

## CLI-Flags

- `-config <path>`: Pfad zur Konfigurationsdatei.
- `-version`: Version ausgeben und beenden (beim Build mit `-ldflags "-X main.Version=..."` injizieren).

## HTTP-API

Mit Basic Auth müssen alle Anfragen außer `/health` den Header `Authorization: Basic <base64(user:password)>` enthalten.

| Pfad | Methode | Beschreibung |
|------|---------|--------------|
| `/health` | GET | Gibt `{"status":"ok"}` zurück; für Ingress/K8s-Probes; immer unauthentifiziert. |
| `/version` | GET | Gibt `{"version":"..."}` zurück. |
| `/api/runners` | GET | Runner-Liste. Im Containermodus bei Probe-Fehler `status=unknown` mit strukturiertem `probe` (`error/type/suggestion/check_command/fix_command`). |
| `/api/runners/:name` | GET | Einzelner Runner. Gleiches `probe` bei Probe-Fehler im Containermodus. |
| `/api/runners/:name/start` | POST | Runner starten. Bei Probe-Fehler startet trotzdem, gibt strukturiertes `probe` in der Antwort zurück. |
| `/api/runners/:name/stop` | POST | Runner stoppen. Bei Probe-Fehler stoppt trotzdem, gibt strukturiertes `probe` in der Antwort zurück. |

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

## Makefile-Ziele

- `make help`: Alle Ziele anzeigen.
- `make build`: Manager bauen (mit Version-ldflags).
- `make build-agent`: Runner Agent bauen (Containermodus).
- `make build-all`: Manager und Agent bauen.
- `make test`: Tests ausführen.
- `make run`: Manager bauen und ausführen.
- `make docker-build` / `make docker-run` / `make docker-stop`: Manager-Image bauen und ausführen; siehe [Benutzerhandbuch](guide.md).
- `make docker-build-runner`: Runner-Image für Containermodus bauen (`Dockerfile.runner`, Standard-Tag in `RUNNER_IMAGE`).
- `make clean`: Gebaute Binaries entfernen (runner-manager, runner-agent).

Containermodus nutzt Agent aus `cmd/runner-agent` und Runner-Image aus `Dockerfile.runner`.

[← Zurück zur Dokumentation](README.md)
