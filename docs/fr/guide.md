# Guide d'utilisation

**文档 / Docs:** [EN](../guide.md) · [中文](../zh/guide.md) · Français · [Deutsch](../de/guide.md) · [한국어](../ko/guide.md) · [日本語](../ja/guide.md)

![](../../.github/assets/fleet.jpg)

Déploiement, configuration, ajout de runners et sécurité sont traités ici. Pour la build et l'API côté contributeur, voir [Développement et build](development.md).

---

## 1. Déploiement (Docker)

- **Linux uniquement**, `linux/amd64` et `linux/arm64`. L'état d'exécution d'un runner est lu dans la table des processus via `/proc` ; sur tout autre OS chaque runner est rapporté « à l'arrêt », donc le démarrage/arrêt et la reprise automatique ne peuvent pas fonctionner. Les images publiées couvrent ces deux architectures.
- **L'interface est traduite ; les messages ne le sont pas.** L'interface existe en six langues, mais tout ce que le serveur répond — messages d'API, notifications et lignes de log — est en chinois uniquement. Les lignes d'autotest sont préfixées `[preflight …]` afin d'être trouvables sans lire le chinois, mais leur texte est en chinois. C'est une limite connue, pas une traduction inachevée.
- L'image est basée sur **Ubuntu** avec les dépendances .NET Core 6.0 ; elle tourne en **UID 1001** — les répertoires montés doivent être accessibles en écriture par cet utilisateur (ex. `chown 1001:1001 config runners`).
- Environ 15 secondes après le démarrage, les runners enregistrés mais arrêtés sont relancés automatiquement ; vérification périodique toutes les 5 minutes.

### Utiliser l'image publiée (recommandé)

En production, utilisez une version précise (ex. v1.7.1). En développement, vous pouvez utiliser le tag `main`.

```bash
docker pull ghcr.io/soulteary/runner-fleet:v1.7.1
```

### Démarrage rapide docker-compose

Le dépôt contient `docker-compose.yml`. N'activez DinD que si vous utilisez le mode conteneur et que les jobs ont besoin de Docker avec `job_docker_backend: dind`.

```bash
mkdir -p config runners && cp config.yaml.example config/config.yaml
# Éditez config/config.yaml : définissez runners.base_path sur /app/runners

sudo chown -R 1001:1001 config runners

docker network create runner-net 2>/dev/null || true
docker compose up -d
# Si job_docker_backend: dind : docker compose --profile dind up -d
```

Interface : http://localhost:8080. Détails d'authentification dans [4. Sécurité et validation](#4-sécurité-et-validation).

### Lancer le conteneur (paramètres complets)

Montez le répertoire `config` et `runners` (montez le répertoire uniquement, pas le fichier config/config.yaml seul, sinon Docker crée un fichier vide si absent) ; le port doit correspondre à `server.port` dans la config (défaut 8080).

```bash
docker run -d --name runner-manager \
  -p 8080:8080 \
  -v $(pwd)/config:/app/config \
  -v $(pwd)/runners:/app/runners \
  ghcr.io/soulteary/runner-fleet:v1.7.1
```

Les répertoires hôte doivent être accessibles en écriture par UID 1001. Basic Auth : `-e BASIC_AUTH_PASSWORD=password`, `-e BASIC_AUTH_USER=admin`. Pour Docker dans les jobs, ajoutez `-v /var/run/docker.sock:/var/run/docker.sock`, ainsi que `--group-add $(getent group docker | cut -d: -f3)` si le GID docker hôte n'est pas 999 (l'image embarque un groupe `docker` en GID 999, modifiable via l'arg de build `DOCKER_GID`), ou utilisez DinD (voir `docker-compose.yml` du dépôt, `--profile dind`). Les deux images incluent le CLI Docker ainsi qu'une base d'outils en ligne de commande alignée sur les runners hébergés par GitHub : `scripts/apt-packages.txt` reprend le jeu de paquets apt de `actions/runner-images` (`toolset-2404.json`), donc `git`, `unzip`, `jq`, `rsync`, `sudo`, `xvfb`, etc. sont présents. Les SDK de langage et de plateforme en sont volontairement exclus — utilisez les actions `setup-*` ou étendez l'image. Comme sur les runners hébergés, les deux images donnent un `sudo` sans mot de passe à l'utilisateur du job, donc `sudo apt-get install -y …` fonctionne ; construisez avec `--build-arg ALLOW_SUDO=false` pour le retirer.

