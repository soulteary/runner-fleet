# Native Windows-Dienste

Zielplattformen: Windows 11 / Windows Server 2022 x64 mit Repository- und Organisations-Runnern auf GitHub.com. Manager und Runner nutzen Windows-Dienste; WSL und Docker sind nicht erforderlich.

## Installation und Start

Das Windows-x64-ZIP entpacken und in **PowerShell als Administrator** ausführen:

```powershell
.\runner-manager.exe service install
.\runner-manager.exe service start
Start-Process http://127.0.0.1:8080
```

Die Installation kopiert das Programm nach `%ProgramFiles%\RunnerFleet\runner-manager.exe` und registriert den automatisch startenden Dienst `RunnerFleet` als `NetworkService`. Standard-Datenpfade:

```text
%ProgramData%\RunnerFleet\config.yaml
%ProgramData%\RunnerFleet\runners\<name>\
%ProgramData%\RunnerFleet\tokens\
%ProgramData%\RunnerFleet\logs\manager-YYYY-MM-DD.log
```

Für eine eigene Konfiguration `config.windows.yaml.example` in ein eigenes Verzeichnis kopieren und `service install -config "D:\RunnerFleet\config.yaml"` ausführen. Pfade werden absolut gespeichert; ein relativer `base_path` gilt relativ zur Konfigurationsdatei. ACLs beschränken Konfigurations-, Runner-Basis- und Programmverzeichnis auf System und Administratoren. Daher eigene Verzeichnisse verwenden. Protokolle bleiben unter ProgramData.

## Runner über die Oberfläche hinzufügen

Auf GitHub Settings → Actions → Runners → New self-hosted runner öffnen und Windows/x64 wählen. `.\config.cmd --url https://github.com/owner/repo --token TOKEN` im Manager einfügen, auslesen, Name und Labels prüfen und hinzufügen. Eingefügter Text wird als Daten verarbeitet und niemals als Skript ausgeführt.

Der Manager lädt das offizielle Windows-ZIP, prüft den offiziellen SHA256, entpackt es und registriert den Dienst unbeaufsichtigt unter `NetworkService`. Vor Dienstaktionen prüft er die offizielle `.service`-Datei und den Programmpfad.

Start speichert `desired_state: running` und aktiviert automatischen Start und Wiederherstellung. Stopp speichert `stopped`, deaktiviert beide und wartet auf das Ende; der Zustand bleibt nach einem Neustart erhalten. Manager-Neustarts oder Updates stoppen keine Runner. Unbekannter Status löst keine automatische Erstellung oder Neustarts aus.

Nach fehlgeschlagener Registrierung ein neues GitHub-Token verwenden. Ist nur die Diensteinrichtung fehlgeschlagen, repariert Start den Dienst ohne erneute Registrierung. Beim Entfernen werden Dienst und zugehörige Berechtigungsgruppe vor den Dateien entfernt. Ohne nutzbares PAT den verbliebenen GitHub-Eintrag entsprechend der Meldung manuell löschen.

## Build-Umgebung und Wartung

MSVC, Windows SDK, Git und weitere Werkzeuge für `NetworkService` vorinstallieren. Persönliches Profil, Anmeldedaten und zugeordnete Netzlaufwerke werden nicht automatisch geteilt. Der Manager verwendet LocalSystem, die Runner das weniger privilegierte Konto NetworkService. Bei einem bisherigen LocalSystem-Runner den Job beenden lassen und danach stoppen und starten. Alle Runner teilen NetworkService ohne getrennte Konten. Die Oberfläche bindet standardmäßig an Loopback; vor Fernzugriff Adresse und Authentifizierung konfigurieren.

```powershell
.\runner-manager.exe service stop
.\runner-manager.exe service start
.\runner-manager.exe service uninstall
```

Die Deinstallation entfernt nur den Manager-Dienst und erhält Dateien, Konfiguration und Runner-Dienste. Für ein Update den Manager stoppen, seine installierte Programmdatei ersetzen und neu starten. Runner-Aufträge laufen weiter.

