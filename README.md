# Zentrale

Zentrale verwaltet autonome Agenten auf SSH-Fernmaschinen. Das Go-Backend hält Konfiguration, Anmeldung, Agentenkontexte und Ereignisse in DuckDB 2.0. Projektdateien, Python-Umgebungen und Prozesse liegen auf den Fernmaschinen. Das Frontend verwendet React 19, TypeScript, Vite, Tailwind 4 und HeroUI 3.

## Starten

Benötigt werden Go 1.27.3 mit CGO und C/C++-Toolchain sowie Node.js ab 22.12.

Die SSH-Ausführungstests benötigen zusätzlich lokales Python 3. Wenn fish installiert ist, prüfen sie auch diese Login-Shell. Die Anwendung selbst führt alle Shell- und Python-Befehle ausschließlich über SSH auf der Fernmaschine aus.

```sh
cd frontend
npm ci
cd ..
make run
```

Die Anwendung ist anschließend unter `http://127.0.0.1:8080` erreichbar. Beim ersten Aufruf werden das Anmeldepasswort und die globale Git-Commit-Identität (Name und E-Mail) gemeinsam eingerichtet. Alternativ übernimmt `LOGIN_ARGON2ID` einen bestehenden Argon2id-Hash im PHC-Format, bevor der HTTP-Server startet. Ein bereits gespeichertes Passwort wird dabei nicht überschrieben; die Commit-Identität kann dann unter **Einstellungen** ergänzt werden.

Für einen separaten Entwicklungsserver:

```sh
make dev
# In einem zweiten Terminal:
cd frontend
npm run dev
```

Vite leitet GraphQL, WebSocket-Ereignisse und die IDE an das Backend weiter. Die TypeScript-Typen werden mit `make generate` aus dem Go-GraphQL-Schema erzeugt.

Die noch unveröffentlichte Anwendung verwendet ein einziges aktuelles Datenbankschema in `backend/datastore/schema.go`, ohne historische Migrationen oder Downgrades. Eine neue Datenbank wird transaktional angelegt; eine bereits passende Datenbank bleibt erhalten. Ein unpassendes Entwicklungsschema wird mit einer Fehlermeldung abgelehnt und nicht automatisch umgebaut oder gelöscht. Pool-YAML verwendet ausschließlich die aktuellen Feldnamen; alte Aliasse wie `stable_directory` und das ungenutzte `ide_port` sind entfernt.

Verbindungsabbrüche erscheinen einmal zentral als HeroUI-Benachrichtigung. Geladene Inhalte und Formulareingaben bleiben erhalten. Die App prüft die Erreichbarkeit mit zunehmenden Abständen und aktualisiert nach Wiederherstellung die Daten; fehlgeschlagene Schreibaktionen werden nicht automatisch wiederholt. **Jetzt verbinden** prüft sofort erneut. Benachrichtigungen lassen sich mit **Alt+T** per Tastatur erreichen und schließen.

Für den Betrieb hinter einem HTTPS-Reverse-Proxy:

```sh
./backend/zentrale \
  -addr 127.0.0.1:8080 \
  -db /var/lib/zentrale/zentrale.duckdb \
  -frontend frontend/dist \
  -origin https://zentrale.example \
  -secure-cookie
```

Das Datenbankverzeichnis muss dem Dienstbenutzer gehören. Datenbank und WAL enthalten SSH-Schlüssel und Provider-Zugangsdaten. Der Dienst verwendet eine private Dateimaske und setzt die Datenbankberechtigungen auf `0600`.

## GitHub, GitLab und Repository-Schlüssel

Unter **Einstellungen → Git-Verbindungen** können GitHub und GitLab per Device-Login verbunden werden. Die Anmeldung lässt sich auch direkt in der Repo-Auswahl eines Projektformulars starten; eingegebene Projektdaten bleiben dabei erhalten. Einmalig ist eine eigene OAuth-App erforderlich:

- **GitHub:** OAuth-App unter den Entwicklereinstellungen registrieren, **Device Flow** aktivieren und ihre Client-ID hinterlegen. Verwendete Berechtigungen: `repo read:org`.
- **GitLab:** eine OAuth-Anwendung mit `api`-Scope registrieren und Client-ID sowie bei vertraulichen Anwendungen das Client-Secret hinterlegen. GitLab ab 17.9 unterstützt den Device-Flow regulär; eine eigene GitLab-Instanz kann über ihre HTTPS-Basisadresse verbunden werden.

