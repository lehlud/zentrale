import { Button, Link } from "@heroui/react";
import { useState } from "react";

import { useAPI, useQuery } from "./api";
import type { Provider } from "./generated";
import {
  modelSelectionKey as key,
  type ModelSelection as ModelChoice,
  type ModelSelections as ModelDefaults,
} from "./model-selection";
import { ModelChooser } from "./models";
import { useI18n } from "./preferences";
import { Failure, Loading, useAction } from "./ui";

type Scope = "chat" | "project";
type ModelProvider = Pick<Provider, "id" | "name">;
type Policy = {
  configured: boolean;
  presets: ModelChoice[];
  defaults: ModelDefaults;
};
const roles = ["main", "approver"] as const;
export const emptyChoice: ModelChoice = {
  providerId: "",
  modelId: "",
  reasoningLevel: "",
};
const choiceFields = "providerId modelId reasoningLevel";
const defaultsFields = `main{${choiceFields}} approver{${choiceFields}}`;

function presetLabel(choice: ModelChoice, providers: ModelProvider[]) {
  const provider = providers.find((item) => item.id === choice.providerId);
  return `${provider?.name || choice.providerId} · ${choice.modelId} · ${choice.reasoningLevel || "default"}`;
}

export function PresetSelect({
  label,
  value,
  presets,
  providers,
  onChange,
  placeholder,
  disabled,
  allowEmpty = true,
}: {
  label: string;
  value: ModelChoice;
  presets: ModelChoice[];
  providers: ModelProvider[];
  onChange: (choice: ModelChoice) => void;
  placeholder: string;
  disabled?: boolean;
  allowEmpty?: boolean;
}) {
  const current = key(value);
  const unavailable =
    current && !presets.some((choice) => key(choice) === current);

  return (
    <label className="grid min-w-0 gap-1 text-sm">
      <span>{label}</span>
      <select
        className="min-h-10 w-full rounded-lg border border-border bg-surface px-3"
        value={current}
        disabled={disabled}
        onChange={(event) =>
          onChange(
            presets.find((choice) => key(choice) === event.target.value) ??
              emptyChoice,
          )
        }
      >
        <option value="" disabled={!allowEmpty}>
          {placeholder}
        </option>
        {unavailable && (
          <option value={current} disabled>
            {presetLabel(value, providers)}
          </option>
        )}
        {presets.map((choice) => (
          <option key={key(choice)} value={key(choice)}>
            {presetLabel(choice, providers)}
          </option>
        ))}
      </select>
    </label>
  );
}

export function ModelPresetSettings({
  personal = false,
}: {
  personal?: boolean;
}) {
  const { t } = useI18n();

  return (
    <section className="space-y-4 border-t border-border pt-6">
      <h2 className="font-medium">
        {t(
          personal
            ? "Persönliche Modellstandards"
            : "Modellfreigaben und Standards",
        )}
      </h2>
      <div className="grid gap-4 lg:grid-cols-2">
        <ScopeSettings scope="chat" personal={personal} />
        <ScopeSettings scope="project" personal={personal} />
      </div>
    </section>
  );
}

