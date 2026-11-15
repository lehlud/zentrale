import { Button, Checkbox, Input, Label, Link, TextField } from "@heroui/react";
import { useEffect, useState } from "react";

import { useAPI } from "./api";
import { browserLoginRedirectURI } from "./browser-login";
import type { HuggingFaceConnection } from "./generated";
import { useI18n } from "./preferences";
import { Failure, useAction } from "./ui";

export function HuggingFaceOAuthSettings({
  connection,
}: {
  connection: HuggingFaceConnection;
}) {
  const api = useAPI();
  const action = useAction();
  const { t } = useI18n();
  const [clientId, setClientId] = useState(connection.oauthClientId ?? "");
  const [secret, setSecret] = useState("");
  const [publicClient, setPublicClient] = useState(
    !connection.oauthConfidential,
  );
  const login = connection.login;
  const redirectUri = browserLoginRedirectURI();

  useEffect(() => {
    setClientId(connection.oauthClientId ?? "");
    setPublicClient(!connection.oauthConfidential);
    setSecret("");
  }, [connection.oauthClientId, connection.oauthConfidential]);

  return (
    <div className="max-w-xl space-y-4 rounded-xl border border-separator p-4">
      <h3 className="font-medium">{t("Mit Hugging Face anmelden")}</h3>
      <p className="text-sm leading-6 text-muted">
        {t(
          "OAuth und Access Token sind alternative Anmeldewege. OAuth erlaubt Profil- und Lesezugriff auf private sowie bereits freigeschaltete Gated-Repositories. Organisationen wählst du bei Hugging Face; für einzelne Repositories ist ein fein eingeschränkter Token genauer.",
        )}
      </p>
      <Failure message={action.error} />
      <details open={!connection.oauthConfigured} className="space-y-3">
        <summary className="cursor-pointer py-2 text-sm font-medium">
          {t("Hugging-Face-OAuth-App einrichten")}
        </summary>
        <form
          className="space-y-4 pt-2"
          onSubmit={(event) => {
            event.preventDefault();

            if (action.busy) return;

            void action.run(async () => {
              await api.mutate(
                `mutation($value:HuggingFaceOAuthConfigInput!){configureHuggingFaceOAuth(value:$value){oauthConfigured}}`,
                { value: { clientId, clientSecret: secret, publicClient } },
              );
              setSecret("");
            });
          }}
        >
          <Link
            href="https://huggingface.co/settings/applications/new"
            target="_blank"
            rel="noopener noreferrer"
            className="inline-flex min-h-11 items-center text-sm"
          >
            {t("OAuth-App bei Hugging Face registrieren")}
          </Link>
          <p className="break-all text-sm leading-6 text-muted">
            {t("Diese Callback-URL exakt in der OAuth-App registrieren:")}
            <code className="mt-1 block select-text">{redirectUri}</code>
          </p>
          <TextField
            value={clientId}
            onChange={setClientId}
            isDisabled={action.busy}
            className="gap-2"
          >
            <Label>{t("Hugging-Face-OAuth-Client-ID")}</Label>
            <Input className="min-h-11" spellCheck={false} />
          </TextField>
          <Checkbox
            isSelected={publicClient}
            onChange={setPublicClient}
            isDisabled={action.busy}
          >
            <Checkbox.Content>
              <Checkbox.Control>
                <Checkbox.Indicator />
              </Checkbox.Control>
              <Label>{t("Öffentliche OAuth-App ohne Client-Secret")}</Label>
            </Checkbox.Content>
          </Checkbox>
          {!publicClient && (
            <TextField
              type="password"
              value={secret}
              onChange={setSecret}
              autoComplete="off"
              isDisabled={action.busy}
              className="gap-2"
            >
              <Label>{t("Hugging-Face-OAuth-Client-Secret")}</Label>
              <Input className="min-h-11" spellCheck={false} />
            </TextField>
          )}
          <p className="text-sm leading-6 text-muted">
            {t(
              "Ein leeres Secret behält das bisherige Secret derselben Client-ID. Eine andere OAuth-App trennt eine bestehende OAuth-Verbindung; eine Token-Verbindung bleibt erhalten. Ohne Client-ID wird OAuth deaktiviert.",
            )}
          </p>
          <Button type="submit" isPending={action.busy} className="min-h-11">
            {t("OAuth-App speichern")}
          </Button>
        </form>
      </details>
      {connection.authMode === "oauth" && connection.connected && (
        <p className="text-sm">{t("Über OAuth verbunden")}</p>
      )}
      <Button
        isDisabled={!connection.oauthConfigured || action.busy}
        className="min-h-11"
        onPress={() =>
          action.run(() =>
            api.mutate(
              `mutation($redirectUri:String!){beginHuggingFaceLogin(redirectUri:$redirectUri){id}}`,
              { redirectUri },
            ),
          )
        }
      >
        {t(login?.id ? "Anmeldung neu starten" : "Mit Hugging Face anmelden")}
      </Button>
      {login?.id && (
        <div className="space-y-3">
          <p className="text-sm leading-6 text-muted">
            {t(
              "Öffne die Anmeldung und bestätige den Zugriff bei Hugging Face.",
            )}
          </p>
          <div className="flex flex-wrap items-center gap-3">
            <Link
              href={login.authorizationUrl}
              target="_blank"
              rel="noopener noreferrer"
              className="inline-flex min-h-11 items-center text-sm"
            >
              {t("Hugging-Face-Anmeldung öffnen")}
            </Link>
            <Button
              variant="ghost"
              isDisabled={action.busy}
              className="min-h-11"
              onPress={() =>
                action.run(() =>
                  api.mutate(
                    `mutation($id:String!){endHuggingFaceLogin(id:$id)}`,
                    { id: login.id },
                  ),
                )
              }
            >
              {t("Abbrechen")}
            </Button>
          </div>
        </div>
      )}
    </div>
  );
}
