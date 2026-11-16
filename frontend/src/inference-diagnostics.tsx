import { useEffect, useState } from "react";
import { useAPI } from "./api";
import type { InferenceTrace } from "./generated";
import { useI18n } from "./preferences";

export function InferenceDiagnostics({ agentId }: { agentId: string }) {
  const api = useAPI();
  const { t, numberLocale } = useI18n();
  const [rows, setRows] = useState<InferenceTrace[]>();
  const [error, setError] = useState("");
  const [revision, setRevision] = useState(0);
  useEffect(() => {
    const controller = new AbortController();
    setRows(undefined);
    setError("");
    api
      .query<{ inferenceTraces: InferenceTrace[] }>(
        `query($id:String!){inferenceTraces(agentId:$id,limit:30){id purpose modelId reasoningLevel createdAt durationMillis outcome prefixChange sharedMessages messageCount estimatedSystemTokens estimatedToolTokens estimatedConversationTokens estimatedRuntimeTokens inputTokens outputTokens cachedTokens tokensKnown cacheReadKnown attempts{number transport outcome durationMillis sentBytes tokensKnown}}}`,
        { id: agentId },
        controller.signal,
      )
      .then((result) => setRows(result.inferenceTraces))
      .catch((error) => {
        if (!controller.signal.aborted) setError(String(error));
      });
    return () => controller.abort();
  }, [api, agentId, revision]);
  const purposes: Record<string, string> = {
    main: t("Hauptarbeit"),
    approval: t("Freigabe"),
    goal_review: t("Zielprüfung"),
    summary: t("Zusammenfassung"),
    native_compaction: t("Kontextverdichtung"),
    side: t("Nebenfrage"),
    connection_test: t("Verbindungstest"),
  };
  return (
    <div className="space-y-2 py-2 text-xs">
      <div className="flex items-center justify-between gap-3">
        <span>{t("Letzte Modellrunden")}</span>
        <button
          type="button"
          className="min-h-8 underline"
          onClick={() => setRevision((value) => value + 1)}
        >
          {t("Aktualisieren")}
        </button>
      </div>
      <p className="text-muted">
        {t(
          "Kontextanteile sind Schätzungen. Präfixvergleiche beziehen sich auf vorbereitete Nachrichten und beweisen keinen Cache-Treffer. Fehlgeschlagene Versuche können Verbrauch verursachen, auch wenn Tokenzahlen fehlen.",
        )}
      </p>
      {error && <p role="alert">{error}</p>}
      {!rows && !error && <p role="status">{t("Wird geladen …")}</p>}
      {rows?.length === 0 && <p>{t("Noch keine Diagnosedaten vorhanden.")}</p>}
      <div className="space-y-2">
        {rows?.map((row) => (
          <details key={row.id} className="rounded border border-border p-2">
            <summary className="cursor-pointer">
              {purposes[row.purpose] ?? row.purpose} · {row.modelId} ·{" "}
              {(row.durationMillis / 1000).toLocaleString(numberLocale)} s ·{" "}
              {row.attempts.length} {t("Versuche")} · {row.outcome}
            </summary>
            <p className="mt-2">
              {row.tokensKnown
                ? `${t("Eingabe")}: ${row.inputTokens.toLocaleString(numberLocale)} · ${t("Ausgabe")}: ${row.outputTokens.toLocaleString(numberLocale)}`
                : t("Tokenzahlen nicht gemeldet")}
              {row.cacheReadKnown
                ? ` · ${t("Cache gelesen")}: ${row.cachedTokens.toLocaleString(numberLocale)}`
                : ""}
            </p>
            <p>
              {t("Geschätzte Kontextanteile")}: {t("System")}{" "}
              {row.estimatedSystemTokens}, {t("Werkzeuge")}{" "}
              {row.estimatedToolTokens}, {t("Verlauf")}{" "}
              {row.estimatedConversationTokens}, {t("Laufzeit")}{" "}
              {row.estimatedRuntimeTokens}.
            </p>
            <p>
              {t("Präfix")}: {row.prefixChange} · {row.sharedMessages}/
              {row.messageCount} {t("Nachrichten unverändert")}
            </p>
            {row.attempts.map((attempt) => (
              <p key={attempt.number}>
                #{attempt.number} {attempt.transport} · {attempt.outcome} ·{" "}
                {attempt.sentBytes.toLocaleString(numberLocale)} B ·{" "}
                {(attempt.durationMillis / 1000).toLocaleString(numberLocale)} s
                {!attempt.tokensKnown ? ` · ${t("Verbrauch unbekannt")}` : ""}
              </p>
            ))}
          </details>
        ))}
      </div>
    </div>
  );
}
