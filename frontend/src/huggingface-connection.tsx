import {
  Button,
  Card,
  Chip,
  Input,
  Label,
  Link,
  Tabs,
  TextField,
} from "@heroui/react";
import { useState } from "react";

import { useAPI } from "./api";
import { ActionsMenu } from "./actions-menu";
import type { HuggingFaceConnection } from "./generated";
import { HuggingFaceOAuthSettings } from "./huggingface-oauth";
import { useI18n } from "./preferences";
import { useResourcePage } from "./resource-page";
import { Failure, Inspect, Loading, useAction } from "./ui";

const connectionFields = `connected userName checkedAt authMode
  oauthClientId oauthConfigured oauthConfidential
  login{id authorizationUrl expiresAt}`;
const connectionQuery = `query{huggingFaceConnection{${connectionFields}}}`;
type ConnectionDialog = { method: "token" | "oauth"; configure?: boolean };

export function HuggingFaceSettings() {
  const { t } = useI18n();
  const resource = useResourcePage<{
    huggingFaceConnection: HuggingFaceConnection | null;
  }>(connectionQuery);
  const [dialog, setDialog] = useState<ConnectionDialog>();
  const connection = resource.data?.huggingFaceConnection;

  return (
    <section
      aria-labelledby="huggingface-settings-title"
      className="border-t border-separator pt-8"
    >
      <Card variant="secondary">
        <Card.Content className="gap-4">
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
          <p className="text-sm leading-6 text-muted">
            {t("Datensätze für Agenten freigeben.")}
          </p>
          <Failure message={resource.error} onRetry={resource.retry} />
          {resource.loading && !connection && <Loading />}
          {connection?.connected && (
            <div className="min-w-0 space-y-1 text-sm">
              <p className="break-all">{connection.userName}</p>
              {connection.authMode === "oauth" && (
                <p className="text-muted">{t("Über OAuth verbunden")}</p>
              )}
            </div>
          )}
          {connection && (
            <div className="flex items-center justify-between gap-2">
              <Button
                variant="secondary"
                className="min-h-11"
                onPress={() =>
                  setDialog({
                    method: connection.oauthConfigured ? "oauth" : "token",
                  })
                }
              >
                {t(connection.connected ? "Verwalten" : "Verbinden")}
              </Button>
              <ActionsMenu
                label={t("Aktionen für {name}", { name: "Hugging Face" })}
                isDisabled={resource.busy}
                actions={[
                  {
                    id: "configure",
                    label: t("OAuth-App konfigurieren"),
                    onAction: () =>
                      setDialog({ method: "oauth", configure: true }),
                  },
                  {
                    id: "token",
                    label: t(
                      connection.connected
                        ? "Token ersetzen"
                        : "Mit Token verbinden",
                    ),
                    onAction: () => setDialog({ method: "token" }),
                  },
                  ...(connection.connected
                    ? [
                        {
                          id: "disconnect",
                          label: t("Trennen"),
                          destructive: true,
                          onAction: () =>
                            resource.mutate("mutation{disconnectHuggingFace}"),
                        },
                      ]
                    : []),
                ]}
              />
            </div>
          )}
        </Card.Content>
      </Card>
      {connection && dialog && (
        <HuggingFaceConnect
          key={`${dialog.method}:${Boolean(dialog.configure)}`}
          connection={connection}
          initial={dialog}
          onClose={() => setDialog(undefined)}
        />
      )}
    </section>
  );
}

function HuggingFaceConnect({
  connection,
  initial,
  onClose,
}: {
  connection: HuggingFaceConnection;
  initial: ConnectionDialog;
  onClose: () => void;
}) {
  const { t } = useI18n();
  const api = useAPI();
  const action = useAction();
  const [method, setMethod] = useState(initial.method);
  const [token, setToken] = useState("");
  const [oauthBusy, setOAuthBusy] = useState(false);
  const busy = action.busy || oauthBusy;

  return (
    <Inspect
      title={t("{name} verbinden", { name: "Hugging Face" })}
      onClose={onClose}
      isBusy={busy}
    >
      <Tabs
        variant="secondary"
        align="start"
        selectedKey={method}
        onSelectionChange={(key) =>
          setMethod(String(key) as ConnectionDialog["method"])
        }
        className="gap-5"
      >
        <Tabs.ListContainer>
          <Tabs.List aria-label={t("Anmeldeart")}>
            <Tabs.Tab id="token" isDisabled={busy} className="min-h-11">
              Access Token
              <Tabs.Indicator />
            </Tabs.Tab>
            <Tabs.Tab id="oauth" isDisabled={busy} className="min-h-11">
              {t("Browser-Anmeldung")}
              <Tabs.Indicator />
            </Tabs.Tab>
          </Tabs.List>
        </Tabs.ListContainer>
        <Tabs.Panel id="token" className="p-0">
          <form
            className="space-y-4"
            onSubmit={(event) => {
              event.preventDefault();
              if (busy || !token.trim()) return;
              void action.run(async () => {
                await api.mutate(
                  `mutation($token:String!){connectHuggingFace(token:$token){${connectionFields}}}`,
                  { token },
                );
                setToken("");
                onClose();
              });
            }}
          >
            <p className="text-sm leading-6 text-muted">
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
            <TextField
              type="password"
              value={token}
              onChange={setToken}
              autoComplete="off"
              isDisabled={busy}
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
            <Failure message={action.error} />
            <div className="flex flex-wrap justify-end gap-2">
              <Button
                type="button"
                variant="ghost"
                className="min-h-11"
                isDisabled={busy}
                onPress={onClose}
              >
                {t("Abbrechen")}
              </Button>
              <Button
                type="submit"
                className="min-h-11"
                isPending={action.busy}
                isDisabled={!token.trim() || busy}
              >
                {t(connection.connected ? "Token ersetzen" : "Verbinden")}
              </Button>
            </div>
          </form>
        </Tabs.Panel>
        <Tabs.Panel id="oauth" className="p-0">
          <HuggingFaceOAuthSettings
            connection={connection}
            configure={initial.configure}
            onBusyChange={setOAuthBusy}
          />
        </Tabs.Panel>
      </Tabs>
    </Inspect>
  );
}
