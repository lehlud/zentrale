import {
  Button,
  Card,
  Input,
  Label,
  ListBox,
  Select,
  TextField,
} from "@heroui/react";
import { useEffect, useState } from "react";
import { useAPI, useQuery } from "./api";
import type { LanguageModel, ModelChoice, Provider } from "./generated";
import { ModelMetadataEditor } from "./model-metadata";
import { ActionsMenu } from "./actions-menu";
import { useI18n } from "./preferences";
import { Empty, Failure, Inspect, Loading, useAction } from "./ui";
import { Icon } from "./icons";

const modelFields =
  "id name providerId contextWindow inputPrice outputPrice cachedPrice pricesKnown metadataEstimated";

export function useModels(providerId: string, totalModels?: number) {
  const [search, setSearch] = useState("");
  const [offset, setOffset] = useState(0);
  const key = providerId + ":" + search;
  const pageSize = 50;
  const result = useQuery<{ models: LanguageModel[] }>(
    `query($providerId:String!,$search:String!,$offset:Int!){models(providerId:$providerId,search:$search,limit:50,offset:$offset){${modelFields}}}`,
    { providerId, search, offset },
  );
  const [collection, setCollection] = useState<{
    key: string;
    models: LanguageModel[];
    hasMore: boolean;
  }>();
  useEffect(() => {
    if (!result.data) return;
    const page = result.data.models;
    setCollection((previous) => {
      const items = offset > 0 && previous?.key === key ? previous.models : [];
      const merged = new Map(items.map((model) => [model.id, model]));
      for (const model of page) merged.set(model.id, model);
      return {
        key,
        models: [...merged.values()],
        hasMore: page.length === pageSize,
      };
    });
  }, [result.data, key, offset]);
  const current = collection?.key === key ? collection : undefined;
  return {
    error: result.error,
    data: current ? { models: current.models } : undefined,
    loading: !current && result.loading,
    fetching: result.loading,
    search,
    setSearch: (value: string) => {
      setSearch(value);
      setOffset(0);
    },
    more: () => setOffset((value) => value + pageSize),
    hasMore: Boolean(
      current?.hasMore &&
      (search !== "" || current.models.length < (totalModels ?? Infinity)),
    ),
  };
}

export function ModelChooser({
  title,
  providers,
  value,
  onChange,
  onClose,
}: {
  title: string;
  providers: Provider[];
  value: ModelChoice;
  onChange: (choice: ModelChoice) => void;
  onClose: () => void;
}) {
  const { t } = useI18n();
  const [providerId, setProviderId] = useState(
    providers.some((provider) => provider.id === value.providerId)
      ? value.providerId
      : providers[0]?.id || "",
  );
  return (
    <Inspect title={title} onClose={onClose} placement="top">
      <div className="space-y-5">
        {providers.length > 1 ? (
          <Select
            value={providerId || null}
            onChange={(id) => setProviderId(String(id ?? ""))}
            className="gap-2"
          >
            <Label>{t("Provider")}</Label>
            <Select.Trigger className="min-h-11">
              <Select.Value />
              <Select.Indicator />
            </Select.Trigger>
            <Select.Popover>
              <ListBox>
                {providers.map((provider) => (
                  <ListBox.Item
                    key={provider.id}
                    id={provider.id}
                    textValue={provider.name}
                  >
                    <Label>{provider.name}</Label>
                    <ListBox.ItemIndicator />
                  </ListBox.Item>
                ))}
              </ListBox>
            </Select.Popover>
          </Select>
        ) : (
          <p className="text-sm text-muted">{providers[0]?.name}</p>
        )}
        {providerId && (
          <ModelChoiceList
            key={providerId}
            providerId={providerId}
            value={value}
            onChoose={(choice) => {
              onChange(choice);
              onClose();
            }}
          />
        )}
      </div>
    </Inspect>
  );
}

function ModelChoiceList({
  providerId,
  value,
  onChoose,
}: {
  providerId: string;
  value: ModelChoice;
  onChoose: (value: ModelChoice) => void;
}) {
  const { t, numberLocale } = useI18n();
  const api = useAPI();
  const models = useModels(providerId);
  return (
    <div className="space-y-4">
      <TextField
        value={models.search}
        onChange={models.setSearch}
        className="gap-2"
      >
        <Label>{t("Modelle suchen")}</Label>
        <Input
          autoFocus
          type="search"
          className="min-h-11"
          placeholder={t("Name oder Modell-ID")}
        />
      </TextField>
      <Failure message={models.error} onRetry={() => api.refresh()} />
      {models.loading ? (
        <Loading />
      ) : models.error && !models.data ? null : !models.data?.models.length ? (
        <Empty>{t("Keine passenden Modelle")}</Empty>
      ) : (
        <ListBox
          aria-label={t("Sprachmodelle")}
          selectionMode="single"
          selectedKeys={
            new Set(
              value.providerId === providerId && value.modelId
                ? [value.modelId]
                : [],
            )
          }
          onSelectionChange={(keys) => {
            if (keys === "all") return;
            const [key] = keys;
            if (key !== undefined)
              onChoose({ providerId, modelId: String(key) });
          }}
          onAction={(key) => onChoose({ providerId, modelId: String(key) })}
          className="max-h-none"
        >
          {models.data.models.map((model) => (
            <ListBox.Item
              key={model.id}
              id={model.id}
              textValue={`${model.name} ${model.id}`}
              className="min-h-16 py-3"
            >
              <div className="min-w-0 space-y-1">
                <Label className="break-words">{model.name}</Label>
                <p className="break-all font-mono text-xs text-muted">
                  {model.id}
                </p>
                <p className="text-xs text-muted">
                  {model.contextWindow.toLocaleString(numberLocale)}{" "}
                  {t("Tokens Kontext")}
                </p>
              </div>
              <ListBox.ItemIndicator />
            </ListBox.Item>
          ))}
        </ListBox>
      )}
      {models.hasMore && (
        <Button
          variant="secondary"
          className="min-h-11"
          isDisabled={models.fetching}
          onPress={models.more}
        >
          {t("Mehr anzeigen")}
        </Button>
      )}
    </div>
  );
}