### Installation et enregistrement automatiques

Dans l'interface « Quick Add Runner », saisissez nom, cible, token et validez ; le script d'installation s'exécute d'abord, puis enregistrement et démarrage. En cas d'échec :

```bash
docker exec runner-manager /app/scripts/install-runner.sh <name> [version]
```

Le script choisit l'architecture via `uname -m`, résout la dernière version via l'API GitHub si aucune n'est donnée, et vérifie **toujours** le SHA-256. Si le hash officiel est inaccessible (hors ligne, miroir), passez-le explicitement : `RUNNER_SHA256=<sha256> ... install-runner.sh <name> <version>`. Un répertoire contenant déjà un runner est ignoré sauf si `RUNNER_FORCE_REINSTALL=1` est défini.

Ou sur l'hôte, extrayez [actions-runner](https://github.com/actions/runner/releases) dans `runners/<name>/`, puis validez dans l'interface ou exécutez `./config.sh` manuellement.

### Mode conteneur (un runner par conteneur)

Chaque runner tourne dans son propre conteneur ; le Manager démarre/arrête via le Docker hôte et récupère le statut en HTTP depuis l'Agent dans le conteneur.

**Option 1 : Env uniquement (recommandé en full-container)**
Inutile de modifier config/config.yaml. Copiez `cp .env.example .env` et définissez par ex. `CONTAINER_MODE=true`, `VOLUME_HOST_PATH=<chemin absolu hôte vers runners>` (ex. `realpath runners`), `JOB_DOCKER_BACKEND=host-socket`, `CONTAINER_NETWORK=runner-net`. Si vous ne créez pas `config/config.yaml`, le programme le génère au premier démarrage à partir de ces variables. Si `RUNNER_IMAGE` n'est pas défini, l'image runner est dérivée de `MANAGER_IMAGE` (ex. `v1.7.1` → `v1.7.1-runner`). Les montages `config` et `runners` doivent rester en `chown 1001:1001`. Voir `.env.example` pour les variables d'override.

**Option 2 : Activer dans config/config.yaml** (voir `config.yaml.example`) :

```yaml
runners:
  base_path: /app/runners
  container_mode: true
  container_image: ghcr.io/soulteary/runner-fleet:v1.7.1-runner
  container_network: runner-net
  agent_port: 8081
  job_docker_backend: dind   # dind | host-socket | none
  dind_host: runner-dind
  volume_host_path: /abs/path/on/host/to/runners
```

Image runner : même nom que le Manager avec le tag `-runner` (production : version ex. v1.7.1-runner ; dev : main-runner), ou build local : `docker build -f Dockerfile.runner -t ghcr.io/soulteary/runner-fleet:v1.7.1-runner .`. Le Manager doit utiliser le Docker hôte (montage de `docker.sock`), pas DinD via `DOCKER_HOST` ; dans Compose, utilisez `group_add` pour le GID docker hôte ou `user: "0:0"`. Avec `job_docker_backend: host-socket`, le Manager passe `--group-add <GID docker hôte>` au conteneur runner (détecté automatiquement depuis `docker.sock`, remplaçable via `runners.docker_gid` / `DOCKER_GID`) ; l'image embarque aussi un groupe `docker` (arg de build `DOCKER_GID`, 999 par défaut). Les noms de runner sont normalisés en noms de conteneurs ; les doublons après mapping entreront en conflit.

**Étendre l'image runner** : les runners hébergés par GitHub embarquent des toolchains (SDK Android, Node, Python…), pas les runners auto-hébergés. Les workflows écrits pour `ubuntu-24.04` s'appuient souvent dessus implicitement et échouent après la migration. Superposez votre toolchain à l'image runner de ce dépôt — des exemples prêts à l'emploi et les quatre règles qui comptent (chown vers UID 1001, variables d'environnement dans l'image, sudo sans mot de passe hérité, préchauffage après `USER app`) sont dans [`examples/runner-images/`](../../examples/runner-images/). `items[].container_image` la réserve à un runner précis ; sélectionnez-le par label dans le workflow.

