import { Alert, Button, Card, Chip, Link, buttonVariants } from "@heroui/react";
import { useEffect, useRef, useState } from "react";
import { useAPI, useQuery } from "./api";
import { ActionsMenu } from "./actions-menu";
import { CodeSnippet } from "./code-snippet";
import type { GitConnection, GitLogin } from "./generated";
import { useI18n } from "./preferences";
import { Editor, Failure, Inspect, Loading, useAction } from "./ui";

export const gitConnectionFields =
  "id kind baseUrl clientId configured connected userName";
export const gitConnectionsQuery = `query{gitConnections{${gitConnectionFields}}}`;
const loginFields =
  "id connectionId verificationUrl userCode status expiresAt interval error";
export function gitName(kind: string) {
  return kind === "github" ? "GitHub" : "GitLab";
}

export function GitConnections() {
  const { t } = useI18n();
  const api = useAPI();
  const { data, error, loading } = useQuery<{
    gitConnections: GitConnection[];
  }>(gitConnectionsQuery);
  const [selected, setSelected] = useState<{
    connection: GitConnection;
    configure?: boolean;
  }>();
  const action = useAction();
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
      <Failure message={error || action.error} onRetry={() => api.refresh()} />
      {loading && <Loading />}
      <div className="grid gap-3 sm:grid-cols-2">
        {data?.gitConnections?.map((connection) => (
          <Card key={connection.id} variant="secondary">
            <Card.Content className="gap-4">
              <div className="flex flex-wrap items-center justify-between gap-3">
                <h3 className="font-medium">{gitName(connection.kind)}</h3>
                <Chip
                  size="sm"
                  color={connection.connected ? "success" : "default"}
                  variant="soft"
                >
                  {t(connection.connected ? "Verbunden" : "Nicht verbunden")}
                </Chip>
              </div>
              <p className="break-all text-sm text-muted">
                {connection.connected
                  ? connection.userName
                  : connection.baseUrl}
              </p>
              <div className="flex items-center justify-end gap-2">
                {!connection.connected && (
                  <Button
                    variant="secondary"
                    className="mr-auto min-h-11"
                    onPress={() => setSelected({ connection })}
                  >
                    {t("Verbinden")}
                  </Button>
                )}
                <ActionsMenu
                  label={t("Aktionen für {name}", {
                    name: gitName(connection.kind),
                  })}
                  isDisabled={action.busy}
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
                              action.run(() =>
                                api.mutate(
                                  "mutation($connectionId:String!){disconnectGitConnection(connectionId:$connectionId)}",
                                  { connectionId: connection.id },
                                ),
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
  if (configuring)
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
    | { attempt: number; promise: Promise<{ beginGitLogin: GitLogin }> }
    | undefined
  >(undefined);
  useEffect(() => {
    let active = true;
    if (request.current?.attempt !== attempt)
      request.current = {
        attempt,
        promise: api.mutate(
          `mutation($connectionId:String!){beginGitLogin(connectionId:$connectionId){${loginFields}}}`,
          { connectionId: connection.id },
          { announce: false, refresh: false },
        ),
      };
    request.current.promise
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
    };
  }, [api, connection.id, attempt]);
  useEffect(() => {
    if (!login || login.status !== "pending" || error) return;
    let active = true;
    const timer = setTimeout(
      () => {
        api
          .mutate<{ pollGitLogin: GitLogin }>(
            `mutation($connectionId:String!,$loginId:String!){pollGitLogin(connectionId:$connectionId,loginId:$loginId){${loginFields}}}`,
            { connectionId: connection.id, loginId: login.id },
            { announce: false, refresh: false },
          )
          .then((value) => {
            if (active) {
              setLogin(value.pollGitLogin);
              if (value.pollGitLogin.status === "connected") api.refresh();
            }
          })
          .catch((failure: Error) => {
            if (active) setError(failure.message);
          });
      },
      Math.max(5, login.interval) * 1000,
    );
    return () => {
      active = false;
      clearTimeout(timer);
    };
  }, [api, connection.id, login, error]);
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
          {error && login?.status === "pending" && (
            <Button
              variant="secondary"
              className="min-h-11"
              onPress={() => setError("")}
            >
              {t("Erneut versuchen")}
            </Button>
          )}
          {(error ||
            login?.status === "failed" ||
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
