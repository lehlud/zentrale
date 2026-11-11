// German source labels are stable translation keys. Keep technical identifiers,
// user content and provider model names outside this dictionary.
export const english: Record<string, string> = {
  Denkintensität: "Reasoning effort",
  "Standard (regulär)": "Default (regular)",
  "Provider-Standard": "Provider default",
  "Auswahl übernehmen": "Apply selection",
  "Die unterstützten Denkstufen dieses Modells sind nicht bekannt. Du kannst sie manuell setzen; der Provider kann nicht unterstützte Werte ablehnen.":
    "This model's supported reasoning levels are unknown. You can set them manually; the provider may reject unsupported values.",
  // Goals and completion review.
  Aktiv: "Active",
  Arbeitsziel: "Goal",
  "Der Auto-Approver hat noch offene Arbeit festgestellt.":
    "The auto-approver identified remaining work.",
  Zielstatus: "Goal status",
  "Ziel setzen": "Set goal",
  "Ziel ansehen": "View goal",
  "Zielstatus anfordern": "Request goal status",
  "Zielabschluss prüfen": "Review goal completion",
  "Ziel erreicht": "Goal achieved",
  Blockiert: "Blocked",
  Pausiert: "Paused",
  Verworfen: "Discarded",
  "Weitere Arbeit nötig": "More work required",
  "Durch neue Anweisung überholt": "Superseded by new instruction",
  "Abschluss wird geprüft": "Completion under review",
  "Auto-Approver prüft den Zielabschluss …":
    "Auto-approver is reviewing goal completion …",
  "Geprüfte Belege": "Verified evidence",
  "Ziel pausieren": "Pause goal",
  "Ziel fortsetzen": "Resume goal",
  "Ziel verwerfen": "Discard goal",
  Ausgeführt: "Executed",
  "Unteragent anweisen": "Steer subagent",
  "Noch kein Ziel gesetzt": "No goal set yet",
  "Die unabhängige Abschlussprüfung steht noch aus.":
    "Independent completion review is still pending.",
  "Keine Abschlussprüfung erfasst": "No completion review recorded",

  // Input delivery and execution progress.
  Nachrichtenwarteschlange: "Message queue",
  "Wartende Nachrichten": "Pending messages",
  "Wird gesendet …": "Sending …",
  "Übernahme noch nicht bestätigt. Wird erneut versucht.":
    "Receipt not confirmed yet. Will retry.",
  "Auf dem Server eingereiht": "Queued on the server",
  "Vom Server übernommen": "Accepted by the server",
  "Nachricht erneut senden": "Retry sending message",
  "Noch nicht übernommen": "Not accepted yet",
  "Verarbeitung fortsetzen": "Resume processing",
  "Wartende Nachricht bearbeiten": "Edit queued message",
  "Wartende Nachricht entfernen": "Remove queued message",
  "Nachricht übernommen …": "Message accepted …",
  "Verbindung zur Maschine wird hergestellt …": "Connecting to the machine …",
  "Projektverzeichnis wird vorbereitet …": "Preparing project directory …",
  "Repository wird vorbereitet …": "Preparing repository …",
  "Sandbox wird geprüft …": "Checking sandbox …",
  "Umgebung wird wiederhergestellt …": "Restoring environment …",
  "Sandbox wird vorbereitet …": "Preparing sandbox …",
  Subagent: "Subagent",

  // Machines, pools and workspace setup.
  "Zufällige Stichprobe: 10 % der Rechner, mindestens 3, höchstens 8. Kleine Pools werden vollständig geprüft.":
    "Random sample: 10% of machines, at least 3, at most 8. Small pools are checked in full.",
  "Verbindungen werden geprüft …": "Checking connections …",
  "Pool-Verbindungsprüfung": "Pool connection check",
  "Stichprobe geprüft": "Sample checked",
  "{successful} von {checked} geprüften Rechnern bereit ({total} im Pool).":
    "{successful} of {checked} checked machines ready ({total} in the pool).",
  "VM-Typ": "VM type",
  "MicroVM (CPU)": "MicroVM (CPU)",
  "q35 (PCI/GPU)": "q35 (PCI/GPU)",
  "GPU-PCI-Geräte": "GPU PCI devices",
  "Kommagetrennte PCI-Adressen der GPU und ihrer zugehörigen Funktionen. Benötigt q35 sowie eine administrative VFIO-Freigabe auf der Fernmaschine und passende Treiber im Gast.":
    "Comma-separated PCI addresses of the GPU and its companion functions. Requires q35, administrator-provided VFIO access on the remote machine, and matching guest drivers.",
  "Pool-Schlüssel": "Pool key",
  "Den Anmeldeschlüssel verwaltet die Zentrale automatisch. Hostschlüssel und CAs können unter known_hosts hinterlegt werden.":
    "Zentrale manages the login key automatically. Host keys and CAs can be included under known_hosts.",
  "Den öffentlichen Pool-Schlüssel in ~/.ssh/authorized_keys der Pool-Mitglieder hinterlegen. Bei gemeinsamem Home genügt das einmal.":
    "Add the public pool key to ~/.ssh/authorized_keys on the pool members. With a shared home, this only needs to be done once.",
  "Prozessausgabe abrufen": "Read process output",
  Läuft: "Running",
  "Live-Antwort": "Live response",
  "Agent stoppen": "Stop agent",
  "Subagenten von {name}": "Subagents of {name}",
  "Antwort unterbrochen": "Response interrupted",
  "Antwort wird vorbereitet …": "Preparing response …",
  "Schreibt …": "Writing …",
  "Arbeitet …": "Working …",
  "Ausgabe öffnen: {command}": "Open output: {command}",
  "Job beenden: {command}": "Stop job: {command}",
  "Code-Editor wird geladen …": "Loading code editor …",
  "Der Code-Editor konnte nicht geladen werden. YAML kann weiterhin im Textfeld bearbeitet werden.":
    "The code editor could not be loaded. You can still edit YAML in the text field.",
  "Automatisch verwalteter Arbeitsbereich": "Automatically managed workspace",
  "workspace_root muss auf allen Pool-Mitgliedern dieselbe persistente Freigabe bezeichnen. Bei gemeinsamem Home ist auch ein Pfad unter ~/ möglich.":
    "workspace_root must refer to the same persistent shared storage on every pool member. A path below ~/ also works with a shared home.",
  "Persistentes Projektverzeichnis": "Persistent project directory",
  Projektverzeichnis: "Project directory",
  "Alle neuen Projekte erhalten hier automatisch einen eigenen Ordner. ~/ bezieht sich auf den SSH-Benutzer.":
    "Each new project automatically gets its own folder here. ~/ refers to the SSH user’s home.",
  "Arbeitsbereich wird automatisch angelegt":
    "Workspace is created automatically",
  "Der Arbeitsbereich wird automatisch auf der gewählten Maschine oder im Pool angelegt.":
    "The workspace is created automatically on the selected machine or in the pool.",
  "workspace root must be an absolute directory other than / or a directory below ~/":
    "Enter an absolute directory other than /, or a directory below ~/.",
  "workspace root contains invalid characters":
    "The project directory contains invalid characters.",

  // Guided setup.
  Weiter: "Continue",
  Zurück: "Back",
  "Commit-Identität anpassen": "Customize commit identity",
  "Schritt {current} von {total}": "Step {current} of {total}",
  Einrichtungsschritte: "Setup steps",
  "Wie heißt dein Projekt?": "What is your project called?",
  "Wähle einen Namen und das Verzeichnis auf der Arbeitsmaschine.":
    "Choose a name and its directory on the work machine.",
  "Wo sollen die Agenten arbeiten?": "Where should agents work?",
  "Wähle mindestens eine Fernmaschine oder einen Pool.":
    "Select at least one remote machine or pool.",
  "Welches Repository gehört dazu?":
    "Which repository belongs to this project?",
  "Optional. Du kannst auch ohne Repository fortfahren und es später verbinden.":
    "Optional. You can continue without a repository and connect one later.",
  "Wer soll als Commit-Autor erscheinen?":
    "Who should appear as the commit author?",
  "Globale Identität: {name} <{email}>.": "Global identity: {name} <{email}>.",
  "Wie erreichen wir die Maschine?": "How do we reach the machine?",
  "Name und SSH-Adresse der Fernmaschine festlegen.":
    "Set the remote machine’s name and SSH address.",
  "Wie meldet sich die Zentrale an?": "How should Zentrale sign in?",
  "Wähle einen SSH-Schlüssel und bei Bedarf Jump-Hosts.":
    "Choose an SSH key and, if needed, jump hosts.",
  "Wie sollen Agenten hier arbeiten?": "How should agents work here?",
  "Automatisch wählt die stärkste verfügbare Sandbox.":
    "Automatic selects the strongest available sandbox.",
  "Hinterlege den öffentlichen Schlüssel auf der Maschine in ~/.ssh/authorized_keys.":
    "Add the public key to ~/.ssh/authorized_keys on the machine.",
  "Neuen SSH-Schlüssel erzeugen": "Generate a new SSH key",
  "Zugriff sichern": "Secure access",
  "Lege das Passwort für den Zugang zur Zentrale fest.":
    "Set the password for access to Zentrale.",
  "Mit welchem Namen und welcher E-Mail sollen Agenten ihre Commits erstellen?":
    "Which name and email should agents use for their commits?",

  // Tool activity and repository access.
  "Prozess fortsetzen": "Continue process",
  Prozesseingabe: "Process input",
  "Eingabe beenden (EOF)": "End input (EOF)",
  "Neue Prozessausgabe abrufen": "Read new process output",
  "MCP-Server verbinden": "Connect MCP server",
  "MCP-Verbindungen prüfen": "Check MCP connections",
  "MCP-Werkzeuge entdecken": "Discover MCP tools",
  "MCP-Werkzeug ausführen": "Run MCP tool",
  "MCP-Verbindung beenden": "Disconnect MCP server",
  "MCP-Server verbunden.": "MCP server connected.",
  "MCP-Verbindung beendet.": "MCP connection closed.",
  "Keine MCP-Server verbunden.": "No MCP servers connected.",
  "Keine passenden Werkzeuge.": "No matching tools.",
  "Weitere Werkzeuge verfügbar.": "More tools are available.",
  "Binärinhalt wird nicht als Text angezeigt.":
    "Binary content is not displayed as text.",
  "Werkzeuge kombinieren": "Combine tools",
  "Werkzeug suchen": "Find tool",
  "Originalanweisungen lesen": "Read original instructions",
  "Ausgeführte Werkzeuge": "Executed tools",
  "Ab Zeile {start} · {total} Zeilen insgesamt":
    "From line {start} · {total} lines in total",
  "Keine Zeilen in diesem Bereich.": "No lines in this range.",
  Arbeitsplan: "Work plan",
  "Dateien ändern": "Edit files",
  "Plan aktualisieren": "Update plan",
  "In Arbeit": "In progress",
  Ausstehend: "Pending",
  "Einmalig eine OAuth-App mit api-Berechtigung registrieren. GitLab muss den Device-Flow unterstützen.":
    "Register an OAuth app with api scope once. GitLab must support the device flow.",
  "Repositories aktualisieren": "Refresh repositories",
  "Repository ändern": "Change repository",
  "Projekte: {names}": "Projects: {names}",
  "Repo-Zugriff besteht. Aktuell keinem Projekt zugewiesen.":
    "Repository access remains active. No project currently uses this key.",
  "Repo-Zugriff beim Anbieter widerrufen und den Schlüssel dauerhaft löschen?":
    "Revoke repository access at the provider and permanently delete the key?",
  "Zugriff widerrufen": "Revoke access",
  "Git-Verbindungen": "Git connections",
  "Repositories auswählen oder erstellen. Die Zentrale verwaltet den passenden Deploy-Key automatisch.":
    "Select or create repositories. Zentrale manages the matching deploy key automatically.",
  "OAuth-App konfigurieren": "Configure OAuth app",
  "{name} verbinden": "Connect {name}",
  "GitLab-Adresse": "GitLab address",
  "OAuth-App Client-ID": "OAuth app client ID",
  "Client-Secret (optional)": "Client secret (optional)",
  "Nur für vertrauliche OAuth-Apps. Leer lassen, um ein vorhandenes Secret zu behalten.":
    "Only for confidential OAuth apps. Leave empty to keep the existing secret.",
  "Einmalig eine eigene OAuth-App registrieren und den Device-Flow aktivieren.":
    "Register your own OAuth app once and enable the device flow.",
  "OAuth-App registrieren ↗": "Register OAuth app ↗",
  "Weiter zur Anmeldung": "Continue to sign in",
  "Warten auf Bestätigung …": "Waiting for confirmation …",
  "Du kannst jetzt Repositories auswählen und erstellen.":
    "You can now select and create repositories.",
  "Anmeldung neu starten": "Restart sign-in",
  Fertig: "Done",
  "Repository auswählen": "Select repository",
  "Repository (optional)": "Repository (optional)",
  "Auswahl entfernen": "Clear selection",
  "Git-Dienst": "Git service",
  "Repository erstellen": "Create repository",
  "Ein eigener Deploy-Key erlaubt Lesen und Schreiben. Vorhandene Repo-Schlüssel werden wiederverwendet.":
    "A dedicated deploy key allows read and write access. Existing repository keys are reused.",
  "Repositories suchen": "Search repositories",
  "Name oder Organisation": "Name or organization",
  "Keine passenden Repositories mit Berechtigung für Deploy-Keys":
    "No matching repositories with permission to manage deploy keys",
  Repositories: "Repositories",
  Privat: "Private",
  Öffentlich: "Public",
  "Repository-Name": "Repository name",
  "Konto oder Organisation": "Account or organization",
  "Beschreibung (optional)": "Description (optional)",
  "Privates Repository": "Private repository",
  "Erweiterter API-Zugang": "Advanced API access",
  "Repository verbinden": "Connect repository",
  "Quelle manuell hinzufügen": "Add source manually",
  "Repo-Zugriff widerrufen und Schlüssel löschen":
    "Revoke repository access and delete key",
  "Nicht verwendet": "Not in use",
  "Zugang wirklich trennen?": "Disconnect this account?",
  "OAuth client ID is required": "Enter the OAuth app client ID.",
  "connect the Git account first": "Connect your Git account first.",
  "repository requires permission to manage deploy keys":
    "This repository requires permission to manage deploy keys.",
  "SSH key is assigned to another purpose":
    "This SSH key is assigned to another purpose.",
  "SSH key is already used for another purpose":
    "This SSH key is already used for another purpose.",
  "SSH key is still used by a project or machine":
    "Remove the key’s project or machine references first.",
  "configure the OAuth app client ID first":
    "Configure the OAuth app client ID first.",
  "Git account session expired; reconnect":
    "Your Git session has expired. Connect the account again.",
  "Git login expired; start again": "The sign-in has expired. Start again.",
  "Git account session could not be refreshed; reconnect":
    "The Git session could not be renewed. Connect the account again.",

  // Conversation, editor and application controls.
  Agent: "Agent",
  "Aktionen für {name}": "Actions for {name}",
  Agentensteuerung: "Agent controls",
  "Agent wechseln": "Switch agent",
  "Agent wechseln: {name}": "Switch agent: {name}",
  "Neueste ↓": "Latest ↓",
  Menü: "Menu",
  Agentaktionen: "Agent actions",
  Agentendetails: "Agent details",
  Projektaktionen: "Project actions",
  "Quellen verwalten": "Manage sources",
  Editorverbindung: "Editor connection",
  "Laufende Jobs": "Running jobs",
  Agentenverlauf: "Agent transcript",
  "Dieser Verlauf ist schreibgeschützt.": "This transcript is read-only.",
  "Nachrichten und Werkzeugaktivität": "Messages and tool activity",
  "Beschreibe unten dein Ziel. Hier erscheinen die Arbeitsschritte und Ergebnisse des Agenten.":
    "Describe your goal below. The agent’s steps and results will appear here.",
  "Der Agent arbeitet …": "The agent is working …",
  "Enter senden · Shift+Enter Zeilenumbruch":
    "Enter to send · Shift+Enter for a new line",
  "Zu neuesten Einträgen ↓": "Latest entries ↓",
  Werkzeugaufruf: "Tool call",
  Rückfrage: "Question",
  Code: "Code",
  Kopieren: "Copy",
  Kopiert: "Copied",
  "Code kopiert": "Code copied",
  "Codeblock: {language}": "Code block: {language}",
  "Kopieren nicht möglich. Den Code markieren und manuell kopieren.":
    "Could not copy. Select the code and copy it manually.",
  "Bild öffnen": "Open image",
  "Tabelle aus der Agentenantwort": "Table from the agent response",
  "VS Code öffnen": "Open VS Code",
  "VS Code in neuem Tab öffnen ↗": "Open VS Code in a new tab ↗",
  "VS Code öffnet sich in einem eigenen Tab. Die Oberfläche wird von Zentrale geladen; Terminal, Dateien und Sprachdienste laufen über SSH auf der Projektmaschine.":
    "VS Code opens in its own tab. Zentrale serves the interface; terminal, files and language services run over SSH on the project machine.",
  "Remote-Agent verbunden mit {machine}": "Remote agent connected to {machine}",
  "Die Weboberfläche wird in Zentrale vorbereitet und der Remote-Agent über SSH verbunden. Der erste Start kann einige Minuten dauern.":
    "Zentrale is preparing the web interface and connecting the remote agent over SSH. The first launch can take a few minutes.",
  "Zurück zum Projekt": "Back to project",
  "Ohne Sandbox": "No sandbox",
  "noch nicht ermittelt": "not checked yet",
  "Ermittlung fehlgeschlagen": "detection failed",
  "Sandbox: {selection}": "Sandbox: {selection}",
  "Zuletzt ermittelt: {time}": "Last checked: {time}",
  Benachrichtigungen: "Notifications",
  "Benachrichtigung schließen": "Dismiss notification",
  "Verbindung unterbrochen": "Connection interrupted",
  "Der Server ist gerade nicht erreichbar. Wir verbinden automatisch neu. Deine Eingaben bleiben erhalten.":
    "The server is currently unreachable. We’ll reconnect automatically. Your input is preserved.",
  "Jetzt verbinden": "Reconnect now",
  "Wieder verbunden": "Connection restored",
  "Die Daten werden aktualisiert. Fehlgeschlagene Aktionen bei Bedarf erneut ausführen.":
    "Your data is being refreshed. Retry failed actions if needed.",
  "Warten auf Verbindung …": "Waiting for connection …",
  Schließen: "Close",
  "SSH-Port": "SSH port",
  "Preis unbekannt": "Price unknown",
  System: "System",
  "API-Basis-URL (optional für API-Modus)":
    "API base URL (optional in API mode)",
  "API-Schlüssel (leer = vorhandenen behalten)":
    "API key (leave empty to keep the current key)",
  Abbrechen: "Cancel",
  Abgelehnt: "Denied",
  Abgeschlossen: "Completed",
  Abmelden: "Sign out",
  "Abo über Inferenz-Gateway": "Subscription through inference gateway",
  Abrechnung: "Billing",
  "Agent anlegen": "Create agent",
  Agenten: "Agents",
  Agentenansicht: "Agent view",
  Agentenbereich: "Agent workspace",
  Anfrage: "Request",
  Anmelden: "Sign in",
  Antworten: "Answer",
  Arbeitet: "Working",
  "Auf einer zugewiesenen Maschine einen code-server-Port konfigurieren.":
    "Configure a code-server port on an assigned machine.",
  "Aufgabe oder neue Instruktion": "Task or new instruction",
  "Ausgabe öffnen": "Open output",
  Automatisch: "Automatic",
  Bearbeiten: "Edit",
  Benutzer: "User",
  Bereit: "Ready",
  "Cache: USD / 1 Mio. Tokens": "Cached input: USD / 1M tokens",
  "Container-Runtime freigeben": "Enable container runtime",
  "Die Passwörter stimmen nicht überein.": "The passwords do not match.",
  Du: "You",
  "Editor verbinden": "Connect editor",
  "Eigene Antwort": "Your answer",
  "Eigene Schlüssel": "Your keys",
  "Eigenschaften (kommagetrennt)": "Properties (comma separated)",
  "Eine Aufgabe starten": "Start a task",
  Einstellungen: "Settings",
  Entfernen: "Remove",
  "Entfernter code-server-Port": "Remote code-server port",
  "Erneut verbinden": "Reconnect",
  Fehler: "Error",
  Fehlgeschlagen: "Failed",
  Fernmaschinen: "Remote machines",
  "Gemeinsames Home": "Shared home directory",
  Genehmigt: "Approved",
  Gestoppt: "Stopped",
  Hauptagent: "Main agent",
  Hauptnavigation: "Main navigation",
  "In neuem Tab öffnen ↗": "Open in a new tab ↗",
  "Input: USD / 1 Mio. Tokens": "Input: USD / 1M tokens",
  "Instruktion senden": "Send instruction",
  "Jump-Hosts (in Reihenfolge)": "Jump hosts (in order)",
  "Keine Fernmaschinen": "No remote machines",
  "Keine Freigabeanfragen": "No approval requests",
  "Keine Jobs": "No jobs",
  "Keine Pools": "No pools",
  "Keine Provider konfiguriert": "No providers configured",
  "Keine Quellen": "No sources",
  "Keine SSH-Schlüssel": "No SSH keys",
  Kontext: "Context",
  "Kontextfenster (Tokens)": "Context window (tokens)",
  Laden: "Loading",
  "Maschine bearbeiten": "Edit machine",
  "Maschine hinzufügen": "Add machine",
  Maschinen: "Machines",
  Maschinentyp: "Machine type",
  "MicroVM-Runner (absoluter Pfad)": "MicroVM runner (absolute path)",
  "Eigener MicroVM-Runner (optional)": "Custom MicroVM runner (optional)",
  "Leer lassen, um den integrierten QEMU/KVM-Runner zu verwenden.":
    "Leave empty to use the built-in QEMU/KVM runner.",
  "microvm_command must be an absolute path when supplied":
    "Enter an absolute path for the custom MicroVM runner, or leave the field empty.",
  "Modell-ID": "Model ID",
  Modellantwort: "Model response",
  "Neue Fernmaschine": "New remote machine",
  "Neuer Agent": "New agent",
  "Neues Projekt": "New project",
  "Nicht verbunden": "Not connected",
  "Noch keine Agenten": "No agents yet",
  "Noch keine Ausgabe": "No output yet",
  "Noch keine Projekte": "No projects yet",
  "Nur Auto-Approval": "Approval only",
  "Nur als alleiniger Benutzer": "Only when no other users are logged in",
  "Nur lesend": "Read only",
  "Nur lesend einbinden": "Use as a read-only source",
  "Output: USD / 1 Mio. Tokens": "Output: USD / 1M tokens",
  Passwort: "Password",
  "Passwort festlegen": "Set your password",
  "Passwort speichern": "Save password",
  "Passwort wiederholen": "Confirm password",
  "Pauschale Lesefreigabe angefragt": "Blanket read permission requested",
  "Pool-Konfiguration": "Pool configuration",
  "Preise sind bekannt": "Prices are known",
  "Projekt anlegen": "Create project",
  "Projekt bearbeiten": "Edit project",
  "Projekt nicht gefunden": "Project not found",
  Projektbereich: "Project workspace",
  Projekte: "Projects",
  "Projektverzeichnis auf der Fernmaschine":
    "Project directory on the remote machine",
  "Provider hinzufügen": "Add provider",
  "Provider konfigurieren": "Configure provider",
  "Quelle hinzufügen": "Add source",
  "Quelle konfigurieren": "Configure source",
  Quellen: "Sources",
  "SSH-Schlüssel": "SSH key",
  "SSH-Schlüssel erzeugen": "Generate SSH key",
  Schlüssel: "Keys",
  Schlüsselart: "Key type",
  Speichern: "Save",
  Starten: "Start",
  Stoppen: "Stop",
  Trennen: "Disconnect",
  Unterbrochen: "Interrupted",
  "VSCode auf der Fernmaschine": "VSCode on the remote machine",
  "Verbindung prüfen": "Test connection",
  Verbunden: "Connected",
  "Verdichteter Kontext": "Compacted context",
  Verlauf: "Conversation",
  Vertrauen: "Trust",
  "Vertrauen entziehen": "Revoke trust",
  "Vertrauenswürdige Hosts": "Trusted hosts",
  Verwerfen: "Dismiss",
  Verzeichnisname: "Directory name",
  Vorherige: "Previous",
  "Was soll der Agent erledigen?": "What should the agent do?",
  Weitere: "More",
  Werkzeug: "Tool",
  "Werkzeugaufrufe / Reasoning": "Tool calls / reasoning",
  Werkzeugergebnis: "Tool result",
  "YAML importieren / aktualisieren": "Import / update YAML",
  "Zuerst einen Provider konfigurieren": "Configure a provider first",
  fiktiv: "equivalent",
  real: "actual",
  "Öffentlichen Schlüssel kopieren": "Copy public key",
  "Öffentlicher Host-Schlüssel": "Public host key",
  Öffnen: "Open",
  "← Projekte": "← Projects",
  "Mehr anzeigen": "Show more",
  Sprache: "Language",
  Design: "Theme",
  Hell: "Light",
  Dunkel: "Dark",
  Deutsch: "Deutsch",
  Englisch: "English",
  "Entfernen bestätigen": "Confirm removal",
  "Dieses Element dauerhaft entfernen?": "Permanently remove this item?",
  "Vertrauen wirklich entziehen?": "Revoke trust for this host key?",
  Gespeichert: "Saved",
  Erfolgreich: "Success",
  "Erneut versuchen": "Try again",
  "Eingaben prüfen und erneut speichern.":
    "Check the fields and try saving again.",
  "Dieses Feld ist erforderlich.": "This field is required.",
  "Eine gültige Zahl eingeben.": "Enter a valid number.",
  "Zum Inhalt springen": "Skip to content",
  "Änderungen verwerfen?": "Discard changes?",
  "Nicht gespeicherte Änderungen verwerfen?": "Discard unsaved changes?",
  "Schlüssel {id}": "Key {id}",
  "Öffentlicher Schlüssel {id}": "Public key {id}",
  "{count} Maschinen · maximal {max} gleichzeitig":
    "{count} machines · maximum {max} at once",
  Freigaben: "Approvals",
  "Job-Ausgabe": "Job output",
  Tokens: "Tokens",
  "invalid password": "Incorrect password. Try again.",
  "password must contain 10–1024 bytes":
    "Use a password containing 10–1024 bytes.",
  "authentication required": "Sign in again to continue.",
  "setup is already complete": "The password is already configured. Sign in.",
  "too many login attempts; retry in one minute":
    "Too many attempts. Try again in one minute.",
  "remote provider URL requires HTTPS": "Use HTTPS for a remote provider.",
  "select a remote machine or pool": "Select a remote machine or pool.",
  "resource is still referenced":
    "This item is still in use. Remove its assignments first.",
  "SSH key is still referenced":
    "This SSH key is still in use. Remove its assignments first.",
  "project is still referenced":
    "This project still has agents or sources. Remove those assignments first.",
  "Projekt mit allen Agenten, Verläufen und Quellen dauerhaft entfernen? Repository-Dateien bleiben erhalten.":
    "Permanently remove this project with all agents, history and sources? Repository files will be preserved.",
  "stop the project agents and their jobs before removing it":
    "Stop this project's agents and their jobs before removing it.",
  "provider is still referenced":
    "Agents still use this provider. Select another provider first.",
  "model is required": "Enter a model ID.",
  "machine requires id, name, host, user and SSH key":
    "Enter a name, host, user and SSH key.",
  "workspace must be an absolute directory other than /":
    "Enter an absolute project directory other than /.",
};

