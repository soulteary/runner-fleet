# Développement et build

**文档 / Docs:** [EN](../development.md) · [中文](../zh/development.md) · Français · [Deutsch](../de/development.md) · [한국어](../ko/development.md) · [日本語](../ja/development.md)

![](../../.github/assets/fleet.jpg)

En production, utilisez le déploiement conteneur ; voir [Guide d'utilisation](guide.md). Ce document est pour les contributeurs : build et débogage locaux.

## Prérequis

- Go 1.26 (en cohérence avec [go.mod](../../go.mod)).

## Build

```bash
# Construire le binaire runner-manager
go build -o runner-manager ./cmd/runner-manager

# Avec version (pour /version et débogage)
go build -ldflags "-X main.Version=1.6.0" -o runner-manager ./cmd/runner-manager

# Construire uniquement le Runner Agent (mode conteneur)
go build -o runner-agent ./cmd/runner-agent

# Ou Make : make build / make build-agent / make build-all
```

Les templates sont intégrés dans le binaire Manager (`cmd/runner-manager/templates/`) ; binaire unique, pas besoin de fournir `templates/`.

## Exécution et débogage locaux

```bash
mkdir -p config && cp config.yaml.example config/config.yaml
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
| `/api/runners` | POST | Ajoute un runner (installation et enregistrement optionnels). En cas de conflit de nom, renvoie **409** avec `conflicts` et `suggested_name` au lieu de renommer silencieusement ; envoyez `auto_rename: true` pour l'ancien comportement. |
| `/api/runners/:name/recreate` | POST | Supprime et recrée le conteneur du runner avec la configuration actuelle (mode conteneur uniquement). Interrompt un job en cours ; un conteneur arrêté est de toute façon recréé automatiquement au démarrage si ses paramètres de création ont divergé. |
| `/api/runner-precheck` | GET | Pré-vérification d'un nom avant l'ajout : `?name=&path=`. Renvoie `available`, un `suggested_name` et les `conflicts` détectés (`name_taken`, `container_name`, `install_dir`, `dir_registered`, `dir_adopt`, `dir_exists`, `container_exists`), chacun avec `level` (`error`/`warn`), `message`, `detail` et un `fix_command` optionnel. En lecture seule ; l'interface l'appelle pendant la saisie. |

### Requêtes intersites (CSRF)

Les endpoints d'écriture (`POST`, `PUT`, `DELETE`) rejettent les requêtes que le navigateur
signale comme intersites. Sans cela, une page servie par n'importe quelle autre origine pouvait
soumettre un formulaire vers `POST /api/runners` : il s'agit d'une « requête simple » au sens
CORS, donc envoyée sans préflight, et le navigateur y joint les identifiants Basic Auth qu'il a
mis en cache pour cette origine. Ouvrir une page malveillante suffisait donc à ajouter ou
arrêter un runner. `PUT` et `DELETE` déclenchent toujours un préflight : ce n'était pas la
partie exposée. C'était `POST`, et `/api/runners/:name/{start,stop,recreate}` sont tous en POST.

Le contrôle lit d'abord `Sec-Fetch-Site` : le navigateur le calcule localement, un reverse
proxy qui réécrit `Host` ne peut donc pas le casser (Chrome 76+, Firefox 90+, Safari 16.4+).
Seul `same-origin` passe ; `same-site` non, car un sous-domaine ou un autre port est une autre
origine et il s'agit ici d'une interface d'administration. Les navigateurs trop anciens pour
l'envoyer retombent sur la comparaison entre `Origin` et le `Host` de la requête — tous les
navigateurs actuels envoient `Origin` sur un POST intersite.

Une requête sans **aucun** de ces deux en-têtes ne vient pas d'un navigateur (curl, scripts de
CI). Elle ne détient ni cookie ni Basic Auth en cache, elle ne peut donc pas constituer une
attaque CSRF, et elle passe — l'API reste scriptable, et aucun jeton ni en-tête supplémentaire
n'est requis nulle part.

Deux conséquences :

- **Les corps de requête sont exclusivement en JSON.** `AddRunnerRequest` et
  `UpdateRunnerRequest` ne portent aucun tag `form` : une requête encodée en formulaire se lie
  donc à une structure vide et échoue à la validation, même si elle franchissait le middleware.
  L'interface web poste déjà du JSON — son usage de `FormData` sert uniquement à lire les
  champs de l'élément de formulaire.
- **`TRUSTED_ORIGINS` est la porte de sortie.** Si un reverse proxy réécrit `Host` au point que
  vos propres requêtes sont refusées, listez-y les origines telles que le navigateur les voit,
  séparées par des virgules (`https://ci.example.com`). Elles sont autorisées quoi que dise
  `Sec-Fetch-Site` : n'y mettez que des origines que vous maîtrisez.

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

### Comment l'état d'exécution est déterminé

`internal/runnerproc` répond à « le processus de ce runner est-il vivant ? » en parcourant
`/proc` à la recherche d'un processus dont l'argv revendique ce répertoire d'installation —
`<dir>/bin/Runner.Listener`, ou un shell exécutant `<dir>/run.sh` ou `<dir>/run-helper.sh`.

Il ne lit délibérément **pas** de fichier pid, parce qu'actions/runner n'en écrit aucun. Ni
`run.sh`, ni `run-helper.sh.template`, ni `runsvc.sh` n'écrivent de pid où que ce soit ; le pid
ne vit que dans une variable de shell. Le fichier `.path` d'un répertoire d'installation
contient une chaîne PATH, pas un pid (`runsvc.sh` fait lui-même `export PATH=$(cat .path)`).
Lire l'un ou l'autre de ces noms échouait donc systématiquement, ce qui faisait rapporter
« enregistré mais non démarré » à chaque runner, indéfiniment.

Le shell superviseur compte comme en cours d'exécution même quand `Runner.Listener` a
momentanément disparu : `run-helper.sh` dort 5 secondes sur un code de sortie 2, puis `run.sh`
relance le listener. Un test portant uniquement sur le listener déclarerait donc le runner mort
à chaque redémarrage.

Deux conséquences pour les appelants :

- `runner.List` est une vue au niveau du disque. En mode conteneur, son `Running` vaut toujours
  false — le Manager et les runners sont dans des espaces de noms PID différents. Utilisez
  `runner.ListWithLiveStatus`, qui interroge l'Agent de chaque conteneur, dès que la réponse
  déclenche une action. Elle transforme un échec de sonde en `status=unknown` plutôt qu'en
  `installed`, de sorte que « enregistré mais pas démarré, donc on le démarre » ne peut pas se
  déclencher sur un runner qu'elle n'a pas pu joindre.
- La détection nécessite `/proc` : elle est donc réservée à Linux ; ailleurs, elle rapporte
  « non démarré ».

### Permissions du répertoire d'un runner

Le répertoire d'installation d'un runner est créé en 0700. `config.sh` y écrit
`.credentials_rsaparams` — la clé privée RSA avec laquelle le runner s'authentifie auprès de
GitHub — et actions/runner ne pose aucune permission Unix dessus (`ConfigurationStore` ne pose
que l'attribut Hidden de Windows, le fichier suit donc l'umask, en général 0644). Le mode du
répertoire est par conséquent la seule chose qui sépare cette clé des autres utilisateurs
locaux de la machine.

`MkdirAll` ne touche pas au mode d'un répertoire existant : ceux créés par les versions
antérieures restent en 0755. `Preflight` les signale, avec un `chmod 700` prêt à exécuter,
plutôt que de les modifier lui-même : resserrer un répertoire alors que le Manager et le
conteneur tournent sous des UID différents casserait un déploiement qui fonctionne, et cette
décision revient à une personne.

`base_path` lui-même reste traversable — il ne contient aucun identifiant, et c'est le point de
montage hôte en mode conteneur.

Cela ne change pas ce qu'un job peut atteindre. Avec `job_docker_backend: host-socket`, un job
peut monter n'importe quel chemin de l'hôte, ce dont la documentation de déploiement avertit
déjà ; le mode protège des autres utilisateurs locaux, pas de cela.

### Suppression d'un runner et GitHub

`DELETE /api/runners/:name` retire un runner de cet outil **et** de GitHub. Le côté GitHub exige
un identifiant, et le seul dont cet outil dispose jamais est le PAT facultatif propre à chaque
runner, dans `<répertoire du runner>/.github_check_token` — le même fichier que celui utilisé
par la vérification de visibilité. Le désenregistrement s'exécute donc *avant* la suppression du
répertoire d'installation, puisque c'est là que vit le jeton ; ignorer cet ordre revient à
perdre silencieusement la possibilité de le faire.

Sans PAT, le runner ne peut pas être désenregistré : GitHub veut un PAT ou un removal token
frais, et `config.sh remove` veut ce dernier. La réponse le dit alors explicitement et indique
où le supprimer à la main. Mieux vaut le dire que l'avaler — un runner laissé derrière fait
échouer le prochain `config.sh --name <même nom>` sur `A runner exists with the same name`.

`registered_on_github` est un booléen **nullable** : `true` / `false` sont des réponses, `null`
signifie qu'aucune réponse n'a été obtenue, et `github_check_error` en porte la raison. Les
templates ne doivent pas le tester avec `{{if .RegisteredOnGitHub}}` — `html/template` juge un
pointeur à sa seule nullité, un pointeur vers `false` est donc vrai. Utilisez les helpers
`GitHubYes` / `GitHubNo` / `GitHubUnknown` de `RunnerInfo`.

## Cibles Makefile

- `make help` : Lister toutes les cibles.
- `make build` : Build du Manager (avec ldflags Version).
- `make build-agent` : Build du Runner Agent (mode conteneur).
- `make build-all` : Build du Manager et de l'Agent.
- `make test` : Lancer les tests.
- `make test-race` : Lancer les tests avec le détecteur de data races (ce que fait la CI).
- `make lint` : Lancer golangci-lint sur `./...` — la même étape que le Lint du job Test en CI.
- `make check` : Tout ce que la CI vérifie, en une cible — gofmt, vet, lint, tests `-race` et les deux scripts de cohérence. À lancer avant de pousser.
- `make run` : Build puis exécution du Manager.
- `make docker-build` / `make docker-run` / `make docker-stop` : Build et exécution de l'image Manager ; voir [Guide d'utilisation](guide.md).
- `make docker-build-runner` : Build de l'image Runner pour le mode conteneur (`Dockerfile.runner`, tag par défaut dans `RUNNER_IMAGE`).
- `make docker-build-runner-example` : Build d'un exemple d'image Runner personnalisée (`EXAMPLE=android|node`, voir [`examples/runner-images/`](../../examples/runner-images/)).
- `make clean` : Supprimer les binaires construits (runner-manager, runner-agent).