**Changements de configuration et reconstruction des conteneurs** : l'image, le réseau, le répertoire monté et le backend Docker des jobs sont figés au moment du `docker create` ; un conteneur existant conserve ce avec quoi il a été créé, donc modifier la configuration ne l'atteint pas. Le Manager compare les paramètres de création réels de chaque conteneur à la configuration actuelle. Un conteneur **arrêté** qui ne correspond plus est supprimé puis recréé au prochain démarrage — que vous cliquiez sur « Démarrer » ou que le Manager le relance automatiquement ; la liste marque alors le runner « config modifiée » et l'infobulle indique l'écart (par ex. `job_docker_backend: → dind`). Un conteneur **en cours d'exécution** n'est pas touché — un job peut être en route. Pour appliquer tout de suite, utilisez le bouton « Recréer » de la ligne (`POST /api/runners/:name/recreate`, interrompt le job en cours), ou arrêtez-le puis redémarrez-le une fois inactif. Reconstruire l'image sous le même tag compte aussi : la comparaison porte sur l'ID d'image, pas seulement sur la référence.

**Exemples de déploiement prêts à l'emploi** : [`examples/deploy/`](../../examples/deploy/) propose deux configurations à copier telles quelles — `standalone/` (un seul conteneur Manager, les processus runner tournent dedans ; `docker run` ou Compose) et `fleet/` (mode conteneur : un conteneur par runner, cache d'images partagé via le daemon de l'hôte, caches de toolchain et d'actions embarqués dans une couche de l'image runner, caches de build isolés par runner). Son README compare les deux, explique quels caches sont partagés ou isolés, et recense les pièges de déploiement (propriétaire des répertoires, `VOLUME_HOST_PATH`, croissance disque avec host-socket).

### Dépannage

- **En cas de problème, regardez d'abord l'autotest de démarrage** : `docker compose logs runner-manager | grep '\[preflight'`. Au démarrage, le répertoire runners, l'accès à Docker, le réseau, l'image runner et le backend Docker des jobs sont vérifiés ; chaque échec indique la commande de correction.
- **Le runner ne démarre pas après compose down** : Exécutez une fois `docker network create runner-net`. Si ça échoue encore, utilisez « Start » dans l'interface pour recréer, ou `docker rm -f github-runner-<name>` puis « Start ».
- **Exécution en root** : Les répertoires montés doivent être accessibles en écriture par l'utilisateur du processus ; pour root, définissez `RUNNER_ALLOW_RUNASROOT=1`.
- **`permission denied` sur docker.sock dans les jobs** : Avec `job_docker_backend: host-socket`, l'utilisateur du conteneur (UID 1001) doit appartenir au groupe du socket. Le Manager ajoute `--group-add` avec le GID docker hôte détecté à la création ; un conteneur dont le GID ne correspond plus est considéré comme divergent et recréé au prochain démarrage (s'il tourne, la ligne affiche « config modifiée » avec le bouton « Recréer »). Si la détection échoue, définissez `runners.docker_gid` (ou `DOCKER_GID` dans `.env`) sur `getent group docker | cut -d: -f3`.
- **`command not found` ou SDK manquant dans un job** : les runners auto-hébergés n'embarquent pas ce que les runners GitHub embarquent. Consultez d'abord l'autotest de démarrage (`docker compose logs runner-manager | grep '\[preflight'`) : il indique lesquels de `git`/`unzip`/`tar`/`curl` manquent à chaque image runner configurée. Pour les SDK de langage ou de plateforme, étendez l'image — voir [`examples/runner-images/`](../../examples/runner-images/).
- **Ancienne image runner** : récupérez-la ou reconstruisez-la, puis démarrez le runner — le Manager détecte le changement d'image (référence et ID d'image, donc une reconstruction du même tag compte aussi) et recrée le conteneur. Un conteneur en cours n'est pas touché ; utilisez « Recréer » sur la ligne quand le job peut être interrompu.
- **Le log répète `已定时拉起 runner: <nom>` toutes les 5 minutes et aucun runner n'apparaît jamais comme en cours d'exécution** : corrigé dans cette version — la mise à jour suffit, aucun runner n'est à réenregistrer. L'état d'exécution provenait d'un fichier pid (`Runner.Listener.pid`, à défaut `.path`), et actions/runner n'écrit ni l'un ni l'autre : aucun de ses scripts de démarrage n'écrit de fichier pid, et `.path` contient une chaîne PATH. Chaque runner se lisait donc comme « enregistré mais pas en cours », et la passe toutes les 5 minutes les relançait tous à chaque tour. L'état est désormais lu dans la table des processus, et en mode conteneur auprès de l'Agent de chaque conteneur — le Manager ne voit pas les processus des autres conteneurs. Même cause : en mode par défaut (hors conteneur), « Arrêter » échouait systématiquement avec `未找到 runner pid 文件或 pid 无效`.
- **Un runner supprimé depuis l'interface reste listé sur GitHub, et le recréer sous le même nom échoue** : la suppression le désinscrit désormais aussi de GitHub, mais seulement si son répertoire contient un `.github_check_token` (le PAT optionnel ; `admin:org` pour une organisation, `repo` pour un dépôt). Sans ce jeton, aucune désinscription n'est possible : la réponse le dit et renvoie vers Settings → Actions → Runners. Les runners supprimés par les versions antérieures n'ont jamais été désinscrits et sont à retirer à la main.
- **Un runner affiche « Échec de la requête GitHub »** : la requête est partie mais n'a pas abouti — survolez pour la cause (jeton expiré, portée insuffisante, limite de débit, cible invisible). C'est différent de « Pas sur GitHub », où GitHub a répondu et le runner n'était pas dans la liste. Les versions antérieures signalaient le premier cas comme le second.
- **status=unknown** : Consultez la sonde dans la fenêtre de détail ; essayez « Start/Stop » pour l’auto-réparation.