export function ProviderModels({ provider }: { provider: Provider }) {
  const { t, numberLocale } = useI18n();
  const api = useAPI();
  const models = useModels(provider.id, provider.modelCount);
  const action = useAction();
  const [editing, setEditing] = useState<LanguageModel | null>(null);
  return (
    <section aria-label={t("Sprachmodelle")} className="min-w-0">
      <div className="space-y-4">
        <div className="flex flex-wrap items-end gap-3">
          <TextField
            className="min-w-0 flex-1 basis-48 gap-2"
            value={models.search}
            onChange={models.setSearch}
          >
            <Label>{t("Modelle suchen")}</Label>
            <Input
              className="min-h-11"
              type="search"
              placeholder={t("Name oder Modell-ID")}
            />
          </TextField>
          <Button
            variant="ghost"
            aria-label={t("Modelle aktualisieren")}
            className="min-h-11"
            isPending={action.busy || provider.catalogStatus === "loading"}
            onPress={() =>
              action.run(() =>
                api.mutate(
                  "mutation($providerId:String!){refreshModels(providerId:$providerId)}",
                  { providerId: provider.id },
                ),
              )
            }
          >
            <Icon name="refresh" />
            {t("Aktualisieren")}
          </Button>
        </div>
        <Failure
          message={models.error || action.error}
          onRetry={() => api.refresh()}
        />
        {models.loading ? (
          <Loading />
        ) : models.error && !models.data ? null : !models.data?.models
            .length ? (
          <Empty>
            {t(
              "Keine passenden Modelle. Modellliste beim Provider aktualisieren.",
            )}
          </Empty>
        ) : (
          <div className="space-y-3">
            {models.data.models.map((model) => (
              <Card key={model.id} variant="secondary" className="min-w-0">
                <Card.Content className="gap-0">
                  <div className="flex min-w-0 items-start justify-between gap-3">
                    <div className="min-w-0 space-y-1">
                      <h3 className="font-medium text-sm break-words leading-6">
                        {model.name}
                      </h3>
                      <p className="text-xs font-mono break-all text-muted leading-5">
                        {model.id}
                      </p>
                    </div>
                    <ActionsMenu
                      label={t("Aktionen für {name}", { name: model.name })}
                      actions={[
                        {
                          id: "test",
                          label: t("Modell testen"),
                          isDisabled: action.busy,
                          onAction: () =>
                            action.run(() =>
                              api.mutate(
                                "mutation($providerId:String!,$modelId:String!){testProvider(providerId:$providerId,modelId:$modelId)}",
                                { providerId: provider.id, modelId: model.id },
                              ),
                            ),
                        },
                        {
                          id: "metadata",
                          label: t("Metadaten ergänzen"),
                          onAction: () => setEditing(model),
                        },
                      ]}
                    />
                  </div>
                  <dl className="mt-3 grid grid-cols-2 gap-x-4 gap-y-3 text-xs sm:grid-cols-4">
                    <div className="space-y-1">
                      <dt className="text-muted">
                        {t("Kontextfenster (Tokens)")}
                      </dt>
                      <dd className="font-mono tabular-nums">
                        {model.contextWindow.toLocaleString(numberLocale)}
                        {model.metadataEstimated ? ` · ${t("geschätzt")}` : ""}
                      </dd>
                    </div>
                    {model.pricesKnown ? (
                      (
                        [
                          ["Input", model.inputPrice],
                          ["Output", model.outputPrice],
                          ["Cache", model.cachedPrice],
                        ] as const
                      ).map(([label, price]) => (
                        <div key={label} className="space-y-1">
                          <dt className="text-muted">
                            {label} <span>{t("USD / 1 Mio.")}</span>
                          </dt>
                          <dd className="font-mono tabular-nums">
                            {price.toLocaleString(numberLocale, {
                              maximumFractionDigits: 6,
                            })}
                          </dd>
                        </div>
                      ))
                    ) : (
                      <div className="space-y-1 sm:col-span-3">
                        <dt className="text-muted">{t("Preise")}</dt>
                        <dd>{t("Preis unbekannt")}</dd>
                      </div>
                    )}
                  </dl>
                </Card.Content>
              </Card>
            ))}
          </div>
        )}
        {models.hasMore && (
          <Button
            className="min-h-11"
            variant="secondary"
            isDisabled={models.fetching}
            onPress={models.more}
          >
            {t("Mehr anzeigen")}
          </Button>
        )}
      </div>
      {editing && (
        <ModelMetadataEditor model={editing} onClose={() => setEditing(null)} />
      )}
    </section>
  );
}
