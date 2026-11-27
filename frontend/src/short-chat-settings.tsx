import { Button } from "@heroui/react";
import { useRef, useState } from "react";

import { useAPI, useQuery } from "./api";
import type { ShortChatDocument } from "./generated";
import { useI18n } from "./preferences";
import { Editor, Failure, Inspect, Loading, useAction } from "./ui";

export function ShortChatSettings() {
  const { t } = useI18n();
  const api = useAPI();
  const [open, setOpen] = useState(false);
  const [promptOpen, setPromptOpen] = useState(false);
  const [knowledgeOpen, setKnowledgeOpen] = useState(false);
  const { data, error } = useQuery<{
    shortChatSettings: {
      local: boolean;
      machineId: string;
      poolId: string;
      retentionDays: number;
      vmLifetimeDays: number;
    };
    shortChatLocalIssue: string | null;
    shortChatSystemPrompt: string;
    projectTargets: { id: string; name: string; kind: string }[];
  }>(
    "query{shortChatSettings{local machineId poolId retentionDays vmLifetimeDays} shortChatLocalIssue shortChatSystemPrompt projectTargets{id name kind}}",
  );

  const settings = data?.shortChatSettings;
  const localIssue = data?.shortChatLocalIssue;
  const localAvailable = localIssue === null;
  const active = settings?.local
    ? "local"
    : settings?.machineId
      ? `machine:${settings.machineId}`
      : settings?.poolId
        ? `pool:${settings.poolId}`
        : "off";

  return (
    <section className="space-y-3 border-t border-separator pt-6">
      <h2 className="text-base font-medium">{t("Kurzchats")}</h2>
      <p className="text-sm text-muted">
        {t(
          "Lege fest, wie lange Kurzchats bleiben und wo sie bei Bedarf Code ausführen. Für Antworten und Dateifreigaben wird keine VM gestartet.",
        )}
      </p>
      <Failure message={error} />
      <div className="flex flex-wrap gap-2">
        <Button
          variant="secondary"
          isDisabled={!settings}
          onPress={() => setOpen(true)}
        >
          {t("Kurzchats konfigurieren")}
        </Button>
        <Button
          variant="secondary"
          isDisabled={!data}
          onPress={() => setPromptOpen(true)}
        >
          {t("Systemprompt ergänzen")}
        </Button>
        <Button variant="secondary" onPress={() => setKnowledgeOpen(true)}>
          {t("Wissensbasis verwalten")}
        </Button>
      </div>
      {promptOpen && (
        <Editor
          title={t("Systemprompt ergänzen")}
          initial={{ prompt: data?.shortChatSystemPrompt ?? "" }}
          fields={[
            {
              key: "prompt",
              type: "textarea",
              label: t("Zusätzliche Anweisungen für Kurzchats"),
              description: t(
                "Diese Anweisungen werden dem Systemprompt aller Kurzchats hinzugefügt.",
              ),
            },
          ]}
          onClose={() => setPromptOpen(false)}
          onSave={async ({ prompt }) => {
            await api.mutate(
              "mutation($prompt:String!){saveShortChatSystemPrompt(prompt:$prompt)}",
              { prompt: String(prompt ?? "") },
            );
          }}
        />
      )}
      {knowledgeOpen && (
        <KnowledgeBase onClose={() => setKnowledgeOpen(false)} />
      )}
      {open && settings && (
        <Editor
          title={t("Kurzchats konfigurieren")}
          initial={{
            target: active,
            retentionDays: settings.retentionDays,
            vmLifetimeDays: settings.vmLifetimeDays,
          }}
          fields={[
            {
              key: "target",
              label: t("Codeausführung"),
              type: "select",
              description: localIssue
                ? t("Zentrale (lokal) ist nicht verfügbar: {reason}", {
                    reason: localIssue,
                  })
                : undefined,
              options: [
                { key: "off", label: t("Deaktiviert") },
                ...(localAvailable || settings.local
                  ? [
                      {
                        key: "local",
                        label: t(
                          localAvailable
                            ? "Zentrale (lokal)"
                            : "Zentrale (KVM nicht verfügbar)",
                        ),
                      },
                    ]
                  : []),
                ...(data?.projectTargets ?? []).map(({ id, name, kind }) => ({
                  key: `${kind}:${id}`,
                  label: `${t(kind === "pool" ? "Pool" : "Fernmaschine")}: ${name}`,
                })),
              ],
            },
            {
              key: "retentionDays",
              label: t("Chat-Aufbewahrung (Tage)"),
              type: "number",
              required: true,
              description: t(
                "Gepinnte Chats bleiben erhalten. Eine neue Nachricht startet die volle Frist neu. Nach dem Entpinnen bleibt mindestens die halbe Frist.",
              ),
            },
            {
              key: "vmLifetimeDays",
              label: t("VM-Lebensdauer (Tage)"),
              type: "number",
              required: true,
              description: t(
                "Pro Benutzer wird erst bei Bedarf eine VM erstellt. Nach Ablauf werden VM und Arbeitsdateien entfernt; Chat-Anhänge bleiben im Chat.",
              ),
            },
          ]}
          onClose={() => setOpen(false)}
          onSave={async ({ target, retentionDays, vmLifetimeDays }) => {
            const selection = String(target ?? "off");
            const keepChats = Number(retentionDays);
            const keepVMs = Number(vmLifetimeDays);
            if (
              ![keepChats, keepVMs].every(
                (days) => Number.isInteger(days) && days >= 1 && days <= 3650,
              )
            ) {
              throw new Error(t("Gib ganze Tage zwischen 1 und 3650 ein."));
            }
            return api.mutate(
              "mutation($input:ShortChatSettingsInput!){saveShortChatSettings(input:$input){local machineId poolId retentionDays vmLifetimeDays}}",
              {
                input: {
                  local: selection === "local",
                  retentionDays: keepChats,
                  vmLifetimeDays: keepVMs,
                  machineId: selection.startsWith("machine:")
                    ? selection.slice(8)
                    : "",
                  poolId: selection.startsWith("pool:")
                    ? selection.slice(5)
                    : "",
                },
              },
            );
          }}
        />
      )}
    </section>
  );
}

