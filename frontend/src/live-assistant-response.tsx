import {
  useDeferredValue,
  useEffect,
  useMemo,
  useSyncExternalStore,
} from "react";

import { useAPI } from "./api";
import type { Message } from "./generated";
import { MessageMarkdown } from "./message-markdown";
import { useI18n } from "./preferences";
import { WorkingIndicator } from "./working-indicator";

const phaseLabels: Record<string, string> = {
  accepted: "Nachricht übernommen …",
  goal_review: "Auto-Approver prüft den Zielabschluss …",
  checking_ready: "Bestehende Umgebung wird kurz geprüft …",
  ready: "Vorbereitete Umgebung ist bereit …",
  connecting: "Verbindung zur Maschine wird hergestellt …",
  retrying_connection:
    "Verbindung unterbrochen, Umgebung wird automatisch erneut geprüft …",
  checking_sandbox: "Sandbox wird geprüft …",
  recovering_sandbox: "Umgebung wird wiederhergestellt …",
  workspace: "Projektverzeichnis wird vorbereitet …",
  sandbox: "Sandbox wird vorbereitet …",
  repository: "Repository wird vorbereitet …",
  retrying_model: "Modellverbindung unterbrochen, neuer Versuch …",
  waiting_question: "Eine notwendige Antwort steht noch aus …",
};

export function LiveAssistantResponse({
  agentId,
  messages,
  working,
  phase,
  repositoryStep,
  repositoryPercent,
  repositoryDetail,
}: {
  agentId: string;
  messages: Message[];
  working: boolean;
  phase?: string;
  repositoryStep?: string;
  repositoryPercent?: number;
  repositoryDetail?: string;
}) {
  const api = useAPI();
  const { t, locale } = useI18n();
  const source = useMemo(
    () => ({
      subscribe: (listener: () => void) =>
        api.responses.subscribe(agentId, listener),
      snapshot: () => api.responses.get(agentId),
    }),
    [api, agentId],
  );
  const current = useSyncExternalStore(source.subscribe, source.snapshot);
  // Defer the whole frame so an older text cannot acquire a newer draft's ID.
  const response = useDeferredValue(current);
  const text = response?.text ?? "";
  const persisted =
    response && messages.some((message) => message.id === response.id);

  useEffect(() => {
    if (persisted && response) api.responses.acknowledge(agentId, response.id);
  }, [api, agentId, persisted, response]);

  const generating = response?.status === "running";
  const label = t(
    phaseLabels[phase ?? ""] ??
      (generating
        ? text
          ? "Schreibt …"
          : "Antwort wird vorbereitet …"
        : "Arbeitet …"),
  );
  const step = phase === "repository" ? repositoryStep : undefined;
  const percentage =
    step && repositoryPercent !== undefined && repositoryPercent >= 0
      ? new Intl.NumberFormat(locale, { style: "percent" }).format(
          repositoryPercent / 100,
        )
      : undefined;
  const statusLabel = [
    percentage ? `${label} (${percentage})` : label,
    step && t(step),
    step && repositoryDetail,
  ]
    .filter(Boolean)
    .join(" · ");

  return (
    <>
      {!persisted && response && text && (
        <article
          aria-label={t("Live-Antwort")}
          aria-live="off"
          aria-busy={generating}
          className="min-w-0 py-5"
        >
          <MessageMarkdown content={text} />
          {["failed", "interrupted"].includes(response.status) && (
            <p className="mt-2 text-xs text-muted">
              {t("Antwort unterbrochen")}
            </p>
          )}
        </article>
      )}
      {working && <WorkingIndicator label={statusLabel} />}
    </>
  );
}
