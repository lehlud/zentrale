import { Button } from "@heroui/react";
import { useState } from "react";

import { useAPI, useQuery } from "./api";
import { useI18n } from "./preferences";
import { Editor, Failure } from "./ui";

export function ShortChatSettings() {
  const { t } = useI18n();
  const api = useAPI();
  const [open, setOpen] = useState(false);
  const { data, error } = useQuery<{
    shortChatSettings: {
      local: boolean;
      machineId: string;
      poolId: string;
      retentionDays: number;
      vmLifetimeDays: number;
    };
    shortChatLocalAvailable: boolean;
    projectTargets: { id: string; name: string; kind: string }[];
  }>(
    "query{shortChatSettings{local machineId poolId retentionDays vmLifetimeDays} shortChatLocalAvailable projectTargets{id name kind}}",
  );

  const settings = data?.shortChatSettings;
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
      <Button
        variant="secondary"
        isDisabled={!settings}
        onPress={() => setOpen(true)}
      >
        {t("Kurzchats konfigurieren")}
      </Button>
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
              options: [
                { key: "off", label: t("Deaktiviert") },
                ...(data?.shortChatLocalAvailable || settings.local
                  ? [
                      {
                        key: "local",
                        label: t(
                          data?.shortChatLocalAvailable
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