Object.assign(english, {
  "Noch keine vertrauenswürdigen Hosts": "No trusted hosts yet",
  "GitHub-Token": "GitHub token",
  "{count} Maschinen · maximal {max} gleichzeitig":
    "{count} machines · up to {max} at a time",
  "API-Schlüssel": "API key",
  "Mit ChatGPT anmelden (Device-Login)": "Sign in with ChatGPT (device login)",
  "Mit OpenRouter anmelden (Browser)": "Sign in with OpenRouter (browser)",
  Zugang: "Sign-in method",
  "API-Basis-URL (optional)": "API base URL (optional)",
  "Provider verbinden": "Connect provider",
  "Anmeldeseite öffnen und diesen Code eingeben:":
    "Open the sign-in page and enter this code:",
  "Bei OpenRouter anmelden und den angezeigten Code hier einfügen.":
    "Sign in to OpenRouter and paste the displayed code here.",
  "Anmeldeseite öffnen ↗": "Open sign-in page ↗",
  "Warte auf Anmeldung im Browser …": "Waiting for browser sign-in …",
  Anmeldecode: "Authorization code",
  Verbinden: "Connect",
  "Verbunden. Die verfügbaren Modelle werden geladen.":
    "Connected. Loading available models.",
  "Dialog schließen und die Anmeldung erneut starten.":
    "Close this dialog and start signing in again.",
  "Zuerst einen Provider auswählen.": "Select a provider first.",
  "Modelle suchen": "Search models",
  "Name oder Modell-ID": "Name or model ID",
  Sprachmodell: "Language model",
  Sprachmodelle: "Language models",
  "Keine passenden Modelle. Modellliste beim Provider aktualisieren.":
    "No matching models. Refresh the provider’s model list.",
  geschätzt: "estimated",
  "USD / 1 Mio. Tokens · Input / Output / Cache":
    "USD / 1M tokens · input / output / cache",
  "Modell testen": "Test model",
  "Globale Modell-Defaults": "Global model defaults",
  "Neue Agenten übernehmen diese Modelle. Bestehende Agenten behalten ihre Auswahl.":
    "New agents use these models. Existing agents keep their selection.",
  "Zuerst einen Provider verbinden und seine Modelle laden.":
    "Connect a provider and load its models first.",
  Hauptmodell: "Main model",
  "Modell-Defaults speichern": "Save model defaults",
  "Ein Sprachmodell auswählen.": "Select a language model.",
  "Provider verbinden, verfügbare Modelle laden und anschließend die globalen Modell-Defaults festlegen.":
    "Connect providers, load available models, then choose the global model defaults.",
  "Modell-Defaults öffnen": "Open model defaults",
  "Modelle werden geladen …": "Loading models …",
  "{count} Sprachmodelle": "{count} language models",
  "Erneut anmelden": "Sign in again",
  "Für Änderungen an GitHub-Actions-Workflows bitte die GitHub-Verbindung erneut autorisieren.":
    "Authorize the GitHub connection again to allow changes to GitHub Actions workflows.",
  "Modelle aktualisieren": "Refresh models",
  "Modelle ansehen": "View models",
  "Zuerst globale Modell-Defaults festlegen":
    "Choose global model defaults first",
  "select a provider and one of its discovered models":
    "Select a provider and one of its discovered models.",
  "configure global main and approver model defaults first":
    "Choose global defaults for the main model and approver first.",
  "sign in to this provider first": "Sign in to this provider first.",
});