## Eigenen Windows-Proxy verwenden

Bei `service install` übernimmt der Manager Proxy-Umgebungsvariablen oder ansonsten den aktivierten manuellen Windows-Systemproxy des installierenden Benutzers, etwa Clash/v2ray. Nur Proxy-Einstellungen werden in `proxy.env` neben `config.yaml` gespeichert und durch die Manager-ACL geschützt. Der Dienst lädt diesen Stand beim Start; Desktop-Änderungen werden nicht laufend verfolgt. Ein erhöhtes Terminal desselben Benutzers verwenden. PAC-Skripte und reine SOCKS-Einstellungen werden nicht importiert; HTTP-/Mixed-Port verwenden oder `proxy.env` bearbeiten.

Bei vorhandener Installation oder geändertem Port folgende Befehle in Administrator-PowerShell ausführen und danach betroffene Runner nach Jobende in der Oberfläche stoppen und starten. Bei eigener Konfiguration denselben `-config`-Pfad wie bei der Installation angeben:

```powershell
.\runner-manager.exe service proxy-import
.\runner-manager.exe service stop
.\runner-manager.exe service start
```

Neue Runner erhalten den Proxy vor der Registrierung, vorhandene beim Start. Laufende Jobs werden nie automatisch neu gestartet. Die Proxy-Anwendung muss laufen und ihr lokaler Port für NetworkService erreichbar sein, auch nach einem Windows-Neustart. Eine bisherige LocalSystem-Installation ohne `proxy.env` benötigt den obigen Importbefehl.

Für eine manuelle Runner-Einstellung den Runner stoppen, `<base_path>\<runner>\.env` bearbeiten, folgende Einträge außerhalb des Blocks `Runner Fleet managed proxy` ergänzen und starten:

```text
http_proxy=http://127.0.0.1:7890
https_proxy=http://127.0.0.1:7890
no_proxy=localhost,127.0.0.1
```

Manuelle Einträge haben Vorrang; andere `.env`-Einstellungen bleiben erhalten. Die `proxy.env` des Managers verwendet dasselbe Format; nach Änderung den Manager neu starten. Proxy-Zugangsdaten sind für Jobs sichtbar, die den Proxy verwenden. Die Funktion installiert keinen Proxy-Dienst, überträgt keine persönliche Anmeldung und ersetzt keine Proxy-Konfiguration einzelner Build-Werkzeuge.

## Abnahme unter Windows

Windows-CI prüft laut Konfiguration Builds, simulierte Lebenszyklusfehler, zwei native SCM-Dienste und Installation/Healthcheck des Managers. Echte GitHub-Zugangsdaten und Neustarts sind auf einem Windows-Zielsystem zu prüfen:

- Zwei Runner mit gültigen Tokens registrieren, den Online-Status auf GitHub prüfen und auf beiden einen echten Build ausführen.
- Einen Runner stoppen, den anderen weiterlaufen lassen, die Abgleichprüfung abwarten und nach Windows-Neustart den gestoppten Zustand prüfen.
- Den Manager während eines langen Auftrags neu starten und prüfen, dass der Auftrag weiterläuft.
- Wiederherstellung nach Absturz und Dienstreparatur ohne doppelte GitHub-Registrierung prüfen.
- Einen Runner entfernen und Dienst-, Gruppen-, Verzeichnisbereinigung sowie GitHub-Abmeldung prüfen.

## Entwicklungsprüfungen

Das Backend wird nur in Windows-Programme eingebaut; die bestehenden Linux-Abdeckungstests messen Linux-Produktionscode. Installationsprogramm und simulierten Controller unter Linux/macOS testen:

```bash
go test -race -tags windowsrunner_portable ./internal/windowsrunner
node --test scripts/test-command-parser.cjs
```

Native SCM-Tests in einer Windows-Administratorsitzung ausführen:

```powershell
$env:RUNNER_FLEET_SCM_TESTS = "1"
go test -race ./internal/windowsrunner
```
