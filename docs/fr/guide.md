# Guide d'utilisation

**文档 / Docs:** [EN](../guide.md) · [中文](../zh/guide.md) · Français · [Deutsch](../de/guide.md) · [한국어](../ko/guide.md) · [日本語](../ja/guide.md)

![](../../.github/assets/fleet.jpg)

Déploiement, configuration, ajout de runners et sécurité sont traités ici. Pour la build et l'API côté contributeur, voir [Développement et build](development.md).

---

## 1. Déploiement (Docker)

- L'image est basée sur **Ubuntu** avec les dépendances .NET Core 6.0 ; elle tourne en **UID 1001** — les répertoires montés doivent être accessibles en écriture par cet utilisateur (ex. `chown 1001:1001 config.yaml runners`).
- Environ 15 secondes après le démarrage, les runners enregistrés mais arrêtés sont relancés automatiquement ; vérification périodique toutes les 5 minutes.

### Utiliser l'image publiée (recommandé)

En production, utilisez une version précise (ex. v1.0.0). En développement, vous pouvez utiliser le tag `main`.

```bash
docker pull ghcr.io/soulteary/runner-fleet:v1.0.0
```

### Démarrage rapide docker-compose

Le dépôt contient `docker-compose.yml`. N'activez DinD que si vous utilisez le mode conteneur et que les jobs ont besoin de Docker avec `job_docker_backend: dind`.

```bash
cp config.yaml.example config.yaml
# Éditez config.yaml : définissez runners.base_path sur /app/runners

chown 1001:1001 config.yaml
mkdir -p runners && chown 1001:1001 runners

docker network create runner-net 2>/dev/null || true
docker compose up -d
# Si job_docker_backend: dind : docker compose --profile dind up -d
```

Interface : http://localhost:8080. Détails d'authentification dans [4. Sécurité et validation](#4-sécurité-et-validation).

### Lancer le conteneur (paramètres complets)

Montez `config.yaml` et `runners` ; le port doit correspondre à `server.port` dans la config (défaut 8080).

```bash
docker run -d --name runner-manager \
  -p 8080:8080 \
  -v $(pwd)/config.yaml:/app/config.yaml \
  -v $(pwd)/runners:/app/runners \
  ghcr.io/soulteary/runner-fleet:v1.0.0
```

Les répertoires hôte doivent être accessibles en écriture par UID 1001. Basic Auth : `-e BASIC_AUTH_PASSWORD=password`, `-e BASIC_AUTH_USER=admin`. Pour Docker dans les jobs, ajoutez `-v /var/run/docker.sock:/var/run/docker.sock`, ou utilisez DinD (voir `docker-compose.yml` du dépôt, `--profile dind`). L'image inclut le CLI Docker ; les Actions courantes fonctionnent avec DinD.

### Installation et enregistrement automatiques

Dans l'interface « Quick Add Runner », saisissez nom, cible, token et validez ; le script d'installation s'exécute d'abord, puis enregistrement et démarrage. En cas d'échec :

```bash
docker exec runner-manager /app/scripts/install-runner.sh <name> [version]
```

Ou sur l'hôte, extrayez [actions-runner](https://github.com/actions/runner/releases) dans `runners/<name>/`, puis validez dans l'interface ou exécutez `./config.sh` manuellement.

### Mode conteneur (un runner par conteneur)

Chaque runner tourne dans son propre conteneur ; le Manager démarre/arrête via le Docker hôte et récupère le statut en HTTP depuis l'Agent dans le conteneur.

Activer dans **config.yaml** (voir `config.yaml.example`) :

```yaml
runners:
  base_path: /app/runners
  container_mode: true
  container_image: ghcr.io/soulteary/runner-fleet:v1.0.0-runner
  container_network: runner-net
  agent_port: 8081
  job_docker_backend: dind   # dind | host-socket | none
  dind_host: runner-dind
  volume_host_path: /abs/path/on/host/to/runners
```

Image runner : même nom que le Manager avec le tag `-runner` (production : version ex. v1.0.0-runner ; dev : main-runner), ou build local : `docker build -f Dockerfile.runner -t ghcr.io/soulteary/runner-fleet:v1.0.0-runner .`. Le Manager doit utiliser le Docker hôte (montage de `docker.sock`), pas DinD via `DOCKER_HOST` ; dans Compose, utilisez `group_add` pour le GID docker hôte ou `user: "0:0"`. Les noms de runner sont normalisés en noms de conteneurs ; les doublons après mapping entreront en conflit.

### Dépannage

- **Le runner ne démarre pas après compose down** : Exécutez une fois `docker network create runner-net`. Si ça échoue encore, utilisez « Start » dans l'interface pour recréer, ou `docker rm -f github-runner-<name>` puis « Start ».
- **Exécution en root** : Les répertoires montés doivent être accessibles en écriture par l'utilisateur du processus ; pour root, définissez `RUNNER_ALLOW_RUNASROOT=1`.
- **Ancienne image runner** : `docker rm -f github-runner-<name>`, puis « Start » dans l'interface pour recréer.
- **status=unknown** : Consultez la sonde dans la fenêtre de détail ; essayez « Start/Stop » pour l’auto-réparation.