export const germanErrors: Record<string, string> = {
  "pool SSH keys are managed automatically; remove key_id from the YAML":
    "Der Pool-Schlüssel wird automatisch verwaltet. Schlüssel-IDs aus der YAML-Konfiguration entfernen.",
  "pool SSH keys are managed automatically":
    "Pool-Schlüssel werden automatisch verwaltet.",
  "pool SSH key is assigned to another purpose":
    "Der gespeicherte Pool-Schlüssel ist einem anderen Zweck zugeordnet.",
  "Paused by the user": "Vom Nutzer pausiert",
  "Cancelled by the user": "Vom Nutzer verworfen",
  "an unfinished goal already exists; continue it or ask the user to change it":
    "Es gibt bereits ein offenes Ziel. Dieses fortsetzen oder vom Nutzer ändern lassen.",
  "auto-approver returned an invalid goal audit":
    "Der Auto-Approver hat keinen gültigen Zielprüfentscheid geliefert. Das Ziel bleibt offen.",
  "goal audit did not provide completion or pause evidence":
    "Der Auto-Approver hat keine Belege für den Abschluss oder die angeforderte Pause geliefert.",
  "goal audit did not provide blocker evidence":
    "Der Auto-Approver hat keine ausreichenden Blockadebelege geliefert.",
  "goal audit did not identify remaining work":
    "Der Auto-Approver hat die noch offene Arbeit nicht benannt.",
  "goal changed; reload before cancelling it":
    "Das Ziel hat sich geändert. Vor dem Verwerfen neu laden.",
  "pause the agent before cancelling its goal":
    "Den Agenten vor dem Verwerfen seines Ziels pausieren.",
  "a completed goal cannot be cancelled":
    "Ein bereits erreichtes Ziel kann nicht verworfen werden.",
  "Server did not confirm the message receipt":
    "Der Server hat die Übernahme der Nachricht nicht bestätigt.",
  "message queue is full (100 pending messages)":
    "Die Warteschlange ist voll (100 wartende Nachrichten).",
  "provider stream ended before completion":
    "Die Modellverbindung wurde vor dem Antwortende unterbrochen. Erneut versuchen.",
  "provider did not complete the response; retry the request":
    "Das Modell konnte seine Antwort nicht abschließen. Erneut versuchen.",
  "invalid provider stream event":
    "Der Anbieter hat einen ungültigen Antwortabschnitt gesendet. Erneut versuchen.",
  "invalid completed provider response":
    "Der Anbieter hat keinen gültigen Antwortabschluss gesendet. Erneut versuchen.",
  "provider streamed invalid tool arguments":
    "Der Anbieter hat eine ungültige Werkzeuganfrage gesendet. Erneut versuchen.",
  "provider streamed a missing content block":
    "In der Modellantwort fehlt ein Abschnitt. Erneut versuchen.",
  "workspace root must be an absolute directory other than / or a directory below ~/":
    "Ein absolutes Verzeichnis außer / oder ein Verzeichnis unter ~/ angeben.",
  "workspace root contains invalid characters":
    "Das Projektverzeichnis enthält ungültige Zeichen.",
  "microvm_command must be an absolute path when supplied":
    "Für einen eigenen MicroVM-Runner einen absoluten Pfad angeben oder das Feld leer lassen.",
  "invalid password": "Falsches Passwort. Erneut versuchen.",
  "password must contain 10–1024 bytes":
    "Ein Passwort mit 10–1024 Bytes verwenden.",
  "authentication required": "Erneut anmelden, um fortzufahren.",
  "setup is already complete":
    "Das Passwort wurde bereits festgelegt. Bitte anmelden.",
  "too many login attempts; retry in one minute":
    "Zu viele Versuche. In einer Minute erneut versuchen.",
  "remote provider URL requires HTTPS":
    "Für entfernte Provider eine HTTPS-Adresse verwenden.",
  "select a remote machine or pool":
    "Eine Fernmaschine oder einen Pool auswählen.",
  "resource is still referenced":
    "Dieses Element wird noch verwendet. Zuerst die Zuweisungen entfernen.",
  "SSH key is still referenced":
    "Dieser SSH-Schlüssel wird noch verwendet. Zuerst die Zuweisungen entfernen.",
  "project is still referenced":
    "Dieses Projekt hat noch Agenten oder Quellen.",
  "stop the project agents and their jobs before removing it":
    "Vor dem Löschen die Agenten dieses Projekts und ihre Jobs stoppen.",
  "provider is still referenced":
    "Agenten verwenden diesen Provider noch. Zuerst einen anderen Provider zuweisen.",
  "model is required": "Eine Modell-ID eingeben.",
  "machine requires id, name, host, user and SSH key":
    "Name, Host, Benutzer und SSH-Schlüssel angeben.",
  "workspace must be an absolute directory other than /":
    "Ein absolutes Projektverzeichnis außer / angeben.",
};

