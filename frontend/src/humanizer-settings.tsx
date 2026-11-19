import {
  Button,
  Card,
  Checkbox,
  Input,
  Label,
  ListBox,
  Select,
  TextField,
} from "@heroui/react";
import { useState } from "react";

import { useAPI, useQuery } from "./api";
import type {
  HumanizerSettings as SavedHumanizer,
  HumanizerSettingsInput,
  Provider,
} from "./generated";
import { ModelChoiceForm } from "./models";
import { useI18n } from "./preferences";
import { isHostedProvider } from "./provider-editor";
import { Failure, Inspect, useAction } from "./ui";

const settingsQuery = `query {
  humanizerSettings {
    model { providerId modelId reasoningLevel }
    hosted { providerId modelId target }
    spaceUrl spaceEndpoint spaceBatch useHuggingFaceAccount
  }
  providers(limit:500) { id name kind connected modelCount }
}`;

type HostedProvider = Pick<Provider, "id" | "name" | "kind">;
const presets = ["jialinyyzz/humanizer", "abacusai/Smaug-Humanizer"];

export function HumanizerSettings() {
  const { t } = useI18n();
  const api = useAPI();
  const { data, error } = useQuery<{
    humanizerSettings?: SavedHumanizer | null;
    providers: (HostedProvider & Pick<Provider, "connected" | "modelCount">)[];
  }>(settingsQuery);
  const action = useAction();
  const [choosing, setChoosing] = useState<"model" | "space" | "hosted">();
  const settings = data?.humanizerSettings;
  const providers =
    data?.providers.filter(
      (provider) => provider.connected && provider.modelCount > 0,
    ) ?? [];
  const hostedProviders =
    data?.providers.filter(
      (provider) => provider.connected && isHostedProvider(provider.kind),
    ) ?? [];
  const model = settings?.model;
  const activeModel = settings?.hosted?.providerId ? settings.hosted : model;

  const save = async (input: HumanizerSettingsInput) => {
    await api.mutate(
      "mutation($input:HumanizerSettingsInput!){setHumanizer(input:$input)}",
      { input },
    );
    setChoosing(undefined);
  };

  return (
    <section aria-labelledby="humanizer-title" className="min-w-0">
      <div className="mb-5 max-w-2xl space-y-1.5">
        <h2 id="humanizer-title" className="text-base font-medium">
          Humanizer
        </h2>
        <p className="text-sm leading-6 text-muted">
          {t(
            "Der Auto-Approver prüft jede angeforderte Textumformulierung, bevor der Text gesendet wird. Aufrufe können Kosten verursachen.",
          )}
        </p>
      </div>
      <Failure message={error || action.error} />
      <Card variant="secondary">
        <Card.Content className="flex flex-wrap items-center justify-between gap-3 py-4">
          <p className="min-w-0 break-all text-sm">
            {activeModel?.modelId
              ? `${data?.providers.find((provider) => provider.id === activeModel.providerId)?.name || activeModel.providerId} · ${activeModel.modelId}`
              : settings?.spaceUrl
                ? `${settings.spaceUrl}/${settings.spaceEndpoint}`
                : t("Ausgeschaltet")}
          </p>
          <div className="flex flex-wrap gap-2">
            <Button
              size="sm"
              variant="secondary"
              className="min-h-11"
              isDisabled={!providers.length || action.busy}
              onPress={() => setChoosing("model")}
            >
              {t("Katalogmodell auswählen")}
            </Button>
            <Button
              size="sm"
              variant="secondary"
              className="min-h-11"
              isDisabled={!data || action.busy}
              onPress={() => setChoosing("space")}
            >
              {t("ZeroGPU-Space einrichten")}
            </Button>
            {hostedProviders.length > 0 && (
              <Button
                size="sm"
                variant="secondary"
                className="min-h-11"
                isDisabled={action.busy}
                onPress={() => setChoosing("hosted")}
              >
                {t("GPU-Humanizer einrichten")}
              </Button>
            )}
            {(activeModel?.modelId || settings?.spaceUrl) && (
              <Button
                size="sm"
                variant="ghost"
                className="min-h-11"
                isPending={action.busy}
                onPress={() => action.run(() => save({}))}
              >
                {t("Ausschalten")}
              </Button>
            )}
          </div>
        </Card.Content>
      </Card>
      {choosing === "model" && (
        <Inspect
          title={t("Humanizer-Modell auswählen")}
          onClose={() => setChoosing(undefined)}
          placement="top"
        >
          <ModelChoiceForm
            role="humanizer"
            providers={providers}
            value={model ?? { providerId: "", modelId: "", reasoningLevel: "" }}
            onApply={(model) => save({ model })}
          />
        </Inspect>
      )}
      {choosing === "space" && (
        <SpaceEditor
          settings={settings}
          onSave={save}
          onClose={() => setChoosing(undefined)}
        />
      )}
      {choosing === "hosted" && (
        <HostedEditor
          settings={settings?.hosted}
          providers={hostedProviders}
          onSave={save}
          onClose={() => setChoosing(undefined)}
        />
      )}
    </section>
  );
}

