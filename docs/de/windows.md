# Native Windows-Dienste

Zielplattformen: Windows 11 / Windows Server 2022 x64 mit Repository- und Organisations-Runnern auf GitHub.com. Manager und Runner nutzen Windows-Dienste; WSL und Docker sind nicht erforderlich.

## Installation und Start

Das Windows-x64-ZIP entpacken und in **PowerShell als Administrator** ausführen:

```powershell
.\runner-manager.exe service install
.\runner-manager.exe service start
Start-Process http://127.0.0.1:8080
```

Die Installation kopiert das Programm nach `%ProgramFiles%\RunnerFleet\runner-manager.exe` und registriert den automatisch startenden Dienst `RunnerFleet` als `LocalSystem`. Standard-Datenpfade:

```text
%ProgramData%\RunnerFleet\config.yaml
%ProgramData%\RunnerFleet\runners\<name>\
%ProgramData%\RunnerFleet\tokens\
%ProgramData%\RunnerFleet\logs\manager-YYYY-MM-DD.log
```

Für eine eigene Konfiguration `config.windows.yaml.example` in ein eigenes Verzeichnis kopieren und `service install -config "D:\RunnerFleet\config.yaml"` ausführen. Pfade werden absolut gespeichert; ein relativer `base_path` gilt relativ zur Konfigurationsdatei. ACLs beschränken Konfigurations-, Runner-Basis- und Programmverzeichnis auf System und Administratoren. Daher eigene Verzeichnisse verwenden. Protokolle bleiben unter ProgramData.

## Runner über die Oberfläche hinzufügen

Auf GitHub Settings → Actions → Runners → New self-hosted runner öffnen und Windows/x64 wählen. `.\config.cmd --url https://github.com/owner/repo --token TOKEN` im Manager einfügen, auslesen, Name und Labels prüfen und hinzufügen. Eingefügter Text wird als Daten verarbeitet und niemals als Skript ausgeführt.

Der Manager lädt das offizielle Windows-ZIP, prüft den offiziellen SHA256, entpackt es und registriert den Dienst unbeaufsichtigt unter `NT AUTHORITY\NETWORK SERVICE`. Vor Dienstaktionen prüft er die offizielle `.service`-Datei und den Programmpfad.

Start speichert `desired_state: running` und aktiviert automatischen Start und Wiederherstellung. Stopp speichert `stopped`, deaktiviert beide und wartet auf das Ende; der Zustand bleibt nach einem Neustart erhalten. Manager-Neustarts oder Updates stoppen keine Runner. Unbekannter Status löst keine automatische Erstellung oder Neustarts aus.

Nach fehlgeschlagener Registrierung ein neues GitHub-Token verwenden. Ist nur die Diensteinrichtung fehlgeschlagen, repariert Start den Dienst ohne erneute Registrierung. Beim Entfernen werden Dienst und zugehörige Berechtigungsgruppe vor den Dateien entfernt. Ohne nutzbares PAT den verbliebenen GitHub-Eintrag entsprechend der Meldung manuell löschen.

## Build-Umgebung und Wartung

MSVC, Windows SDK, Git und weitere Werkzeuge für `NetworkService` vorinstallieren. Persönliches Profil, Anmeldedaten und zugeordnete Netzlaufwerke werden nicht automatisch geteilt. Alle Runner verwenden dasselbe Konto, ohne Kontentrennung pro Instanz. Die Oberfläche bindet standardmäßig an Loopback; vor Fernzugriff Adresse und Authentifizierung konfigurieren.

```powershell
.\runner-manager.exe service stop
.\runner-manager.exe service start
.\runner-manager.exe service uninstall
```

Die Deinstallation entfernt nur den Manager-Dienst und erhält Dateien, Konfiguration und Runner-Dienste. Für ein Update den Manager stoppen, seine installierte Programmdatei ersetzen und neu starten. Runner-Aufträge laufen weiter.

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