Object.assign(germanErrors, {
  "configure global main and approver model defaults first":
    "Zuerst globale Defaults für Hauptmodell und Approver festlegen.",
  "select a provider and one of its discovered models":
    "Einen Provider und eines seiner abgerufenen Modelle auswählen.",
  "sign in to this provider first": "Zuerst bei diesem Provider anmelden.",
});

Object.assign(english, {
  Kostenmodell: "Billing model",
  "Nach Verbrauch": "Pay as you go",
  "Festpreis / Abo": "Fixed price / subscription",
  "Diese Preise für Kostenschätzungen verwenden":
    "Use these prices for cost estimates",
  "Modell-Metadaten ergänzen": "Add model metadata",
  "Metadaten ergänzen": "Add metadata",
  "Kosten unvollständig für {count} Anfragen":
    "Incomplete costs for {count} requests",
  "Cache gelesen (gemeldete Tokens)": "Cache read (reported tokens)",
  "Cache-Leseanteil nicht gemeldet": "Cache read share not reported",
  "Cache-Tokens geschrieben": "cache tokens written",
  "Tokenzahlen geschätzt oder unbekannt: {count} Anfragen":
    "Token counts estimated or unknown: {count} requests",
  "Cache-Schreibdaten fehlen für {count} Anfragen":
    "Cache write data missing for {count} requests",
});

