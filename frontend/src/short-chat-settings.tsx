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
    shortChatSettings: { machineId: string; poolId: string };
    projectTargets: { id: string; name: string; kind: string }[];
  }>("query{shortChatSettings{machineId poolId} projectTargets{id name kind}}");

  const settings = data?.shortChatSettings;
  const active = settings?.machineId
    ? `machine:${settings.machineId}`
    : settings?.poolId
      ? `pool:${settings.poolId}`
      : "off";

  return (
    <section className="space-y-3 border-t border-separator pt-6">
      <h2 className="text-base font-medium">{t("Kurzchats")}</h2>
      <p className="text-sm text-muted">
        {t(
          "Für Antworten und Dateifreigaben wird keine VM gestartet. Wähle einen Ausführungsort, wenn Kurzchats auch Code ausführen dürfen.",
        )}
      </p>
      <Failure message={error} />
      <Button
        variant="secondary"
        isDisabled={!settings}
        onPress={() => setOpen(true)}
      >
        {t("Codeausführung konfigurieren")}
      </Button>
      {open && settings && (
        <Editor
          title={t("Codeausführung in Kurzchats")}
          initial={{ target: active }}
          fields={[
            {
              key: "target",
              label: t("Fernmaschine oder Pool"),
              type: "select",
              options: [
                { key: "off", label: t("Deaktiviert") },
                ...(data?.projectTargets ?? []).map(({ id, name, kind }) => ({
                  key: `${kind}:${id}`,
                  label: `${t(kind === "pool" ? "Pool" : "Fernmaschine")}: ${name}`,
                })),
              ],
            },
          ]}
          onClose={() => setOpen(false)}
          onSave={({ target }) => {
            const selection = String(target ?? "off");
            return api.mutate(
              "mutation($input:ShortChatSettingsInput!){saveShortChatSettings(input:$input){machineId poolId}}",
              {
                input: {
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
