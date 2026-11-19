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

const modalPresets = ["jialinyyzz/humanizer", "abacusai/Smaug-Humanizer"];

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
  const [value, setValue] = useState<Record<string, unknown>>(() => {
    const destination = String(initial.baseUrl ?? "");
    const native =
      initial.kind === "modal" && destination.startsWith("modal://");
    const [modalEnvironment = "main", modalApp = "", modalFunction = ""] =
      native ? destination.slice("modal://".length).split("/") : [];
    return {
      ...initial,
      baseUrl: native ? "" : destination,
      modalMode:
        initial.modalModelId || !initial.id
          ? "managed"
          : native
            ? "native"
            : "proxy",
      modalModelId: initial.modalModelId || modalPresets[0],
      modalEnvironment,
      modalApp,
      modalFunction,
    };
  });
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
  const isModal = value.kind === "modal";
  const managedModal = isModal && value.modalMode === "managed";
  const nativeModal = isModal && value.modalMode === "native";
  const apiModal = nativeModal || managedModal;
  const requiresEndpoint = value.kind === "litellm" || (isModal && !apiModal);
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

    if (!managedModal && !String(value.name ?? "").trim()) {
      setInvalid({ name: t("Dieses Feld ist erforderlich.") });
      focus("name");

      return;
    }

    let endpoint = String(value.baseUrl ?? "").trim();
    let credential = value.credential ?? "";
    const tokenID = String(value.modalTokenID ?? "").trim();
    const tokenSecret = String(value.modalTokenSecret ?? "").trim();
    if (requiresEndpoint && !endpoint) {
      setInvalid({ baseUrl: t("Dieses Feld ist erforderlich.") });
      focus("baseUrl");

      return;
    }

    if (isModal) {
      if (nativeModal) {
        const fields = ["modalEnvironment", "modalApp", "modalFunction"];
        const segments = fields.map((field) =>
          String(value[field] ?? "").trim(),
        );
        const invalidSegment = segments.findIndex(
          (segment) => !/^[A-Za-z0-9][A-Za-z0-9_.-]{0,99}$/.test(segment),
        );
        if (invalidSegment >= 0) {
          const field = fields[invalidSegment];
          setInvalid({ [field]: t("Gib einen gültigen Modal-Namen ein.") });
          focus(field);
          return;
        }
        endpoint = `modal://${segments.join("/")}`;
      } else {
        endpoint = endpoint.replace(/\/+$/, "");
      }
      const keepToken =
        initial.kind === "modal" &&
        (managedModal
          ? initial.modalModelId === value.modalModelId &&
            initial.catalogStatus !== "disconnected"
          : initial.connected &&
            endpoint === String(initial.baseUrl ?? "").replace(/\/+$/, "")) &&
        !tokenID &&
        !tokenSecret;
      if (!keepToken) {
        const idPrefix = apiModal ? "ak-" : "wk-";
        const secretPrefix = apiModal ? "as-" : "ws-";
        for (const [field, token, prefix] of [
          ["modalTokenID", tokenID, idPrefix],
          ["modalTokenSecret", tokenSecret, secretPrefix],
        ]) {
          if (
            !token.startsWith(prefix) ||
            !/^[A-Za-z0-9_-]+$/.test(token.slice(prefix.length))
          ) {
            setInvalid({
              [field]: t(
                "Dieses Feld braucht einen gültigen Wert mit Präfix {prefix}.",
                { prefix },
              ),
            });
            focus(field);
            return;
          }
        }
        credential = `${tokenID}.${tokenSecret}`;
      } else {
        credential = "";
      }
    }

    submitting.current = true;
    setBusy(true);
    setError("");
    setInvalid({});

    try {
      if (managedModal) {
        const result = await api.mutate<{ setupModalHumanizer: Provider }>(
          "mutation($tokenId:String,$tokenSecret:String,$modelId:String){setupModalHumanizer(tokenId:$tokenId,tokenSecret:$tokenSecret,modelId:$modelId){id}}",
          { tokenId: tokenID, tokenSecret, modelId: value.modalModelId },
        );
        onSaved?.(result.setupModalHumanizer);
        onClose();
        return;
      }
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
            baseUrl: value.authMode === "api" ? endpoint : "",
            credential: value.authMode === "api" ? credential : "",
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
                modalMode: "managed",
                modalModelId: modalPresets[0],
                modalEnvironment: "main",
                modalApp: "",
                modalFunction: "",
                modalTokenID: "",
                modalTokenSecret: "",
              }))
            }
            isDisabled={busy || Boolean(initial.modalModelId)}
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
          {!managedModal && (
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
          )}
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
              {isModal ? (
                <>
                  <Select
                    value={String(value.modalMode)}
                    onChange={(mode) => {
                      setInvalid({});
                      setValue((previous) => ({
                        ...previous,
                        modalMode: mode,
                        modalTokenID: "",
                        modalTokenSecret: "",
                      }));
                    }}
                    isDisabled={busy || Boolean(initial.modalModelId)}
                    className="gap-2"
                  >
                    <Label>{t("Modal-Zugang")}</Label>
                    <Select.Trigger className="min-h-11">
                      <Select.Value />
                      <Select.Indicator />
                    </Select.Trigger>
                    <Select.Popover>
                      <ListBox>
                        <ListBox.Item
                          id="managed"
                          textValue={t("Humanizer automatisch bereitstellen")}
                          className="min-h-11"
                        >
                          <Label>
                            {t("Humanizer automatisch bereitstellen")}
                          </Label>
                          <ListBox.ItemIndicator />
                        </ListBox.Item>
                        <ListBox.Item
                          id="proxy"
                          textValue={t("Proxy-Token (Web Function)")}
                          className="min-h-11"
                        >
                          <Label>{t("Proxy-Token (Web Function)")}</Label>
                          <ListBox.ItemIndicator />
                        </ListBox.Item>
                        <ListBox.Item
                          id="native"
                          textValue={t("API-Token (Modal-Funktion)")}
                          className="min-h-11"
                        >
                          <Label>{t("API-Token (Modal-Funktion)")}</Label>
                          <ListBox.ItemIndicator />
                        </ListBox.Item>
                      </ListBox>
                    </Select.Popover>
                  </Select>
                  <p className="text-sm leading-6 text-muted">
                    {managedModal
                      ? t(
                          "Zentrale stellt den Humanizer in deinem Modal-Workspace bereit. Das erste Deployment kann einige Minuten dauern.",
                        )
                      : nativeModal
                        ? t(
                            "Erstelle das Token-Paar unter API Tokens in den Modal-Einstellungen. Zentrale ruft eine bereitgestellte Modal-Funktion auf.",
                          )
                        : t(
                            "Erstelle das Token-Paar unter Proxy Tokens in den Modal-Einstellungen. Zentrale ruft die URL deiner Web Function auf.",
                          )}
                  </p>
                  <div className="grid gap-4 sm:grid-cols-2">
                    {[
                      {
                        field: "modalTokenID",
                        label: "Token-ID",
                        placeholder: apiModal ? "ak-…" : "wk-…",
                        secret: false,
                      },
                      {
                        field: "modalTokenSecret",
                        label: "Token-Secret",
                        placeholder: apiModal ? "as-…" : "ws-…",
                        secret: true,
                      },
                    ].map(({ field, label, placeholder, secret }) => (
                      <TextField
                        key={field}
                        value={String(value[field] ?? "")}
                        onChange={(next) => update(field, next)}
                        type={secret ? "password" : "text"}
                        autoComplete={secret ? "new-password" : "off"}
                        isDisabled={busy}
                        isInvalid={Boolean(invalid[field])}
                        className="gap-2"
                        data-field={field}
                      >
                        <Label>{t(label)}</Label>
                        <Input className="min-h-11" placeholder={placeholder} />
                        <FieldError>{invalid[field]}</FieldError>
                      </TextField>
                    ))}
                  </div>
                  {initial.kind === "modal" &&
                    (Boolean(initial.connected) ||
                      Boolean(
                        initial.modalModelId &&
                        initial.catalogStatus !== "disconnected",
                      )) && (
                      <p className="text-sm text-muted">
                        {t(
                          "Bei gleichem Ziel kannst du beide Token-Felder leer lassen, um den gespeicherten Zugang zu behalten.",
                        )}
                      </p>
                    )}
                  {managedModal && (
                    <Select
                      value={String(value.modalModelId)}
                      onChange={(model) => update("modalModelId", model)}
                      isDisabled={busy || Boolean(initial.modalModelId)}
                      className="gap-2"
                    >
                      <Label>{t("Humanizer-Modell")}</Label>
                      <Select.Trigger className="min-h-11">
                        <Select.Value />
                        <Select.Indicator />
                      </Select.Trigger>
                      <Select.Popover>
                        <ListBox>
                          {modalPresets.map((model) => (
                            <ListBox.Item
                              key={model}
                              id={model}
                              className="min-h-11"
                            >
                              <Label>{model}</Label>
                              <ListBox.ItemIndicator />
                            </ListBox.Item>
                          ))}
                        </ListBox>
                      </Select.Popover>
                    </Select>
                  )}
                  {nativeModal && (
                    <div className="grid gap-4 sm:grid-cols-3">
                      {[
                        ["modalEnvironment", "Modal-Umgebung", "main"],
                        ["modalApp", "App-Name", "zentrale-humanizer"],
                        ["modalFunction", "Funktionsname", "run_batch"],
                      ].map(([field, label, placeholder]) => (
                        <TextField
                          key={field}
                          value={String(value[field] ?? "")}
                          onChange={(next) => update(field, next)}
                          isRequired
                          isDisabled={busy}
                          isInvalid={Boolean(invalid[field])}
                          className="gap-2"
                          data-field={field}
                        >
                          <Label>{t(label)}</Label>
                          <Input
                            className="min-h-11"
                            placeholder={placeholder}
                          />
                          <FieldError>{invalid[field]}</FieldError>
                        </TextField>
                      ))}
                    </div>
                  )}
                </>
              ) : (
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
              )}
              {requiresEndpoint && (
                <ProviderEndpoint
                  value={String(value.baseUrl ?? "")}
                  onChange={(url) => update("baseUrl", url)}
                  error={invalid.baseUrl}
                  label={isModal ? "Web-Function-URL" : undefined}
                  required
                  disabled={busy}
                />
              )}
              {!managedModal && (
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
              )}
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
              {t(managedModal ? "Bereitstellen" : "Speichern")}
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
  label = "API-Basis-URL",
  required,
  disabled,
}: {
  value: string;
  onChange: (value: string) => void;
  error?: string;
  label?: string;
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
      <Label>{t(label)}</Label>
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