Object.assign(english, {
  "Agent entfernen": "Remove agent",
  "Agentenbaum und zugehörigen Verlauf dauerhaft entfernen?":
    "Permanently remove this agent, its descendants and their histories?",
  "stop the agent and its jobs before removing it":
    "Stop the agent and its jobs before removing it.",
});

Object.assign(germanErrors, {
  "stop the agent and its jobs before removing it":
    "Vor dem Entfernen den Agenten und seine Jobs stoppen.",
});

Object.assign(english, { Anfragedaten: "Request details" });

Object.assign(english, {
  "SSH-Host": "SSH host",
  "Dateiänderungen: {path}": "File changes: {path}",
  "Alte Zeile": "Old line",
  "Neue Zeile": "New line",
  Änderung: "Change",
  Inhalt: "Content",
  Erstellt: "Created",
  Geändert: "Modified",
  Gelöscht: "Deleted",
  Unverändert: "Unchanged",
  "Für Binärdateien ist kein Text-Diff verfügbar.":
    "No text diff is available for binary files.",
  "Die Datei ist für einen Text-Diff zu groß.":
    "The file is too large for a text diff.",
  "Der vorherige Dateiinhalt konnte nicht gelesen werden.":
    "The previous file contents could not be read.",
  "Verzeichnis entfernt. Für Verzeichnisse ist kein Text-Diff verfügbar.":
    "Directory removed. No text diff is available for directories.",
  "Kein Text-Diff verfügbar.": "No text diff is available.",
  "Diff gekürzt. Die Zeilenzähler gelten für die gesamte Änderung.":
    "Diff truncated. Line counts cover the entire change.",
  "Dateiinhalt unverändert.": "File contents unchanged.",
  "Leere Datei.": "Empty file.",
  "Kein Zeilenumbruch am Dateiende": "No newline at end of file",
  failed: "Failed",
  interrupted: "Interrupted",
});

Object.assign(germanErrors, {
  failed: "Fehlgeschlagen",
  interrupted: "Unterbrochen",
});

Object.assign(english, { "Job beenden": "Stop job" });

Object.assign(english, {
  "Diesen Job wirklich beenden?": "Stop this job?",
  "Provider-Zugang wirklich trennen?": "Disconnect this provider account?",
  "Aktion bestätigen": "Confirm action",
});

Object.assign(english, {
  "Menü öffnen": "Open menu",
  "Menü schließen": "Close menu",
});

Object.assign(english, {
  Auswählen: "Select an option",
  "API-Basis-URL": "API base URL",
  "z. B. Forschungsteam": "e.g. Research team",
  "Adresse des Inference-Gateways.": "Address of the inference gateway.",
  "Optional. Leer lassen, um die Standardadresse des Providers zu verwenden.":
    "Optional. Leave blank to use the provider’s default address.",
  "Leer lassen, um den vorhandenen Schlüssel zu behalten.":
    "Leave blank to keep the existing key.",
  "Noch kein gespeicherter Kontext": "No saved context yet",
  "Öffentlicher Schlüssel": "Public key",
});