function ScopeSettings({
  scope,
  personal,
}: {
  scope: Scope;
  personal: boolean;
}) {
  const { t } = useI18n();
  const api = useAPI();
  const action = useAction();
  const [draft, setDraft] = useState<Policy>();
  const [choosing, setChoosing] = useState(false);
  const { data, error } = useQuery<{
    modelPresetPolicy: Policy;
    personalModelDefaults?: ModelDefaults;
    modelProviders: ModelProvider[];
  }>(
    `query($scope:String!,$personal:Boolean!){
    modelPresetPolicy(scope:$scope){configured presets{${choiceFields}} defaults{${defaultsFields}}}
    personalModelDefaults(scope:$scope) @include(if:$personal){${defaultsFields}}
    modelProviders{id name}
  }`,
    { scope, personal },
  );

  if (!data?.modelPresetPolicy)
    return error ? <Failure message={error} /> : <Loading />;

  const policy = data.modelPresetPolicy;
  const providers = data.modelProviders ?? [];
  const legacyChoices = new Map(
    roles.map((role) => [key(policy.defaults[role]), policy.defaults[role]]),
  );
  legacyChoices.delete("");
  const value = draft ?? {
    ...policy,
    presets: policy.configured ? policy.presets : [...legacyChoices.values()],
    defaults: data.personalModelDefaults ?? policy.defaults,
  };

  function remove(choice: ModelChoice) {
    setDraft({
      ...value,
      presets: value.presets.filter((item) => key(item) !== key(choice)),
      defaults: {
        main:
          key(value.defaults.main) === key(choice)
            ? emptyChoice
            : value.defaults.main,
        approver:
          key(value.defaults.approver) === key(choice)
            ? emptyChoice
            : value.defaults.approver,
      },
    });
  }

  async function save() {
    const defaults = value.defaults;

    if (personal) {
      await api.mutate(
        "mutation($scope:String!,$defaults:ModelDefaultsInput!){setPersonalModelDefaults(scope:$scope,defaults:$defaults)}",
        { scope, defaults },
      );
    } else {
      await api.mutate(
        "mutation($scope:String!,$presets:[ModelChoiceInput!]!,$defaults:ModelDefaultsInput!){setModelPresetPolicy(scope:$scope,presets:$presets,defaults:$defaults)}",
        { scope, presets: value.presets, defaults },
      );
    }

    setDraft(undefined);
  }

  return (
    <div className="min-w-0 space-y-4 rounded-xl border border-border p-4">
      <h3 className="font-medium">
        {t(scope === "chat" ? "Unterhaltungen" : "Projekte")}
      </h3>
      <Failure message={error || action.error} />
      {personal && !policy.configured ? (
        <p className="text-sm text-muted">
          {t("Zuerst Modellfreigaben festlegen.")}
        </p>
      ) : (
        <>
          {!personal && (
            <>
              {!policy.configured && (
                <p className="text-sm text-muted">
                  {t(
                    "Bisherige Standards gelten, bis du die Freigaben speicherst.",
                  )}
                </p>
              )}
              <ul className="space-y-1">
                {value.presets.map((choice) => (
                  <li
                    key={key(choice)}
                    className="flex items-center justify-between gap-2 text-sm"
                  >
                    <span className="break-all">
                      {presetLabel(choice, providers)}
                    </span>
                    <Button
                      variant="ghost"
                      size="sm"
                      aria-label={`${t("Entfernen")}: ${presetLabel(choice, providers)}`}
                      onPress={() => remove(choice)}
                    >
                      {t("Entfernen")}
                    </Button>
                  </li>
                ))}
              </ul>
              {providers.length ? (
                <Button variant="secondary" onPress={() => setChoosing(true)}>
                  {t("Modell freigeben")}
                </Button>
              ) : (
                <Link href="#/providers">{t("Provider konfigurieren")}</Link>
              )}
            </>
          )}
          {roles.map((role) => (
            <PresetSelect
              key={role}
              label={t(role === "main" ? "Standardmodell" : "Auto-Approver")}
              placeholder={t(
                personal ? "Globaler Standard" : "Nicht festgelegt",
              )}
              value={value.defaults[role]}
              presets={value.presets}
              providers={providers}
              onChange={(choice) =>
                setDraft({
                  ...value,
                  defaults: { ...value.defaults, [role]: choice },
                })
              }
            />
          ))}
          <Button
            isPending={action.busy}
            isDisabled={!draft && (personal || policy.configured)}
            onPress={() => action.run(save)}
          >
            {t("Speichern")}
          </Button>
        </>
      )}
      {choosing && (
        <ModelChooser
          title={t("Modell freigeben")}
          value={emptyChoice}
          providers={providers}
          onClose={() => setChoosing(false)}
          onChange={(choice) => {
            if (!value.presets.some((item) => key(item) === key(choice))) {
              setDraft({ ...value, presets: [...value.presets, choice] });
            }
          }}
        />
      )}
    </div>
  );
}
