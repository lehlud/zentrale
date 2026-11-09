import { Button, Card, Link } from "@heroui/react";
import { useEffect, useState } from "react";

import { CONFIG, useAPI, useQuery } from "./api";
import type { Config } from "./config";
import type { LanguageModel, ModelChoice, ModelDefaults } from "./generated";
import { ModelChooser } from "./models";
import { useI18n } from "./preferences";
import { Failure, Loading, useAction } from "./ui";

export function GlobalModelDefaults() {
  const { t } = useI18n();
  const api = useAPI();
  const { data, loading, error } = useQuery<Config>(CONFIG);
  const [draft, setDraft] = useState<ModelDefaults>();
  const [choosing, setChoosing] = useState<"main" | "approver">();
  const action = useAction();
  const defaults = draft ?? data?.modelDefaults;
  const providers =
    data?.providers.filter(
      (provider) => provider.connected && provider.modelCount > 0,
    ) ?? [];

  // Server refreshes update the summary; local choices survive errors until saved.
  useEffect(() => {
    if (
      data &&
      draft &&
      JSON.stringify(draft) === JSON.stringify(data.modelDefaults)
    )
      setDraft(undefined);
  }, [data, draft]);

  if (loading && !data) {
    return <Loading />;
  }

  return (
    <section aria-labelledby="global-models-title" className="min-w-0">
      <div className="mb-5 max-w-2xl space-y-1.5">
        <h2 id="global-models-title" className="text-base font-medium">
          {t("Standardmodelle")}
        </h2>
        <p className="text-sm leading-6 text-muted">
          {t(
            "Neue Agenten verwenden das Hauptmodell für Aufgaben und den Auto-Approver für Freigaben. Bestehende Agenten bleiben unverändert.",
          )}
        </p>
      </div>
      <Failure message={error || action.error} onRetry={() => api.refresh()} />
      {defaults && (
        <Card variant="secondary">
          <Card.Content className="divide-y divide-separator">
            {(["main", "approver"] as const).map((role) => (
              <div
                key={role}
                className="grid min-w-0 grid-cols-[minmax(0,1fr)_auto] items-center gap-x-4 gap-y-3 py-4 sm:grid-cols-[10rem_minmax(0,1fr)_auto]"
                role="group"
                aria-label={t(
                  role === "main" ? "Hauptmodell" : "Auto-Approver",
                )}
              >
                <div className="col-span-2 min-w-0 space-y-1 sm:col-span-1">
                  <h3 className="text-sm font-medium">
                    {t(role === "main" ? "Hauptmodell" : "Auto-Approver")}
                  </h3>
                  <p className="text-xs text-muted">
                    {t(
                      role === "main"
                        ? "Bearbeitet die Aufgabe"
                        : "Prüft Aktionen und Zugriffe",
                    )}
                  </p>
                </div>
                <div className="min-w-0">
                  {defaults[role].modelId ? (
                    <SelectedModel
                      choice={defaults[role]}
                      providerName={
                        data?.providers.find(
                          (provider) =>
                            provider.id === defaults[role].providerId,
                        )?.name ?? ""
                      }
                    />
                  ) : (
                    <p className="text-sm text-muted">
                      {t("Noch nicht ausgewählt")}
                    </p>
                  )}
                </div>
                <Button
                  className="min-h-11 shrink-0"
                  size="sm"
                  variant="secondary"
                  isDisabled={!providers.length || action.busy}
                  onPress={() => setChoosing(role)}
                >
                  {t(defaults[role].modelId ? "Ändern" : "Auswählen")}
                </Button>
              </div>
            ))}
          </Card.Content>
        </Card>
      )}
      {data && !providers.length && (
        <p className="mt-3 text-sm text-muted">
          {t("Zuerst einen Provider verbinden und seine Modelle laden.")}{" "}
          <Link href="#/providers" className="text-sm">
            {t("Provider konfigurieren")}
          </Link>
        </p>
      )}
      {draft && (
        <div className="mt-4 flex flex-wrap items-center justify-end gap-2 border-t border-separator pt-4">
          <p role="status" className="mr-auto text-sm text-muted">
            {t("Nicht gespeicherte Änderungen")}
          </p>
          <Button
            variant="ghost"
            className="min-h-11"
            isDisabled={action.busy}
            onPress={() => {
              setDraft(undefined);
              action.clearError();
            }}
          >
            {t("Verwerfen")}
          </Button>
          <Button
            className="min-h-11"
            isPending={action.busy}
            isDisabled={!draft.main.modelId || !draft.approver.modelId}
            onPress={() =>
              action.run(async () => {
                await api.mutate(
                  "mutation($input:ModelDefaultsInput!){setModelDefaults(input:$input)}",
                  { input: draft },
                );
              })
            }
          >
            {t("Modell-Defaults speichern")}
          </Button>
        </div>
      )}
      {choosing && defaults && (
        <ModelChooser
          title={t(
            choosing === "main"
              ? "Hauptmodell auswählen"
              : "Auto-Approver auswählen",
          )}
          providers={providers}
          value={defaults[choosing]}
          onClose={() => setChoosing(undefined)}
          onChange={(choice) => setDraft({ ...defaults, [choosing]: choice })}
        />
      )}
    </section>
  );
}

function SelectedModel({
  choice,
  providerName,
}: {
  choice: ModelChoice;
  providerName: string;
}) {
  const { data } = useQuery<{ model: LanguageModel | null }>(
    "query($providerId:String!,$modelId:String!){model(providerId:$providerId,modelId:$modelId){id name}}",
    choice,
  );
  return (
    <div className="min-w-0 space-y-1 text-sm">
      <p className="break-words font-medium">
        {data?.model?.name || choice.modelId}
      </p>
      <p className="break-words text-xs text-muted">{providerName}</p>
    </div>
  );
}