Object.assign(english, {
  "Host-Schlüssel prüfen": "Review host key",
  "Diesem Host-Schlüssel wird noch nicht vertraut. Den Fingerprint vor dem Verbinden prüfen.":
    "This host key is not trusted yet. Check its fingerprint before connecting.",
  "Öffentlichen Schlüssel anzeigen": "Show public key",
  "Schlüssel vertrauen und erneut verbinden": "Trust key and reconnect",
  "Verbindung erfolgreich": "Connection successful",
  "Die SSH-Anmeldung auf {machine} funktioniert.":
    "SSH sign-in to {machine} works.",
  "VS Code auf der Fernmaschine": "VS Code on the remote machine",
  "Verbunden mit {machine}": "Connected to {machine}",
  "Eine verfügbare Projektmaschine wird automatisch ausgewählt. Beim ersten Öffnen wird VS Code dort eingerichtet.":
    "An available project machine is selected automatically. VS Code is set up there when you first open the editor.",
  "Editor wird gestartet …": "Starting editor …",
  "Editor öffnen": "Open editor",
  "Neu verbinden": "Reconnect",
  "Editor beenden": "Stop editor",
  "Webapp-Vorschau": "Web app preview",
  "Dev-Server-Port": "Dev server port",
  "Vorschau öffnen ↗": "Open preview ↗",
  "Starte den Dev-Server im VS-Code-Terminal und öffne seinen Port hier. Die Vorschau verwendet dieselbe HTTPS-Adresse wie Zentrale.":
    "Start the dev server in the VS Code terminal and open its port here. The preview uses the same HTTPS address as Zentrale.",
  "VS Code wird über SSH vorbereitet …": "Preparing VS Code over SSH …",
  "VS Code wird geladen …": "Loading VS Code …",
  "Editor wirklich beenden? Nicht gespeicherte Änderungen können verloren gehen.":
    "Stop the editor? Unsaved changes may be lost.",
  "remote editor disconnected; reconnect the editor":
    "The editor connection was interrupted. Reconnect the editor.",
  "editor session expired; reconnect the editor":
    "The editor session has expired. Reconnect the editor.",
  "editor disconnected; reconnect the editor":
    "The editor connection was interrupted. Reconnect the editor.",
});

Object.assign(germanErrors, {
  "remote editor disconnected; reconnect the editor":
    "Die Editor-Verbindung wurde unterbrochen. Den Editor erneut verbinden.",
  "editor session expired; reconnect the editor":
    "Die Editor-Sitzung ist abgelaufen. Den Editor erneut verbinden.",
  "editor disconnected; reconnect the editor":
    "Die Editor-Verbindung wurde unterbrochen. Den Editor erneut verbinden.",
  "editor is still starting": "Der Editor wird noch gestartet.",
});

Object.assign(english, {
  Schrittdetails: "Step details",
  Aktionsdetails: "Action details",
  "Aktionsdetails: {action}": "Action details: {action}",
  Befehl: "Command",
  Pfad: "Path",
  Datei: "File",
  Verzeichnis: "Directory",
  Begründung: "Reason",
  Netzwerkzugriff: "Network access",
  "Außerhalb der Sandbox": "Outside the sandbox",
  "Administration im Gast": "Guest administration",
  Pakete: "Packages",
  Maschine: "Machine",
  Maschinen: "Machines",
  "Übergeordneter Agent": "Parent agent",
  Hauptagent: "Main agent",
  Modell: "Model",
  Kennung: "Identifier",
  Quelle: "Source",
  Quellen: "Sources",
  Benutzer: "User",
  "SSH-Schlüssel": "SSH key",
  Sprungserver: "Jump hosts",
  "Exklusive Nutzung": "Exclusive use",
  "Maximale Maschinenanzahl": "Maximum machines",
  "Gemeinsames Home": "Shared home",
  "Stabiles Verzeichnis": "Stable directory",
  Betriebssystem: "Operating system",
  "VM-Startkennung": "VM boot identity",
  "VM läuft": "VM running",
  "Dauerhafte Umgebung": "Persistent environment",
  "Projektdateien bleiben erhalten": "Project files preserved",
  "Erstellt am": "Created",
  "Gestartet am": "Started",
  "Beendet am": "Finished",
  "Pauschale Lesefreigabe": "Reusable read permission",
  Gespeichert: "Saved",
  Ausgewählt: "Selected",
  Suchanfrage: "Search query",
  Adresse: "Address",
  Methode: "Method",
  Originalinhalt: "Original content",
  Laufzeitumgebung: "Runtime",
  Parameter: "Parameters",
  Aktion: "Action",
  Ergebnis: "Result",
  Dateien: "Files",
  Verknüpfung: "Symbolic link",
  Anzahl: "Count",
  Gesamt: "Total",
  "Exit-Code": "Exit code",
  Rolle: "Role",
  Zusammenfassung: "Summary",
  Antwortmöglichkeiten: "Answer options",
  Antwort: "Answer",
  Aufgabe: "Task",
  "Python-Umgebung": "Python environment",
  Position: "Position",
  "Nächste Position": "Next position",
  "Größe in Bytes": "Size in bytes",
  "Weitere Inhalte vorhanden": "More content available",
  Gekürzt: "Truncated",
  Vorschau: "Preview",
  Nutzeranweisungen: "User instructions",
  Prüfsumme: "Checksum",
  Wartet: "Waiting",
  Offen: "Open",
  Beantwortet: "Answered",
  Zurückgezogen: "Withdrawn",
  "Ohne Sandbox": "No sandbox",
  Automatisch: "Automatic",
  Unteragent: "Subagent",
  Zurückgesetzt: "Reset",
  Entfernt: "Removed",
  "Keine Angabe": "Not specified",
  Ja: "Yes",
  Nein: "No",
  "Keine Einträge": "No entries",
  "Die Dateiänderungen stehen im Diff.": "File changes are shown in the diff.",
  "Keine Parameter erforderlich.": "No parameters needed.",
  "{count} Einträge": "{count} entries",
  "Dieses Verzeichnis ist leer.": "This directory is empty.",
  "Keine Ausgabe": "No output",
  Dateiinhalt: "File contents",
  "Datei entfernt.": "File removed.",
  "Datei gespeichert.": "File saved.",
  "Gespeichert.": "Saved.",
  "Aktion abgeschlossen.": "Action completed.",
  "Aktion nicht ausgeführt.": "Action not performed.",
  "Keine weiteren Angaben.": "No further details.",
  "Für diese Antwort sind keine lesbaren Details verfügbar.":
    "No readable details are available for this response.",
  Überlegungen: "Reasoning",
  "Befehl ausführen": "Run command",
  "Python ausführen": "Run Python",
  "Datei lesen": "Read file",
  "Datei schreiben": "Write file",
  "Datei entfernen": "Remove file",
  "Dateien auflisten": "List files",
  "Pakete installieren": "Install packages",
  "MicroVM prüfen": "Inspect MicroVM",
  "MicroVM zurücksetzen": "Reset MicroVM",
  "Python-Umgebung erstellen": "Create Python environment",
  "Python-Umgebung entfernen": "Remove Python environment",
  "Container ausführen": "Run container",
  "Im Web suchen": "Search the web",
  "Webseite abrufen": "Fetch web page",
  "GitHub-Anfrage": "GitHub request",
  "Quelle klonen": "Clone source",
  "Git ausführen": "Run Git",
  "Maschinen auflisten": "List machines",
  "Maschineneigenschaften auflisten": "List machine properties",
  "Maschine auswählen": "Select machine",
  "Rechenressourcen prüfen": "Inspect compute resources",
  "Projektspeicher prüfen": "Inspect project storage",
  "Speicherquota abfragen": "Check storage quota",
  "Notiz speichern": "Save note",
  "Notizen auflisten": "List notes",
  "Notiz lesen": "Read note",
  "Notiz entfernen": "Remove note",
  "Kontext leeren": "Clear context",
  "Kontext verschieben": "Shift context",
  "Kontext verdichten": "Compact context",
  "Unteragent starten": "Start subagent",
  "Unteragent prüfen": "Inspect subagent",
  "Nutzeranweisungen lesen": "Read user instructions",
  "Verlauf lesen": "Read history",
  "Lesefreigaben prüfen": "Inspect read permissions",
  "Weitere Ausgabe lesen": "Read more output",
  "Rückfrage stellen": "Ask question",
  "Rückfragen auflisten": "List questions",
  "Rückfrage zurückziehen": "Withdraw question",
  "Job prüfen": "Inspect job",
  "Job-Ausgabe lesen": "Read job output",
  "Auf Job warten": "Wait for job",
  "Job beenden": "Stop job",
});