### Construire les images localement

```bash
docker build -t runner-manager .
docker build -f Dockerfile.runner -t ghcr.io/soulteary/runner-fleet:v1.7.1-runner .
```

Make : `make docker-build`, `make docker-run`, `make docker-stop`.

---

## 2. Configuration

```bash
mkdir -p config && cp config.yaml.example config/config.yaml
```

| Champ | Description | Défaut |
|-------|-------------|--------|
| `server.port` | Port du serveur HTTP | `8080` |
| `server.addr` | Adresse d'écoute ; vide = toutes les interfaces | vide |
| `runners.base_path` | Chemin racine des répertoires d'installation ; **définir `/app/runners` en conteneur** | `./runners` |
| `runners.items` | Liste prédéfinie de runners | Peut aussi être ajoutée via l'interface |
| `runners.container_mode` | Activer le mode conteneur | `false` |
| `runners.container_image` | Image runner en mode conteneur (tag -runner) | `ghcr.io/soulteary/runner-fleet:v1.7.1-runner` |
| `runners.container_network` | Réseau des runners en mode conteneur | `runner-net` |
| `runners.agent_port` | Port de l'Agent dans le conteneur | `8081` |
| `runners.job_docker_backend` | Docker dans les jobs : `dind` / `host-socket` / `none` | `dind` |
| `runners.dind_host` | Nom d'hôte DinD quand `job_docker_backend=dind` | `runner-dind` |
| `runners.docker_gid` | GID du groupe docker de l'hôte ajouté aux conteneurs runner quand `job_docker_backend=host-socket` ; vide ou `0` le détecte depuis `docker.sock` | vide (détection auto) |
| `runners.volume_host_path` | Chemin absolu hôte vers runners en mode conteneur (obligatoire) | vide |
| `runners.items[].name` | Nom affiché ; aussi le nom du répertoire d'installation et, en mode conteneur, le nom du conteneur. Unique, non modifiable après création | requis |
| `runners.items[].path` | Sous-répertoire sous `base_path` ; vide utilise `name` | vide (= `name`) |
| `runners.items[].target_type` | `org` ou `repo` | requis |
| `runners.items[].target` | Nom d'organisation, ou `owner/repo` | requis |
| `runners.items[].labels` | Étiquettes personnalisées ; le `runs-on` d'un workflow sélectionne dessus | vide |
| `runners.items[].container_image` | Surcharge d'image par runner (mode conteneur) ; vide = valeur globale | vide |
| `runners.items[].job_docker_backend` | Surcharge du backend Docker par runner (mode conteneur) ; vide = valeur globale | vide |
| `runners.resources` | Limites de ressources des conteneurs runner (`cpus` / `memory` / `memory_swap` / `pids_limit`), transmises à `docker create` et appliquées aux conteneurs existants via `docker update` au démarrage | vide (illimité) |

