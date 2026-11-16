# Codex: übertragbare Effizienzideen

Untersucht wurde der öffentliche Codex-Stand
[`e58bf8f5233b51e7cd1679d947e3028c1ed91b06`](https://github.com/openai/codex/tree/e58bf8f5233b51e7cd1679d947e3028c1ed91b06).
Die folgenden Aussagen wurden an den unveränderlichen Quelldateien geprüft;
sie sind keine Zusage über private OpenAI-Endpunkte oder die Verfügbarkeit
eines Modells mit anderen Zugangsdaten.

## Was ist `codex-auto-review`?

- [`model-provider/src/provider.rs`, Zeilen 94–98](https://github.com/openai/codex/blob/e58bf8f5233b51e7cd1679d947e3028c1ed91b06/codex-rs/model-provider/src/provider.rs#L94-L98)
  benennt es ausdrücklich als bevorzugtes Modell für **automatische
  Ausführungsfreigaben**, nicht als allgemeinen Code-Reviewer. Derselbe Code
  unterscheidet einen anderen bevorzugten Modellnamen für API-Key-Zugänge.
- [`ext/guardian-reviewer/src/model.rs`](https://github.com/openai/codex/blob/e58bf8f5233b51e7cd1679d947e3028c1ed91b06/codex-rs/ext/guardian-reviewer/src/model.rs#L19-L83)
  berücksichtigt den Modellkatalog, einen expliziten Override, unterstützte
  Denkintensitäten und einen Fallback auf das Hauptmodell. `low` wird nur bei
  entsprechender Unterstützung bevorzugt.
- [`core/src/guardian/reviewer_config.rs`](https://github.com/openai/codex/blob/e58bf8f5233b51e7cd1679d947e3028c1ed91b06/codex-rs/core/src/guardian/reviewer_config.rs#L103-L147)
  lädt bei fehlendem Standard-Reviewer den Katalog einmal nach. Die Session
  teilt diese Aktualisierung; sie startet nicht für jede Freigabe einen neuen
  Katalogabruf.

**Konsequenz für Zentrale:** keinen privaten Modellalias erzwingen, keine
unbemerkte Änderung der gewählten Denkintensität und keinen weniger strengen
Freigabeprozess als vermeintliche Tokenoptimierung einführen. Anbieter,
Modellkatalog und explizite Nutzerauswahl bleiben maßgeblich. Die gewünschte
Standardauswahl `medium` für den Approver bleibt unabhängig von Codex' `low`.

### Explizite Auswahl als Zentrale-Auto-Approver

Wenn der angemeldete ChatGPT/Codex-Zugang `codex-auto-review` in seinem
Modellkatalog liefert, ist es nun auch dann auswählbar, wenn der Eintrag für
den gewöhnlichen Chat-Picker als `hidden` markiert ist. Andere versteckte
Modelle werden dadurch nicht freigeschaltet; ein vom Zugang nicht geliefertes
Modell wird nicht erfunden. Nach einer Katalogaktualisierung kann es unter
„Standardmodelle ändern“ als Approver gewählt werden. Alle bisherigen
Approver-Modelle bleiben auswählbar, die bestehende Auswahl wird nicht ersetzt.

Der geprüfte Codex-Stand verwendet dafür einen normalen Reviewer-Turn mit
Modell-ID, Denkintensität und finalem Ausgabeschema:
[`core/src/guardian/review_session.rs`, 579–605](https://github.com/openai/codex/blob/e58bf8f5233b51e7cd1679d947e3028c1ed91b06/codex-rs/core/src/guardian/review_session.rs#L579-L605).
Das Schema wird im Responses-Format `text.format` mit `type: json_schema`
übertragen, siehe
[`codex-api/src/common.rs`, 199–225 und 405–423](https://github.com/openai/codex/blob/e58bf8f5233b51e7cd1679d947e3028c1ed91b06/codex-rs/codex-api/src/common.rs#L199-L225).
Es gibt in diesen geprüften Pfaden keinen separaten Auto-Review-HTTP-Endpunkt.
Zentrale verwendet entsprechend den vorhandenen Account-Responses-Transport,
OAuth-/Account-Header, Streaming und den persistenten Familien-Cache-Key.

Codex' eigener Guardian-Ausgabevertrag (`outcome`, Risiko, Autorisierung,
Begründung) ist unter
[`assessment.rs`, 72–113](https://github.com/openai/codex/blob/e58bf8f5233b51e7cd1679d947e3028c1ed91b06/codex-rs/ext/guardian-reviewer/src/assessment.rs#L72-L113)
definiert. Zentrale fordert stattdessen sein unverändertes unabhängiges
Freigabeschema `allowed/reason/blanket` als strukturiertes finales Ergebnis an.
Ein fremdes `outcome: allow` oder ein niedriger Risikowert wird **nicht** in eine
Ausführungsfreigabe umgedeutet. Inspektionen bleiben schreibgeschützt; fehlende,
ungültige oder fehlgeschlagene Antworten autorisieren weiterhin keine Aktion.

Account-gelieferte Denkintensitäten werden angezeigt und bei der Auflösung
berücksichtigt, statt sie allein aus dem Modellnamen zu erraten. Die
Approver-Standardintensität bleibt `medium` (bzw. ein konservatives unterstütztes
Äquivalent), nicht stillschweigend Codex' bevorzugtes `low`.
Loopback-Transporttests prüfen Request, Account-Header, Schema, Denkintensität
und Inspektions-Tool-Roundtrip. Das ist keine Behauptung über die Verfügbarkeit
oder einen erfolgreichen Live-Aufruf mit beliebigen OpenAI-Zugangsdaten.

## Konkrete Verbesserungen in diesem Änderungsstand

1. Ein unabhängiger Approver pro Hauptagentenfamilie statt neuer Kontexte je
   Subagent. Reviews sind serialisiert, Aktionen weiterhin einzeln geprüft.
   Das reduziert unnötig getrennte Review-Kontexte; **Freigabeentscheidungen
   werden nicht gecacht oder wiederverwendet**. Siehe
   [shared-auto-approver.md](shared-auto-approver.md).
2. Bereits vorbereitete Sandbox/Checkouts werden bei unveränderter Identität
   und tatsächlich gesunder VM wiederverwendet. Boot-, Konfigurations-,
   Vertrauens- und Verbindungsänderungen invalidieren die Wiederverwendung.
   Das spart wiederholte Vorbereitung, ohne Gesundheitsprüfungen wegzulassen.
3. Der Chat-Bootstrap lässt Nutzungsaggregation, Freigaben, Notizen und volle
   Jobausgaben aus dem ersten Renderpfad. Livezustand wird vom Server über
   WebSocket-Abonnements gepusht, nicht durch wiederholte HTTP-Abfragen oder
   WebSocket-Polling nachgeladen. Details werden bei Bedarf separat geladen.
4. Temporäre Provider-HTTP-Fehler und Transport-/Lesetimeouts bekommen einen
   begrenzten Backoff. Ein Retry ersetzt einen unvollständigen sichtbaren
   Entwurf, statt Antworttext zu verdoppeln. Ausgeführte Werkzeuge werden
   dadurch niemals erneut ausgeführt.

Zentrale übermittelt bereits stabile `CacheKey`-/`prompt_cache_key`-Werte
und providerabhängige Cachehinweise (siehe `backend/providers/responses.go`
und `anthropic.go`). Ein Cache-Key allein garantiert keinen Cache-Hit:
unveränderte Präfixe, Katalog-/Modellunterstützung und Anbieterregeln sind
entscheidend. Benutzer-, Projekt- und Reviewergrenzen dürfen für mehr
Cache-Hits nicht aufgeweicht werden.

## Wie der Nutzen zu messen ist

Vorher/Nachher unter identischem Modell, Prompt, Denkintensität und Testfall
vergleichen: Anzahl Modell-/Freigabeanfragen, Eingabe-/Ausgabetokens,
anbieterbestätigte Cache-Reads/-Writes, Zeit bis zum ersten Token und
Gesamtlaufzeit. Unbekannte Cachewerte sind **nicht null**. Weniger RTTs oder
kleinere Browserantworten sind keine Messung eingesparter Modell-Tokens.
Diese Untersuchung behauptet deshalb keine gemessene prozentuale
Token-/Kostenersparnis und keine geprüfte Nutzbarkeit von `codex-auto-review`
mit Zentrales konfigurierten Zugangsdaten.

## Nachprüfung vom 16. November 2026

Der offizielle Codex-Quellcode wurde erneut flach nach
`/tmp/zentrale-codex-audit-20261116` geklont. Vergleichsstand:
[`0184dfa6a48f0110dd9c6835522492908e5abcf3`](https://github.com/openai/codex/tree/0184dfa6a48f0110dd9c6835522492908e5abcf3).
Geprüft wurden insbesondere Requestaufbau, Cache-Schlüssel, Reasoning-Replay,
Kontextschätzung, Compaction und Reviewer-Kontext. Der gewünschte
Hauptagentenstandard **`xhigh` bleibt unverändert**; die Optimierungen setzen
keine niedrigere Denkintensität und kein anderes Modell voraus.

### Gemessener Verbrauch des untersuchten Agenten

Quelle ist eine private Kopie von `zentrale.duckdb`, Projekt
`d3d053aa-b240-4516-af6f-0d44397ec7ea`, Hauptagent
`C5I32OMEIFR6INOR53ZHOODM6R`, Familien-Approver
`TZMZVDF4KZ6T7ZGNZOZEISFEKE`. Die Quelldateien einschließlich eines eventuell
vorhandenen WAL wurden kopiert und Größe/Änderungszeit vor und nach dem Kopieren
verglichen. Die gesperrte Originaldatenbank wurde nicht verändert. Ausgewertet
wurden gespeicherte Nachrichten und Nutzungswerte; es wurden keine Prompts an
einen Provider geschickt und keine kostenpflichtigen Modelltests gestartet.

Im Ausschnitt **16. November, 05:20 bis 06:45 UTC**:

| Rolle | Modell | Anfragen | Eingabe-Tokens | Cache-Lese-Tokens | Ausgabe-Tokens |
| --- | --- | ---: | ---: | ---: | ---: |
| Hauptagent | `gpt-6.1-sol` | 20 | 1.384.767 | 231.552 | 25.116 |
| Approver | `codex-auto-review` | 26 | 1.556.661 | 0 | 2.688 |

Alle 46 Datensätze markieren Tokenzahlen und Cache-Lesen als vom Provider
gemeldet. Die Cache-Lesequote beträgt beim Hauptagenten 16,72 %, beim Approver
0 %. Die aktuellen Review-Snapshots haben in diesem Zeitraum dieselben
Nutzeranweisungen, dasselbe Projekt und dieselben Quellen. Null Cache-Lesen
belegt deshalb nicht, dass diese Anweisungen ständig geändert wurden; die
gespeicherten Daten erklären das serverseitige Cache-Verhalten nicht vollständig.

Der gesamte gespeicherte Projektlauf enthält 1.215 Hauptagenten-, 181 Subagenten-
und 1.404 Approver-Anfragen mit insgesamt 282.655.319 Eingabe-Tokens.
Das ist eine Verbrauchsbasis, keine Berechnung des ChatGPT-Abo-Kontingents.

Der aktuelle Ausschnitt enthält außerdem echte Review- und Helper-Timeouts,
einen ungültigen Patch mit mehrfach genanntem Dateipfad sowie einen Patch mit
nicht passendem Kontext. Acht gespeicherte Fehler des gesamten Hauptagentenlaufs
melden ausdrücklich einen Patch-Timeout im Remotehelper mit möglicherweise
bereits erfolgter Änderung. Diese Unsicherheit erklärt notwendige
Kontrolllesungen; sie ist kein Grund, Änderungen automatisch zu wiederholen.

### Umgesetzte Änderungen

1. **Begrenzte optionale Review-Historie.** Früher durfte alte Review-Historie
   bis zu einem Viertel des Modellfensters beanspruchen: hier 68.000 Tokens bei
   einem 272.000-Token-Fenster. Jetzt gilt zusätzlich eine feste Obergrenze von
   8.192 geschätzten Tokens. Vollständige aktuelle Nutzeranweisungen, aktuelle
   Aktion und laufende Inspektionen bleiben separat verpflichtend. Alte Reviews
   bleiben als vollständige Gruppen im Audit erhalten und sind nachlesbar.
   Entscheidungen werden weiterhin einzeln getroffen, nicht wiederverwendet.
2. **Laufzeitänderungen statt wiederholter Vollzustände.** Die Modellansicht
   beginnt mit einem vollständigen Zustand und hängt danach nur geänderte
   Felder an. Änderungen ersetzen ganze Felder; entfernte Felder werden
   ausdrücklich invalidiert. Nach Rotation/Compaction steht wieder ein
   vollständiger Grundzustand am Anfang. Gespeicherte Snapshots bleiben ganz;
   das Anhängen eines Updates verändert keine früher gesendete Nachricht.
   Das entspricht dem Grundprinzip von Codex'
   [World-State-Updates](https://github.com/openai/codex/tree/0184dfa6a48f0110dd9c6835522492908e5abcf3/codex-rs/core/src/context/world_state).
3. **Inhaltsbezogene Responses-Schätzung.** IDs, äußere JSON-Verpackung und
   Escaping werden nicht mehr wie Modelltext gezählt. Für verschlüsseltes
   Reasoning und Compaction werden die groben Codex-Heuristiken übernommen:
   [History-Schätzung](https://github.com/openai/codex/blob/0184dfa6a48f0110dd9c6835522492908e5abcf3/codex-rs/core/src/context_manager/history.rs#L1297),
   [Compaction-Heuristik](https://github.com/openai/codex/blob/0184dfa6a48f0110dd9c6835522492908e5abcf3/codex-rs/protocol/src/protocol.rs#L3811).
   Ein Checkpoint mit rund 72 KB Wire benötigt im Regressionstest damit etwa
   6.000 geschätzte Tokens statt 18.000 und bleibt verwendbar. Gemeldete hohe
   Provider-Tokenzahlen lösen weiterhin Compaction aus. Unbekannte Items und
   andere Protokolle behalten ihre konservative Schätzung; der Replay bleibt
   unverändert. Diese Heuristik ist keine exakte Tokenisierung und kann ältere
   verschlüsselte Checkpointformate unterschätzen.
4. **Code-Mode-Fristen passen zu Werkzeugaufrufen.** Bisher lief die
   Zweiminutenfrist der VM auch während Freigabe und Remote-I/O; die inneren
   Werkzeuge erhielten jedoch den äußeren Kontext ohne diese Batchfrist.
   Jetzt stehen zwei Minuten für JavaScript und maximal zehn Minuten für den
   gesamten Batch zur Verfügung. Werkzeugwartezeit verbraucht das
   JavaScriptbudget nicht. Elternabbruch und Gesamtfrist erreichen die VM
   und jeden inneren Werkzeugaufruf. Einzelne Review-, Datei- und
   Befehlsfristen gelten weiterhin. Nach Abbruch starten keine weiteren
   Batchaktionen, auch wenn der JavaScriptcode Fehler abfängt. Bereits
   ausgeführte Mutationen werden weder zurückgerollt noch automatisch wiederholt.
5. **Knappere Werkzeughilfe.** Doppelte Erklärungen im ständig übertragenen
   Hilfetext wurden gekürzt: 4.153 auf 3.107 UTF-8-Bytes. Die eigentlichen
   Systemregeln, Werkzeugberechtigungen und Denkstufen bleiben bestehen.

### Lokaler Vergleich und Grenzen

Im ersten Vergleich wurden die 26 gespeicherten Approver-Antworten ohne Provideraufrufe erneut mit
der jeweils vorhergehenden Historie aufbereitet. Bei gleichem Prompt-Overhead
von 2.000 geschätzten Tokens und derselben aktuellen Schätzfunktion sinkt die
Summe von **2.043.219 auf 487.391 geschätzte Eingabe-Tokens (76,1 %)** allein
durch die neue Historienobergrenze. Das isoliert die Änderung am lokalen Budget.
Die Werte sind ausdrücklich nicht die tatsächlich gemeldeten Tokenzahlen der
obigen Tabelle: Nachrichtenrahmen, Tokenisierung, Providerinstruktionen und
Laufzeitkontext sind nicht vollständig rekonstruiert.

Eine sequenzielle Projektion aller 382 gespeicherten Hauptagenten-Snapshots
reduziert deren Text von **2.684.829 auf 556.681 UTF-8-Bytes (79,3 %)**. Dabei
wird jeder Zustand einmal verglichen; es ist keine über alle Modellanfragen
gewichtete Token- oder Cache-Messung. Die feste Regression mit 20 Zuständen
prüft außerdem Präfixstabilität, vollständige Grundzustände nach Rotation und
die Wiederherstellbarkeit des letzten Zustands.

Gezählt werden gespeicherte Nutzungsdatensätze. Fehlgeschlagene Anfragen ohne
zurückgelieferte Nutzungsdaten können zusätzlichen Verbrauch verursachen.
Ein kontrollierter Live-Vergleich von Ergebnisqualität, Cache-Treffern und
Abonnementverbrauch nach dieser Änderung steht noch aus. Weniger optionale
Review-Historie kann zusätzliche gezielte Inspektionen erfordern. Hohe
Anfragezahlen und externe SSH-/Dateisystemprobleme werden durch diese Änderungen
nicht automatisch beseitigt.

### Einordnung des Recherchetexts

Zentrale sendet bereits einen eigenen stabilen Cache-Key, `store:false` und
verschlüsseltes Reasoning. Vollständiger HTTP-Replay bedeutet nicht automatisch
vollständige erneute Berechnung: Cache-Wiederverwendung hängt vom identischen
Präfix und den Modell-/Serverregeln ab, siehe
[OpenAI Prompt Caching](https://developers.openai.com/api/docs/guides/prompt-caching).
Codex' inkrementelle WebSocket-Übertragung ist ein zusätzlicher Transportausbau,
keine belegte pauschale Einsparung am Abo-Kontingent.

Eine Nutzung ausschließlich über `/v1/responses` lässt sich aus Codex' eigenem
Quellcode nicht ableiten. Zentrales bestehender ChatGPT-Account-Transport und
ehrlicher `originator:zentrale` werden hier nicht geändert. Interne Endpunkte
und Codex-Featureflags sind keine allgemeine Verfügbarkeitszusage für fremde
Clients. Ein fremder Clientname wird nicht vorgetäuscht.
Die behaupteten Strafmultiplikatoren, Fraud-Ursachen und historischen Vorfälle
werden nicht als Erklärung für diese Messdaten übernommen.

### Validierung dieser Nachprüfung

Die gesamte Go-Suite besteht mit `go test -race -p 2 ./...`; `go vet -p 2 ./...`,
Go-Formatierung und `git diff --check` bestehen ebenfalls. Neue Regressionen
prüfen die feste Review-Obergrenze samt Pflichtkontext, Zustandsupdates und
Rotation, unveränderten nativen Replay sowie JavaScriptbudget, Werkzeugwartezeit,
Gesamtfrist, Elternabbruch und das Unterbinden weiterer Batchaktionen.
Die lokalen Datenbankvergleiche liefen separat mit einem temporären Go-Overlay;
private Nachrichten wurden nicht in das Repository übernommen.

## Weitere Prüfung nach der ersten Optimierungsrunde

Die erste Runde war keine vollständige Erklärung der niedrigen Cache-Quote.
Bei der vertieften Prüfung wurden drei weitere konkrete Lücken behoben:

1. **Routing-Zustand für ChatGPT-Responses.** Codex behält den ersten vom
   Server gelieferten `x-codex-turn-state` und sendet ihn für Fortsetzungen und
   Retries desselben Turns unverändert zurück. Ein neuer Turn darf den alten
   Wert nicht weiterverwenden:
   [Codex-Turn-Lebensdauer](https://github.com/openai/codex/blob/0184dfa6a48f0110dd9c6835522492908e5abcf3/codex-rs/core/src/client.rs#L325).
   Auch `session-id` und `thread-id` werden im offiziellen Client gesetzt:
   [Session-Header](https://github.com/openai/codex/blob/0184dfa6a48f0110dd9c6835522492908e5abcf3/codex-rs/codex-api/src/requests/headers.rs#L25).
   Zentrale hatte bisher nur den Cache-Key und Account-/Client-Header.
   Jetzt verwendet der bestehende OAuth-/Device-Responses-Transport eigene
   stabile Thread-IDs und einen nur im laufenden Turn gehaltenen Routing-Wert.
   Hauptagent, einzelne Freigaben, Abschlussprüfungen und Textverdichtung haben
   getrennte Lebensdauern. Neue Nutzereingaben während eines laufenden Workers
   beginnen ebenfalls einen neuen Turn. Konto, Modell, Anbieteradresse und
   Thread begrenzen die Gültigkeit zusätzlich. Späte Antworten früherer
   Eigentümer oder fehlerhafte HTTP-Antworten können keinen neuen Routing-Wert
   setzen. API-Key-Anfragen und andere Protokolle übernehmen ihn nicht.
   `originator:zentrale` bleibt unverändert; es werden keine Codex-Featureflags
   oder fremden Clientidentitäten gesetzt. Das korrigiert einen belegten
   Transportunterschied, garantiert aber keine höhere serverseitige Cache-Quote.
2. **Begrenzte Historieninspektion beim Approver.** `inspect history` lieferte
   bislang den gesamten Verlauf einschließlich nativer Wire-Daten zurück;
   auch die Suche durchlief verschlüsseltes Reasoning. Jetzt verwendet es
   dieselbe begrenzte Projektion wie öffentliche Thread-Abfragen, nach seiner
   eigenen unveränderten Projektzugriffsprüfung. Standard sind zehn aktuelle
   Auszüge mit je bis zu 512 Zeichen, maximal 50 pro Seite. Cursor erlauben
   lückenloses Nachladen; `messageId`, `field:content|calls` und Bytebereiche
   liefern genaue Inhalte und semantische Werkzeugaufrufe. Suche betrifft
   sichtbare Inhalte und Werkzeugnamen. Opaque Providerzustände werden nicht
   als Textevidenz dupliziert; der normale native Modell-Replay bleibt ganz.
   Im untersuchten Approver-Verlauf gab es 256 Inspektionen, darunter eine
   Historienantwort mit 22.595 Bytes. Dieser Pfad ist deshalb eine zusätzliche
   Begrenzung, keine Erklärung für den Großteil seines Verbrauchs.
3. **Argumentprüfung vor bezahlter Freigabe.** Patchrahmen, Größenlimit,
   doppelte Pfade, Dateianzahl und ungültige Lesegrenzen werden jetzt lokal im
   gemeinsamen Dispatcher geprüft, bevor der Approver aufgerufen wird.
   Das gilt für benannte Werkzeuge, Harness-Aufrufe und Code Mode. Die
   Werkzeugbeschreibung erklärt außerdem die erforderlichen mehreren
   `@@`-Blöcke unter einem einzigen Dateimarker. Der konkrete doppelte
   Dateipfad aus dem gezeigten Verlauf wurde bisher erst nach einer
   Modellprüfung abgelehnt. Dateisystempfade, aktuelle Dateiinhalte,
   Patch-Semantik und atomare Ausführung validiert weiterhin der Remotehelper;
   syntaktisch zulässige Änderungen benötigen weiterhin unabhängige Freigabe.

Neue Tests prüfen Routing innerhalb eines Turns und über dessen Grenzen,
Retries nach abgebrochenem Stream, laufende Nutzerkorrekturen, getrennte
Freigaben mit eigenen Inspektionen, Konto-/Modell-/Thread-/Endpunktwechsel,
späte Antworten und ungültige Header. Historientests prüfen Seitenlimits,
vollständiges Nachladen, genaue Inhalte/Aufrufe und Projektgrenzen.
Ungültige Argumente lösen im Test null Modellaufrufe aus; ein zulässiger Patch
wird weiter unabhängig geprüft und bei Ablehnung nicht ausgeführt.

Weitere mögliche Hebel bleiben explizite Cache-Breakpoints auf unterstützten
Modellen sowie inkrementeller WebSocket-Transport. Die aktuelle
[Cache-Dokumentation](https://developers.openai.com/api/docs/guides/prompt-caching)
unterscheidet dabei implizite und explizite Schreibgrenzen: Ein gemeinsamer
Präfix allein garantiert keine Wiederverwendung. Diese Parameter werden hier
nicht ungeprüft auf private Modellaliase oder Account-Routen übertragen.
Für Routing und die neuen Inspektionsgrenzen wird keine zusätzliche
prozentuale Ersparnis am tatsächlichen Abonnementverbrauch behauptet.

## Dritte Prüfung: stabile Review-Abschnitte und Ausführungsquittungen

Die 8.192-Token-Obergrenze verkleinerte den Review-Kontext bereits erheblich.
Die bisherige Auswahl verschob jedoch bei nahezu jeder neuen Prüfung die
ältesten enthaltenen Review-Gruppen. Damit veränderte sich gerade nach längeren
Läufen regelmäßig der bereits gesendete Nachrichtenanfang.

Abgeschlossene Reviews werden nun chronologisch in begrenzte Abschnitte
aufgeteilt. Innerhalb eines Abschnitts werden neue Nachrichten nur angehängt;
erst beim Füllen des Abschnitts beginnt ein neuer. Übermäßig große alte Gruppen
werden ganz ausgelassen. Die aktuelle Prüfung, ihre laufenden Inspektionen und
die vollständigen aktuellen Nutzeranweisungen bleiben weiterhin Pflichtkontext.
Benötigt die aktuelle Prüfung außergewöhnlich viel Platz, entfällt der optionale
Abschnitt ganz. Gespeicherte Historie und Einzelentscheidungen bleiben erhalten.

Für die gleichen 26 Approver-Antworten aus dem jüngsten gespeicherten Ausschnitt
wurde das begrenzte gleitende Fenster mit den neuen Abschnitten verglichen.
Beide Varianten verwenden dieselbe Inhaltsprojektion, Gesprächsnormalisierung
und einen festen geschätzten Prompt-Overhead von 2.000 Tokens:

| Lokaler Vergleich | Gleitendes 8.192-Token-Fenster | Begrenzte Abschnitte |
| --- | ---: | ---: |
| Übergänge mit vollständig unverändertem vorherigem Nachrichtenpräfix | 4 von 25 | 21 von 25 |
| Summe geschätzter Eingabe-Tokens | 487.391 | 406.601 |

Das sind **weitere 16,6 % weniger geschätzte Eingabe** gegenüber der ersten
Historienbegrenzung. Es sind weder gemessene zusätzliche Cache-Treffer noch
16,6 % weniger Abo-Verbrauch. Tatsächliche Provider-Prompts enthalten weitere
Rahmenbedingungen; bewusst geänderte Nutzeranweisungen, Laufzeitdaten oder
Ausgabeverträge können weiterhin den Präfix verändern. Die feste Regression
mit 100 Prüfungen erhält bei 89 von 99 Übergängen den ganzen vorherigen Präfix
und überschreitet dabei das optionale Budget nicht.

Die Analyse möglicher zusätzlicher Modellrunden zeigte bei den 1.190 gespeicherten
nativen Hauptagenten-Antworten keine alleinstehenden `commentary`-Antworten;
50 Antworten kombinierten Kommentar und Werkzeugaufrufe bereits. Eine neue
Promptregel gegen vermeintlich häufige separate Fortschrittsrunden wurde daher
nicht als belegte Einsparung eingeführt.

Ein zweiter konkreter Hebel betrifft Code-Mode-Fehler: Bisher waren die detaillierten
inneren Aktivitäten im UI-Audit gespeichert, dem Modell blieben nach einem
JavaScriptfehler oft nur Zähler, Fehlermeldung und eventuell bereits gedruckte
Ausgaben. Jetzt enthält ein fehlgeschlagener Batch knappe Quittungen in
Aufrufreihenfolge: Werkzeug, Rückkehrstatus, bestätigte Änderungsanzahl und eine
vorhandene Job-ID. Ein Syntaxfehler mit null Werkzeugaufrufen wird ausdrücklich
als `no_tool_calls_started` gemeldet. Auch vom Script ignorierte Werkzeugfehler
liefern Quittungen. Erfolgreiche Batches erhalten keine zusätzlichen Quittungen
in der Modellansicht.

`ok` besagt nur, dass der Werkzeugaufruf ohne Fehler zurückgekehrt ist. Es ist
keine neue Freigabe, kein Beleg für die Fertigstellung eines Hintergrundjobs und
keine Aufforderung, Aktionen zu wiederholen. Fehler und nur begonnene Aufrufe
können weiterhin bereits Auswirkungen haben. Tests prüfen einen bestätigten
Patch und einen gestarteten Job vor einem späteren JavaScriptfehler, den Erhalt
ihrer IDs/Änderungen, null zusätzliche Ausführungen sowie eindeutige Quittungen
bei Syntax- und ignorierten Werkzeugfehlern.

## Vierte Prüfung: gespeicherte innere Ergebnisse gezielt nachlesen

Die Ausführungsquittungen verhinderten bereits einen Teil der Ungewissheit nach
Code-Mode-Fehlern. Der Agent konnte die gespeicherten inneren Antworten aber
bisher nicht über `tool_output` lesen: Das Werkzeug lieferte ausschließlich die
äußere Batch-Antwort. Fehlende Details mussten dadurch gegebenenfalls erneut
über Datei- oder Jobwerkzeuge beschafft werden.

Code-Mode-Antworten enthalten jetzt ihre gespeicherte `messageId`; jede
Fehlerquittung mit vorhandenem Aufrufprotokoll verweist zusätzlich auf dessen
`activity`. Mit `tool_output {messageId,activity,offset?,limit?}` kann der Agent
diesen bestehenden Datensatz gezielt nachlesen. Ohne `activity` bleibt der
bisherige Zugriff auf das äußere Ergebnis erhalten. Eine Seite umfasst
standardmäßig 4.096 und höchstens 16.000 UTF-8-Bytes. Es werden keine weiteren
Originaldaten kopiert und keine Modell- oder SSH-Aufrufe zum Nachlesen ausgeführt.

Die inneren Aktivitäten enthielten schon bisher nur begrenzte Vorschauen großer
Ergebnisse. `recordingTruncated` kennzeichnet diesen Fall ausdrücklich; die
fehlenden Originalbytes lassen sich aus dieser Vorschau nicht rekonstruieren.
Datei- und Jobwerkzeuge bleiben nötig, wenn gerade diese fehlenden Daten
gebraucht werden. Sowohl die äußere Nachricht als auch das innere Protokoll
müssen dem aufrufenden Agenten gehören.

Regressionen prüfen die persistierten Referenzen nach einem JavaScriptfehler,
genaue UTF-8-Ausschnitte, ungültige Indizes, Agentengrenzen und die Kennzeichnung
gespeicherter Vorschauen. Der Test mit tatsächlich ausgeführtem Patch und
gestartetem Hintergrundjob liest beide vorhandenen Antworten über den normalen
Dispatcher nach, ohne zusätzliche Modellaufrufe oder erneut gestartete Aktionen.
Eine zusätzliche prozentuale Abo-Ersparnis wurde damit nicht gemessen.

## Fünfte Prüfung: Einträge in Laufzeitlisten statt ganzer Listen

Die bisherigen Laufzeitupdates ersetzten bereits nur veränderte Felder. Bei
`completedJobs`, `runningJobs` und `notes` enthielt ein solches Feld aber weiter
die ganze Liste. Im gespeicherten Verlauf änderte sich `completedJobs` 159-mal;
von rund 331 KB Listendaten in diesen Feldänderungen waren rund 283 KB unveränderte
Einträge. Ein neuer Jobabschluss wiederholte so bis zu sieben frühere Meldungen.

Für diese drei Listen können Updates jetzt einzelne Datensätze anhand ihrer
eindeutigen ID ersetzen oder hinzufügen. Die vollständige aktuelle ID-Liste
bestimmt die Reihenfolge und entfernt nicht mehr enthaltene Einträge. Andere
veränderte Felder werden weiter vollständig ersetzt. Unklare Listen ohne
eindeutige IDs fallen auf vollständige Feldersetzung zurück. Die jeweils
kleinere Darstellung wird gewählt; ist schon ein vollständiger Snapshot kürzer,
wird dieser verwendet.

Der Offline-Replay verwendet die gleichen 382 gespeicherten Laufzeit-Snapshots
wie die erste Nachprüfung und die tatsächliche Go-Projektion:

| Darstellung derselben Zustandsfolge | UTF-8-Bytes |
| --- | ---: |
| Vollständige Snapshots | 2.684.829 |
| Bisherige Updates ganzer veränderter Felder | 556.681 |
| Feld- und Eintragsupdates | 297.475 |

Das sind **weitere 46,6 % weniger Bytes für diese Zustandsfolge** gegenüber den
bisherigen Feldupdates. 170 Nachrichten verwenden Eintragsupdates. Nach jedem
der 382 Schritte wurde der aktuelle Zustand unabhängig aus der Modellansicht
rekonstruiert und mit dem vollständigen Original verglichen. Die Regression
prüft außerdem alle 41 möglichen Fenstergrenzen einer 40-Schritt-Folge mit
Bearbeitungen, Löschungen und Umsortierungen. Gespeicherte Snapshots bleiben
vollständig, der bereits gesendete Präfix bleibt beim Anhängen erhalten und
jedes gekürzte Fenster beginnt mit einem vollständigen Ausgangsstand.

Diese Messung zählt Zustandsbytes einmal pro Snapshot. Sie gewichtet sie nicht
nach echten Modellrequests und belegt keine entsprechende prozentuale Ersparnis
an Tokens oder Abo-Kontingent.

Die Werkzeughilfe nennt außerdem jetzt die bestehenden Notizgrenzen bereits
vor dem Speicherversuch: zehn Notizen, Titel mit 1–30 und Inhalt mit höchstens
350 Unicode-Codepoints. Das adressiert die im Verlauf beobachteten Größenfehler,
ohne Inhalte automatisch abzuschneiden. Auf Nutzerwunsch wurde die bisherige
Systemanweisung zum Ignorieren von `AGENTS.md` und `AGENTS.override.md` entfernt.
Der Hauptmodell-Default bleibt `xhigh`.

## Sechste Prüfung: Modellrunden, Cache-Grenzen und inkrementeller Transport

Die fünf weiteren Vorschläge und der anschließend genannte WebSocket-Ausbau
sind implementiert. Der Hauptmodell-Default bleibt `xhigh`; kein Modell wird
für diese Optimierungen automatisch ausgetauscht.

### Diagnosen pro Modellrunde und Transportversuch

`inference_trace` erfasst Hauptarbeit, Freigaben, Zielprüfungen, textuelle
Zusammenfassungen, native Verdichtung, Nebenfragen und Verbindungstests getrennt.
Auch fehlgeschlagene Runden bleiben sichtbar. Jeder HTTP-/WebSocket-Versuch
enthält Dauer, gesendete Bytes, Status, eine feste Fehlerkategorie und vorhandene
gemeldete Tokenzahlen. Fehlende Werte bleiben unbekannt. Erfolgreiche
Nutzungsdatensätze verweisen über `traceId` auf die Runde; fehlgeschlagene
Versuche werden nicht als nachweislich kostenfrei dargestellt.

System-, Werkzeug-, Verlaufs- und Laufzeitanteile werden separat geschätzt.
Ein Vergleich mit der vorigen vorbereiteten Anfrage derselben Rolle/Aufgabe
meldet unveränderte Nachrichtenanfänge, geänderte Konfiguration und veränderte
Nachrichten. Das sind **keine serverseitig gemessenen Cache-Grenzen**. Nur
anbieterbestätigte Cache-Lesezahlen belegen tatsächlich erfolgte Wiederverwendung.
Der Präfixvergleich hält ausschließlich Hashes und höchstens 256 letzte
Vergleichszustände im Arbeitsspeicher; Diagnosen speichern keine zusätzlichen
Prompts, Zugangsdaten, Routingtokens oder Provider-Reasoning.

Die Verbrauchsdetails laden auf Anforderung die letzten 30 Runden. Auch bei
null erfolgreichen Nutzungsdatensätzen ist die Diagnose erreichbar. Die
authentifizierte GraphQL-Abfrage `inferenceTraces(agentId,limit,offset)` bietet
bis zu 100 Datensätze pro Seite in absteigender zeitlicher Reihenfolge.

### Explizite Cache-Grenzen mit begrenzter Aushandlung

`promptCacheMode=auto` ergänzt bei bekannten modernen Modellen der öffentlichen
OpenAI-API höchstens zwei explizite Grenzen an den ersten geeigneten
Nutzer-/Entwicklernachrichten. Diese Positionen bleiben beim Anhängen stabil.
Die implizite Cache-Grenze für den neueren Verlauf bleibt aktiviert. Native
Reasoning- und Compaction-Einträge werden nicht verändert.

Für ChatGPT-Konten und andere OpenAI-Routen lässt sich diese Ergänzung über
„Zusätzliche Cache-Grenzen → Aktivieren“ ausdrücklich einschalten. `off`
deaktiviert Zentrales zusätzliche Grenzen, nicht den serverseitigen impliziten
Cache. Werden genau diese optionalen Parameter abgelehnt, wird die unveränderte
Anfrage ohne Ergänzungen gesendet. Bereits veröffentlichter Antworttext verhindert
diesen automatischen Rückfall. Negative Fähigkeiten werden 15 Minuten je
Konto/Token, Modell und Endpunkt gespeichert. Andere Parameter-, Authentifizierungs-
oder Kontingentfehler lösen keine Cache-Aushandlung aus.

Grundlage ist die aktuelle
[OpenAI-Cache-Dokumentation](https://developers.openai.com/api/docs/guides/prompt-caching).
Die Implementierung behauptet nicht, dass ein privater Modellalias diese
öffentlichen Parameter mit beliebigen Zugangsdaten unterstützt. Es gibt keine
kostenpflichtige Cache-Vorwärmung und keine erzwungene längere Cache-Laufzeit.

### Gebündelte Inspektionen und wiederholte Ergebnisse

Der Approver kann unabhängige Inspektionen in einer Modellantwort oder über
`inspect` mit `action=batch` anfordern. Ein Batch enthält 1–8 einzeln bezeichnete
Leseaktionen, führt höchstens zwei gleichzeitig aus und bewahrt Ergebnispositionen
und Einzelfehler. Der gesamte Batch wird vor dem Start auf zulässige Aktionen
geprüft. Befehle, rekursive Batches, Notizänderungen und Kontextmutationen sind
ausgeschlossen. Jede eigentliche Aktion benötigt weiterhin ihre eigene Freigabe.

Erneut identische erfolgreiche Werkzeugausgaben ab 1.024 Bytes können in der
Modellansicht eine Referenz auf das frühere Ergebnis erhalten. Vergleichsgrundlage
sind Eigentümer, Werkzeugname und der Hash des vollständigen Inhalts. Referenzen
gelten nur innerhalb des aktuellen Fensters. Das erste Ergebnis bleibt regulär
sichtbar; die aktuelle Originalantwort ist über `tool_output` nachladbar.
Ausführungen, Aufruf-IDs und gespeicherte Inhalte ändern sich dadurch nicht.
Ergebnisse mit erkennbaren Fehlern – einschließlich Fehlern in Inspektionsbatches –
sowie explizite Nachleseaufrufe werden nicht so ersetzt.

### Wiederholte lokale Fehler und WebSockets

Wiederholt unveränderte, lokal ungültige Argumente erhalten ab dem zweiten
Versuch eine konkrete Diagnose. Dies betrifft Patch-Struktur, Dateiausschnitt-
grenzen und Notizgrößen; auch wiederholte Code-Mode-Syntaxfehler können erkannt
werden. Die Rückmeldung sagt nur bei diesen vor Ausführung erkannten Fällen,
dass der Aufruf nicht ausgeführt wurde. Sie stoppt den Agenten nicht, erweitert
keine Berechtigungen und behandelt unbekannte Remote-Fehler nicht als folgenlos.
Neue menschliche Eingaben beginnen eine eigene Erkennungslebensdauer.

`responsesTransport=auto` versucht an offiziellen OpenAI-Adressen den
WebSocket-Transport. `http` erzwingt den bisherigen Weg; `websocket` erlaubt den
Versuch auch an ausdrücklich konfigurierten Gateways. Nicht unterstützte
Handshake-/Protokollvarianten fallen auf HTTP zurück. Benutzereigene
HTTP-Transportgrenzen werden dabei nicht umgangen.

Ein Socket gehört zu einem Turn, Konto/Token, Modell und Thread. Nur wenn alle
Requestoptionen und der gesamte bisherige Input-/Output-Präfix passen, werden
`previous_response_id` und neue Einträge gesendet. Geänderte Konfiguration,
Kontextkürzung oder verlorener Serverzustand benötigen vollständigen vorbereiteten
Kontext. `store:false` bleibt gesetzt; lokale Historie bleibt die belastbare
Ausgangsbasis. Eltern-Cancellation, begrenzte Transport-Retries, Idle-Grenzen,
Rücksetzen abgebrochener sichtbarer Entwürfe und Ressourcenfreigabe an Turngrenzen
sind berücksichtigt. Noch kein unvollständiger Modellaufruf führt Werkzeuge aus.

Die aktuelle
[WebSocket-Dokumentation](https://developers.openai.com/api/docs/guides/websocket-mode)
beschreibt inkrementelle Fortsetzungen und den Rückfall bei fehlendem Serverzustand.
Kleinere Übertragungen und weniger Wartezeit belegen keine identische Einsparung
an Modell-Tokens oder Abo-Kontingent. Die Regressionen verwenden lokale
HTTP-/SSE-/WebSocket-/SSH-Fixtures; echte OpenAI-Konten wurden nicht für neue
kostenpflichtige Vergleichsläufe benutzt.

Validiert wurden der vollständige Go-Testbestand mit Race Detector, `go vet`,
der Frontend-Produktionsbuild einschließlich TypeScript-Prüfung sowie gezielte
Chromium-Fälle für Einstellungsübernahme, Legacy-Defaults, gemeldete Cachewerte,
fehlgeschlagene Runden ohne erfolgreiche Nutzung und den bestehenden Geräte-Login.
Neue Transporttests prüfen außerdem Konto-/Token-/Turngrenzen, fehlenden
Serverzustand, sichtbare Teilantworten, Cancellation und optionale Cacheparameter.