Object.assign(english, {
  Schreibgeschützt: "Read-only",
  Zeichen: "Characters",
  Ziel: "Target",
  Zähler: "Counters",
});

Object.assign(english, {
  Ausgabe: "Output",
  Titel: "Title",
  Beschreibung: "Description",
  Projekt: "Project",
  Container: "Containers",
  Eigenschaften: "Properties",
  "Die Parameter konnten nicht gelesen werden.":
    "The parameters could not be read.",
});

Object.assign(english, {
  "Ein Projekt bündelt Agenten, Dateien und die zugewiesenen Maschinen.":
    "A project brings together agents, files and assigned machines.",
  "Projekte suchen": "Search projects",
  "Name oder Verzeichnis": "Name or directory",
  "Keine passenden Projekte": "No matching projects",
  "Host-Schlüssel hinzufügen": "Add host key",
  "Eigene Schlüssel erlauben der Zentrale den Zugriff auf Maschinen. Host-Schlüssel bestätigen die Identität der Gegenstelle.":
    "Your keys let Zentrale access machines. Host keys verify the identity of the remote endpoint.",
  "Den öffentlichen Schlüssel auf der Fernmaschine in ~/.ssh/authorized_keys hinterlegen. Private Schlüssel bleiben in der Zentrale.":
    "Add the public key to ~/.ssh/authorized_keys on the remote machine. Private keys stay in Zentrale.",
  "Verwendet von {names}": "Used by {names}",
  "Keiner Maschine zugewiesen": "Not assigned to a machine",
  "Host-Schlüssel werden beim ersten Verbindungsaufbau geprüft. Hier lassen sich vorhandene Vertrauensbeziehungen einsehen und entziehen.":
    "Host keys are checked when connecting for the first time. Review or revoke existing trust here.",
  "Vertrauenswürdiger Host": "Trusted host",
  "Maschinen verwalten": "Manage machines",
  "Den Schlüssel mit einer vertrauenswürdigen Quelle vergleichen, bevor du diesem Host vertraust.":
    "Compare this key with a trusted source before trusting the host.",
  "Keine passenden Modelle": "No matching models",
  "Tokens Kontext": "context tokens",
  Standardmodelle: "Default models",
  "Neue Agenten verwenden das Hauptmodell für Aufgaben und den Auto-Approver für Freigaben. Bestehende Agenten bleiben unverändert.":
    "New agents use the main model for tasks and the auto-approver for permissions. Existing agents keep their models.",
  "Bearbeitet die Aufgabe": "Works on the task",
  "Prüft Aktionen und Zugriffe": "Reviews actions and access",
  "Noch nicht ausgewählt": "Not selected yet",
  Ändern: "Change",
  Verwerfen: "Discard",
  "Nicht gespeicherte Änderungen": "Unsaved changes",
  "Hauptmodell auswählen": "Choose main model",
  "Auto-Approver auswählen": "Choose auto-approver",
  "Verbinde einen Zugang, entdecke seine Modelle und wähle die Modelle für deine Agenten.":
    "Connect an account, explore its models and choose models for your agents.",
  "Standardmodelle ändern": "Change default models",
  "Provider-Zugänge": "Provider accounts",
  "Modelle von {name}": "Models from {name}",
  "Zugang konfigurieren": "Configure access",
  "Diesen Provider verbinden, um verfügbare Modelle abzurufen.":
    "Connect this provider to discover its available models.",
  "Der Schlüssel wird gespeichert und nicht wieder angezeigt.":
    "The key is stored and will not be shown again.",
  "Erweiterte Einstellungen": "Advanced settings",
  "Nach dem Speichern startet die Anmeldung. Anschließend werden die verfügbaren Modelle automatisch geladen.":
    "Sign-in starts after saving. Available models are then loaded automatically.",
  "Mit einem API-Schlüssel des Providers verbinden.":
    "Connect with a provider API key.",
  "Im Browser anmelden. Hier wird kein API-Schlüssel benötigt.":
    "Sign in using your browser. No API key is needed here.",
  "ChatGPT-Konto": "ChatGPT account",
  "OpenRouter-Konto": "OpenRouter account",
  "Diesen Code auf der Anmeldeseite eingeben. Die Zentrale erkennt die Anmeldung automatisch.":
    "Enter this code on the sign-in page. Zentrale detects completion automatically.",
  "Code kopieren": "Copy code",
  "Code gültig bis {time}": "Code valid until {time}",
  "Provider verbunden": "Provider connected",
  "Anmeldung erneut starten": "Restart sign-in",
});

Object.assign(english, {
  "Standardmodelle auswählen": "Choose default models",
  "Wähle zuerst die Standardmodelle für Aufgaben und Freigaben.":
    "First choose the default models for tasks and approvals.",
});

Object.assign(english, {
  "Provider-Zugang": "Provider account",
  "Modelle und verbundene Dienste für deine Projekte.":
    "Models and connected services for your projects.",
  "Verbinde GitHub, um auf private Repositorys zuzugreifen.":
    "Connect GitHub to access private repositories.",
  "SSH-Zugänge und Ausführungsumgebungen für deine Projekte.":
    "SSH connections and execution environments for your projects.",
  "GitHub-Token": "GitHub token",
  "USD / 1 Mio.": "USD / 1M",
  Preise: "Pricing",
  Aktualisieren: "Refresh",
});

Object.assign(english, {
  "Zentrale einrichten": "Set up Zentrale",
  "Einrichtung abschließen": "Complete setup",
  "Standard-Identität für Commits": "Default commit identity",
  "Name für Commits": "Commit name",
  "E-Mail für Commits": "Commit email",
  "z. B. Entwicklungsteam": "e.g. Development team",
  "Agenten verwenden diesen Namen und diese E-Mail als Autor und Committer. Projekte können eine eigene Identität festlegen.":
    "Agents use this name and email as the author and committer. Projects can override this identity.",
  "Commit-Identität speichern": "Save commit identity",
  "Commit-Name (optional)": "Commit name (optional)",
  "Commit-E-Mail (optional)": "Commit email (optional)",
  "Beide Felder leer lassen, um die globale Commit-Identität zu verwenden.":
    "Leave both fields empty to use the global commit identity.",
  "VS Code öffnet sich in einem eigenen Tab. Die Zentrale stellt die Oberfläche bereit. Terminal und Extensions verwenden die Umgebung des gewählten Agenten, einschließlich seiner Fedora-MicroVM.":
    "VS Code opens in its own tab. Zentrale hosts the interface. Terminals and extensions use the selected agent's environment, including its Fedora MicroVM.",
});

Object.assign(germanErrors, {
  "commit name must contain 1–200 characters without line breaks or angle brackets":
    "Einen Commit-Namen mit 1–200 Zeichen eingeben. Zeilenumbrüche und spitze Klammern sind nicht erlaubt.",
  "commit email must be a valid email address":
    "Eine gültige E-Mail-Adresse für Commits eingeben.",
  "select a working agent from this project for VS Code":
    "Einen Arbeitsagenten aus diesem Projekt für VS Code auswählen.",
  "managed MicroVM did not expose its editor tunnel":
    "Die MicroVM hat keinen Editor-Tunnel bereitgestellt. VS Code erneut verbinden.",
  "VS Code inside a MicroVM requires the managed Fedora runner; this custom runner does not expose an editor tunnel":
    "Der eigene MicroVM-Runner stellt keinen Editor-Tunnel bereit. Für VS Code den integrierten Fedora-Runner verwenden.",
});

