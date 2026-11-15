import { Button, Chip, Input, Label, Link, TextField } from "@heroui/react";
import { useState } from "react";

import type { HuggingFaceConnection } from "./generated";
import { HuggingFaceOAuthSettings } from "./huggingface-oauth";
import { useI18n } from "./preferences";
import { useResourcePage } from "./resource-page";
import { Failure, Loading } from "./ui";

const connectionFields = `connected userName checkedAt authMode
  oauthClientId oauthConfigured oauthConfidential
  login{id authorizationUrl expiresAt}`;
const connectionQuery = `query{huggingFaceConnection{${connectionFields}}}`;

export function HuggingFaceSettings() {
  const { t } = useI18n();
  const resource = useResourcePage<{
    huggingFaceConnection: HuggingFaceConnection | null;
  }>(connectionQuery);
  const [token, setToken] = useState("");
  const connection = resource.data?.huggingFaceConnection;

  return (
    <section
      aria-labelledby="huggingface-settings-title"
      className="space-y-4 border-t border-separator pt-8"
    >
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h2 id="huggingface-settings-title" className="font-medium">
          Hugging Face
        </h2>
        {connection && (
          <Chip
            size="sm"
            variant="soft"
            color={connection.connected ? "success" : "default"}
          >
            {t(connection.connected ? "Verbunden" : "Nicht verbunden")}
          </Chip>
        )}
      </div>
      <p className="max-w-xl text-sm leading-6 text-muted">
        {t(
          "Datensätze für Agenten freigeben. Verwende einen fein eingeschränkten Lese-Token für die benötigten Repositories und gegebenenfalls freigeschaltete Gated-Datensätze.",
        )}
      </p>
      <Link
        href="https://huggingface.co/settings/tokens"
        target="_blank"
        rel="noopener noreferrer"
        className="inline-flex min-h-11 items-center text-sm"
      >
        {t("Token bei Hugging Face erstellen oder widerrufen")}
      </Link>
      <Failure message={resource.error} onRetry={resource.retry} />
      {resource.loading && !connection && <Loading />}
      {connection?.connected && (
        <p className="break-all text-sm">{connection.userName}</p>
      )}
      {connection && <HuggingFaceOAuthSettings connection={connection} />}
      {connection && (
        <form
          className="max-w-xl space-y-4"
          onSubmit={(event) => {
            event.preventDefault();

            if (resource.busy || !token.trim()) return;

            void resource.run(async () => {
              await resource.api.mutate(
                `mutation($token:String!){connectHuggingFace(token:$token){${connectionFields}}}`,
                { token },
              );
              setToken("");
            });
          }}
        >
          <p className="text-sm leading-6 text-muted">
            {t(
              "Der Account wird beim Verbinden geprüft. Bei Zugriffsfehlern prüfe Freigaben und Token-Rechte bei Hugging Face.",
            )}
          </p>
          <TextField
            type="password"
            value={token}
            onChange={setToken}
            autoComplete="off"
            isDisabled={resource.busy}
            className="min-w-0 gap-2"
          >
            <Label>{t("Hugging-Face-Token")}</Label>
            <Input className="min-h-11" spellCheck={false} />
          </TextField>
          <p className="text-sm leading-6 text-muted">
            {t(
              "Der Token bleibt bei Zentrale und wird nicht an Agenten oder die VM weitergegeben. Bedingungen und Zugriffsanfragen für Gated-Datensätze erledigst du selbst bei Hugging Face.",
            )}
          </p>
          <div className="flex flex-wrap gap-2">
            <Button
              type="submit"
              className="min-h-11"
              isPending={resource.busy}
              isDisabled={!token.trim()}
            >
              {t(connection.connected ? "Token ersetzen" : "Verbinden")}
            </Button>
            {connection.connected && (
              <Button
                type="button"
                variant="ghost"
                className="min-h-11"
                isDisabled={resource.busy}
                onPress={() =>
                  resource.run(async () => {
                    await resource.api.mutate(
                      "mutation{disconnectHuggingFace}",
                    );
                    setToken("");
                  })
                }
              >
                {t("Trennen")}
              </Button>
            )}
          </div>
          <p className="text-sm leading-6 text-muted">
            {t(
              "Trennen löscht die lokale Verbindung und beendet laufende HF-Übertragungen. Bereits geladene Dateien bleiben erhalten; den Token selbst widerrufst du bei Hugging Face.",
            )}
          </p>
        </form>
      )}
    </section>
  );
}
