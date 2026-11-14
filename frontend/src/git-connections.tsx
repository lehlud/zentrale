import { Alert, Button, Card, Chip, Link, buttonVariants } from "@heroui/react";
import { useEffect, useRef, useState } from "react";

import { useAPI } from "./api";
import { ResourceContent, useResourcePage } from "./resource-page";
import { ActionsMenu } from "./actions-menu";
import { CodeSnippet } from "./code-snippet";
import type { GitConnection, GitLogin } from "./generated";
import { useI18n } from "./preferences";
import { Editor, Failure, Inspect, Loading } from "./ui";

export const gitConnectionFields =
  "id kind baseUrl clientId configured connected userName reauthorizationRequired";

export const gitConnectionsQuery = `query{gitConnections{${gitConnectionFields}}}`;

const loginFields =
  "id connectionId verificationUrl userCode status expiresAt interval error";

export function gitName(kind: string) {
  return kind === "github" ? "GitHub" : "GitLab";
}

export function GitConnections() {
  const { t } = useI18n();
  const resource = useResourcePage<{
    gitConnections: GitConnection[];
  }>(gitConnectionsQuery);
  const { data } = resource;
  const [selected, setSelected] = useState<{
    connection: GitConnection;
    configure?: boolean;
  }>();

  return (
    <section
      aria-labelledby="git-connections-title"
      className="space-y-4 border-t border-separator pt-8"
    >
      <div>
        <h2 id="git-connections-title" className="font-medium">
          {t("Git-Verbindungen")}
        </h2>
        <p className="mt-2 text-sm leading-6 text-muted">
          {t(
            "Repositories auswählen oder erstellen. Die Zentrale verwaltet den passenden Deploy-Key automatisch.",
          )}
        </p>
      </div>
      <Failure message={resource.error} onRetry={resource.retry} />
      <ResourceContent resource={resource} items={data?.gitConnections}>
        <div className="grid gap-3 sm:grid-cols-2">
          {data?.gitConnections.map((connection) => (
            <Card key={connection.id} variant="secondary">
              <Card.Content className="gap-4">
                <div className="flex flex-wrap items-center justify-between gap-3">
                  <h3 className="font-medium">{gitName(connection.kind)}</h3>
                  <Chip
                    size="sm"
                    color={
                      connection.reauthorizationRequired
                        ? "warning"
                        : connection.connected
                          ? "success"
                          : "default"
                    }
                    variant="soft"
                  >
                    {t(
                      connection.reauthorizationRequired
                        ? "Erneut anmelden"
                        : connection.connected
                          ? "Verbunden"
                          : "Nicht verbunden",
                    )}
                  </Chip>
                </div>
                <p className="break-all text-sm text-muted">
                  {connection.connected
                    ? connection.userName
                    : connection.baseUrl}
                </p>
                {connection.reauthorizationRequired && (
                  <Alert status="warning">
                    <Alert.Content>
                      <Alert.Description>
                        {t(
                          "Für Änderungen an GitHub-Actions-Workflows bitte die GitHub-Verbindung erneut autorisieren.",
                        )}
                      </Alert.Description>
                    </Alert.Content>
                  </Alert>
                )}
                <div className="flex items-center justify-end gap-2">
                  {(!connection.connected ||
                    connection.reauthorizationRequired) && (
                    <Button
                      variant="secondary"
                      className="mr-auto min-h-11"
                      onPress={() => setSelected({ connection })}
                    >
                      {t(
                        connection.reauthorizationRequired
                          ? "Erneut anmelden"
                          : "Verbinden",
                      )}
                    </Button>
                  )}
                  <ActionsMenu
                    label={t("Aktionen für {name}", {
                      name: gitName(connection.kind),
                    })}
                    isDisabled={resource.busy}
                    actions={[
                      {
                        id: "configure",
                        label: t("OAuth-App konfigurieren"),
                        onAction: () =>
                          setSelected({ connection, configure: true }),
                      },
                      ...(connection.connected
                        ? [
                            {
                              id: "sign-in",
                              label: t("Erneut anmelden"),
                              onAction: () => setSelected({ connection }),
                            },
                            {
                              id: "disconnect",
                              label: t("Trennen"),
                              destructive: true,
                              onAction: () =>
                                resource.mutate(
                                  "mutation($connectionId:String!){disconnectGitConnection(connectionId:$connectionId)}",
                                  { connectionId: connection.id },
                                ),
                            },
                          ]
                        : []),
                    ]}
                  />
                </div>
              </Card.Content>
            </Card>
          ))}
        </div>
      </ResourceContent>
      {selected && (
        <GitConnect
          connection={selected.connection}
          configureOnly={selected.configure}
          onClose={() => setSelected(undefined)}
        />
      )}
    </section>
  );
}

