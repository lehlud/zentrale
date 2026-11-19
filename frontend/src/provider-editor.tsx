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
import { browserLoginRedirectURI } from "./browser-login";
import { CodeSnippet } from "./code-snippet";
import type { Provider, ProviderKind } from "./generated";
import { ProviderSignIn } from "./provider-login";
import { useI18n } from "./preferences";
import { Failure, Inspect } from "./ui";

export const loginLabels: Record<string, string> = {
  api: "API-Schlüssel",
  device: "ChatGPT-Konto",
  browser: "OpenRouter-Konto",
  oauth: "Google-API-Konto",
};

export function isHostedProvider(kind: unknown) {
  return kind === "modal" || kind === "replicate" || kind === "fal";
}

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
  const requiresEndpoint = value.kind === "litellm" || value.kind === "modal";
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
    if (submitting.current) {
      return;
    }

    if (!String(value.name ?? "").trim()) {
      setInvalid({ name: t("Dieses Feld ist erforderlich.") });
      focus("name");

      return;
    }

    if (requiresEndpoint && !String(value.baseUrl ?? "").trim()) {
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
            responsesTransport:
              value.kind === "openai" ? value.responsesTransport || "auto" : "",
            promptCacheMode:
              value.kind === "openai" ? value.promptCacheMode || "auto" : "",
            baseUrl: value.authMode === "api" ? (value.baseUrl ?? "") : "",
            credential:
              value.authMode === "api" ? (value.credential ?? "") : "",
            oauthClientId:
              value.authMode === "oauth" ? value.oauthClientId : "",
            oauthProjectId:
              value.authMode === "oauth" ? value.oauthProjectId : "",
            oauthClientSecret:
              value.authMode === "oauth" ? value.oauthClientSecret : "",
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
          initial.authMode === provider.authMode &&
          provider.connected
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
                oauthClientId: "",
                oauthProjectId: "",
                oauthClientSecret: "",
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
              {value.kind === "anthropic" && (
                <p className="text-sm leading-6 text-muted">
                  {t(
                    "Anthropic unterstützt hier API-Schlüssel, keinen Claude-Abo-Login. Claude-Free-/Pro-/Max-/Ultra-Zugänge dürfen nicht in Drittanbieter-Apps übernommen werden.",
                  )}
                </p>
              )}
              <TextField
                value={String(value.credential ?? "")}
                onChange={(credential) => update("credential", credential)}
                type="password"
                autoComplete="off"
                isDisabled={busy}
                className="gap-2"
                data-field="credential"
              >
                <Label>
                  {t(
                    value.kind === "modal"
                      ? "Modal-Proxy-Token"
                      : "API-Schlüssel",
                  )}
                </Label>
                <Input className="min-h-11" />
                <Description>
                  {value.kind === "modal"
                    ? t("Token-ID und Token-Secret mit einem Punkt verbinden.")
                    : initial.id
                      ? t(
                          "Leer lassen, um den vorhandenen Schlüssel zu behalten.",
                        )
                      : t(
                          "Der Schlüssel wird gespeichert und nicht wieder angezeigt.",
                        )}
                </Description>
              </TextField>
              {requiresEndpoint && (
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
                      {!requiresEndpoint && !isHostedProvider(value.kind) && (
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
          {value.authMode === "oauth" && (
            <div className="space-y-4">
              <p className="text-sm leading-6 text-muted">
                {t(
                  "Benötigt eine eigene Google-Cloud-OAuth-App (Webanwendung), eine aktivierte Gemini API und ein berechtigtes API-Projekt. Es gelten API-Kontingente und gegebenenfalls verbrauchsabhängige Kosten, nicht dein AI-Pro-/Ultra-Abo.",
                )}
              </p>
              <CodeSnippet
                text={browserLoginRedirectURI()}
                label={t("OAuth-Callback-Adresse")}
                wrap
              />
              {[
                { key: "oauthClientId", label: "Google OAuth Client-ID" },
                { key: "oauthProjectId", label: "Google-Cloud-Projekt-ID" },
                {
                  key: "oauthClientSecret",
                  label: "Google OAuth Client-Secret",
                },
              ].map((field) => (
                <TextField
                  key={field.key}
                  value={String(value[field.key] ?? "")}
                  onChange={(next) => update(field.key, next)}
                  type={field.key === "oauthClientSecret" ? "password" : "text"}
                  autoComplete="off"
                  isDisabled={busy}
                  isRequired={field.key !== "oauthClientSecret" || !initial.id}
                  className="gap-2"
                >
                  <Label>{t(field.label)}</Label>
                  <Input className="min-h-11" />
                  {field.key === "oauthClientSecret" && (
                    <Description>
                      {t(
                        "Leer lassen, um das vorhandene App-Secret zu behalten. Es wird verschlüsselt gespeichert und nicht wieder angezeigt.",
                      )}
                    </Description>
                  )}
                </TextField>
              ))}
            </div>
          )}
          {value.kind === "openai" && (
            <div className="grid gap-5 sm:grid-cols-2">
              {[
                {
                  key: "responsesTransport",
                  label: t("Antwortübertragung"),
                  choices: [
                    { id: "auto", label: t("Automatisch") },
                    { id: "http", label: "HTTP / SSE" },
                    { id: "websocket", label: "WebSocket" },
                  ],
                },
                {
                  key: "promptCacheMode",
                  label: t("Zusätzliche Cache-Grenzen"),
                  choices: [
                    { id: "auto", label: t("Automatisch") },
                    { id: "off", label: t("Aus") },
                    { id: "explicit", label: t("Aktivieren") },
                  ],
                },
              ].map((field) => (
                <Select
                  key={field.key}
                  value={String(value[field.key] || "auto")}
                  onChange={(next) => update(field.key, String(next))}
                  isDisabled={busy}
                  className="gap-2"
                >
                  <Label>{field.label}</Label>
                  <Select.Trigger className="min-h-11">
                    <Select.Value />
                    <Select.Indicator />
                  </Select.Trigger>
                  <Select.Popover>
                    <ListBox>
                      {field.choices.map((choice) => (
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
              ))}
              <p className="text-xs text-muted sm:col-span-2">
                {t(
                  "Automatisch nutzt WebSocket an offiziellen OpenAI-Adressen und zusätzliche Cache-Grenzen bei unterstützten API-Modellen. Für ChatGPT-Konten lassen sich die Cache-Grenzen separat aktivieren. Nicht unterstützte Funktionen fallen auf den bisherigen Weg zurück.",
                )}
              </p>
            </div>
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