Tous les champs ci-dessus qu'un déploiement en conteneur doit changer peuvent venir de l'environnement, si bien qu'une installation tout-conteneur ne touche que `.env` — voir [Variables d'environnement](#variables-denvironnement) ci-dessous.

### Variables d'environnement

Lues une seule fois au démarrage : changer l'une d'elles demande un redémarrage du Manager.
Lorsque deux noms pointent vers le même réglage, ils sont lus dans l'ordre du tableau, donc
c'est le **second** qui l'emporte si les deux sont définis.

| Variable | Surcharge | Défaut / note |
|---|---|---|
| `MANAGER_PORT`, `SERVER_PORT` | `server.port` | `8080`. Compose fixe `SERVER_PORT` dans le conteneur et y mappe `MANAGER_PORT`, donc le port publié peut différer du port d'écoute |
| `SERVER_ADDR` | `server.addr` | vide (toutes les interfaces) |
| `RUNNERS_BASE_PATH` | `runners.base_path` | `./runners` ; `/app/runners` dans l'image |
| `CONTAINER_MODE` | `runners.container_mode` | `false`. Seuls `true` et `1` sont lus : cette variable **active** le mode conteneur, elle ne le désactive jamais, pour qu'une valeur erronée ne change pas silencieusement la forme d'un déploiement |
| `RUNNER_IMAGE`, `CONTAINER_IMAGE` | `runners.container_image` | Non définies : dérivé de `MANAGER_IMAGE` (`:v1.7.1` → `:v1.7.1-runner`), sinon de `FLEET_IMAGE_TAG` |
| `CONTAINER_NETWORK` | `runners.container_network` | `runner-net` |
| `VOLUME_HOST_PATH`, `RUNNERS_VOLUME_HOST_PATH` | `runners.volume_host_path` | vide ; requis en mode conteneur |
| `JOB_DOCKER_BACKEND` | `runners.job_docker_backend` | `dind` |
| `DOCKER_GID` | `runners.docker_gid` | vide = détection depuis `docker.sock` |

Celles-ci n'ont pas d'équivalent dans le fichier de configuration :

| Variable | Rôle | Défaut |
|---|---|---|
| `BASIC_AUTH_PASSWORD` | La définir active l'authentification Basic | vide (pas d'auth) |
| `BASIC_AUTH_USER` | Nom d'utilisateur Basic Auth | `admin` |
| `TRUSTED_ORIGINS` | Origines exemptées du contrôle intersites, séparées par des virgules ; voir [4. Sécurité et validation](#4-sécurité-et-validation) | vide |
| `LOG_LEVEL` | `trace` / `debug` / `info` / `warn` / `error` | `info` |
| `LOG_FORMAT` | `console` pour les humains, `json` pour ELK ou Loki ; une valeur inconnue retombe sur `console` au lieu d'empêcher le démarrage | `console` |
| `DOCKER_HOST` | Quel daemon Docker le **Manager lui-même** utilise. Le mode conteneur exige la socket de l'hôte — la pointer vers DinD casse la création des runners | `unix:///var/run/docker.sock` |
| `MANAGER_IMAGE` | Quelle image Manager Compose récupère ; l'image runner en est dérivée | le tag de la release |
| `FLEET_IMAGE_TAG` | Tag de l'image runner par défaut quand rien d'autre ne le décide | `v1.7.1` |

`scripts/install-runner.sh` lit en plus `RUNNER_VERSION`, `RUNNER_SHA256` et
`RUNNER_FORCE_REINSTALL` — voir [Installation et enregistrement automatiques](#installation-et-enregistrement-automatiques).

Dans un conteneur runner, l'Agent lit `AGENT_TOKEN`, `AGENT_PORT` et `RUNNER_INSTALL_DIR`. Le
Manager les fournit tous les trois à la création du conteneur ; les définir à la main ne fait
partie d'aucun déploiement normal.


**Validation** : Pas de noms dupliqués ; le mode conteneur vérifie les conflits de noms de conteneurs. `job_docker_backend` n'accepte que `dind`/`host-socket`/`none` ; en mode conteneur avec `base_path` conteneur, `volume_host_path` est requis. L'absence de `job_docker_backend` donne `dind`. Après un changement de backend, un conteneur **arrêté** est reconstruit à son prochain démarrage, tandis qu'un conteneur **en cours d'exécution** est marqué « configuration modifiée » et le bouton « Recréer » de la ligne l'applique immédiatement — voir « Changements de configuration et reconstruction des conteneurs » ci-dessus.

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

**Résultat d'enregistrement** : Écrit dans `.registration_result.json` dans le répertoire du runner. **Vérification de visibilité GitHub** (optionnel) : Placez `.github_check_token` (PAT ; org nécessite `admin:org`, repo nécessite `repo`) dans le répertoire du runner ; vérifié ~toutes les 5 minutes, résultat dans `.github_status.json`. Le même contrôle enregistre aussi si GitHub indique que le runner **exécute un job** ; la liste affiche alors un badge « Occupé » et la boîte de dialogue une ligne dédiée. Il partage la cadence d’environ 5 minutes et peut donc avoir jusqu’à 5 minutes de retard ; sans PAT la valeur reste « inconnu » plutôt que signalée comme inactive. Dans la liste, « Enregistré » et « GitHub ✓ » renvoient tous deux vers la page des runners de la cible.

**Vérification des conflits de nom** : pendant la saisie du nom, le formulaire interroge `/api/runner-precheck` et montre avant l'envoi ce qui poserait problème — un runner de ce nom existe déjà, le nom donne le même nom de conteneur qu'un autre, le répertoire d'installation est déjà pris, un répertoire résiduel contient déjà un runner enregistré (`.runner`), ou un conteneur de ce nom traîne encore sur l'hôte. Les blocages s'affichent en rouge avec un nom proposé applicable en un clic ; les avertissements (répertoire non vide réutilisé) n'empêchent pas de continuer. Envoyer quand même est refusé côté serveur par un **409** portant les mêmes conflits — l'ancien ajout silencieux d'un suffixe aléatoire est supprimé (`auto_rename: true` pour le retrouver).

Plusieurs runners par machine : utilisez des sous-répertoires distincts.

---

## 4. Sécurité et validation

**Authentification** : Pas de connexion par défaut ; à utiliser uniquement sur réseau interne ou localhost. Définir la variable d'environnement `BASIC_AUTH_PASSWORD` pour activer Basic Auth ; `BASIC_AUTH_USER` optionnel (défaut `admin`). Toutes les routes sauf `GET /health` et `GET /ready` nécessitent une authentification ; ne commitez pas les secrets — utilisez `.env`. En conteneur : `-e BASIC_AUTH_PASSWORD=...` ou `env_file` dans compose.

**Requêtes intersites** : les endpoints d'écriture rejettent tout ce que le navigateur signale comme intersite, de sorte qu'une page d'une autre origine ne peut pas piloter cette API avec vos identifiants Basic Auth en cache. Rien à configurer. Si un reverse proxy réécrit `Host` au point que vos propres requêtes sont refusées, listez les origines visibles dans le navigateur dans `TRUSTED_ORIGINS` (séparées par des virgules). Les appelants non navigateurs (curl, scripts de CI) ne sont pas concernés — ils ne portent aucun identifiant en cache et ne peuvent donc pas être l'attaquant ici. Voir la [doc de développement](development.md) pour la règle exacte.

**Chemins et unicité** : name/path ne doivent pas contenir `..`, `/`, `\` ; les répertoires doivent être sous `runners.base_path`. Pas de noms dupliqués ; le nom est en lecture seule à l'édition. En mode conteneur les noms sont normalisés en noms de conteneurs ; les doublons après mapping provoquent une erreur.

**Authentification de l'Agent** (mode conteneur) : le Manager écrit un jeton aléatoire par runner dans `<répertoire runner>/.agent_token` (mode 0600), l'injecte dans le conteneur via `AGENT_TOKEN` et l'envoie en `Authorization: Bearer` à chaque appel. L'Agent lit la variable d'environnement, donc cela ne dépend pas d'UID identiques ; le fichier est la copie persistante côté Manager. L'Agent refuse `/status`, `/start` et `/stop` sans jeton ; `/health` reste ouvert pour le HEALTHCHECK. Les conteneurs créés avant ce changement n'ont pas de jeton et continuent sans authentification ; ils sont désormais considérés comme divergents et recréés au prochain démarrage (pour un conteneur en cours, utilisez « Recréer »).

**Fichiers sensibles** : config/config.yaml et .env sont dans `.gitignore`. Pour `.github_check_token` de chaque runner, utilisez `chmod 600` ; ajoutez `**/.github_check_token` à `.gitignore` si sous contrôle de version.

**Permissions des répertoires runner** : le répertoire d'installation de chaque runner est créé en 0700. `config.sh` y écrit `.credentials_rsaparams` — la clé privée RSA avec laquelle le runner s'authentifie auprès de GitHub — et actions/runner ne pose aucune permission Unix sur ces fichiers ; le mode du répertoire est donc la seule chose qui empêche les autres utilisateurs locaux de la machine de la lire et d'usurper ce runner. **Les répertoires créés par les versions antérieures restent en 0755.** L'auto-test de démarrage (`docker compose logs runner-manager | grep '\[preflight'`) les nomme et fournit le `chmod 700` à exécuter. Il ne les modifie pas lui-même : en cas d'UID divergents (Manager en root, conteneur en app(1001)), resserrer les permissions casserait un déploiement qui fonctionne — vérifiez avant d'exécuter.

---

## 5. Exploitation

### Sondes

| Chemin | Rôle |
|---|---|
| `GET /health` | Liveness. 200 tant que le processus tourne, sans aucune vérification de dépendance — il reste 200 même si la configuration est cassée ou le montage inutilisable. Pour un `livenessProbe` K8s : redémarrer est la bonne réponse à un processus figé, et la mauvaise à une configuration erronée |
| `GET /ready` | Readiness. 503 si la configuration ne peut être chargée, ou si `runners.base_path` est absent ou non inscriptible — il écrit réellement un fichier de test, donc il attrape l'erreur de propriétaire de répertoire que `/health` ne voit pas. Pour un `readinessProbe` K8s, et c'est celui à interroger après avoir modifié un déploiement |

Les deux restent sans authentification même avec Basic Auth activé, car une sonde ne porte
aucune identifiant. Ni l'une ni l'autre ne dit *quelle* vérification a échoué ; cela est dans le log.

### Métriques

`GET /metrics` sert du texte Prometheus — volume d'appels et latence par route. Le label `path`
est le gabarit de route d'Echo (`/api/runners/:name`), pas l'URL de la requête, si bien qu'une
flotte de runners ne devient pas une flotte de valeurs de label.

Contrairement aux sondes, `/metrics` **exige l'authentification** quand Basic Auth est activé :
il expose le volume d'appels de chaque endpoint, une donnée d'exploitation qui n'a pas de raison
d'être plus publique que `/api`. Configurez le job de scrape en conséquence :

```yaml
scrape_configs:
  - job_name: runner-fleet
    static_configs:
      - targets: ['runner-manager:8080']
    basic_auth:
      username: admin
      password: <BASIC_AUTH_PASSWORD>
```

### Version et logs

`GET /version` retourne la version et rien d'autre. Les détails de build en sont délibérément
absents : `go_version` permettrait à quiconque d'associer une CVE publiée du runtime Go à la
version exacte que vous exécutez. `runner-manager -version` affiche le commit, la date de build,
la version de Go et la plateforme — celle-là s'exécute sur l'hôte et n'est pas exposée.

`LOG_LEVEL` et `LOG_FORMAT` pilotent le log ; mettez `LOG_FORMAT=json` pour l'expédier vers ELK
ou Loki. L'autotest de démarrage écrit une ligne par élément, chacune préfixée `[preflight …]` —
ce préfixe est l'ancre de grep utilisée dans tout le [Dépannage](#dépannage).

[← Retour à l'accueil du projet](../../README.md)