Le mode conteneur utilise l'Agent de `cmd/runner-agent` et l'image Runner de `Dockerfile.runner`.

## Tests

`go test ./...`, ou `make test-race` pour exécuter ce que fait réellement la CI. Tous les
workflows de CI et de release lancent `go test -race`, de sorte qu'une data race fait échouer
le build au lieu de resurgir plus tard sous la forme d'un état erroné occasionnel en
production — la concurrence de ce projet (file d'enregistrement à worker unique, verrou
`runnerOps` par runner, création à vainqueur unique dans `EnsureAgentToken`) repose sur des
conventions que le système de types n'impose pas. Le golangci-lint du dépôt tourne en CI ;
`errcheck` et `staticcheck` via `go run` couvrent l'essentiel de ce qu'il signale si vous ne
pouvez pas l'exécuter localement.

Quelques conventions à connaître avant d'enrichir la suite :

- **La détection de processus est éprouvée contre de vrais processus**, pas seulement contre un
  faux `/proc` (`internal/runnerproc`, `internal/runner`, `cmd/runner-agent`). Un `run.sh`
  jetable qui bloque tient lieu de runner ; il ne doit pas faire `exec`, sinon le shell est
  remplacé et l'argv ne correspond plus à ce que cherche `internal/runnerproc`.