export function GitConnect({
  connection,
  onClose,
  configureOnly = false,
}: {
  connection: GitConnection;
  onClose: () => void;
  configureOnly?: boolean;
}) {
  const { t } = useI18n();
  const api = useAPI();
  const [current, setCurrent] = useState(connection);
  const [configuring, setConfiguring] = useState(
    configureOnly || !connection.configured,
  );
  if (configuring) {
    return (
      <Editor
        title={t("{name} verbinden", { name: gitName(connection.kind) })}
        initial={{
          kind: current.kind,
          baseUrl: current.baseUrl,
          clientId: current.clientId,
          clientSecret: "",
        }}
        fields={[
          ...(connection.kind === "gitlab"
            ? [
                {
                  key: "baseUrl",
                  label: t("GitLab-Adresse"),
                  required: true,
                  placeholder: "https://gitlab.com",
                },
              ]
            : []),
          { key: "clientId", label: t("OAuth-App Client-ID"), required: true },
          ...(connection.kind === "gitlab"
            ? [
                {
                  key: "clientSecret",
                  label: t("Client-Secret (optional)"),
                  type: "password" as const,
                  description: t(
                    "Nur für vertrauliche OAuth-Apps. Leer lassen, um ein vorhandenes Secret zu behalten.",
                  ),
                },
              ]
            : []),
        ]}
        renderExtra={() => (
          <p className="text-sm leading-6 text-muted">
            {t(
              connection.kind === "github"
                ? "Einmalig eine eigene OAuth-App registrieren und den Device-Flow aktivieren."
                : "Einmalig eine OAuth-App mit api-Berechtigung registrieren. GitLab muss den Device-Flow unterstützen.",
            )}{" "}
            <Link
              href={
                connection.kind === "github"
                  ? "https://github.com/settings/developers"
                  : `${current.baseUrl}/-/user_settings/applications`
              }
              target="_blank"
              rel="noopener noreferrer"
            >
              {t("OAuth-App registrieren ↗")}
            </Link>
          </p>
        )}
        onClose={onClose}
        closeOnSave={configureOnly}
        submitLabel={t(configureOnly ? "Speichern" : "Weiter zur Anmeldung")}
        onSave={async (input) => {
          const result = await api.mutate<{
            configureGitConnection: GitConnection;
          }>(
            `mutation($input:GitConnectionConfigInput!){configureGitConnection(input:$input){${gitConnectionFields}}}`,
            { input },
            { announce: configureOnly },
          );
          setCurrent(result.configureGitConnection);
          if (!configureOnly) setConfiguring(false);
        }}
        // A successful configuration continues into the browser login in this dialog.
      />
    );
  }

  return <GitDeviceLogin connection={current} onClose={onClose} />;
}

