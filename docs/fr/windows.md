# Services Windows natifs

Plateformes cibles : Windows 11 / Windows Server 2022 x64, avec des runners de dépôt ou d’organisation GitHub.com. Le Manager et les runners utilisent les services Windows, sans WSL ni Docker.

## Installation et démarrage

Extrayez le ZIP Windows x64 et exécutez dans **PowerShell administrateur** :

```powershell
.\runner-manager.exe service install
.\runner-manager.exe service start
Start-Process http://127.0.0.1:8080
```

L’installation copie le programme dans `%ProgramFiles%\RunnerFleet\runner-manager.exe` et inscrit le service automatique `RunnerFleet` sous `LocalSystem`. Emplacements des données par défaut :

```text
%ProgramData%\RunnerFleet\config.yaml
%ProgramData%\RunnerFleet\runners\<name>\
%ProgramData%\RunnerFleet\tokens\
%ProgramData%\RunnerFleet\logs\manager-YYYY-MM-DD.log
```

Pour une configuration personnalisée, copiez `config.windows.yaml.example` dans un répertoire dédié et utilisez `service install -config "D:\RunnerFleet\config.yaml"`. Les chemins deviennent absolus ; `base_path` relatif est résolu depuis le répertoire de configuration. Les ACL réservent les répertoires de configuration, de base des runners et du programme au système et aux administrateurs : utilisez des répertoires dédiés. Les journaux restent dans ProgramData.

## Ajout depuis l’interface

Dans GitHub, ouvrez Settings → Actions → Runners → New self-hosted runner et choisissez Windows/x64. Collez `.\config.cmd --url https://github.com/owner/repo --token TOKEN` dans le Manager, analysez, vérifiez le nom et les étiquettes, puis ajoutez. Le texte collé est une donnée, jamais un script exécuté.

Le Manager télécharge le ZIP Windows officiel, vérifie son SHA256 officiel, extrait les fichiers et inscrit un service sans interaction sous `NetworkService`. Il lit le fichier officiel `.service` et vérifie le chemin du programme avant de contrôler le service.

Démarrer enregistre `desired_state: running` et active le démarrage et la récupération automatiques. Arrêter enregistre `stopped`, désactive ces mécanismes et attend l’arrêt ; cet état persiste après redémarrage. Redémarrer ou mettre à jour le Manager ne coupe pas les runners. Un état inconnu ne déclenche ni création ni redémarrage automatique.

Après un échec d’inscription, utilisez un nouveau jeton GitHub. Si seul le service a échoué, Démarrer le répare sans nouvelle inscription. La suppression arrête et désinstalle le service et son groupe de permissions avant les fichiers. Sans PAT utilisable, supprimez manuellement l’entrée GitHub restante selon le résultat affiché.

## Environnement de compilation et maintenance

Installez MSVC, Windows SDK, Git et les outils nécessaires pour `NetworkService`. Le profil personnel, les identifiants et les lecteurs réseau mappés ne sont pas partagés automatiquement. Le Manager utilise LocalSystem et les runners le compte moins privilégié NetworkService. Pour migrer un runner LocalSystem, attendez la fin du travail puis arrêtez et démarrez le runner. Tous les runners partagent NetworkService, sans isolation par compte. L’interface écoute localement par défaut ; configurez l’adresse et l’authentification avant un accès distant.

```powershell
.\runner-manager.exe service stop
.\runner-manager.exe service start
.\runner-manager.exe service uninstall
```

La désinstallation retire uniquement le service Manager, en conservant fichiers, configuration et services Runner. Pour mettre à jour, arrêtez le Manager, remplacez son programme installé et redémarrez-le. Les tâches continuent.

## Utiliser votre proxy Windows

Pendant `service install`, le Manager importe les variables de proxy ou, à défaut, le proxy système Windows manuel activé par l’utilisateur qui installe le service (Clash/v2ray, par exemple). Seuls les paramètres de proxy sont enregistrés dans `proxy.env`, à côté de `config.yaml`, protégé par les ACL du Manager. Le service charge cet instantané au démarrage, sans suivre les changements du bureau. Utilisez un terminal élevé sous le même utilisateur. Les scripts PAC et SOCKS seul ne sont pas importés ; choisissez le port HTTP/mixte ou modifiez `proxy.env`.

Pour une installation existante ou un changement de port, exécutez les commandes suivantes dans PowerShell administrateur, puis arrêtez et démarrez les runners concernés après leurs travaux. Pour une configuration personnalisée, indiquez le même chemin `-config` qu’à l’installation :

```powershell
.\runner-manager.exe service proxy-import
.\runner-manager.exe service stop
.\runner-manager.exe service start
```

Les nouveaux runners reçoivent le proxy avant inscription ; les runners existants le reçoivent au démarrage. Aucun travail actif n’est redémarré automatiquement. L’application proxy doit fonctionner et son port local être accessible à NetworkService, y compris après redémarrage de Windows. Une ancienne installation LocalSystem sans `proxy.env` nécessite la commande d’importation ci-dessus.

Pour une modification propre à un runner, arrêtez-le puis modifiez `<base_path>\<runner>\.env` en ajoutant ces entrées hors du bloc `Runner Fleet managed proxy`, puis démarrez-le :

```text
http_proxy=http://127.0.0.1:7890
https_proxy=http://127.0.0.1:7890
no_proxy=localhost,127.0.0.1
```

Les entrées manuelles ont priorité et les autres paramètres `.env` sont conservés. Le fichier `proxy.env` du Manager utilise le même format ; redémarrez le Manager après modification. Les identifiants du proxy sont visibles par les travaux qui l’utilisent. Cette fonction n’installe pas de service proxy, ne transmet pas l’authentification personnelle et ne remplace pas la configuration propre aux outils de compilation.

## Validation sous Windows

La CI Windows est configurée pour vérifier la compilation, les erreurs de cycle de vie simulées, deux services SCM natifs et l’installation/santé du Manager. Les vrais identifiants GitHub et le redémarrage doivent être validés sur Windows :

- Inscrire deux runners avec des jetons valides, vérifier leur état en ligne sur GitHub et lancer une compilation sur chacun.
- Arrêter un runner, vérifier que l’autre continue, attendre la réconciliation puis redémarrer Windows et vérifier que le premier reste arrêté.
- Redémarrer le Manager pendant une tâche longue et vérifier sa continuité.
- Vérifier la récupération après panne et la réparation du service sans inscription GitHub en double.
- Supprimer un runner et vérifier le nettoyage du service, du groupe, du répertoire et le résultat de désinscription GitHub.

## Vérifications de développement

Le backend est compilé uniquement dans les programmes Windows ; la couverture Linux existante mesure le code de production Linux. Pour tester l’installateur et le contrôleur simulé sur Linux/macOS :

```bash
go test -race -tags windowsrunner_portable ./internal/windowsrunner
node --test scripts/test-command-parser.cjs
```

Exécutez les tests SCM natifs dans une session Windows administrateur :

```powershell
$env:RUNNER_FLEET_SCM_TESTS = "1"
go test -race ./internal/windowsrunner
```
