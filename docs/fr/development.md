# Développement et build

En production, utilisez le déploiement conteneur ; voir [Guide d'utilisation](guide.md). Ce document est pour les contributeurs : build et débogage locaux.

## Prérequis

- Go 1.26 (en cohérence avec [go.mod](../../go.mod)).

## Build

```bash
# Construire le binaire runner-manager
go build -o runner-manager ./cmd/runner-manager

# Avec version (pour /version et débogage)
go build -ldflags "-X main.Version=1.0.0" -o runner-manager ./cmd/runner-manager

# Construire uniquement le Runner Agent (mode conteneur)
go build -o runner-agent ./cmd/runner-agent

# Ou Make : make build / make build-agent / make build-all
```

Les templates sont intégrés dans le binaire Manager (`cmd/runner-manager/templates/`) ; binaire unique, pas besoin de fournir `templates/`.

## Exécution et débogage locaux

```bash
cp config.yaml.example config.yaml
go run ./cmd/runner-manager
# Ou make run (build puis exécution) ; config personnalisée : ./runner-manager -config /path/to/config.yaml
```

Écoute sur `:8080`, http://localhost:8080. Basic Auth pour le débogage : `BASIC_AUTH_PASSWORD=secret go run ./cmd/runner-manager` ; voir [Guide – Sécurité](guide.md#4-sécurité-et-validation).

## Options CLI

- `-config <path>` : Chemin du fichier de configuration.
- `-version` : Affiche la version et quitte (injection à la build avec `-ldflags "-X main.Version=..."`).

## API HTTP

Avec Basic Auth, toutes les requêtes sauf `/health` doivent inclure `Authorization: Basic <base64(user:password)>` dans l'en-tête.

| Chemin | Méthode | Description |
|--------|---------|-------------|
| `/health` | GET | Retourne `{"status":"ok"}` ; pour sondes Ingress/K8s ; toujours sans authentification. |
| `/version` | GET | Retourne `{"version":"..."}`. |
| `/api/runners` | GET | Liste des runners. En mode conteneur, en cas d'échec de sonde retourne `status=unknown` avec `probe` structuré (`error/type/suggestion/check_command/fix_command`). |
| `/api/runners/:name` | GET | Détails d'un runner. Même `probe` en cas d'échec de sonde en mode conteneur. |
| `/api/runners/:name/start` | POST | Démarrer le runner. En cas d'échec de sonde tente quand même le démarrage, retourne `probe` structuré dans la réponse. |
| `/api/runners/:name/stop` | POST | Arrêter le runner. En cas d'échec de sonde tente quand même l'arrêt, retourne `probe` structuré dans la réponse. |

### Changement incompatible (note de mise à jour)

Les anciens champs plats `probe_*` sont supprimés ; utilisez l'objet `probe` : `probe.error`, `probe.type`, `probe.suggestion`, `probe.check_command`, `probe.fix_command`. Valeurs de `probe.type` : `docker-access`, `agent-http`, `agent-connect`, `unknown`. L'interface peut toujours « Start/Stop » pour l’auto-réparation quand `status=unknown`.

Exemple (échec de sonde) :

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

## Cibles Makefile

- `make help` : Lister toutes les cibles.
- `make build` : Build du Manager (avec ldflags Version).
- `make build-agent` : Build du Runner Agent (mode conteneur).
- `make build-all` : Build du Manager et de l'Agent.
- `make test` : Lancer les tests.
- `make run` : Build puis exécution du Manager.
- `make docker-build` / `make docker-run` / `make docker-stop` : Build et exécution de l'image Manager ; voir [Guide d'utilisation](guide.md).
- `make docker-build-runner` : Build de l'image Runner pour le mode conteneur (`Dockerfile.runner`, tag par défaut dans `RUNNER_IMAGE`).
- `make clean` : Supprimer les binaires construits (runner-manager, runner-agent).

Le mode conteneur utilise l'Agent de `cmd/runner-agent` et l'image Runner de `Dockerfile.runner`.

[← Retour à la doc](README.md)