type KnowledgeDocument = Pick<ShortChatDocument, "id" | "name" | "size">;

function KnowledgeBase({ onClose }: { onClose: () => void }) {
  const { t, locale } = useI18n();
  const api = useAPI();
  const action = useAction();
  const input = useRef<HTMLInputElement>(null);
  const [offset, setOffset] = useState(0);
  const [deleting, setDeleting] = useState<KnowledgeDocument>();
  const pageSize = 25;
  const { data, error, loading } = useQuery<{
    shortChatDocuments: KnowledgeDocument[];
  }>(
    "query($limit:Int!,$offset:Int!){shortChatDocuments(limit:$limit,offset:$offset){id name size}}",
    { limit: pageSize + 1, offset },
  );
  const docs = data?.shortChatDocuments ?? [];

  return (
    <Inspect title={t("Wissensbasis")} onClose={onClose} isBusy={action.busy}>
      <div className="space-y-4">
        <p className="text-sm text-muted">
          {t(
            "PDFs mit Text, Markdown und Textdateien werden für alle Kurzchats durchsuchbar.",
          )}
        </p>
        <input
          ref={input}
          type="file"
          accept=".pdf,.md,.markdown,.txt"
          className="hidden"
          onChange={(event) => {
            const file = event.currentTarget.files?.[0];
            event.currentTarget.value = "";
            if (!file) return;
            void action.run(async () => {
              const url = `/short-chat-knowledge?${new URLSearchParams({ name: file.name })}`;
              const response = await api.fetch(url, {
                method: "POST",
                body: file,
              });
              if (!response.ok) {
                const result = await response.json().catch(() => ({}));
                throw new Error(
                  result.message ||
                    t("Die Datei konnte nicht indexiert werden."),
                );
              }
              setOffset(0);
              api.refresh();
            });
          }}
        />
        <Button
          variant="secondary"
          isPending={action.busy}
          onPress={() => input.current?.click()}
        >
          {t("Datei zur Wissensbasis hinzufügen")}
        </Button>
        <Failure message={action.error || error} />
        {loading && <Loading />}
        {!loading && !error && docs.length === 0 && offset === 0 && (
          <p className="text-sm text-muted">
            {t("Noch keine Nachschlagewerke")}
          </p>
        )}
        {docs.length > 0 && (
          <ul className="divide-y divide-border">
            {docs.slice(0, pageSize).map((doc) => (
              <li key={doc.id} className="flex items-center gap-3 py-2">
                <a
                  href={`/short-chat-knowledge/${encodeURIComponent(doc.id)}`}
                  download={doc.name}
                  className="min-w-0 flex-1 break-all text-sm underline"
                >
                  {doc.name}
                </a>
                <span className="shrink-0 text-xs text-muted">
                  {new Intl.NumberFormat(locale).format(
                    Math.ceil(doc.size / 1024),
                  )}{" "}
                  KiB
                </span>
                <Button
                  variant="ghost"
                  size="sm"
                  isDisabled={action.busy}
                  onPress={() => setDeleting(doc)}
                >
                  {t("Entfernen")}
                </Button>
              </li>
            ))}
          </ul>
        )}
        {(offset > 0 || docs.length > pageSize) && (
          <div className="flex justify-end gap-2">
            <Button
              variant="ghost"
              isDisabled={offset === 0 || action.busy}
              onPress={() => setOffset((value) => value - pageSize)}
            >
              {t("Zurück")}
            </Button>
            <Button
              variant="ghost"
              isDisabled={docs.length <= pageSize || action.busy}
              onPress={() => setOffset((value) => value + pageSize)}
            >
              {t("Weiter")}
            </Button>
          </div>
        )}
        {deleting && (
          <Inspect
            title={t("Nachschlagewerk entfernen")}
            onClose={() => setDeleting(undefined)}
            isBusy={action.busy}
          >
            <p className="break-all text-sm">{deleting.name}</p>
            <Failure message={action.error} />
            <div className="mt-4 flex justify-end gap-2">
              <Button variant="ghost" onPress={() => setDeleting(undefined)}>
                {t("Abbrechen")}
              </Button>
              <Button
                variant="danger"
                isPending={action.busy}
                onPress={() =>
                  action.run(async () => {
                    await api.mutate(
                      "mutation($id:String!){deleteShortChatDocument(id:$id)}",
                      { id: deleting.id },
                      { announce: false },
                    );
                    setDeleting(undefined);
                  })
                }
              >
                {t("Entfernen")}
              </Button>
            </div>
          </Inspect>
        )}
      </div>
    </Inspect>
  );
}