Object.assign(english, {
  "Neue Unterhaltung": "New conversation",
  "Alle Projekte": "All projects",
  "Projekte und Unterhaltungen": "Projects and conversations",
  "Projekte filtern": "Filter projects",
  Verwaltung: "Administration",
  "Allgemeine Einstellungen": "General settings",
  "Provider und Modelle": "Providers and models",
  "Darstellung und Sprache": "Appearance and language",
  "Seitenleiste öffnen": "Open sidebar",
  "Seitenleiste schließen": "Close sidebar",
  "Unterhaltung starten": "Start conversation",
  "In welchem Projekt möchtest du arbeiten?":
    "Which project would you like to work in?",
  "Lege zuerst ein Projekt mit einer Arbeitsmaschine an.":
    "First create a project with a work machine.",
  "Unterhaltung umbenennen": "Rename conversation",
  Umbenennen: "Rename",
  Projekteinstellungen: "Project settings",
  "Woran möchtest du arbeiten?": "What would you like to work on?",
  "Beschreibe dein Ziel. Der Agent kümmert sich um die nächsten Schritte.":
    "Describe your goal. The agent will take care of the next steps.",
  Ausführungsumgebung: "Execution environment",
  "Wähle ein Projekt und setze eine Unterhaltung fort.":
    "Choose a project and continue a conversation.",
});

Object.assign(germanErrors, {
  "conversation name must contain 1–200 characters without line breaks":
    "Einen Namen mit 1–200 Zeichen ohne Zeilenumbrüche eingeben.",
  "stop the agent before renaming the conversation":
    "Den Agenten vor dem Umbenennen der Unterhaltung stoppen.",
});

Object.assign(english, {
  "Unterhaltung wird erstellt …": "Creating conversation …",
});

Object.assign(english, {
  "Einstellungen öffnen": "Open settings",
  Seitenleiste: "Sidebar",
  "Agentendetails für {model}": "Agent details for {model}",
});

Object.assign(english, {
  "Diagnosedetails im Verlauf": "Show diagnostics in activity logs",
  "Noch keine Aktionen geprüft.": "No actions reviewed yet.",
  "{count} Prüfungen im angezeigten Verlauf":
    "{count} reviews in the displayed history",
  Freigabeprüfungen: "Approval reviews",
  "Anfrage aus älterem Verlauf": "Request from earlier history",
  "Wird geprüft": "Under review",
  "Keine Freigabe erfasst": "No approval recorded",
  "Wiederholtes Lesen erlaubt": "Repeated reads allowed",
  "Anfrage ansehen": "View request",
  Prüfschritte: "Review steps",
  "Keine gültige Prüfentscheidung erhalten.":
    "No valid review decision received.",
});

Object.assign(english, {
  "Gespeicherten API-Token entfernen": "Remove saved API token",
});

Object.assign(germanErrors, {
  "OAuth client ID is required": "Die Client-ID der OAuth-App eingeben.",
  "configure the OAuth app client ID first":
    "Zuerst die Client-ID der OAuth-App hinterlegen.",
  "connect the Git account first": "Zuerst das Git-Konto verbinden.",
  "Git account session expired; reconnect":
    "Die Git-Anmeldung ist abgelaufen. Das Konto erneut verbinden.",
  "Git account session could not be refreshed; reconnect":
    "Die Git-Anmeldung konnte nicht erneuert werden. Das Konto erneut verbinden.",
  "Git login expired; start again":
    "Die Anmeldung ist abgelaufen. Erneut starten.",
  "Git login is no longer current; start again":
    "Diese Anmeldung wurde ersetzt. Erneut starten.",
  "repository requires permission to manage deploy keys":
    "Für dieses Repository wird die Berechtigung zur Verwaltung von Deploy-Keys benötigt.",
  "SSH key is assigned to another purpose":
    "Dieser SSH-Schlüssel ist einem anderen Zweck zugewiesen.",
  "SSH key is already used for another purpose":
    "Dieser SSH-Schlüssel wird bereits für einen anderen Zweck verwendet.",
  "SSH key cannot have two purposes":
    "Pro Maschine oder Repository einen eigenen SSH-Schlüssel verwenden.",
  "SSH key is still used by a project or machine":
    "Der Schlüssel wird noch in einem Projekt oder einer Maschine verwendet. Zuerst diese Verwendungen entfernen.",
  "reconnect the original Git service before revoking this key":
    "Zum Widerrufen des Schlüssels zuerst die ursprüngliche Git-Instanz verbinden.",
  "reconnect the original Git service before updating repository authorization":
    "Zum Aktualisieren der Repository-Berechtigung zuerst die ursprüngliche Git-Instanz verbinden.",
  "GitHub workflow permission was not granted; sign in again and approve workflow access":
    "GitHub hat den Zugriff auf Workflows nicht freigegeben. Erneut anmelden und den Workflow-Zugriff bestätigen.",
  "select an owner or organization":
    "Ein Konto oder eine Organisation auswählen.",
  "selected repository owner is not available to this account":
    "Das gewählte Konto oder die Organisation ist für diese Anmeldung nicht verfügbar.",
  "repository name must contain 1–100 letters, digits, dots, underscores or hyphens":
    "Der Repo-Name darf 1–100 Buchstaben, Ziffern, Punkte, Unterstriche oder Bindestriche enthalten.",
  "Git service requires an HTTPS origin":
    "Die HTTPS-Adresse der Git-Instanz ohne zusätzlichen Pfad eingeben.",
  "Git login failed: access_denied":
    "Die Anmeldung wurde abgelehnt. Erneut anmelden und den Zugriff bestätigen.",
  "Git login failed: expired_token":
    "Der Anmeldecode ist abgelaufen. Die Anmeldung erneut starten.",
  "Git login failed: incorrect_client_credentials":
    "Die Client-ID der OAuth-App prüfen und die Anmeldung erneut starten.",
  "Git login failed: unsupported_grant_type":
    "Diese Git-Instanz unterstützt den Device-Flow nicht. Die GitLab-Version und OAuth-Konfiguration prüfen.",
  "Git login failed: device_flow_disabled":
    "Den Device-Flow in der GitHub-OAuth-App aktivieren und die Anmeldung erneut starten.",
  "provider did not confirm a writable deploy key; retry the repository connection":
    "Der Anbieter hat den Schreibzugriff des Deploy-Keys nicht bestätigt. Die Repo-Verbindung erneut speichern.",
});

Object.assign(english, {
  "Eigene Schlüssel erlauben den Zugriff auf Maschinen und Repositories. Jeder Schlüssel hat einen eigenen Zweck.":
    "Keys allow access to machines and repositories. Each key has a dedicated purpose.",
  "Maschinenschlüssel gehören in ~/.ssh/authorized_keys. Repo-Schlüssel werden automatisch als Deploy-Keys hinterlegt. Private Schlüssel bleiben in der Zentrale.":
    "Machine keys belong in ~/.ssh/authorized_keys. Repository keys are registered automatically as deploy keys. Private keys stay in Zentrale.",
  "Gefundene Werkzeuge": "Discovered tools",
  "Terminal in der VM öffnen": "Open terminal in VM",
  "Terminal wird geladen …": "Loading terminal …",
  "VM-Terminal": "VM terminal",
  Verbunden: "Connected",
  "Verbindung fehlgeschlagen": "Connection failed",
  "Terminal-Ein- und Ausgabe": "Terminal input and output",
  "Terminal-Eingabe": "Terminal input",
  "VM und Terminal werden gestartet …": "Starting VM and terminal …",
  "Terminal-Verbindung beendet": "Terminal connection closed",
  "Terminal-Verbindung fehlgeschlagen.": "Terminal connection failed.",
  "Terminal konnte nicht initialisiert werden.":
    "Could not initialize terminal.",
  "Ungültige Antwort der Terminal-Verbindung.":
    "Invalid terminal connection response.",
  "Zu viele ausstehende Terminal-Eingaben. Verbindung beendet.":
    "Too much pending terminal input. Connection closed.",
  "Esc bleibt im Terminal. Alt+Umschalt+T oder Schließen beendet diese Sitzung. Die VM wird automatisch gestoppt, wenn sie nicht mehr genutzt wird.":
    "Esc stays in the terminal. Alt+Shift+T or Close ends this session. The VM stops automatically when no longer in use.",
  "Stopp angefordert.": "Stop requested.",
  "Optionaler Token für GitHub-API-Werkzeuge. Die Repo-Auswahl verwendet die Git-Verbindung.":
    "Optional token for GitHub API tools. Repository selection uses the Git connection.",
});