function HostedEditor({
  settings,
  providers,
  onSave,
  onClose,
}: {
  settings: SavedHumanizer["hosted"] | undefined;
  providers: HostedProvider[];
  onSave: (input: HumanizerSettingsInput) => Promise<void>;
  onClose: () => void;
}) {
  const { t } = useI18n();
  const action = useAction();
  const [providerId, setProviderId] = useState(
    settings?.providerId || providers[0]?.id || "",
  );
  const [modelId, setModelId] = useState(settings?.modelId || presets[0]);
  const [target, setTarget] = useState(settings?.target ?? "");
  const provider = providers.find((entry) => entry.id === providerId);
  const needsTarget = provider && provider.kind !== "modal";
  const valid = Boolean(provider && modelId && (!needsTarget || target.trim()));
  const models =
    modelId && !presets.includes(modelId) ? [...presets, modelId] : presets;
  return (
    <Inspect
      title={t("GPU-Humanizer einrichten")}
      isBusy={action.busy}
      onClose={onClose}
    >
      <form
        className="space-y-4"
        onSubmit={(event) => {
          event.preventDefault();
          if (!valid || action.busy) return;
          void action.run(() =>
            onSave({
              hosted: {
                providerId,
                modelId,
                target: needsTarget ? target : "",
              },
            }),
          );
        }}
      >
        <p className="text-sm text-muted">
          {t(
            "Der Endpunkt verarbeitet den gesamten Batch in einem Auftrag. Verwende einen für Batch-Inferenz bereitgestellten Humanizer.",
          )}
        </p>
        <Select
          value={providerId}
          onChange={(id) => {
            setProviderId(String(id));
            setTarget("");
          }}
          className="gap-2"
        >
          <Label>{t("Provider")}</Label>
          <Select.Trigger className="min-h-11">
            <Select.Value />
            <Select.Indicator />
          </Select.Trigger>
          <Select.Popover>
            <ListBox>
              {providers.map((entry) => (
                <ListBox.Item key={entry.id} id={entry.id}>
                  {entry.name}
                  <ListBox.ItemIndicator />
                </ListBox.Item>
              ))}
            </ListBox>
          </Select.Popover>
        </Select>
        <Select
          value={modelId}
          onChange={(id) => setModelId(String(id))}
          className="gap-2"
        >
          <Label>{t("Modell")}</Label>
          <Select.Trigger className="min-h-11">
            <Select.Value />
            <Select.Indicator />
          </Select.Trigger>
          <Select.Popover>
            <ListBox>
              {models.map((id) => (
                <ListBox.Item key={id} id={id}>
                  {id}
                  <ListBox.ItemIndicator />
                </ListBox.Item>
              ))}
            </ListBox>
          </Select.Popover>
        </Select>
        {needsTarget && (
          <TextField
            value={target}
            onChange={setTarget}
            isRequired
            className="gap-2"
          >
            <Label>{t("Modell- oder Deployment-ID")}</Label>
            <Input
              placeholder={
                provider.kind === "replicate"
                  ? "owner/model:version"
                  : "owner/app"
              }
              className="min-h-11"
            />
          </TextField>
        )}
        <Failure message={action.error} />
        <div className="flex justify-end gap-2">
          <Button
            type="button"
            variant="ghost"
            className="min-h-11"
            isDisabled={action.busy}
            onPress={onClose}
          >
            {t("Abbrechen")}
          </Button>
          <Button
            type="submit"
            className="min-h-11"
            isPending={action.busy}
            isDisabled={!valid}
          >
            {t("Speichern")}
          </Button>
        </div>
      </form>
    </Inspect>
  );
}