### Construire les images localement

```bash
docker build -t runner-manager .
docker build -f Dockerfile.runner -t ghcr.io/soulteary/runner-fleet:v1.0.0-runner .
```

Make : `make docker-build`, `make docker-run`, `make docker-stop`.

---

## 2. Configuration

```bash
cp config.yaml.example config.yaml
```

| Champ | Description | Défaut |
|-------|-------------|--------|
| `server.port` | Port du serveur HTTP | `8080` |
| `server.addr` | Adresse d'écoute ; vide = toutes les interfaces | vide |
| `runners.base_path` | Chemin racine des répertoires d'installation ; **définir `/app/runners` en conteneur** | `./runners` |
| `runners.items` | Liste prédéfinie de runners | Peut aussi être ajoutée via l'interface |
| `runners.container_mode` | Activer le mode conteneur | `false` |
| `runners.container_image` | Image runner en mode conteneur (tag -runner) | `ghcr.io/soulteary/runner-fleet:v1.0.0-runner` |
| `runners.container_network` | Réseau des runners en mode conteneur | `runner-net` |
| `runners.agent_port` | Port de l'Agent dans le conteneur | `8081` |
| `runners.job_docker_backend` | Docker dans les jobs : `dind` / `host-socket` / `none` | `dind` |
| `runners.dind_host` | Nom d'hôte DinD quand `job_docker_backend=dind` | `runner-dind` |
| `runners.volume_host_path` | Chemin absolu hôte vers runners en mode conteneur (obligatoire) | vide |

**Validation** : Pas de noms dupliqués ; le mode conteneur vérifie les conflits de noms de conteneurs. `job_docker_backend` n'accepte que `dind`/`host-socket`/`none` ; en mode conteneur avec `base_path` conteneur, `volume_host_path` est requis. L'absence de `job_docker_backend` donne `dind` ; après changement de backend, redémarrez les runners depuis l'interface.

Exemple :

```yaml
server:
  port: 8080
  addr: 0.0.0.0
runners:
  base_path: /app/runners
  items: []
```

---

## 3. Ajout de runners

**Obtenir un token** : Repo/org → Settings → Actions → Runners → New self-hosted runner, copiez le token (valide ~1 h). Chaque runner nécessite un nouveau token.

**Ajouter dans le service** : Dans l'interface « Quick Add Runner », saisissez le nom (unique), le type de cible (org/repo), la cible, le token (optionnel ; si renseigné, la validation peut enregistrer et démarrer automatiquement). Vous pouvez coller `./config.sh --url ... --token ...` depuis GitHub dans « Parse from GitHub command » et cliquer « Parse & fill ». L'enregistrement auto est pour GitHub.com uniquement ; GitHub Enterprise nécessite un `config.sh` manuel dans le répertoire du runner.

**Quand le runner n'est pas installé** : Téléchargez depuis [GitHub Actions Runner](https://github.com/actions/runner/releases), extrayez dans `runners/<name>/`, puis saisissez le token dans l'interface ou exécutez `./config.sh`. Avec déploiement conteneur, soumettre un token dans l'interface déclenche l'installation puis l'enregistrement ; le mode conteneur nécessite d'abord l'image Runner et `volume_host_path` (voir mode conteneur ci-dessus).

**Résultat d'enregistrement** : Écrit dans `.registration_result.json` dans le répertoire du runner. **Vérification de visibilité GitHub** (optionnel) : Placez `.github_check_token` (PAT ; org nécessite `admin:org`, repo nécessite `repo`) dans le répertoire du runner ; vérifié ~toutes les 5 minutes, résultat dans `.github_status.json`.

Plusieurs runners par machine : utilisez des sous-répertoires distincts.

---

## 4. Sécurité et validation

**Authentification** : Pas de connexion par défaut ; à utiliser uniquement sur réseau interne ou localhost. Définir la variable d'environnement `BASIC_AUTH_PASSWORD` pour activer Basic Auth ; `BASIC_AUTH_USER` optionnel (défaut `admin`). Toutes les routes sauf `GET /health` nécessitent une authentification ; ne commitez pas les secrets — utilisez `.env`. En conteneur : `-e BASIC_AUTH_PASSWORD=...` ou `env_file` dans compose.

**Chemins et unicité** : name/path ne doivent pas contenir `..`, `/`, `\` ; les répertoires doivent être sous `runners.base_path`. Pas de noms dupliqués ; le nom est en lecture seule à l'édition. En mode conteneur les noms sont normalisés en noms de conteneurs ; les doublons après mapping provoquent une erreur.

**Fichiers sensibles** : config.yaml et .env sont dans `.gitignore`. Pour `.github_check_token` de chaque runner, utilisez `chmod 600` ; ajoutez `**/.github_check_token` à `.gitignore` si sous contrôle de version.

[← Retour à l'accueil du projet](../../README.md)
