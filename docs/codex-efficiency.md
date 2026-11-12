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