Die App-Daten lassen sich in der Oberfläche oder über `GITHUB_CLIENT_ID`, `GITLAB_CLIENT_ID`, optional `GITHUB_CLIENT_SECRET` / `GITLAB_CLIENT_SECRET` und `GITLAB_BASE_URL` vorgeben. Gerätecodes, Zugangstokens und erneuerte Refresh-Tokens bleiben im Backend. Die Anmeldeseite öffnet in einem separaten Tab; die Zentrale erkennt die Bestätigung automatisch. Pro Dienst ist ein Konto verbunden. Details zu den Protokollen: [GitHub Device Flow](https://docs.github.com/en/apps/oauth-apps/building-oauth-apps/authorizing-oauth-apps#device-flow), [GitLab OAuth Device Flow](https://docs.gitlab.com/api/oauth2/#device-authorization-grant-flow).

Bei **Projekt anlegen / bearbeiten** und **Quellen verwalten → Repository verbinden** können vorhandene Repositories ausgewählt oder neue angelegt werden, auch in einer Organisation beziehungsweise Gruppe. Die Auswahl zeigt Repositories mit Berechtigung zur Verwaltung ihrer Deploy-Keys; Organisationsrichtlinien gelten weiterhin. Neue Repositories sind zunächst privat. Das primäre Projekt-Repository bezieht sich auf das Projektverzeichnis selbst, zusätzliche Quellen auf eigene Unterverzeichnisse. Dateiinhalte werden beim Speichern der Auswahl nicht überschrieben; Agenten verwenden `clone_source` und `source_git` für die eigentlichen Git-Operationen.

Die Zentrale erzeugt einen eigenen SSH-Schlüssel pro Repository und registriert ihn als **Read/Write-Deploy-Key**. Die stabile Repository-Kennung verbindet mehrere Projekte mit demselben Schlüssel. Ein bereits lokal vorhandener, beim Anbieter registrierter Repo-Schlüssel wird übernommen, sofern er ausschließlich dieses Repository bedient. Eine unterbrochene Registrierung bleibt als ausstehende Verwendung gespeichert; erneutes Verbinden sucht den vorhandenen Key, statt einen weiteren anzulegen. Die SSH-Hostprüfung bleibt aktiv; öffentliche Host-Schlüssel lassen sich wie bei anderen SSH-Verbindungen unter **Schlüssel → Vertrauenswürdige Hosts** hinterlegen.

**Schlüssel** zeigt alle Maschinen-, Pool- und Repo-Verwendungen. Verwendete Schlüssel sind gegen Löschen geschützt, auch im Backend. Ein Pool besitzt eine eigene automatisch verwaltete SSH-Identität, die seine Mitglieder gemeinsam nutzen. Der Pool-Schlüssel kann während seiner Verwendung keinem anderen Pool, keiner Einzelmaschine und keinem Repository zugewiesen werden. Das Entfernen einer Projektquelle gibt ihre Verwendung frei, widerruft jedoch keinen von weiteren Projekten genutzten Deploy-Key. Nach dem Entfernen aller Projektverwendungen kann **Repo-Zugriff widerrufen und Schlüssel löschen** den entfernten Deploy-Key und anschließend den lokalen Schlüssel entfernen. Dafür muss das ursprüngliche Git-Konto verbunden sein.

## Commit-Identität

Unter **Einstellungen → Standard-Identität für Commits** lassen sich Name und E-Mail ändern. Agentenbefehle verwenden diese Werte für Autor und Committer, auch bei direkten Git-Aufrufen über Shell oder Python. In **Projekt bearbeiten** können beide Werte gemeinsam überschrieben werden; zwei leere Felder übernehmen wieder den globalen Standard. Bestehende Installationen ohne globale Identität verwenden zunächst die vorhandene Remote-Git-Konfiguration. Die Anwendung ändert keine benutzerweite Git-Konfiguration auf der SSH-Maschine. Eine bereits offene VS-Code-Verbindung muss nach einer Identitätsänderung neu geöffnet werden, damit ihre Prozesse die neuen Werte übernehmen.

## Provider und Modelle

Ein Provider beschreibt einen Zugang, kein festgelegtes Modell. Nach dem Verbinden wird dessen Modellkatalog abgefragt. Unter **Einstellungen → Globale Modell-Defaults** werden Hauptmodell und Auto-Approver unabhängig ausgewählt. Neue Agenten übernehmen diese Auswahl; bestehende Unterhaltungen behalten ihre Modelle.

| Provider | Zugänge |
| --- | --- |
| OpenAI | API-Schlüssel oder ChatGPT Device-Login mit Token-Erneuerung |
| OpenRouter | API-Schlüssel oder Browser-Anmeldung mit PKCE |
| Anthropic | API-Schlüssel |
| OpenCode | API-Schlüssel |
| Mistral | API-Schlüssel |
| LiteLLM | Konfigurierbarer API-Endpunkt, optional mit Schlüssel |
| Google / Gemini | API-Schlüssel |

Für den OpenAI Device-Login muss die Geräteanmeldung in den ChatGPT-Sicherheitseinstellungen beziehungsweise den Workspace-Berechtigungen aktiviert sein. Die nativen Anmeldungen verwenden die [OpenAI-Device-Anmeldung](https://developers.openai.com/codex/auth) beziehungsweise [OpenRouter PKCE](https://openrouter.ai/docs/guides/overview/auth/oauth). Es werden keine lokal vorhandenen CLI-Sitzungen importiert. Andere Anbieter erhalten keine erfundene Abo-Anmeldung über ein Gateway.

Modellverfügbarkeit stammt aus dem verbundenen Konto. Modelllisten lassen sich durchsuchen und aktualisieren. Fehlende Kontextgrößen werden zunächst konservativ mit 32.000 Tokens angesetzt und als Schätzung markiert. Kontextgröße und Tokenpreise lassen sich unter **Modelle ansehen → ⋯ am Modell → Metadaten ergänzen** korrigieren; diese Angaben überleben Katalogaktualisierungen. Preise gelten in USD pro Million Tokens. Verbrauchsbasierte Kosten und API-Kostenäquivalente für Festpreiszugänge werden getrennt ausgewiesen. Unbekannte Preise bleiben ausdrücklich unbekannt.

## Fernmaschinen und Pools

Zuerst einen SSH-Schlüssel erzeugen, dann eine Maschine mit Host, SSH-Benutzer und Schlüssel anlegen. Die Fernmaschine benötigt eine POSIX-Shell und Python 3; für Quellen zusätzlich Git. **Verbindung prüfen** zeigt die erfolgreiche SSH-Anmeldung direkt an der Maschine. Bei einem unbekannten Host-Schlüssel erscheinen Host und Fingerprint mit **Schlüssel vertrauen und erneut verbinden**. Vertrauen wird ausschließlich nach dieser ausdrücklichen Aktion vergeben und bleibt unter **Schlüssel → Vertrauenswürdige Hosts** verwaltbar. Jump-Hosts referenzieren konfigurierte Maschinen.

Maschinen- und Agentenansicht zeigen die tatsächlich ermittelte Sandbox, etwa **Automatisch → Bubblewrap**, **Automatisch → MicroVM** oder **Automatisch → Ohne Sandbox**. Die Auswahl wird bei der Verbindungsprüfung, der Agentenvorbereitung und regulären Werkzeugaufrufen gespeichert. Der Prüfzeitpunkt bleibt nach dem Neuladen sichtbar. Geänderte Maschinenkonfigurationen benötigen eine neue Prüfung; ein Fehler bei der Erkennung gilt nicht als Auswahl von „Ohne Sandbox“. Einzelne genehmigte Ausnahmen außerhalb der Sandbox überschreiben die automatische Auswahl nicht.

Jede Maschine hat ein **Persistentes Projektverzeichnis** (`workspaceRoot` in der API, `workspace_root` in YAML). Die Vorgabe `~/.local/share/zentrale/projects` bezieht sich auf das Home des SSH-Benutzers; absolute Pfade sind ebenfalls möglich. Neue Projekte brauchen keinen Pfad mehr. Beim ersten Agenten- oder Editorstart entsteht automatisch `project-<Projekt-ID>` unter diesem Basisverzeichnis. Agenten desselben Projekts und VS Code verwenden dieselbe Zuordnung. Umbenennen und spätere Änderungen des Maschinen-Defaults verschieben bereits erfolgreich angelegte Arbeitsbereiche nicht. Ein fehlgeschlagener erster Versuch kann nach Korrektur des Basisverzeichnisses wiederholt werden.

Ein explizit gewähltes Projektverzeichnis wird direkt verwendet. Verschiedene eigenständige Maschinen können unabhängige Arbeitsbereiche besitzen; zwischen ihnen werden keine Dateien automatisch synchronisiert.

Pool-Konfigurationen werden unter **Maschinen → Pools → YAML importieren / aktualisieren** im Monaco-Editor mit YAML-Hervorhebung, Zeilennummern, Suche und Zwei-Leerzeichen-Einrückung bearbeitet. Der Editor wird lokal von der Zentrale ausgeliefert und erst beim Öffnen geladen. Eingaben bleiben nach Validierungsfehlern erhalten.

Pool-Konfigurationen enthalten genau ein YAML-Dokument; unbekannte Felder sind Fehler:

```yaml
id: training
name: Training
max_machines: 1
shared_home: false
workspace_root: /mnt/projects
single_user: true
sandbox: auto
machines:
  - id: gpu-a
    name: GPU A
    host: gpu-a.example
    user: developer
    properties: [GPU, RTX3070]
  - id: gpu-b
    name: GPU B
    host: gpu-b.example
    user: developer
    properties: [GPU, RTX4090]
```

Ein Pool verwendet ein gemeinsames `workspace_root` für alle Mitglieder. Dieses Verzeichnis muss auf allen Mitgliedern dieselbe persistente Freigabe bezeichnen, etwa ein bereits eingebundenes NFS-Verzeichnis. Mit `shared_home: true` ist auch ein Pfad unter `~/` im gemeinsamen Home möglich. Eine gespeicherte Projektkennung prüft beim Wechsel des Mitglieds, ob der vorbereitete Arbeitsbereich dort vorhanden ist; eine fehlende Freigabe wird nicht durch ein neues leeres Verzeichnis ersetzt. Fehlende oder nicht beschreibbare Arbeitsbereiche stoppen den Agentenstart vor der Modellanfrage.

Schlüssel-IDs sind im Pool-YAML nicht zulässig, weder am Pool noch an seinen Mitgliedern. Beim ersten Import erzeugt die Zentrale einen eigenen SSH-Schlüssel und speichert ihn zusammen mit dem Pool in einer Transaktion. Weitere Imports derselben Pool-ID sowie hinzugefügte Mitglieder verwenden diesen Schlüssel erneut. Die Pool-Karte zeigt den Fingerprint und bietet den öffentlichen Schlüssel zum Kopieren an. Er gehört in `~/.ssh/authorized_keys` der Pool-Mitglieder; bei gemeinsamem Home genügt das einmal. Der private Schlüssel bleibt in der Zentrale.

Ein Pool hat genau eine verwaltete SSH-Identität. Schlüssel anderer Zwecke werden weder übernommen noch umgewidmet; unvollständige frühere Entwicklungskonfigurationen werden mit einer Fehlermeldung abgelehnt.

Öffentliche Hostschlüssel und SSH-Zertifizierungsstellen können optional im Pool-Feld `known_hosts: |` im normalen OpenSSH-Format stehen. Unterstützt werden Hostnamen und Portangaben, mehrere Schlüssel pro Host, Muster, gehashte Hostnamen sowie `@cert-authority` und `@revoked`. Ein vorhandener Block ist für Verbindungen zu Mitgliedern dieses Pools verbindlich; globale Vertrauensfreigaben umgehen ihn nicht. Zertifikate werden auf passende Hostnamen, Signatur, Gültigkeit und Widerruf geprüft. Änderungen gelten auch beim erneuten Verwenden einer SSH-Verbindung. Ohne Block bleibt die bisherige Host-Key-Freigabe bestehen.

Die [FAU-Konfiguration](fau-cip-pool.yaml) enthält die veröffentlichte FAU-CA. Damit sind passende Host-Zertifikate abgedeckt, ohne die 253 Hostschlüssel einzeln zu sammeln. Der Eintrag bleibt auf Verbindungen dieses Pools beschränkt, auch wenn seine Hostmuster weitere FAU-Dienste enthalten.

Das Limit zählt gleichzeitig verwendete Maschinen über alle Agenten hinweg. Hintergrundjobs behalten ihre Pool-Belegung. Mit `single_user` werden fremde angemeldete Benutzer vor der Nutzung und während laufender Jobs geprüft.

Failover terminiert betroffene Prozesse und informiert den Agenten. Der Agent wählt anschließend eine verfügbare Maschine und entscheidet anhand der Logs und vorhandener Checkpoints, wie er weiterarbeitet. Prozesse werden weder gesnapshottet noch automatisch erneut gestartet.

## Parallele Jobs auf mehreren Rechnern

Der Agent erhält die Anweisung, unabhängige GPU-Arbeiten bei einem erwarteten Zeitgewinn selbst auf mehrere konfigurierte Rechner zu verteilen. Dafür enthält sein Laufzeitkontext die GPU-Konfiguration und aktuelle Job-Belegungen; `resource_list {}` liefert dieselbe Übersicht. Die fachliche Aufteilung bleibt eine Modellentscheidung. Diese Anweisung garantiert nicht, dass jedes Modell jede passende Gelegenheit erkennt.

`exec_command {cmd, gpu:true, background:true}` reserviert einen freien konfigurierten GPU-Rechner und liefert sofort `jobId` und `machineId`. Weitere unabhängige Aufträge können danach gleichzeitig auf anderen Rechnern laufen. Die Zentrale vergibt konservativ höchstens einen als GPU-Job angeforderten Auftrag pro GPU-Rechner. Das Pool-Limit gilt weiterhin global für gleichzeitig belegte Maschinen, einschließlich der Standardverbindung und anderer Agenten. Hintergrundjobs halten ihre eigene Belegung bis zum Ende. GPU-Eigenschaftstags reichen nicht: Automatische GPU-Zuordnung setzt `vm_type: q35` und konkrete `gpu_devices` voraus und fällt nicht auf CPUs zurück.

Optional legt `machineId` einen bestimmten zugewiesenen Rechner für genau diesen Auftrag fest. Die Standardmaschine des Agenten bleibt erhalten. Lesende Dateiwerkzeuge und Approver-Inspektionen akzeptieren ebenfalls `machineId`, damit Skripte und Ergebnisse auf der tatsächlichen Job-Maschine geprüft werden können. Bei eigenständigen Maschinen werden Dateien nicht automatisch kopiert; Pool-Arbeitsbereiche und als gemeinsam konfigurierte Speicherbereiche müssen dieselben Daten bereitstellen. Unabhängige Läufe brauchen getrennte Ausgabe- und Checkpoint-Verzeichnisse.

Ist eine GPU noch durch eine andere persistente VM belegt oder scheitert die VM-Vorbereitung, darf die automatische Auswahl vor dem Start des Benutzerbefehls einen anderen konfigurierten GPU-Rechner versuchen. Ein bereits gestarteter Job wird bei einem Fehler niemals automatisch erneut ausgeführt. Jeder Auftrag wird unabhängig vom Auto-Approver geprüft; dessen Kontext enthält angeforderte Zielmaschine, GPU-Anforderung und Ressourcenübersicht. Der lokale Integrationstest weist echte zeitliche Überlappung über mehrere SSH-Testpeers nach und prüft Belegungen, Pool-Limit und unveränderte Standardmaschine. Ein Live-Test mit mehreren physischen GPUs bleibt auf den CIPs bis zur IOMMU/VFIO-Einrichtung blockiert.

## Zusätzlicher Projektspeicher und Quota

Pools und einzelne Maschinen akzeptieren zusätzliche Speicherwurzeln und einen festen Host-Befehl zur Quota-Abfrage:

```yaml
storage:
  - id: ciptmp
    path: /proj/ciptmp/at69yvos
    shared: true
    description: Temporärer Speicher ohne Backup; wichtige Resultate separat sichern.
quota_command: [/usr/bin/cip-quota]
```

Unter jeder Wurzel wird ausschließlich `project-<Projekt-ID>` für das betreffende Projekt angelegt und in der VM unter `/mnt/zentrale-storage/<id>` eingebunden. Der gesamte benutzerspezifische Ordner und seine anderen Dateien werden nicht in den Gast exportiert. `storage_list {}` nennt die genauen Mounts. Agenten desselben Projekts teilen diesen Bereich; andere Projekte erhalten eigene Unterordner. Eine Änderung des konfigurierten Standardpfads verschiebt bereits erfolgreich eingerichtete Bereiche nicht. `shared: true` verlangt bei Pool-Wechseln die vorhandene gemeinsame Projektkennung, statt auf einer fehlenden Freigabe ein leeres Ersatzverzeichnis anzulegen. Ohne `shared` bleibt die Zuordnung pro Maschine getrennt. Konfigurierte Speicherbeschreibungen erscheinen als Daten im Agentenkontext.

Zusätzliche Speicherbereiche benötigen den integrierten VM-Runner mit `sandbox: auto` oder `microvm`; fehlt die VM-Unterstützung, wird dieser Zugriff nicht über eine schwächere Ausführung ersetzt. VS Code, normale Gastbefehle und Gast-Root erhalten dieselben Projekt-Mounts. Stoppen oder Zurücksetzen einer VM entfernt die zusätzlichen Daten nicht. Das Entfernen eines Projekts löscht ebenfalls keine Dateien auf diesen Speicherwurzeln automatisch.

`storage_quota {machineId?}` führt den konfigurierten Befehl direkt auf dem SSH-Host aus, ohne VM-Start und ohne einen vom Modell gelieferten Befehl oder zusätzliche Argumente. `quota_command` ist ein von der Pool-Administration vorgegebener Argumentvektor, kein Shelltext. Der Auto-Approver sieht den tatsächlichen Host-Befehl vor der Ausführung. Rückgabe sind Ausgabe ohne ANSI-Farbsequenzen, Exit-Code und Prüfzeitpunkt; eine überschrittene Quota darf selbst einen von null verschiedenen Exit-Code melden. Die Quota gilt für den gesamten Benutzer und kann deshalb auch Dateien umfassen, die außerhalb der Projekt-Mounts liegen.

Der Agent soll vor großen Datenmengen, Checkpoints und Cache-Schreibvorgängen die aktuelle Quota und gegebenenfalls die Inode-Grenzen abfragen. Bereinigung erfolgt mit regulären, einzeln geprüften Befehlen innerhalb der VM und nur für eigene, entbehrliche und reproduzierbare Projektdaten. Vorhandene fremde oder andere Benutzerdaten sowie wertvolle Ergebnisse und aktive Checkpoints bleiben erhalten. Ist nicht ausreichend Platz sicher freizugeben, muss der Agent diese Blockade melden.

Die FAU-Konfiguration enthält `/proj/ciptmp/at69yvos` als gemeinsamen temporären NFS-Speicher und `/usr/bin/cip-quota` als Host-Abfrage. Beim Anpassen des SSH-Benutzers auch diesen Pfad ändern. Die aktuellen Quota-Werte werden abgefragt und nicht aus der älteren `/proj/ciptmp/README` übernommen. ciptmp hat laut dieser lokalen Betriebsbeschreibung kein Backup und keine Bestandsgarantie. Der vollständige q35-Live-Test auf `cip1e0` prüfte tatsächliche Schreibzugriffe zwischen VM und ciptmp, die Host-Quota-Abfrage, den gemeinsamen Editor-Gast sowie den Erhalt der Zusatzdaten nach VM-Reset. Alle dabei erzeugten Testdaten und Test-VMs wurden entfernt.

## Sandbox und Editor

Die Fedora-MicroVM stellt vor der ersten Modellanfrage eine Grundausstattung bereit: Git, ripgrep (`rg`), Python 3 mit pip, curl, SSH-/SCP-Clients, jq, less, make und fish. Dazu kommen `file`, find/xargs, diff/cmp, patch, tar, gzip, bzip2, xz, zip/unzip, which, ps/pgrep und flock/script. Beim VM-Start werden vorhandene Befehle innerhalb des Gastes geprüft und nur fehlende Pakete nachinstalliert. Damit erhalten auch bestehende Gastplatten neue Standardwerkzeuge ohne Reset; zusätzlich installierte Pakete und Gastdateien bleiben erhalten. Sind alle Werkzeuge vorhanden, ist dafür kein Zugriff auf Paketquellen nötig.

Die Fedora-MicroVM verwendet für den Benutzer `zentrale` standardmäßig **fish**, einschließlich neuer VS-Code-Terminals. Beim nächsten Start einer bestehenden MicroVM wird fish ohne Zurücksetzen des persistenten Datenträgers ergänzt. Eine bereits bewusst eingestellte andere Shell bleibt erhalten. Automatisierte Harness-Befehle laufen weiterhin ausdrücklich über die POSIX-Shell.

`auto` bevorzugt den integrierten QEMU/KVM-Runner mit **Fedora Minimal 45**. Ein eigener `microVMCommand` ist dafür nicht nötig. Voraussetzungen auf der SSH-Maschine: Linux x86_64, zugängliches `/dev/kvm`, QEMU mit dem gewählten Maschinentyp (`microvm` als Vorgabe oder `q35`), `qemu-img`, Python ab 3.10, `mkfs.ext4`, `tar` und `modprobe`. Fehlen Voraussetzungen der automatischen CPU-Auswahl, folgt Bubblewrap mit cgroups v2 und `systemd-run --user`. Eine ausdrücklich gewählte q35-VM oder GPU-Zuordnung fällt bei Fehlern nicht auf Hostausführung oder Bubblewrap zurück. Fehler beim Download oder Boot führen zu einem Fehler, ohne die gewählte Isolation stillschweigend abzuschwächen.

Der Gast verwendet bevorzugt den bereits vorhandenen Kernel der Fernmaschine. Vor dem Start werden dessen Lesbarkeit, die Unterstützung für `9p`, `9pnet_virtio`, `virtio_mmio` beziehungsweise `virtio_pci` bei q35, `virtio_console`, `virtio_blk`, `ext4` und `virtio_net` sowie explizit deaktivierte benötigte Namespaces geprüft. Dabei werden keine Hostmodule geladen. Nur wenn dieser Kernel ungeeignet oder nicht lesbar ist, lädt die Zentrale einen fest versionierten Linux-Gastkernel samt passenden Modulen aus dem offiziellen Alpine-Paket, prüft dessen SHA-256 und legt ihn atomar im privaten MicroVM-Cache ab. Weitere Starts verwenden diese Kopie ohne erneuten Download; ein wieder geeigneter Hostkernel hat weiterhin Vorrang. Für die Aufbereitung der Ersatzmodule wird zusätzlich `depmod` benötigt. Das Paket wird ausschließlich für den Gast bereitgestellt und schreibgeschützt eingebunden; Kernel und Module der Fernmaschine werden nicht verändert. Ein fehlgeschlagener Download oder Start bricht die Vorbereitung ab. Die Kernelkonsole wird im privaten Boot-Log erfasst. Scheitert die Fedora-Ersteinrichtung, bleiben Exit-Code und die letzten 10.000 Log-Bytes unter `/var/tmp/zentrale-microvm-<UID>/fedora-bootstrap-failure.json` erhalten, auch nachdem das temporäre Bootstrap-Verzeichnis entfernt wurde.

Die Zentrale hält **eine laufende VM und eine dauerhafte Gastplatte pro Agent und Projekt auf der jeweiligen Maschine**. Weitere Werkzeugaufrufe verwenden dieselbe VM; auch im Leerlauf bleiben Pakete und Gastdateien erhalten. Ein eigener Supervisor auf der SSH-Maschine hält sie unabhängig von einzelnen SSH-Kommandos und Backend-Neustarts am Leben. Pro VM gelten 2 vCPUs, 4 GiB RAM und eine auf 8 GiB begrenzte, bedarfsgerecht wachsende Gastplatte. Projekt und Agenten-Arbeitsverzeichnis werden zusätzlich eingebunden. Andere Agenten haben eigene Gastplatten und VMs. Vor dem ersten Modellaufruf muss auch die zugehörige Fedora-VM erfolgreich gestartet sein.

Der Agent kann über folgende unabhängig geprüfte Werkzeuge seine Umgebung verwalten:

- `packages_install {packages:["gcc","gcc-c++"]}` installiert Fedora-Pakete ausschließlich im eigenen Gast und verlangt `network=true` für diesen Aufruf.
- `exec {command,guestRoot:true}` erlaubt nötige Administration innerhalb des Fedora-Gastes. Es erteilt keine Root-Rechte auf der SSH-Maschine und funktioniert ausschließlich mit dem integrierten MicroVM-Runner.
- `microvm_info {}` liefert Betriebssystem, Boot-ID, Zuordnung und Laufzustand.
- `microvm_reset {}` setzt den eigenen Fedora-Gast zurück. Laufende Jobs müssen vorher beendet werden. Installierte Pakete und gastinterne Dateien werden entfernt; Projektdateien und das eingebundene Agenten-Arbeitsverzeichnis bleiben erhalten. Andere Agenten sind davon nicht betroffen.

Normale Befehle laufen mit einer unprivilegierten Benutzer-ID. Jeder Auftrag erhält eigene Prozess- und Mount-Namensräume. Ohne individuelle Netzwerkfreigabe läuft er zusätzlich in einem eigenen Netzwerk-Namensraum mit ausschließlich Loopback. Eine vorherige Freigabe wird nicht übernommen, auch nicht für Gast-Root. Stoppen und SSH-Verlust beenden den betroffenen Job samt Nachkommen; die VM bleibt für weitere Aufrufe verfügbar. Beim Entfernen eines Agenten werden seine VMs und Gastplatten auf den verwendeten Maschinen beseitigt. Failover überträgt weder Gastplatten noch laufende Prozesse: Projekt-Checkpoints und gezielter Neustart bleiben Aufgabe des Agenten. GPU-Geräte werden nicht automatisch durchgereicht.

Beim ersten Gebrauch werden festgelegte virtme-ng-, BusyBox- und offizielle Fedora-Minimal-Artefakte über HTTPS geladen und gegen eingebettete SHA-256-Prüfsummen geprüft. Das Fedora-Archiv wird ausschließlich innerhalb einer VM auf eine Gastplatte entpackt; der Host mountet diese Platte nicht und führt keine Programme aus dem Image aus. Runtime, unveränderte Lizenzdateien und private Steuerdaten liegen unter `/var/tmp/zentrale-microvm-UID`, ausschließlich für den SSH-Benutzer zugänglich. Kein schreibbarer Workspace darf diesen Cache enthalten. Versionen, Prüfsummen und Herkunft stehen in `backend/remote/microvm/release.json`. Die Einrichtung benötigt weder `sudo` noch Änderungen an Host-Systempaketen. Befehle, Dateiinhalte und Ein-/Ausgabe werden über private Auftragsdateien übertragen; der Steuerkanal ist für Gastprogramme unerreichbar.

### q35 und GPU-Passthrough

Unter **Maschine bearbeiten → VM-Typ** lässt sich `q35` wählen. Beide VM-Typen verwenden denselben Fedora-Gast, dieselben Werkzeugfreigaben, Projektfreigaben und persistenten Gastplatten. q35 verwendet PCI für VirtIO und eigene PCIe-Root-Ports für konfigurierte GPU-Funktionen; unnötige QEMU-Standardgeräte bleiben abgeschaltet. Es wird kein vollständiges Firmware- oder Desktop-Image benötigt. Die Auswahl des Gastkernels bleibt bedarfsabhängig: Ein geeigneter Hostkernel hat Vorrang; nur bei fehlender Unterstützung wird der geprüfte Ersatzkernel bereitgestellt.

Ein Pool kann `vm_type: q35` als Vorgabe enthalten; Mitglieder können den Typ überschreiben. `gpu_devices` wird ausschließlich am einzelnen Mitglied konfiguriert, weil PCI-Adressen zu dessen Host gehören. Beispiel für einen bereits von der Administration vorbereiteten Host:

```yaml
id: gpu-pool
name: GPU-Pool
max_machines: 1
shared_home: true
workspace_root: "~/.local/share/zentrale/projects"
sandbox: auto
vm_type: q35
machines:
  - id: gpu-a
    host: gpu-a.example
    name: GPU A
    user: developer
    gpu_devices: ["0000:01:00.0", "0000:01:00.1"]
```

Die Adressen müssen der tatsächlichen Hardware entsprechen und alle benötigten Funktionen der IOMMU-Gruppe enthalten. GPUs und zugehörige Funktionen desselben PCI-Slots (etwa Audio) sind zulässig; beliebige andere PCI-Geräte werden abgelehnt. Ohne expliziten VM-Typ wählt eine GPU-Zuordnung q35; ein ausdrücklich angegebenes `microvm` ist damit unvereinbar. In der API heißen die Felder `vmType` und `gpuDevices`.

Vor dem Start prüft Zentrale GPU-Klasse, IOMMU-Gruppe, Bindung an `vfio-pci`, vollständige Gruppenzuordnung, VFIO-Zugriffsrechte und mindestens 4 GiB freigebbaren gesperrten Speicher (`memlock`). Die Hostadministration muss IOMMU aktivieren, die GPU samt Begleitfunktionen für VFIO vorbereiten und dem SSH-Benutzer Zugriff auf `/dev/vfio/vfio` und die Gruppen-Geräte geben. Zentrale verändert keine Hosttreiber, Kernelparameter oder Geräterechtedateien. Eine für VFIO reservierte GPU steht dem normalen Hosttreiber währenddessen nicht zur Verfügung.

Jede GPU-Gruppe wird exklusiv für die Lebensdauer der VM reserviert. Eine zweite Agenten-VM mit derselben Zuordnung erhält einen verständlichen Belegungsfehler. Im Menü einer ruhenden Unterhaltung gibt **VM stoppen** die Geräte frei und schließt den zugehörigen Editor; Gastplatte, installierte Pakete, Projekt und Unterhaltung bleiben erhalten. Laufende Agenten, Jobs und Vorbereitungen verhindern diesen Vorgang. Beim nächsten Arbeitsauftrag wird die VM erneut gestartet. Auch ein Editor auf einer GPU-Maschine benötigt deshalb eine Unterhaltung als Besitzer. Das Beenden eines einzelnen Jobs gibt die GPU nicht frei, solange seine VM läuft.

`microvm_info` enthält `vmType`, die konfigurierten Hostadressen und bei GPU-Zuordnung `guestGPUs` mit den tatsächlich im Gast gefundenen Display-/3D-Geräten samt Treiber. PCI-Passthrough allein stellt noch kein CUDA bereit: Der Gast braucht einen zum laufenden Gastkernel passenden NVIDIA-Treiber und passende CUDA-Benutzerprogramme. Der NVIDIA-Treiber des Hosts wird nicht in den Gast übernommen; der bereitgestellte Ersatzkernel enthält keinen proprietären NVIDIA-Treiber. Diese Treiberinstallation wird nicht automatisch durchgeführt.

Validierung: Der vollständige q35-Live-Test auf `cip1e0` bestand mit QEMU 10.0.13 einschließlich Fedora-Werkzeugen, gemeinsamem Editor/Agenten-Gast, Netzwerkfreigaben, Reset und Datenerhalt nach Stop/Neustart. Die reine GPU-Vorprüfung auf `cip4a0` meldet für dessen RTX 2080 fehlende IOMMU-Gruppen; dort ist deshalb noch kein VFIO-/CUDA-Live-Test möglich. GPU-Tags im FAU-Pool aktivieren keine unvorbereiteten Geräte.

Ein optionaler administrativer MicroVM-Runner kann den eingebauten Runner über `microvm_command` ersetzen und erhält unverändert:

```text
runner --root PROJECT --temporary-directory AGENT_TMP --network=false -- /bin/sh -c COMMAND
```

Er muss Projekt und Arbeitsverzeichnis innerhalb seiner VM bereitstellen, Netzfreigaben beachten, Ein-/Ausgabe weiterreichen und beim Abbruch seine VM samt Prozessen beenden. Für integrierten und externen Runner gelten dieselben Freigaben. Ein ausdrücklich genehmigtes `outsideSandbox` überspringt die technische Sandbox-Erkennung und wird als `none` protokolliert. Ohne technische Sandbox können Netzwerksperren nur durch Auto-Approval durchgesetzt werden; wiederholte Lesefreigaben bleiben dann deaktiviert.

**VS Code in neuem Tab öffnen** startet einen eigenen Browser-Tab. Die VS-Code-Oberfläche füllt diesen vollständig aus; sie wird weder in die Projektansicht eingebettet noch auf der Fernmaschine gehostet. Zentrale lädt die offiziellen Webdateien einmalig in einen lokalen Cache und liefert Oberfläche, Browser-Worker und integrierte Web-Extensions selbst aus. Deutsch und Englisch sowie das gewählte Theme werden beim Öffnen übernommen.

Die Editor-Verbindung folgt dem gewählten Arbeitsagenten und bleibt an dessen Maschine gebunden. Bei MicroVM-Ausführung laufen der kopflose [VS-Code-Remote-Agent](https://code.visualstudio.com/docs/remote/remote-overview), Terminal, Debugger und Workspace-Extensions in derselben stabilen Fedora-MicroVM. Installierte Pakete und die Gastplatte werden mit den Agentenwerkzeugen geteilt; VS Code kann beendet werden, ohne diese VM zurückzusetzen. Fehlende Editor-Pakete werden beim ersten Start innerhalb von Fedora installiert. Projekte ohne Arbeitsagenten erhalten eine eigene stabile Editor-VM. Ohne MicroVM läuft der Remote-Agent direkt über SSH auf der Projektmaschine. Pool-Limits und `single_user` bleiben berücksichtigt; bei einem VM-Fehler wird nicht auf Host-Ausführung ausgewichen.

Oberfläche und Remote-Agent sind auf denselben Commit festgelegt (`backend/vscode/release.json`); die offiziellen Archive werden vor der Installation mit SHA-256 geprüft. Die aktuelle Distribution unterstützt Linux und macOS auf x64 und ARM64. Beim Erststart benötigen Zentrale und die gewählte Ausführungsumgebung Zugriff auf Microsofts Downloadserver.

Der zentrale Cache lässt sich mit `ZENTRALE_VSCODE_CACHE` einstellen. Auf der SSH-Maschine können `ZENTRALE_VSCODE_SERVER_CACHE` und `ZENTRALE_VSCODE_SERVER_DATA` die üblichen benutzereigenen Verzeichnisse überschreiben. Es wird kein lokaler VS-Code-Prozess auf Zentrale gestartet. UI-Extensions laufen als Web-Extensions im Browser; Workspace-Extensions verwenden den Remote-Extension-Host.

Nur das Workspace-Protokoll und Remote-Ressourcen laufen durch SSH. Auf dem Host endet der Tunnel an einem privaten Unix-Socket. Bei MicroVMs führt er über eine ausschließlich an Host-Loopback gebundene QEMU-Weiterleitung zu einem zusätzlich mit zufälligem Sitzungsschlüssel geschützten Relay vor dem Gast-Unix-Socket. Es wird kein öffentlicher Editor-Port freigegeben. Editor-Sitzungen unterstützen den integrierten Fedora-Runner. Bei eigenen MicroVM-Runnern meldet die Zentrale einen Fehler und startet keinen Host-Editor.

Alle Editor-Routen verlangen eine gültige Zentrale-Sitzung. Deren Cookie wird dem Remote-Agenten nicht weitergegeben. Die gewählte Maschine und ihr Pool-Platz bleiben für die Sitzung reserviert. **Editor beenden**, Dienstende oder ein verlorener SSH-Kanal räumen den Agentenprozess und seinen Socket auf; inaktive Sitzungen werden nach 15 Minuten freigegeben. Agentenlöschung und MicroVM-Reset beenden die zugehörigen Editor-Sitzungen. Die zentralen Webdateien bleiben bei einem Tunnelausfall erreichbar. Lade- und Fehlerzustände sowie die Host-Key-Prüfung laufen im neuen Tab und blockieren die übrige App nicht.

## Agenten und Prüfungen

Sichtbarer Antworttext erscheint schon während der Generierung. Die Live-Ausgabe unterstützt OpenAI Responses einschließlich Device-Login, Chat-Completions-Zugänge, Anthropic und Gemini. Mistrals wechselnde Thinking-/Text-Blöcke werden zusammengesetzt und nur gewöhnlicher Antworttext wird angezeigt. Reasoning, Signaturen und Werkzeugargumente bleiben für den vollständigen Provider-Replay erhalten. Die Adapter folgen den Protokollen für [OpenAI-Streaming](https://developers.openai.com/api/docs/guides/streaming-responses), [Anthropic-Streaming](https://platform.claude.com/docs/en/build-with-claude/streaming) und [Mistral-Inhaltsblöcke](https://docs.mistral.ai/inference/conversations/reasoning). Gateways, die weiterhin vollständiges JSON liefern, bleiben lesbar.

Textfragmente laufen gebündelt über den authentifizierten WebSocket, ohne Datenbankeintrag oder GraphQL-Abfrage pro Fragment. Reconnect liefert den aktuellen Entwurf als Snapshot. Die fertige Nachricht übernimmt dessen Kennung und wird genau einmal gespeichert. Abgebrochene Entwürfe bleiben im offenen Tab als unterbrochen sichtbar und gehen nicht als vollständige Antwort oder ausführbarer Werkzeugaufruf in den Modellverlauf ein. Der Verlauf folgt neuer Ausgabe nur, solange der Nutzer am Ende liest.

Der Stopp-Knopf im Eingabebereich beendet den Agenten samt seinen laufenden Jobs und MCP-Prozessen; neue Instruktionen lassen sich während der Arbeit weiterhin senden. Hintergrundjobs erscheinen als kompakte Zeilen mit Status, direkt aufklappbarer Ausgabe und einem eigenen Stopp-Knopf. Das Beenden eines Jobs verlangt weiterhin Bestätigung.

### Dauerhafte Ziele und Abschlussprüfung

Jede Unterhaltung besitzt höchstens ein offenes Ziel. Haupt- und Subagenten dürfen mit `create_goal {objective}` selbst ein Ziel für den Nutzerauftrag formulieren; `get_goal {}` liest den aktuellen Zustand. Ein offenes Ziel lässt sich vom Agenten nicht durch ein kleineres ersetzen. Ohne selbst gesetztes Ziel legt die Harness spätestens beim vorgeschlagenen Abschluss ein Ziel aus dem aktuellen Nutzerauftrag an. Originalanweisungen und spätere Nutzerkorrekturen bleiben maßgeblich; ein automatisch formuliertes Ziel begründet keine zusätzlichen Berechtigungen.

`update_goal {status:complete|blocked|paused,reason?}` meldet eine gewünschte Zustandsänderung an. Der Hauptagent kann den Erfolg damit nicht selbst bestätigen. Beim Beenden einer Runde prüft sein unabhängiger Auto-Approver Ziel, ursprüngliche Anforderungen, finale Antwort, tatsächliche Werkzeugergebnisse, Jobs und Subagenten. Er kann Dateien, Verläufe und aktuelle Jobausgaben lesend inspizieren. Fehlen Arbeit oder Nachweise, erhält der Agent einen konkreten nächsten Schritt und arbeitet weiter. Auch wiederholte Fortschrittsmeldungen ohne Aktion durchlaufen diese Prüfung. Aktionsfreigaben zählen niemals als Abschlussnachweis.

Eine Blockade beendet das Ziel erst nach drei aufeinanderfolgenden Prüfungen derselben belegten Bedingung, wenn kein sinnvoller autorisierter Fortschritt mehr möglich ist. Fortsetzen beginnt diese Blockadeprüfung erneut. Vorübergehende Provider- oder Verbindungsfehler lassen das Ziel offen und erscheinen als technische Unterbrechung, statt Erfolg oder eine endgültige Blockade vorzutäuschen. Pausieren ist eine Nutzerentscheidung. Der Stoppzustand wird dauerhaft gespeichert; nach einem Neustart läuft pausierte oder blockierte Arbeit nicht selbstständig weiter. Der Nutzer kann ein angehaltenes Ziel verwerfen und anschließend ein neues beginnen; der alte Verlauf bleibt erhalten.

Ziel, Prüfergebnis und Gründe erscheinen im Chat; der Auto-Approver zeigt Zielprüfungen gesondert von Zugriffsfreigaben. Neue Nutzeranweisungen entwerten eine noch laufende Abschlussprüfung. Verifizierter Zielabschluss und die Erledigung der zugehörigen Nachrichten werden gemeinsam gespeichert, damit ein Neustart keinen bereits bestätigten Auftrag erneut anstößt.

Die drei Goal-Werkzeuge, das einzelne persistente Ziel, automatische Fortsetzungen und die erneute Blockadeprüfung orientieren sich am offiziellen [Codex-Goal-Vertrag](https://github.com/openai/codex/blob/748ab55ca7de0b647d9a7a9d9a8e70e00f28666d/codex-rs/ext/goal/src/spec.rs), der [Fortsetzungssteuerung](https://github.com/openai/codex/blob/748ab55ca7de0b647d9a7a9d9a8e70e00f28666d/codex-rs/ext/goal/src/runtime.rs) und dem [Completion-/Blocked-Audit](https://github.com/openai/codex/blob/748ab55ca7de0b647d9a7a9d9a8e70e00f28666d/codex-rs/ext/goal/templates/goals/continuation.md). Die verbindliche unabhängige Abschlussprüfung ist in Zentrales bestehende Auto-Approver-Harness integriert und gilt für sämtliche Provider.

Die Harness orientiert sich bei Werkzeugen und Kontextverwaltung am offiziellen [Codex-Prompting-Guide](https://developers.openai.com/cookbook/examples/gpt-5/codex_prompting_guide) und am [Codex-Quellstand `12dc86b`](https://github.com/openai/codex/tree/12dc86beb3d407091f34a246b22af348933a79c2). Sie bleibt eine eigene, providerunabhängige Implementierung mit SSH-Ausführung und separatem Auto-Approver.

Häufige Aufgaben haben typisierte Werkzeuge mit direkten JSON-Argumenten: `exec_command`, `read_file`, `list_files`, `apply_patch` und `update_plan`. Das `harness`-Werkzeug erschließt weitere Aktionen ohne umfangreichen Werkzeugkatalog im Prompt; Modelle mit weniger als 8.192 Kontext-Tokens erhalten ausschließlich diesen kompakteren Dispatcher. Die Instruktionen bevorzugen gezielte Suchen, relevante Dateiausschnitte, kleine Patches und überprüfte Ergebnisse. Ein kurzer persistenter Plan unterstützt mehrteilige Aufgaben und bleibt nach einer Kontextverdichtung verfügbar.

`read_file {path,offset?,limit?}` liest über alle Werkzeugzugänge standardmäßig 200 Zeilen ab der ersten Zeile. Das Ergebnis nennt Startzeile, Gesamtzeilenzahl und weitere verfügbare Inhalte. Lesefreigaben bleiben an den Hash der vollständigen Datei gebunden. `apply_patch` versteht die Codex-Marker für Hinzufügen, Ändern, Verschieben und Löschen mehrerer Dateien. Alle Pfade und Änderungskontexte werden vor dem Schreiben geprüft; ein Kontextfehler verändert keine Dateien. Bei einem späteren Schreibfehler werden vorherige Änderungen zurückgenommen, soweit das Dateisystem dies zulässt. Grenzen: 1 MiB Patch, 2 MiB pro bestehender Datei, 16 MiB Arbeitsmenge und 50 Dateioperationen. Tatsächliche Diffs werden getrennt für die Oberfläche gespeichert. Der Patch und alle Umweltaktionen durchlaufen weiterhin den Auto-Approver.

Lange Werkzeugergebnisse erhalten im Modellkontext eine begrenzte Vorschau von Anfang und Ende (maximal 16.000 UTF-8-Bytes einschließlich JSON-Metadaten, bei kleinen Kontextfenstern weniger). Fehlerstatus, Call-IDs und Nutzerinstruktionen bleiben erhalten. Das private Werkzeug `tool_output` lädt ausgelassene Abschnitte anhand von Nachrichten-ID und Byte-Offset nach; es kann ausschließlich eigene Werkzeugergebnisse lesen. Die gespeicherten Nachrichten bleiben vollständig, innerhalb der bestehenden Speichergrenzen der jeweiligen Werkzeuge. Die Begrenzung wird vor der automatischen Verdichtung angewandt und vermeidet so Zusammenfassungsanfragen allein wegen großer Logs.

Die automatische Kontextverdichtung berücksichtigt Systeminstruktionen, Werkzeugschemata und vorhandene Zusammenfassungen. Sie fasst semantische Nachrichten und Toolaufrufe zusammen, ohne Datenbank-Metadaten, doppelte Antworttexte oder verschlüsseltes Reasoning mitzuschicken. Originalauftrag, letzte Nutzerkorrekturen und nach Möglichkeit die jüngsten vollständigen Werkzeuggruppen bleiben wörtlich erhalten. Bei regulärer Fortsetzung bleiben native Reasoning-Daten und Signaturen unverändert. Unterbrochene Werkzeuggruppen bekommen eine ausdrückliche Fehlermeldung für fehlende Ergebnisse; gleichzeitig eingegangene Nutzerinstruktionen folgen unmittelbar danach. Eine Zusammenfassung ersetzt keine ursprüngliche Nutzeranweisung.

Der Approver erhält einen vollständigen aktuellen Projekt-, Quellen- und Nutzeranweisungs-Snapshot vor den Freigabeanfragen. Die Anfragen verweisen auf ihren jeweiligen Snapshot und behalten Aktion, Argumente und Prüfergebnisse. Neue Anfragen ändern dadurch bei identischem Kontext keinen früheren Nachrichtenpräfix. Erst danach begrenzt ein eigenes Budget ältere vollständige Prüfgruppen; aktuelle Anweisungen, Anfrage und laufende Inspektionen sind verpflichtend. Das gespeicherte Audit-Protokoll bleibt unverändert. Responses mit `phase=commentary` setzen den Hauptagenten fort, statt fälschlich eine abgeschlossene Aufgabe zu melden. Drei aufeinanderfolgende reine Fortschrittsantworten ohne Aktion führen zu einem Fehler, damit keine unbegrenzte Modellschleife entsteht. Unvollständige Provider-Antworten gelten ebenfalls als Fehler.

Regressionsprüfungen decken die Werkzeugfolge über einen authentifizierten SSH-Testpeer, verweigerte Änderungen, Mehrdatei-Diffs, fortgesetzte Hintergrundjobs, unterbrochene Werkzeuggruppen für vier Provider-Protokolle und Kontextverdichtung ab. Zwei feste Nutzdatenbeispiele: Eine kleine Änderung an einer 72.019-Byte-Datei samt neuer Prüfdatei benötigt einen 118-Byte-Patch; 20 wiederholte Approver-Anfragen werden für die Inferenz von 95.571 auf 10.206 Bytes reduziert. Das sind gemessene Fixture-Größen, keine Token- oder Qualitätsbenchmarks mit echten Modellen.

Die Verbrauchsanzeige trennt gemeldete Tokenzahlen von Schätzungen und weist fehlende Cache-Schreibdaten aus. Der Cache-Leseanteil wird ausschließlich aus Anfragen mit gemeldeten Eingabe- und Cache-Lesezahlen berechnet. Responses und native Komprimierung erfassen Cache-Schreib-Tokens einschließlich gemeldeter 12-Stunden-Anteile; die Kostenrechnung berücksichtigt deren OpenAI-Schreibaufschläge. Die [Analyse vom 9. November 2026](docs/token-cache-audit.md) enthält den Codex-Abgleich, die gemessene Ausgangslage und verbleibende Optimierungspunkte.

Der zusätzliche Abgleich mit [Codex `e6bf88a`](https://github.com/openai/codex/tree/e6bf88ae022cdab968e5a48d5cbeea4d2e304fc9) vom 7. November 2026 betrifft Ausgabegrenzen und Wiederholungen: Remote-Logs behalten bei ihrer Grenze von 1 MiB den Anfang und das aktuelle Ende, mit einer Angabe ausgelassener Bytes. Späte Tracebacks und Testzusammenfassungen gehen dadurch nicht hinter Fortschrittsausgaben verloren. Vorübergehende Provider-Ablehnungen (408, 429, 500, 502–504, 529) werden mit Backoff höchstens dreimal angefragt. `Retry-After` wird als Sekunden oder HTTP-Datum berücksichtigt; liegt die geforderte Wartezeit außerhalb des 30-Sekunden-Budgets, bleibt der Fehler bestehen. Authentifizierungs-, bekannte Kontingent-/Abrechnungsfehler, ungültige Antworten, abgerissene Streams und mehrdeutige Transportfehler werden nicht automatisch wiederholt. Dies gilt für alle Provider; ausgeführte Werkzeuge werden dadurch nicht erneut ausgeführt.

Agenten können eigene **MCP-Server** verwenden. Die Harness nutzt das [offizielle MCP-Go-SDK](https://github.com/modelcontextprotocol/go-sdk/tree/v1.8.0), unterstützt stdio, Streamable HTTP und ältere HTTP/SSE-Server und verhandelt die Protokollversion. Der Einstieg ist `tool_search {query:"mcp"}`. Die Aktionen funktionieren über den allgemeinen Dispatcher und als `tools.mcp_connect`, `tools.mcp_tools` und `tools.mcp_call` in Code Mode.

`mcp_connect {name,transport:"stdio",command}` startet einen Server über SSH in derselben Sandbox wie Agentenbefehle. Bei MicroVMs läuft er als gewöhnlicher Gastbenutzer. Zu einem bereits gestarteten Server verbindet sich der Agent mit `mcp_connect {name,transport:"http"|"sse",url:"http://127.0.0.1:PORT/ENDPOINT"}`. HTTP-Anfragen laufen ebenfalls innerhalb der Sandbox; `localhost` bezeichnet niemals die Zentrale. Der HTTP-Relay nutzt bei verwalteten VMs die vorhandene Harness-Python-Laufzeit, ohne Python im Fedora-Gast vorauszusetzen. HTTP/SSE ist zunächst auf Loopback-Endpunkte beschränkt; externe MCP-Konten und OAuth sind nicht Bestandteil dieser Anbindung.

Für HTTP/SSE müssen Serverstart, Verbindung und weitere MCP-Aktionen `network:true` verwenden: Netzwerkfreie Einzelaktionen haben jeweils einen privaten Netzwerk-Namespace und erreichen zuvor gestartete Server nicht über deren Loopback. Bei stdio ist ein Start ohne Netzfreigabe möglich. Eine Verbindung behält die beim Start geprüften Prozess-/Netzberechtigungen; zum Ändern muss sie neu aufgebaut werden. Serverstarts, Katalogabfragen und jeder Werkzeugaufruf durchlaufen den Auto-Approver einzeln, einschließlich tatsächlicher Serverkonfiguration, Eingabeschema und Parametern. `readOnlyHint` erteilt keine Freigabe. Sampling, automatische Modellaufrufe, Elicitation und Zugriff auf Dateien der Zentrale werden nicht angeboten.

`mcp_tools {serverId,query?,offset?}` sucht im Katalog und liefert bis zu 20 Namen/Beschreibungen. `mcp_tools {serverId,tool:"NAME"}` liefert das Eingabeschema bei Bedarf; `mcp_call {serverId,tool,arguments:{...}}` ruft es auf. Geänderte Kataloge müssen neu entdeckt werden. Serverdaten erteilen keine Nutzerinstruktionen oder Berechtigungen. Text und strukturierte Ergebnisse werden providerunabhängig weitergegeben; Bilder und Audio derzeit als ausgelassene Binärinhalte gekennzeichnet. Der Verlauf zeigt lesbare Parameter und Ergebnisse statt Protokoll-JSON.

MCP-Verbindungen sind agenteneigen, auf acht gleichzeitige Server begrenzt und überleben Modellrunden sowie die normale Freigabe des Agenten-SSH-Handles. `mcp_list` nennt aktive Verbindungen; `mcp_disconnect` trennt sie und beendet zugehörige stdio-Prozesse. Eigenständig gestartete HTTP-Server bleiben ihre eigenen Hintergrundjobs. Stoppen, Löschen, VM-Reset, eine ungültig gewordene Arbeitsverbindung und Backend-Shutdown schließen die Sitzungen. Nach Neustart oder Failover verbindet sich der Agent erneut; Werkzeugaufrufe werden nicht automatisch wiederholt. Tests prüfen stdio, HTTP-JSON, HTTP-SSE und ältere SSE-Server über authentifizierte SSH-Testpeers, einschließlich Ablehnung, Pagination, Abbruch, Agententrennung und Sandbox-Übergabe. Ein echter Ghidra-Server oder KVM-Gast wird dabei nicht gestartet.

Die MicroVM überträgt Prozessausgaben jetzt über quittierte Chunks mit maximal 2 MiB wartenden Daten pro Strom. Langsame Leser bremsen den Produzenten, statt Protokollnachrichten zu verlieren; gelesene Chunks werden entfernt. Für MCP gilt dies auch für die Eingabe ohne kumulatives 16-MiB-Limit. Die UI-Loggrenze bleibt davon unabhängig. Lokale Tests übertragen 24 MiB durch die tatsächlichen Gast-Prozess-Pipes sowie große normale Jobausgaben und prüfen unveränderte Bytes, begrenzten Plattenverbrauch, partielle SSH-Schreibvorgänge und Abbruch bei Rückstau.

Die weiteren Änderungen orientieren sich am [Codex-Quellstand `1be4eb1`](https://github.com/openai/codex/tree/1be4eb123e5f870cfdee701dee171801cab39c66) vom 6. November 2026. Die gemeinsame Implementierung gilt für alle Provider:

- **Stabile Anfragepräfixe:** Systeminstruktionen und Kernwerkzeuge bleiben unverändert. Projektzustand, Notizen und Arbeitsplan liegen als deduplizierte, am Verlauf verankerte Snapshots vor. Neue Zustände werden angehängt; vorherige Anfragen bleiben als Präfix erhalten. Diese Daten erscheinen nicht als technische Nachrichten im Chat. Seltenere Aktionen sind über `tool_search {query}` auffindbar, statt den vollständigen Katalog im Systemprompt zu wiederholen.
- **Getrennte Kontextbudgets:** Originalanweisungen werden ausschließlich aus gespeicherten Nutzernachrichten übernommen. Frage und Antwort bleiben in einem Datensatz erhalten. Originalauftrag und letzte Korrekturen bleiben geschützt; weitere ältere Originale passen als ganze Datensätze in ein eigenes Budget. Fehlende Originale werden ausdrücklich markiert und sind über `instructions_read {}` wieder erreichbar. Zusammenfassungen und Assistentennotizen begründen keine Berechtigungen. Beim Approver bleiben die aktuelle Anfrage und ihre laufenden Inspektionen vollständig. Frühere Prüfungen werden als ganze Gruppen aus einem separaten Budget ausgewählt, ohne dafür eine zusätzliche Zusammenfassung anzufordern. Die gespeicherten Prüfprotokolle bleiben vollständig.
- **Providerunabhängiger Code Mode:** `code_mode {code}` nimmt einen JavaScript-Body entgegen. `tools.<action>({...})` ruft registrierte Werkzeuge auf; `print(...)`, `text(...)` oder `console.log(...)` wählen die ans Modell zurückgegebenen Informationen aus. Auch abhängige Aufrufe können so innerhalb einer Modellrunde erfolgen. Die einzelnen Aktionen laufen in Aufrufreihenfolge, auch bei `Promise.all`, und Umweltaktionen werden jeweils unabhängig geprüft. Neue Nutzerinstruktionen werden nach dem laufenden Block übernommen. Fehler innerer Aktionen bleiben zusätzlich in der Modellantwort erkennbar. Aktionen, Ergebnisse und tatsächliche Diffs werden separat für die Oberfläche gespeichert und lesbar aufgeklappt; diese UI-Daten landen nicht zusätzlich im Modellkontext.

Die Code-Mode-Laufzeit verwendet QuickJS in WebAssembly, startet keinen lokalen Prozess und besitzt keinen nutzbaren Dateisystem-Mount, Netzwerk- oder Prozesszugriff. Die feste Grenze des WASM-Speichers beträgt **64 MiB**; höchstens vier Instanzen laufen gleichzeitig. Weitere Grenzen: 64 KiB Quelltext, 32 Werkzeugaufrufe, 16.000 UTF-8-Bytes Ausgabe und zwei Minuten pro Block. Lange Projektbefehle laufen über die bestehenden Hintergrundjobs weiter. Das eingebettete Modul stammt aus `github.com/fastschema/qjs v0.0.6`. `backend/codemode/prepare_wasm.py` reproduziert die zusätzliche WASM-Speichergrenze aus dem per SHA-256 festgelegten Original. Die MIT-Lizenzen liegen neben dem Modul.

**Native Compaction** ist eine zusätzliche Provider-Fähigkeit, keine Voraussetzung für diese Verbesserungen. Der OpenAI-Adapter verwendet bei offiziellen API- und ChatGPT-Endpunkten `/responses/compact`; den vollständigen zurückgegebenen Kontext einschließlich verschlüsseltem Zustand gibt er unverändert weiter. Details zum Protokoll beschreibt die [offizielle Dokumentation](https://developers.openai.com/api/docs/guides/compaction). Andere Provider und eigene Gateways verwenden die portable Textverdichtung. Eine nicht unterstützte native Route oder ein zu großer nativer Checkpoint wird für diesen Zugang und dieses Modell 24 Stunden lang nicht erneut versucht. Authentifizierungs-, Rate-Limit- und vorübergehende Serverfehler werden als Fehler gemeldet und lösen keine zweite Inferenz als versteckten Fallback aus. Native Aufrufe werden in der Verbrauchserfassung berücksichtigt.

Kontexte sind an Provider, Zugang und Modell gebunden. Bei einem Wechsel wird eine portable Zusammenfassung aus den gespeicherten semantischen Originalen aufgebaut, bevor das neue Modell seine Aufgabe fortsetzt. Verschlüsselter Zustand und fremde native Protokollnachrichten gelangen nicht zum anderen Zugang. Eine zuvor ausdrücklich geleerte Unterhaltung bleibt dabei geleert; ein nativer Checkpoint kann aus dem vollständigen Audit-Verlauf rekonstruiert werden. Die Prüfungen verwenden lokale HTTP-/SSH-Fixtures für alle sieben Provider-Arten sowie echte WASM-Ausführung; sie sind kein Leistungsbenchmark mit kostenpflichtigen Modellen.

Diese Trennung orientiert sich an Codex’ [Begrenzung der Werkzeugergebnisse im Kontext](https://github.com/openai/codex/blob/ede589336e9ecc64cb05045c2961fb1277858f1d/codex-rs/core/src/context_manager/history.rs) und [separater Erfassung von Diffs für die Oberfläche](https://github.com/openai/codex/blob/ede589336e9ecc64cb05045c2961fb1277858f1d/codex-rs/core/src/turn_diff_tracker.rs). Zentrale verwendet dafür eine eigene, providerunabhängige Umsetzung; daraus folgt keine Aussage über eine gleiche Token-Effizienz oder Agentenleistung.

Jeder Haupt- und Subagent besitzt einen eigenen Auto-Approver mit unabhängigem Kontext und lesenden Inspektionswerkzeugen. Umweltaktionen durchlaufen die Prüfung. Notizen, Kontextverwaltung, Rückfragen, eigene Jobkontrollen und das Aufräumen eigener temporärer Python-Umgebungen sind genehmigungsfrei. Technische Lesefreigaben hängen an Dateiinhalt, Maschine und aktuellen Nutzerinstruktionen und werden bei Änderungen widerrufen.

Prompts und Steering werden vor Maschinenauswahl, SSH-Anmeldung und Sandboxvorbereitung dauerhaft in der Zentrale übernommen. Nach der Serverbestätigung darf der Browser geschlossen oder der Client ausgeschaltet werden; die Verarbeitung gehört zum Backend. Verbindungsaufbau, Projektverzeichnis und Sandbox haben sichtbare Fortschrittszustände. Ein fehlgeschlagener Aufbau startet keine Inferenz: Der gespeicherte Auftrag kann ohne erneute Eingabe fortgesetzt werden. Unbekannte Host-Schlüssel lassen sich nach Prüfung des Fingerprints bestätigen. Die SSH-Verbindung wird vor weiteren Modellrunden geprüft und von Werkzeugen wiederverwendet. Hintergrundjobs behalten eigene Verbindungshandles, wenn der Agent seine Runde beendet.

Enter sendet eine Instruktion für die nächste sichere Grenze zwischen Werkzeug-Batches. Ein bereits gestarteter Batch läuft fertig; noch nicht gestartete Werkzeugaufrufe aus einer inzwischen überholten Modellgenerierung werden verworfen und mit der neuen Instruktion neu geplant. Tab bei gefülltem Eingabefeld reiht eine Nachricht für den nächsten abgeschlossenen Durchlauf ein. Leeres Tab und Shift+Tab behalten ihre Fokusnavigation, Shift+Enter erzeugt einen Zeilenumbruch. Die serverseitige Warteschlange lässt sich nach Neuladen weitersehen, bearbeiten und leeren; wartende Nachrichten werden erst bei ihrer Verarbeitung als Nutzerinstruktion für Modell und Auto-Approver übernommen.

Jede Übermittlung besitzt eine eindeutige Empfangskennung. Ein verlorener HTTP-Antwortweg kann mit derselben Kennung wiederholt werden, ohne einen zweiten Auftrag anzulegen. Noch unbestätigte Nachrichten bleiben als solche erkennbar und werden lokal für eine Wiederholung gespeichert; eine unterbrochene Verbindung ist keine Serverbestätigung. Kleine Übermittlungen verwenden Fetch-Keepalive. Nach einem Backend-Neustart werden übernommene, noch laufende Aufträge und ihre Warteschlange wieder aufgenommen. Finalantwort und Abschluss der zugehörigen Eingaben werden gemeinsam gespeichert; explizit gestoppte Arbeit bleibt pausiert. Das ist keine Zusage, einen externen Werkzeugaufruf über einen Serverabsturz hinweg genau einmal auszuführen: Schon zuvor gestartete Prozesse können dabei unterbrochen sein.

Beim Aufbau der Arbeitsverbindung werden auch `mkdir`, `chmod` und Python 3 geprüft. Harness-Befehle laufen ausdrücklich über `/bin/sh -c`, unabhängig von der Login-Shell des SSH-Benutzers. Die äußere SSH-Übergabe erhält Backslashes auch bei `fish` unverändert; Regressionstests kompilieren den tatsächlich übergebenen Editor-Supervisor unter `sh`, `bash` und `fish`. Zusätzliche Login-Profile werden nicht geladen; der vorhandene SSH-PATH bleibt erhalten. Prozessfehler enthalten Exitstatus und entfernte Fehlerausgabe. Dateiwerkzeuge initialisieren das Projektverzeichnis vor ihrem ersten Zugriff.

Native Konten und Gateways können Responses als SSE liefern, teilweise mit falschem oder fehlendem Content-Type. Der Provider-Client erkennt auch die Event-Framings, wartet auf `response.completed` und übernimmt Text, Toolaufrufe, Reasoning und Usage aus der vollständigen Antwort. Fehlgeschlagene und abgebrochene Streams werden als Fehler behandelt.

`exec_command` liefert standardmäßig nach zehn Sekunden einen laufenden Hintergrundjob zurück; `yield_time_ms` erlaubt eine bis 30 Sekunden. Bestehende `harness`-Befehle behalten 90 Sekunden als Standard. Der Prozess läuft beim Zurückgeben weiter. `interactive:true` hält seine Standardeingabe offen; `tty:true` stellt zusätzlich ein steuerndes Terminal bereit. Bei verwalteten MicroVMs entsteht dieses Terminal innerhalb des isolierten Gastprozesses, ohne Python im Fedora-Gast vorauszusetzen. Andere Sandboxes verwenden ihren Python-3-Relay. Das Terminal startet mit 120 Spalten, 32 Zeilen und `TERM=xterm-256color`.

`write_stdin {id,chars?,eof?,yield_time_ms?,cursor?,max_output_bytes?}` setzt genau diesen Job fort. Eine neue Eingabe, Ctrl+C und EOF werden einzeln geprüft, zusammen mit dem ursprünglichen Befehl, Startverzeichnis, Sandbox-/Netz-/Rootrechten, letzten Eingaben und aktueller Ausgabe. Geänderte Interpreter oder Arbeitsverzeichnisse begründen keine pauschalen Rechte. Pro Aufruf sind 16 KB Eingabe erlaubt; höchstens 64 KiB warten in der atomaren Eingabequeue. Abgebrochene Einreihungen liefern keine teilweise angenommene Eingabe. Leere Aufrufe lesen nur Ausgabe und brauchen keine Freigabe. Bei PTYs bedeutet EOF die Terminaltaste Ctrl+D; bei Pipes wird stdin nach den angenommenen Bytes geschlossen. Dieselben Aktionen funktionieren über den kompakten Dispatcher und Code Mode.

`job_logs`, `job_wait` und `write_stdin` liefern **nur ungelesene Ausgabe**, standardmäßig höchstens 8.000 Bytes. Ohne `cursor` geht es an der gespeicherten Position weiter; `nextCursor` ermöglicht eine ausdrückliche Fortsetzung. Positionen überleben Backend-Neustarts. `hasMore` zeigt weitere gespeicherte Bytes an. Für späte Fehler führt `tail:true` oder `cursor:tailCursor` direkt ans Ende, ohne Fortschrittszeilen vollständig abzufragen. Aus dem begrenzten Speicher entfernte Zwischenbereiche werden mit ihrer Bytezahl gekennzeichnet. Ein ausdrückliches Zurücklesen verschiebt die normale Leseposition nicht zurück. Die vollständige begrenzte UI-Logansicht bleibt erhalten. `job_wait` wartet standardmäßig bis zu 30 Sekunden; `wait_time_ms` begrenzt einzelne Warteaufrufe. Neue Nutzerinstruktionen unterbrechen sie.

Abgeschlossene Hintergrundjobs erscheinen als kurze Statusdaten im nächsten Modellkontext. Sie starten keinen bereits ruhenden Agenten und keine zusätzliche kostenpflichtige Modellrunde. Unterbrechungen durch Failover behalten ihre ausdrückliche Agentenbenachrichtigung. Notizen überleben Kontextleerung, Rotation und automatische Verdichtung bei 85 Prozent. Subagenten dürfen keine weiteren Subagenten erstellen. In der WebApp lässt sich zwischen Hauptagent, Approver und Subagenten wechseln. `AGENTS.md` und `AGENTS.override.md` werden bewusst nicht als Instruktionsquellen geladen; maßgeblich bleiben Gespräch und gespeicherte Konfiguration.

Der Vergleichsrunner unter `backend/cmd/harness-eval` verwendet dieselbe Harness mit unterschiedlichen konfigurierten Providern/Modellen und einem gemeinsamen Approver. Die drei festen Aufgaben prüfen eine gezielte Codekorrektur, Eingaben an einen laufenden Prozess und Diagnose großer Jobausgaben. Jede Wiederholung erhält ein neues Remote-Verzeichnis und einen eigenen Agenten beziehungsweise eine eigene VM. Ein separater Prüfer kontrolliert tatsächliche Dateien und Ergebnisse; eine Modellbehauptung genügt nicht.

`backend/harnesseval/example-plan.json` enthält die Modell-/Maschinenauswahl, Wiederholungen, Zeitlimit und Budget logischer Modellanfragen einschließlich Approver und Verdichtung. Der CLI arbeitet ausschließlich mit einer temporären Kopie einer geschlossenen Datenbank-Sicherung; Originalprojekte und die Quelldatei werden nicht bearbeitet. Device-Sitzungen müssen für den Lauf noch gültig sein. Kopierte Refresh-Tokens werden nicht zur Rotation des ursprünglichen Kontos verwendet.

```sh
cd backend
go run ./cmd/harness-eval \
  -snapshot /tmp/zentrale-evaluation-snapshot.duckdb \
  -plan harnesseval/example-plan.json \
  -report /tmp/zentrale-evaluation-after.json
```

Vor einem echten Lauf die Platzhalter im Plan durch konfigurierte Kennungen ersetzen. Der Aufruf verwendet die ausgewählten Provider-Konten und kann Kosten verursachen. Der private Bericht enthält Erfolg, Aufgaben-Hash, Revision/Änderungsstand des Builds, Werkzeugaufrufe, Token-/Cache-Nutzung und Kosten, getrennte Angaben für Hauptagent und Approver sowie Gesamtsummen einschließlich Unteragenten. Setup, Agentenlauf, Verifikation, Aufräumen und Provider-Wartezeit werden getrennt gemessen. Geschätzte Tokens/Kosten, fehlende Preise und fiktive Verbrauchskosten bei Pauschaltarifen sind gekennzeichnet. Für Vergleiche denselben Plan, Approver und Aufgaben-Hash verwenden; `label` und getrennte Berichtsdateien benennen die Varianten. Die lokale Regression prüft 21 Kombinationen aus drei Aufgaben und sieben Provider-Arten mit simulierten Modellentscheidungen über echte SSH-Testpeers. Das sind Funktionsprüfungen des Runners und keine gemessenen Qualitäts- oder Kostenvergleiche echter Modelle.

Die Seitenleiste bündelt Projekte und ihre Unterhaltungen. Sie lässt sich am Desktop einklappen und öffnet auf schmalen Bildschirmen als Schublade. Provider, Maschinen, SSH-Schlüssel sowie Sprache und Design sind über das Einstellungsmenü erreichbar. **Neue Unterhaltung** startet direkt im aktuellen Projekt; außerhalb eines Projekts wird bei Bedarf nur das Projekt ausgewählt. Die erste Nutzeranweisung liefert den Titel ohne zusätzliche Modellanfrage. Über das Drei-Punkte-Menü lässt sich eine ruhende Unterhaltung umbenennen. Entwürfe bleiben beim Wechsel zwischen Unterhaltungen und beim Neuladen im aktuellen Browser-Tab erhalten.

Die Projektansicht konzentriert sich auf den Chat. Der kompakte Chat-Kopf bleibt beim Scrollen sichtbar; ein Menü am Agentennamen wechselt zwischen Agenten, ohne ein separates Auswahlfeld einzublenden. Sie zeigt direkt einen zusammenhängenden Arbeitsverlauf mit kompakten Werkzeugzeilen; Quellen und Editorverbindungen werden über das Projektmenü verwaltet, Freigaben, abgeschlossene Jobs und Kontext in einem Dialog für Agentendetails. Modell und ermittelte Sandbox stehen dezent unter der Eingabe; Kosten und Ausführungsumgebung in den Details. Befehle und Pfade stehen direkt an der Aktion; Aufgeklappte Aktionen zeigen relevante Inhalte und Fehler ohne technische Kennungen oder wiederholte Statusfelder. Für Fehlersuche können unter **Darstellung und Sprache** ausdrücklich **Diagnosedetails im Verlauf** aktiviert werden. Der Approver gruppiert Anfrage, tatsächliche Freigabe und Begründung; eine bloße Modellantwort gilt nicht als erfasste Freigabe. Antworten unterstützen Markdown, kopierbare Codeblöcke und Tabellen mit eigenem Scrollbereich. Der Verlauf verwendet ausschließlich den normalen Seitenscroll. Die Eingabe bleibt am unteren Fensterrand erreichbar: `Enter` sendet, `Shift+Enter` fügt einen Zeilenumbruch ein. Neue Einträge werden automatisch verfolgt, solange man am Ende des Verlaufs bleibt. Beim Zurückscrollen bleibt die Leseposition erhalten; ein kleiner schwebender Pfeil über der Eingabe führt zu den neuesten Einträgen zurück. Entwürfe, SSH-Prüfung, Rückfragen und der schreibgeschützte Approver-Verlauf bleiben erhalten.

Die Oberfläche verwendet HeroUI-Komponenten mit deren nativer Gestaltung, ergänzt um Layouts und wenige gemeinsame Theme-Tokens. Verwaltungsgruppen nutzen die native `secondary`-Fläche; Formfelder haben kontrastgeprüfte Konturen, während Karten und Felder ohne zusätzliche Schatten auskommen. Seitenbeschreibungen nutzen auch auf schmalen Bildschirmen die gesamte verfügbare Breite.

Ersteinrichtung, Projektanlage und Maschinenanlage führen durch kurze Schritte mit Fortschrittsanzeige und Zurücknavigation. Schrittprüfungen und Serverfehler fokussieren das betroffene Feld, auch in einem früheren oder aufgeklappten Schritt. Entwürfe bleiben im geöffneten Flow erhalten; Projekt- und Maschinendaten werden erst am Ende gespeichert. Im SSH-Schritt können Schlüssel ausdrücklich erzeugt und kopiert werden. Änderungen bestehender Konfigurationen bleiben direkt erreichbar.

Projektzeilen öffnen über ihre ganze Inhaltsfläche; ungespeicherte Modell- und Commit-Einstellungen sind gekennzeichnet und lassen sich verwerfen. Icons stammen einheitlich aus Lucide. Sekundäre Kartenaktionen liegen in einem einheitlichen Drei-Punkte-Menü. Primäre Aufgaben wie Öffnen, Anmelden und Verbindungsprüfung bleiben direkt erreichbar. Werkzeugstatus und Fehler sind durch Farbe, Symbol und Text unterscheidbar.

`write_file`, `delete_file` und `apply_patch` erfassen tatsächliche Vorher-/Nachher-Inhalte als separate UI-Datensätze. Der Verlauf zeigt daraus Git-artige Diffs mit Zeilennummern, Kontext und Plus-/Minus-Zählern. Geschriebener Quellcode, Codeblöcke und Diffs erhalten Syntax-Highlighting anhand von Dateiname beziehungsweise Sprache; Kopieren übernimmt den unveränderten Inhalt. Lange Diffs werden über „Mehr anzeigen“ erweitert und scrollen nur bei langen Zeilen horizontal. Binärdateien, große Dateien und fehlende Leserechte erhalten einen Hinweis. Frühere Nachrichten und beliebige Änderungen über Shellbefehle erhalten keinen nachträglich erfundenen Diff. Die Diff-Daten werden weder dem Hauptmodell noch dem Approver als zusätzliche Dateilesefreigabe übergeben.

```sh
make test        # Backend-/Dateiwerkzeugtests und vollständiger Typecheck
make test-editor # Tests für den entfernten Editor-Bootstrap (Python 3)
make race        # Backendtests mit Race Detector
make check       # go vet und Formatprüfung
make e2e         # Chromium-Browsertests mit lokaler Provider-Fixture
make build       # Frontend-Produktionsbuild und Go-Binary
```

Vor dem ersten Browserlauf: `cd frontend && npx playwright install chromium`. Alternativ kann `CHROMIUM_PATH` auf einen installierten Chromium zeigen. Die Tests starten eine temporäre In-Memory-Instanz auf Port 18080. Login-Protokolle, Modellkataloge und Inferenz werden mit lokalen Testservern geprüft; echte Provider-Konten, produktive SSH-Maschinen und ein MicroVM-Runner benötigen die jeweiligen Zugangsdaten beziehungsweise Laufzeitumgebung für Live-Tests.

Die API akzeptiert maximal Tiefe 4, begrenzt Listen und projiziert angeforderte Ressourcenfelder in SQL. Browserprüfungen umfassen den Einrichtungsablauf, unterschiedliche Modell-Defaults, Approval-Ablehnungen, Dialogfokus, Fehlerbehebung, deutsche und englische Texte, beide Themes, mobile und Desktop-Layouts sowie Zoom und Browsernavigation.

`cd frontend && npm run test:ui` erstellt zusätzlich eine Screenshot-Galerie unter `frontend/artifacts/ui/index.html`. Der Durchlauf verwendet isolierte UI-Fixtures, deckt Seiten und Dialoge auf Desktop, Tablet und Mobilgeräten ab und prüft beide Sprachen und Themes sowie Lade-, Leer- und Fehlerzustände. Er überprüft Feldhöhen, Formularabstände, verschachtelten Dialogfokus und horizontales Überlaufen. Die funktionalen Browsertests prüfen weiterhin die Integration mit dem Backend. Screenshots und Prüfberichte werden lokal erzeugt und nicht eingecheckt.

Für einen echten VS-Code-Browsertest lassen sich die zur Release-Datei passenden, entpackten offiziellen Web- und Serverarchive mit `ZENTRALE_VSCODE_SMOKE_WEB` und `ZENTRALE_VSCODE_SMOKE_SERVER` angeben. `cd backend && go test ./webapi -run TestPublishedVSCodeWorkbenchOverSSH -v` prüft damit einen isolierten SSH-Peer, den eigenen Browser-Tab, vollständige Fenstergröße, Dateizugriff, Terminal, TypeScript-Diagnosen und weiter verfügbare zentrale Webdateien nach einem SSH-Ausfall. `CHROMIUM_PATH` wird auch hier unterstützt. `ZENTRALE_VSCODE_SMOKE_SHELL=fish` prüft denselben Ablauf mit fish als SSH-Login-Shell. Der Test verändert keine produktiven Daten.

Der echte MicroVM-Test verwendet eine **wegwerfbare Datenbankkopie** mit der Maschinen- und SSH-Konfiguration. `ZENTRALE_MICROVM_TEST_SNAPSHOT=/tmp/snapshot.duckdb ZENTRALE_MICROVM_TEST_MACHINE=machine-id go test ./remote -run '^TestManagedMicroVMLive$' -v` (aus `backend/`) prüft auf der zugeordneten Maschine tatsächliche KVM-Isolation, eine gleichbleibende Boot-ID, echte Fedora-Paketinstallationen, getrennte Agenten-Umgebungen, individuelle Netzfreigaben, große Dateiwerkzeuge und stdin/stderr/Exitstatus. Der offizielle VS-Code-Server muss dabei Dateien derselben Fedora-VM sehen, und deren Terminal-Geräte werden geprüft. Er prüft außerdem, dass Stoppen und SSH-Abbruch nur den Job beenden, ein Reset Projektdateien und andere Agenten bewahrt und die Gastplatte nach erneutem Start erhalten bleibt. Remote-Dateien und Gastplatten gehören ausschließlich zu neuen privaten Testverzeichnissen beziehungsweise Test-Agenten und werden anschließend entfernt. Mit `ZENTRALE_MICROVM_TEST_STORAGE_ROOT=/proj/ciptmp/<benutzer>` wird zusätzlich ein eigener Test-Projektordner auf diesem Dateisystem eingebunden und wieder entfernt. Optional prüft `ZENTRALE_MICROVM_TEST_QUOTA_COMMAND=/usr/bin/cip-quota` die feste Host-Abfrage. Mit `ZENTRALE_MICROVM_TEST_VM_TYPE=q35` prüft derselbe Test den q35-Backend ohne physische GPU-Zuordnung; er ändert dafür nur die wegwerfbare Datenbankkopie. Ohne die beiden Variablen wird dieser Live-Test übersprungen. Die lokalen Python-Tests des Runners und Editor-Relays sind Bestandteil von `make test`.

`TestVSCodeWorkbenchInsideMicroVMLive` in `webapi` ergänzt den vollständigen Browserablauf mit Terminal-Commit, projektbezogener Identität und TypeScript-Diagnostik. Er benötigt `ZENTRALE_VSCODE_MICROVM_TEST_SNAPSHOT` (eine separate wegwerfbare Datenbankkopie), `ZENTRALE_MICROVM_TEST_MACHINE` sowie `ZENTRALE_VSCODE_SMOKE_WEB` (das entpackte passende offizielle Webarchiv). Auch dieser Test legt ausschließlich eigene Remote-Dateien und eine eigene Test-VM an und räumt sie anschließend auf.