function GitDeviceLogin({
  connection,
  onClose,
}: {
  connection: GitConnection;
  onClose: () => void;
}) {
  const api = useAPI();
  const { t } = useI18n();
  const [login, setLogin] = useState<GitLogin>();
  const [error, setError] = useState("");
  const [attempt, setAttempt] = useState(0);
  const request = useRef<
    | {
        attempt: number;
        connectionId: string;
        promise: Promise<{ beginGitLogin: GitLogin }>;
      }
    | undefined
  >(undefined);
  const lifecycle = useRef(0);

  useEffect(() => {
    let active = true;
    const generation = ++lifecycle.current;
    if (
      request.current?.attempt !== attempt ||
      request.current.connectionId !== connection.id
    )
      request.current = {
        attempt,
        connectionId: connection.id,
        promise: api.mutate(
          `mutation($connectionId:String!){beginGitLogin(connectionId:$connectionId){${loginFields}}}`,
          { connectionId: connection.id },
          { announce: false, refresh: false },
        ),
      };

    const promise = request.current.promise;
    promise
      .then((value) => {
        if (active) {
          setLogin(value.beginGitLogin);
          setError("");
        }
      })
      .catch((failure: Error) => {
        if (active) setError(failure.message);
      });

    return () => {
      active = false;
      // StrictMode reuses the same begin request on its immediate remount.
      // Cancel only a real close/replacement, including a begin still in flight.
      queueMicrotask(() => {
        if (
          lifecycle.current !== generation &&
          request.current?.promise === promise
        )
          return;
        void promise
          .then((value) =>
            api.query(
              "mutation($connectionId:String!,$loginId:String!){cancelGitLogin(connectionId:$connectionId,loginId:$loginId)}",
              { connectionId: connection.id, loginId: value.beginGitLogin.id },
            ),
          )
          .catch(() => {});
      });
    };
  }, [api, connection.id, attempt]);

  const loginId = login?.id;
  useEffect(() => {
    if (!loginId) return;
    return api.watch<{ gitLogin: GitLogin | null }>(
      `query($connectionId:String!,$loginId:String!){gitLogin(connectionId:$connectionId,loginId:$loginId){${loginFields}}}`,
      { connectionId: connection.id, loginId },
      (value) => {
        if (value.gitLogin) {
          setLogin(value.gitLogin);
          setError("");
        } else {
          setLogin((previous) =>
            previous ? { ...previous, status: "cancelled" } : previous,
          );
          setError("This login is no longer current; start again.");
        }
      },
      (failure) => setError(failure.message),
    );
  }, [api, connection.id, loginId]);

  const restart = () => {
    setLogin(undefined);
    setError("");
    setAttempt((value) => value + 1);
  };

  return (
    <Inspect
      title={t("{name} verbinden", { name: gitName(connection.kind) })}
      onClose={onClose}
    >
      <div className="space-y-5">
        <Failure message={error || login?.error} />
        {!login && !error && <Loading />}
        {login?.status === "pending" && (
          <>
            <p className="text-sm leading-6 text-muted">
              {t(
                "Diesen Code auf der Anmeldeseite eingeben. Die Zentrale erkennt die Anmeldung automatisch.",
              )}
            </p>
            <CodeSnippet text={login.userCode} label={t("Anmeldecode")} wrap />
            <Link
              className={buttonVariants({ className: "min-h-11" })}
              href={login.verificationUrl}
              target="_blank"
              rel="noopener noreferrer"
            >
              {t("Anmeldeseite öffnen ↗")}
            </Link>
            <p role="status" className="text-sm text-muted">
              {t("Warten auf Bestätigung …")}
            </p>
          </>
        )}
        {login?.status === "connected" && (
          <Alert status="success" role="status">
            <Alert.Indicator />
            <Alert.Content>
              <Alert.Title>{t("Verbunden")}</Alert.Title>
              <Alert.Description>
                {t("Du kannst jetzt Repositories auswählen und erstellen.")}
              </Alert.Description>
            </Alert.Content>
          </Alert>
        )}
        <div className="flex flex-wrap justify-end gap-2">
          {(error ||
            login?.status === "failed" ||
            login?.status === "cancelled" ||
            login?.status === "expired") && (
            <Button variant="secondary" className="min-h-11" onPress={restart}>
              {t("Anmeldung neu starten")}
            </Button>
          )}
          <Button
            variant={login?.status === "connected" ? "primary" : "ghost"}
            className="min-h-11"
            onPress={onClose}
          >
            {t(login?.status === "connected" ? "Fertig" : "Schließen")}
          </Button>
        </div>
      </div>
    </Inspect>
  );
}