- **Les tests de `cmd/runner-manager` atteignent le câblage via les fonctions qu'appelle
  `main()`** (`basicAuthMiddleware`, `httpErrorHandler`, `registerRoutes`, `listenAddr`,
  `loadI18n`). Ajouter une route implique de mettre à jour
  `TestRegisterRoutes_AllEndpointsPresent`, qui affirme l'ensemble exact — l'occasion de se
  demander si la nouvelle route doit être authentifiée.
- **Les middlewares se testent à travers `newEchoServer()`, pas seulement isolément.** Une
  protection correcte mais jamais montée est le mode de défaillance typique de ce genre de
  garde : `TestCSRFGuardIsMountedOnWriteRoutes` passe donc par la vraie table de routage.
  Ajouter une route d'écriture implique de l'ajouter à `writeRoutes` là-bas.
- **Les fichiers i18n sont vérifiés les uns par rapport aux autres.** Une clé présente dans
  `en.json` et absente ailleurs s'affiche comme un blanc, silencieusement : les tests affirment
  que les ensembles de clés coïncident dans les six langues, qu'aucune valeur n'est vide et que
  chaque clé référencée par le template existe.
- **Un défaut de template appelle un test au niveau du template.** Deux bugs passés vivaient
  dans `index.html` et non en Go — un `{{if}}` sur un `*bool` qui lisait un pointeur vers
  `false` comme vrai, et une affectation à `innerHTML` qui sautait `escapeHtml`. Les deux sont
  couverts en rendant le vrai template ou en le parcourant, car un test du seul helper Go
  n'aurait rien remarqué.


## Publication

Les références de version dans la doc et les exemples doivent correspondre au tag d'image par défaut dans `internal/config/config.go`. La CI le vérifie via `scripts/check-version-consistency.sh` ; exécutez-le localement avant d'ouvrir une PR de release :

```bash
sh scripts/check-version-consistency.sh
```

Si une ligne cite légitimement une ancienne version (notes de release, instructions de mise à niveau), ajoutez le marqueur `version-check-ignore` sur cette ligne pour l'ignorer.

Les traductions sont vérifiées de la même façon. `scripts/check-docs-structure.sh` compare la
séquence des niveaux de titre de chaque `docs/<lang>/*.md` à son original anglais — le texte des
titres est censé différer, la structure non — de sorte qu'une section ajoutée en anglais et
oubliée dans les cinq traductions fait échouer la PR au lieu de passer inaperçue :

```bash
sh scripts/check-docs-structure.sh
```

[← Retour à la doc](README.md)
