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

Le Manager télécharge le ZIP Windows officiel, vérifie son SHA256 officiel, extrait les fichiers et inscrit un service sans interaction sous `NT AUTHORITY\NETWORK SERVICE`. Il lit le fichier officiel `.service` et vérifie le chemin du programme avant de contrôler le service.

Démarrer enregistre `desired_state: running` et active le démarrage et la récupération automatiques. Arrêter enregistre `stopped`, désactive ces mécanismes et attend l’arrêt ; cet état persiste après redémarrage. Redémarrer ou mettre à jour le Manager ne coupe pas les runners. Un état inconnu ne déclenche ni création ni redémarrage automatique.

Après un échec d’inscription, utilisez un nouveau jeton GitHub. Si seul le service a échoué, Démarrer le répare sans nouvelle inscription. La suppression arrête et désinstalle le service et son groupe de permissions avant les fichiers. Sans PAT utilisable, supprimez manuellement l’entrée GitHub restante selon le résultat affiché.

## Environnement de compilation et maintenance

Installez MSVC, Windows SDK, Git et les outils nécessaires pour `NetworkService`. Le profil personnel, les identifiants et les lecteurs réseau mappés ne sont pas partagés automatiquement. Tous les runners utilisent ce compte, sans isolation par compte. L’interface écoute localement par défaut ; configurez l’adresse et l’authentification avant un accès distant.

```powershell
.\runner-manager.exe service stop
.\runner-manager.exe service start
.\runner-manager.exe service uninstall
```

La désinstallation retire uniquement le service Manager, en conservant fichiers, configuration et services Runner. Pour mettre à jour, arrêtez le Manager, remplacez son programme installé et redémarrez-le. Les tâches continuent.

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
