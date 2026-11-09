# Token- und Cache-Analyse, 9. November 2026

## Umfang und Messbasis

Untersucht wurden die aktuellen Provideradapter, Hauptagent und Auto-Approver,
Kontextaufbereitung, Werkzeuge und Verbrauchsanzeige. Als Codex-Referenz diente
der lokal vorhandene Quellstand
[`e6bf88ae022cdab968e5a48d5cbeea4d2e304fc9`](https://github.com/openai/codex/tree/e6bf88ae022cdab968e5a48d5cbeea4d2e304fc9).
Das ist ein benannter Vergleichsstand, keine Behauptung über sämtliche internen
Codex-Versionen oder deren tatsächliche Cache-Trefferquote.

Die Entwicklungsdatenbank wurde über eine konsistente private Kopie ausgewertet.
Es wurden nur aggregierte Verbrauchszahlen gelesen; keine gespeicherten Prompts
an einen Provider gesendet und keine kostenpflichtigen Vergleichsläufe gestartet.
Die Originaldatenbank wurde nicht verändert.

| Rolle                                     | Anfragen | Eingabe-Tokens | Cache-Lese-Tokens | Anteil an Eingabe | Anfragen mit Cache-Treffern |
| ----------------------------------------- | -------: | -------------: | ----------------: | ----------------: | --------------------------: |
| Hauptagent (`gpt-6.1-sol`)                |       72 |      1.116.872 |           198.272 |           17,75 % |                          13 |
| Approver (`gpt-6-luna`)                   |       56 |        411.335 |            13.696 |            3,33 % |                           2 |
| Zwei kleine Anfragen ohne Rollenzuordnung |        2 |             32 |                 0 |               0 % |                           0 |
| Gesamt                                    |      130 |      1.528.239 |           211.968 |           13,87 % |                          15 |

Zusätzlich wurden 27.007 Ausgabe-Tokens gespeichert. Der Anteil berechnet sich
aus Cache-Lese-Tokens geteilt durch Eingabe-Tokens, nicht aus dem Anteil der
Anfragen mit mindestens einem Treffer.

**Grenzen:** Historisch wurden gemeldete Tokenzahlen und Schätzungen nicht
unterschieden und Cache-Schreibvorgänge nicht gespeichert. `estimated` beschreibt
die Kostenschätzung, nicht die Herkunft der Tokenzahlen. Die Tabelle ist daher
eine gespeicherte Ausgangsbasis, kein kontrollierter Benchmark. Unterschiedliche
Aufgaben, Kontextwechsel und Abstände zwischen Anfragen begrenzen den Vergleich.
Eine belastbare Cache-Quote nach den Änderungen liegt noch nicht vor.

## Abgleich mit Codex und Änderungen

| Bereich            | Codex-Referenz / dokumentiertes Verhalten                                                                                    | Zentrale                                                                                                                                                                                                                                      |
| ------------------ | ---------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Präfix             | Unveränderte Anweisungen, Werkzeuge und bestehende Nachrichten erlauben Wiederverwendung.                                    | Systemtext und Werkzeugreihenfolge sind stabil; Laufzeitänderungen werden als zusätzliche Zustände angehängt.                                                                                                                                 |
| Cache-Schlüssel    | Codex leitet Schlüssel aus der Sitzung ab oder akzeptiert eine explizite Vorgabe.                                            | Eigener stabiler Schlüssel pro Agent, getrennt für Hauptagent und Approver. Ein Schlüssel allein garantiert keinen Treffer.                                                                                                                   |
| Historie           | Codex prüft für inkrementelle Übertragung sowohl Anfrageeigenschaften als auch das vollständige bisherige Nachrichtenpräfix. | Native Antworten, Reasoning und Phasen werden für regulären Replay erhalten. Beim Approver wurde ein früheres Ereignis bisher nachträglich umgeschrieben: behoben.                                                                            |
| Kontextverdichtung | Native kompakte Zustände müssen unverändert weitergereicht werden.                                                           | `/responses/compact` für unterstützte OpenAI-Routen; providerübergreifend textuelle Zusammenfassung mit Originalanweisungen. Opaque Zustände bleiben an Modell und Konto gebunden.                                                            |
| Werkzeugausgaben   | Codex begrenzt Ausgaben und rechnet mit heuristischen Kontextgrößen.                                                         | Begrenzte Vorschauen, nachladbare Originalausgaben, Log-Cursor, gezielte Dateiausschnitte und kleine Patches sind vorhanden. Der alte vollständige Standard-Read über den Dispatcher entfällt: alle Zugänge liefern standardmäßig 200 Zeilen. |
| Werkzeugkatalog    | Der Codex-Guide empfiehlt knappe, passende Anweisungen und klare Werkzeuge.                                                  | Häufige Aktionen typisiert; weitere Aktionen werden über `tool_search` entdeckt. Code Mode spart Modellrunden und vermeidet unnötige Zwischenausgaben.                                                                                        |
| Verbrauch          | Codex erfasst Cache-Lese- und Cache-Schreib-Tokens.                                                                          | Schreibzahlen und Token-Provenienz wurden ergänzt; fehlende Daten werden ausgewiesen.                                                                                                                                                         |
| Transport          | Codex kann über WebSockets nur den neuen Nachrichtenanteil übertragen.                                                       | Vollständiger HTTP-Replay mit SSE-Ausgabe. Das kostet Übertragungsvolumen; die Prompt-Cache-Wiederverwendung hängt weiterhin vom Modellpräfix ab.                                                                                             |

Der Präfix-Abgleich folgt der aktuellen
[OpenAI-Cache-Dokumentation](https://developers.openai.com/api/docs/guides/prompt-caching).
Die Empfehlungen zu Werkzeugen und knappen Anweisungen stammen aus dem
[Codex-Prompting-Guide](https://developers.openai.com/cookbook/examples/gpt-5/codex_prompting_guide),
der Umgang mit nativen Zuständen aus der
[Compaction-Dokumentation](https://developers.openai.com/api/docs/guides/compaction).

Im lokalen Codex-Quellstand wurden dafür insbesondere
[`core/src/client.rs`](https://github.com/openai/codex/blob/e6bf88ae022cdab968e5a48d5cbeea4d2e304fc9/codex-rs/core/src/client.rs),
[`core/src/context_manager/history.rs`](https://github.com/openai/codex/blob/e6bf88ae022cdab968e5a48d5cbeea4d2e304fc9/codex-rs/core/src/context_manager/history.rs)
und
[`codex-api/src/sse/responses.rs`](https://github.com/openai/codex/blob/e6bf88ae022cdab968e5a48d5cbeea4d2e304fc9/codex-rs/codex-api/src/sse/responses.rs)
gelesen.

### Konkreter Approver-Fehler

Bisher enthielt die jeweils neueste Anfrage ihren gesamten Kontext. Sobald eine
weitere Prüfung hinzukam, wurde dieser zuvor neueste Datensatz verkürzt. Damit
änderte sich eine bereits gesendete Nachricht, obwohl sich die Nutzeranweisungen
nicht geändert hatten. Zusätzlich budgetierte der Approver noch die mehrfachen
vollständigen Snapshots, bevor er sie verkürzte.

Jetzt steht ein vollständiger aktueller Snapshot vor der Historie. Alle
Prüfereignisse werden von Anfang an gleich aufbereitet und verweisen auf ihre
Snapshot-Version. Unveränderte Anweisungen führen dadurch zu identischen alten
Nachrichten. Tatsächlich geänderte Anweisungen ändern den Snapshot bewusst.
Das Budget wird anschließend angewandt; Originalanweisungen und laufende Prüfung
bleiben verpflichtend. Das vollständige Audit-Protokoll bleibt erhalten.

Die feste Testhistorie mit 20 Prüfereignissen benötigt 95.571 Bytes gespeichert
und 10.206 Bytes in der Modellansicht, etwa **89,3 % weniger**. Tests prüfen auch
das unveränderte Präfix beim Anhängen einer weiteren Prüfung, geänderte
Anweisungen und den Abbruch bei zu großem Pflichtkontext. Das belegt Payloadgröße
und Präfixstabilität, keine garantierte Einsparung berechneter Modell-Tokens.

### Verbrauch und Kosten

Neue Datensätze halten fest, ob Eingabe-/Ausgabezahlen vom Provider stammen,
ob Cache-Lese- und Schreibzahlen vorlagen und wie viele Schreib-Tokens gemeldet
wurden. Responses und native Komprimierung verwenden dieselbe Normalisierung.
Die Oberfläche bildet die Cache-Lesequote nur über Anfragen mit gemeldeten Zahlen.
Historische Datensätze werden dabei nicht nachträglich zu Messwerten erklärt.

Die aktuelle OpenAI-Dokumentation unterscheidet normale Eingaben, Cache-Lesen
und Cache-Schreiben; Schreib-Tokens sind bereits in den Eingabe-Tokens enthalten.
Gemeldete 30-Minuten-Schreibvorgänge werden mit 1,25-fachem, 12-Stunden-Anteile mit
2,5-fachem Eingabepreis verrechnet. Das ist durch Tests für gemischte Anteile
abgesichert. Andere Provider-Schreibpreise bleiben als unvollständige
Kostenberechnung markiert, sofern keine explizite Provider-Kostenzahl vorliegt.
Festpreiszugänge behalten ihr getrennt ausgewiesenes API-Kostenäquivalent.
[Quelle: Cache-Abrechnung](https://developers.openai.com/api/docs/guides/prompt-caching)

## Verbleibende Optimierungspunkte

1. **Statische Anweisungen kürzen.** Der aktuelle Hauptagent sendet 11.186 Bytes
   Systemtext und 7.531 Bytes Werkzeugschemata: grob 4.680 Tokens vor der ersten
   Nachricht. Mit dem kleinen Dispatcher sind es 428 Schema-Bytes und rund 2.904
   Tokens; das belegt bereits 71 % eines 4.096-Token-Fensters. Der Approver liegt
   bei etwa 1.276 Tokens. Die Byte/4-Werte sind Schätzungen ohne exakte
   Provider-Tokenisierung oder vollständige Nachrichtenrahmen. Seltene
   Betriebsdetails sollten gezielter nachgeladen werden; Sicherheits- und
   Nutzeranweisungen müssen vollständig erhalten bleiben.
2. **Neue Messbasis sammeln.** Gleichartige Aufgaben auf demselben Modell mit
   festgehaltenen Kontextwechseln, Cache-Lese-/Schreibzahlen, Laufzeit und Kosten
   vergleichen. Hauptagent, Approver und Komprimierungsaufrufe getrennt
   auswerten. Die vorhandene Historie erlaubt keine kausale Aussage darüber,
   welcher Anteil der niedrigen Trefferquote durch den gefundenen Fehler entstand.
3. **TTL erst nach Messung wählen.** Zentrale erzwingt keine verlängerte
   Cache-Laufzeit und wärmt keinen Cache kostenpflichtig vor. Der Codex-Referenzstand
   kann eine TTL übergeben. Eine eigene Option müsste Modell, API-Route und
   zusätzliche Schreibkosten berücksichtigen; öffentliche API und ChatGPT-Zugang
   dürfen nicht ungeprüft gleich konfiguriert werden.
4. **Semantische Kontextgrößen und Transport.** Codex berücksichtigt bei seiner
   Schätzung modellrelevante Inhaltsarten gesondert. Zentrales Byte/4-Schätzung
   zählt bei nativem Wire auch Verpackung und opaque Daten und kann dadurch zu
   früh verdichten. Das sollte anhand gemeldeter Größen verbessert werden.
   Inkrementelle WebSocket-Übertragung wäre ein eigener Ausbau für weniger
   Übertragungsvolumen und Latenz; sie ersetzt keine stabilen Modellpräfixe.

## Entfernte Entwicklungsaltlast

- Eine aktuelle, transaktionale Schema-Initialisierung ersetzt die versionierte
  Migrationskette, deren Registry und ausschließlich dort benötigte Hilfsprogramme.
- `stable_directory`/`stableDirectory` und das ungenutzte `ide_port`/`idePort`
  entfallen aus Eingaben, Domainmodell und generiertem Frontendschema.
- Automatische Übernahmen und Umwidmungen alter Pool-/Repository-Schlüssel sowie
  Ausnahmen für zweckfremd geteilte Schlüssel entfallen.
- VM-Prozessreferenzen benötigen PID und Startkennung; alte reine PID-Dateien
  werden nicht mehr als Eigentumsnachweis akzeptiert.
- Der Dispatcher behält seine tatsächliche Aufgabe für seltene Aktionen und
  kleine Kontextfenster. Unterschiedliche Providerprotokolle, echte optionale
  Fähigkeiten und die Aktualisierung persistenter Gäste bleiben unterstützt.

Passende vorhandene Datenbanken lassen sich weiterhin öffnen. Unpassende
Entwicklungsschemata werden atomar abgelehnt; Daten, SSH-Schlüssel und entfernte
Arbeitsbereiche werden dabei nicht gelöscht oder automatisch konvertiert.

## Validierung

Alle Go-Pakete, die Python-Suiten für Agentenwerkzeuge und Remote-Ausführung
sowie 51 VM-Runner-Tests und die TypeScript-Prüfung bestehen. Für die betroffenen
Backend-Pakete wurden Race-Tests ausgeführt. Zehn Browserprüfungen decken die
Verbrauchsanzeige, Pool-YAML-Editor, q35-Konfiguration, VM-Freigabe und
Protokolldarstellung ab. `go vet`, Frontend-Formatierung und `git diff --check`
wurden ebenfalls geprüft. Diese lokalen Tests verwenden isolierte Testdaten
und keine kostenpflichtigen Modellaufrufe.
