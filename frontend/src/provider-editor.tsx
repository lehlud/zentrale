import {
  Accordion,
  Button,
  Description,
  FieldError,
  Input,
  Label,
  ListBox,
  Radio,
  RadioGroup,
  Select,
  TextField,
} from "@heroui/react";
import { useRef, useState } from "react";
import { useAPI } from "./api";
import type { Provider, ProviderKind } from "./generated";
import { ProviderSignIn } from "./provider-login";
import { useI18n } from "./preferences";
import { Failure, Inspect } from "./ui";

export const loginLabels: Record<string, string> = {
  api: "API-Schlüssel",
  device: "ChatGPT-Konto",
  browser: "OpenRouter-Konto",
};

export function ProviderEditor({
  initial,
  kinds,
  onClose,
  onSaved,
}: {
  initial: Record<string, unknown>;
  kinds: ProviderKind[];
  onClose: () => void;
  onSaved?: (provider: Provider) => void;
}) {
  const { t } = useI18n();
  const api = useAPI();
  const [value, setValue] = useState(initial);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [invalid, setInvalid] = useState<Record<string, string>>({});
  const [advanced, setAdvanced] = useState(Boolean(initial.baseUrl));
  const [connecting, setConnecting] = useState<Provider>();
  const form = useRef<HTMLFormElement>(null);
  const submitting = useRef(false);
  const methods = kinds.find((kind) => kind.id === value.kind)?.authMethods ?? [
    "api",
  ];
  const update = (key: string, next: unknown) =>
    setValue((previous) => ({ ...previous, [key]: next }));
  const focus = (field: string) =>
    requestAnimationFrame(() =>
      form.current
        ?.querySelector<HTMLElement>(
          `[data-field="${field}"] input,[data-field="${field}"] button`,
        )
        ?.focus(),
    );
  async function save() {
    if (submitting.current) return;
    if (!String(value.name ?? "").trim()) {
      setInvalid({ name: t("Dieses Feld ist erforderlich.") });
      focus("name");
      return;
    }
    if (value.kind === "litellm" && !String(value.baseUrl ?? "").trim()) {
      setInvalid({ baseUrl: t("Dieses Feld ist erforderlich.") });
      focus("baseUrl");
      return;
    }
    submitting.current = true;
    setBusy(true);
    setError("");
    setInvalid({});
    try {
      const result = await api.mutate<{ saveProvider: Provider }>(
        "mutation($input:ProviderInput!){saveProvider(input:$input){id name kind authMode billingMode connected accountLabel modelCount catalogStatus catalogError}}",
        {
          input: {
            id: value.id,
            name: value.name,
            kind: value.kind,
            authMode: value.authMode,
            billingMode: value.billingMode,
            baseUrl: value.authMode === "api" ? (value.baseUrl ?? "") : "",
            credential:
              value.authMode === "api" ? (value.credential ?? "") : "",
          },
        },
      );
      const provider = result.saveProvider;
      onSaved?.(provider);
      if (
        provider.authMode !== "api" &&
        !(
          initial.connected &&
          initial.kind === provider.kind &&
          initial.authMode === provider.authMode
        )
      )
        setConnecting(provider);
      else onClose();
    } catch (error) {
      const message = (error as Error).message;
      if (/provider URL/i.test(message)) {
        setAdvanced(true);
        setInvalid({ baseUrl: t(message) });
        focus("baseUrl");
      } else setError(message);
    } finally {
      submitting.current = false;
      setBusy(false);
    }
  }
  return (
    <Inspect
      title={
        connecting
          ? `${t("Provider verbinden")} · ${connecting.name}`
          : t("Provider konfigurieren")
      }
      onClose={() => {
        if (!busy) onClose();
      }}
    >
      {connecting ? (
        <ProviderSignIn provider={connecting} embedded onClose={onClose} />
      ) : (
        <form
          ref={form}
          noValidate
          className="space-y-5"
          onSubmit={(event) => {
            event.preventDefault();
            void save();
          }}
        >
          <Select
            value={String(value.kind ?? "openai")}
            onChange={(kind) =>
              setValue((previous) => ({
                ...previous,
                kind: String(kind),
                authMode: "api",
                billingMode: "metered",
                baseUrl: "",
                credential: "",
              }))
            }
            isDisabled={busy}
            className="gap-2"
            data-field="kind"
          >
            <Label>{t("Provider")}</Label>
            <Select.Trigger className="min-h-11">
              <Select.Value />
              <Select.Indicator />
            </Select.Trigger>
            <Select.Popover>
              <ListBox>
                {kinds.map((kind) => (
                  <ListBox.Item
                    key={kind.id}
                    id={kind.id}
                    textValue={kind.name}
                    className="min-h-11"
                  >
                    <Label>{kind.name}</Label>
                    <ListBox.ItemIndicator />
                  </ListBox.Item>
                ))}
              </ListBox>
            </Select.Popover>
          </Select>
          <TextField
            value={String(value.name ?? "")}
            onChange={(name) => update("name", name)}
            isRequired
            isInvalid={Boolean(invalid.name)}
            isDisabled={busy}
            className="gap-2"
            data-field="name"
          >
            <Label>{t("Name")}</Label>
            <Input
              className="min-h-11"
              placeholder={t("z. B. Forschungsteam")}
            />
            <FieldError>{invalid.name}</FieldError>
          </TextField>
          {methods.length > 1 && (
            <RadioGroup
              value={String(value.authMode ?? "api")}
              onChange={(mode) =>
                setValue((previous) => ({
                  ...previous,
                  authMode: mode,
                  credential: "",
                  baseUrl: "",
                  billingMode: mode === "device" ? "fixed" : "metered",
                }))
              }
              isDisabled={busy}
              className="gap-2"
            >
              <Label>{t("Zugang")}</Label>
              {methods.map((method) => (
                <Radio key={method} value={method} className="min-h-11">
                  <Radio.Content className="min-h-11">
                    <Radio.Control>
                      <Radio.Indicator />
                    </Radio.Control>
                    <Label>{t(loginLabels[method] ?? method)}</Label>
                  </Radio.Content>
                  <Description>
                    {t(
                      method === "api"
                        ? "Mit einem API-Schlüssel des Providers verbinden."
                        : "Im Browser anmelden. Hier wird kein API-Schlüssel benötigt.",
                    )}
                  </Description>
                </Radio>
              ))}
            </RadioGroup>
          )}
          {value.authMode === "api" ? (
            <>
              <TextField
                value={String(value.credential ?? "")}
                onChange={(credential) => update("credential", credential)}
                type="password"
                autoComplete="off"
                isDisabled={busy}
                className="gap-2"
                data-field="credential"
              >
                <Label>{t("API-Schlüssel")}</Label>
                <Input className="min-h-11" />
                <Description>
                  {initial.id
                    ? t(
                        "Leer lassen, um den vorhandenen Schlüssel zu behalten.",
                      )
                    : t(
                        "Der Schlüssel wird gespeichert und nicht wieder angezeigt.",
                      )}
                </Description>
              </TextField>
              {value.kind === "litellm" && (
                <ProviderEndpoint
                  value={String(value.baseUrl ?? "")}
                  onChange={(url) => update("baseUrl", url)}
                  error={invalid.baseUrl}
                  required
                  disabled={busy}
                />
              )}
              <Accordion
                hideSeparator
                expandedKeys={new Set(advanced ? ["advanced"] : [])}
                onExpandedChange={(keys) => setAdvanced(keys.has("advanced"))}
              >
                <Accordion.Item id="advanced">
                  <Accordion.Heading>
                    <Accordion.Trigger className="min-h-11 text-sm">
                      <span className="flex-1 text-left">
                        {t("Erweiterte Einstellungen")}
                      </span>
                      <Accordion.Indicator />
                    </Accordion.Trigger>
                  </Accordion.Heading>
                  <Accordion.Panel>
                    <Accordion.Body className="space-y-5 pb-3">
                      {value.kind !== "litellm" && (
                        <ProviderEndpoint
                          value={String(value.baseUrl ?? "")}
                          onChange={(url) => update("baseUrl", url)}
                          error={invalid.baseUrl}
                          disabled={busy}
                        />
                      )}
                      <Select
                        value={String(value.billingMode ?? "metered")}
                        onChange={(billing) =>
                          update("billingMode", String(billing))
                        }
                        isDisabled={busy}
                        className="gap-2"
                      >
                        <Label>{t("Kostenmodell")}</Label>
                        <Select.Trigger className="min-h-11">
                          <Select.Value />
                          <Select.Indicator />
                        </Select.Trigger>
                        <Select.Popover>
                          <ListBox>
                            {[
                              { id: "metered", label: t("Nach Verbrauch") },
                              { id: "fixed", label: t("Festpreis / Abo") },
                            ].map((choice) => (
                              <ListBox.Item
                                key={choice.id}
                                id={choice.id}
                                textValue={choice.label}
                              >
                                <Label>{choice.label}</Label>
                                <ListBox.ItemIndicator />
                              </ListBox.Item>
                            ))}
                          </ListBox>
                        </Select.Popover>
                      </Select>
                    </Accordion.Body>
                  </Accordion.Panel>
                </Accordion.Item>
              </Accordion>
            </>
          ) : (
            <p className="rounded-lg bg-default p-3 text-sm leading-6 text-muted">
              {t(
                "Nach dem Speichern startet die Anmeldung. Anschließend werden die verfügbaren Modelle automatisch geladen.",
              )}
            </p>
          )}
          <Failure message={error} />
          <div className="flex justify-end gap-2 border-t border-border pt-4">
            <Button
              className="min-h-11"
              variant="ghost"
              isDisabled={busy}
              onPress={onClose}
            >
              {t("Abbrechen")}
            </Button>
            <Button type="submit" className="min-h-11" isPending={busy}>
              {t("Speichern")}
            </Button>
          </div>
        </form>
      )}
    </Inspect>
  );
}

function ProviderEndpoint({
  value,
  onChange,
  error,
  required,
  disabled,
}: {
  value: string;
  onChange: (value: string) => void;
  error?: string;
  required?: boolean;
  disabled: boolean;
}) {
  const { t } = useI18n();
  return (
    <TextField
      value={value}
      onChange={onChange}
      isRequired={required}
      isInvalid={Boolean(error)}
      isDisabled={disabled}
      className="gap-2"
      data-field="baseUrl"
    >
      <Label>{t("API-Basis-URL")}</Label>
      <Input placeholder="https://…" className="min-h-11" />
      <Description>
        {required
          ? t("Adresse des Inference-Gateways.")
          : t(
              "Optional. Leer lassen, um die Standardadresse des Providers zu verwenden.",
            )}
      </Description>
      <FieldError>{error}</FieldError>
    </TextField>
  );
}
