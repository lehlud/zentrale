import { useState } from "react";
import { createRoot } from "react-dom/client";
import { AgentConversation } from "./agent-conversation";
import { AgentGoal } from "./agent-goal";
import { ArchivedAttachments } from "./attachment-list";
import { ModelResponse } from "./model-response";
import { PreferencesProvider, useI18n } from "./preferences";
import { Empty, Inspect } from "./ui";
import type { ExportDocument } from "./transcript-export";
import "./index.css";

function TranscriptViewer({ archive }: { archive: ExportDocument }) {
  const { t, numberLocale } = useI18n();
  const [selected, setSelected] = useState(0);
  const [inspection, setInspection] = useState<string | null>(null);
  const root = archive.threads[0];
  const thread = archive.threads[selected];

  const depth = new Map<string, number>();
  for (const item of archive.threads) {
    depth.set(item.id, item.parentId ? (depth.get(item.parentId) ?? 0) + 1 : 0);
  }

  return (
    <ArchivedAttachments.Provider value={archive.attachments}>
      <header className="border-b border-border bg-surface-secondary">
        <div className="mx-auto flex max-w-5xl flex-wrap items-center justify-between gap-3 px-4 py-4 sm:px-8">
          <div className="min-w-0">
            <p className="text-xs font-medium text-muted">
              Zentrale · {t("HTML-Export")}
            </p>
            <h1 className="break-words text-xl font-semibold">
              {root.name || t("Unterhaltung")}
            </h1>
            <p className="text-sm text-muted">
              {archive.projectName && `${archive.projectName} · `}
              {new Intl.DateTimeFormat(numberLocale, {
                dateStyle: "medium",
                timeStyle: "short",
              }).format(new Date(archive.exportedAt))}
            </p>
          </div>
          {archive.threads.length > 1 && (
            <label className="grid min-w-0 max-w-full gap-1 text-sm text-muted">
              {t("Thread auswählen")}
              <select
                className="min-h-11 max-w-full rounded-lg border border-border bg-surface px-3 text-foreground"
                value={selected}
                onChange={(event) => {
                  setSelected(Number(event.target.value));
                  window.scrollTo(0, 0);
                }}
              >
                {archive.threads.map((item, index) => (
                  <option key={item.id} value={index}>
                    {"› ".repeat(depth.get(item.id) ?? 0)}
                    {item.name || t("Agent")}
                  </option>
                ))}
              </select>
            </label>
          )}
        </div>
      </header>
      <main className="mx-auto max-w-5xl px-4 py-6 sm:px-8">
        {selected !== 0 && (
          <h2 className="mb-4 text-lg font-semibold">{thread.name}</h2>
        )}
        {thread.goal &&
          !["complete", "cancelled", "superseded"].includes(
            thread.goal.status,
          ) && (
            <AgentGoal goal={thread.goal} working={false} reviewing={false} />
          )}
        {thread.messages.length === 0 ? (
          <Empty>{t("Noch keine Nachrichten.")}</Empty>
        ) : (
          <AgentConversation
            key={thread.id}
            messages={thread.messages}
            projectId={archive.projectId}
            projectDirectory={archive.projectDirectory}
            working={false}
            onInspect={setInspection}
          />
        )}
      </main>
      {inspection !== null && (
        <Inspect
          title={t("Schrittdetails")}
          onClose={() => setInspection(null)}
        >
          <ModelResponse content={inspection} />
        </Inspect>
      )}
    </ArchivedAttachments.Provider>
  );
}

const source = document.getElementById("transcript-data")?.textContent;

if (source && !source.startsWith("__")) {
  const archive: ExportDocument = JSON.parse(source);
  document.title = `${archive.threads[0].name || "Zentrale"} · Zentrale`;

  createRoot(document.getElementById("root")!).render(
    <PreferencesProvider snapshot={archive.preferences}>
      <TranscriptViewer archive={archive} />
    </PreferencesProvider>,
  );
}