function SpaceEditor({
  settings,
  onSave,
  onClose,
}: {
  settings: SavedHumanizer | null | undefined;
  onSave: (input: HumanizerSettingsInput) => Promise<void>;
  onClose: () => void;
}) {
  const { t } = useI18n();
  const action = useAction();
  const [spaceUrl, setSpaceUrl] = useState(settings?.spaceUrl ?? "");
  const [spaceEndpoint, setSpaceEndpoint] = useState(
    settings?.spaceEndpoint || "predict",
  );
  const [useHuggingFaceAccount, setUseHuggingFaceAccount] = useState(
    settings?.useHuggingFaceAccount ?? false,
  );
  const [spaceBatch, setSpaceBatch] = useState(settings?.spaceBatch ?? false);

  return (
    <Inspect
      title={t("ZeroGPU-Space einrichten")}
      isBusy={action.busy}
      onClose={onClose}
    >
      <form
        className="space-y-4"
        onSubmit={(event) => {
          event.preventDefault();
          if (action.busy || !spaceUrl.trim() || !spaceEndpoint.trim()) return;
          void action.run(() =>
            onSave({
              spaceUrl,
              spaceEndpoint,
              useHuggingFaceAccount,
              ...(spaceBatch ? { spaceBatch } : {}),
            }),
          );
        }}
      >
        <p className="text-sm leading-6 text-muted">
          {t(
            "Verwende die .hf.space-Adresse und einen Gradio-Endpunkt für Text. Für Batch-Inferenz muss der Endpunkt eine Liste annehmen und zurückgeben.",
          )}
        </p>
        <TextField
          type="url"
          value={spaceUrl}
          onChange={setSpaceUrl}
          isRequired
          isDisabled={action.busy}
          className="gap-2"
        >
          <Label>{t("Space-Adresse")}</Label>
          <Input
            placeholder="https://owner-spacename.hf.space"
            className="min-h-11"
          />
        </TextField>
        <TextField
          value={spaceEndpoint}
          onChange={setSpaceEndpoint}
          isRequired
          isDisabled={action.busy}
          className="gap-2"
        >
          <Label>{t("Gradio-Endpunkt")}</Label>
          <Input className="min-h-11" />
        </TextField>
        <Checkbox
          isSelected={spaceBatch}
          onChange={setSpaceBatch}
          isDisabled={action.busy}
        >
          <Checkbox.Content>
            <Checkbox.Control>
              <Checkbox.Indicator />
            </Checkbox.Control>
            {t("Textliste in einem Auftrag verarbeiten")}
          </Checkbox.Content>
        </Checkbox>
        <Checkbox
          isSelected={useHuggingFaceAccount}
          onChange={setUseHuggingFaceAccount}
          isDisabled={action.busy}
        >
          <Checkbox.Content>
            <Checkbox.Control>
              <Checkbox.Indicator />
            </Checkbox.Control>
            {t(
              "Verbundenes Hugging-Face-Konto und dessen ZeroGPU-Kontingent verwenden",
            )}
          </Checkbox.Content>
        </Checkbox>
        <Failure message={action.error} />
        <div className="flex justify-end gap-2">
          <Button
            type="button"
            variant="ghost"
            className="min-h-11"
            isDisabled={action.busy}
            onPress={onClose}
          >
            {t("Abbrechen")}
          </Button>
          <Button
            type="submit"
            className="min-h-11"
            isPending={action.busy}
            isDisabled={!spaceUrl.trim() || !spaceEndpoint.trim()}
          >
            {t("Speichern")}
          </Button>
        </div>
      </form>
    </Inspect>
  );
}
