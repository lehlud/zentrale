import {
  Alert,
  Button,
  Card,
  FieldError,
  Input,
  Label,
  Link,
  TextField,
  buttonVariants,
} from "@heroui/react";
import { useCallback, useEffect, useRef, useState } from "react";
import { useAPI } from "./api";
import type { Provider, ProviderLogin } from "./generated";
import { useI18n } from "./preferences";
import { Failure, Inspect, Loading, useAction } from "./ui";

const loginFields =
  "id providerId method status verificationUrl userCode interval expiresAt error";

export function ProviderSignIn({
  provider,
  onClose,
  embedded = false,
}: {
  provider: Provider;
  onClose: () => void;
  embedded?: boolean;
}) {
  const api = useAPI();
  const { t, numberLocale } = useI18n();
  const [login, setLogin] = useState<ProviderLogin>();
  const [code, setCode] = useState("");
  const [error, setError] = useState("");
  const [attempt, setAttempt] = useState(0);
  const [copied, setCopied] = useState(false);
  const codeInput = useRef<HTMLInputElement>(null);
  const action = useAction();
  const cancel = useCallback(
    (id: string) =>
      api
        .query("mutation($id:String!){cancelProviderLogin(id:$id)}", { id })
        .catch(() => {}),
    [api],
  );
  useEffect(() => {
    let active = true;
    let sessionId = "";
    setLogin(undefined);
    setError("");
    setCode("");
    setCopied(false);
    api
      .query<{ beginProviderLogin: ProviderLogin }>(
        `mutation($providerId:String!){beginProviderLogin(providerId:$providerId){${loginFields}}}`,
        { providerId: provider.id },
      )
      .then((result) => {
        sessionId = result.beginProviderLogin.id;
        if (active) setLogin(result.beginProviderLogin);
        else void cancel(sessionId);
      })
      .catch((error: Error) => {
        if (active) setError(error.message);
      });
    return () => {
      active = false;
      if (sessionId) void cancel(sessionId);
    };
  }, [api, provider.id, attempt, cancel]);
  useEffect(() => {
    if (login?.method !== "device" || login.status !== "pending" || error)
      return;
    let active = true;
    const timer = setTimeout(
      () => {
        api
          .query<{ pollProviderLogin: ProviderLogin }>(
            `mutation($id:String!){pollProviderLogin(id:$id){${loginFields}}}`,
            { id: login.id },
          )
          .then((result) => {
            if (!active) return;
            setLogin(result.pollProviderLogin);
            if (result.pollProviderLogin.status === "connected") api.refresh();
          })
          .catch((error: Error) => {
            if (active) setError(error.message);
          });
      },
      Math.max(5, login.interval) * 1000,
    );
    return () => {
      active = false;
      clearTimeout(timer);
    };
  }, [api, login, error]);
  const failed = Boolean(
    error || login?.status === "error" || login?.status === "expired",
  );
  const body = (
    <div className="space-y-5">
      <Failure
        message={
          error ||
          (login?.method !== "browser" || login.status !== "pending"
            ? login?.error
            : undefined)
        }
      />
      {!login && !error && <Loading />}
      {login?.status === "pending" && !error && (
        <>
          <p className="text-sm leading-6 text-muted">
            {t(
              login.method === "device"
                ? "Diesen Code auf der Anmeldeseite eingeben. Die Zentrale erkennt die Anmeldung automatisch."
                : "Bei OpenRouter anmelden und den angezeigten Code hier einfügen.",
            )}
          </p>
          {login.userCode && (
            <Card variant="secondary">
              <Card.Content className="flex-row flex-wrap items-center justify-between gap-3">
                <div className="min-w-0 space-y-2">
                  <p className="text-xs text-muted">{t("Anmeldecode")}</p>
                  <p className="break-all font-mono text-2xl tracking-wider select-all">
                    {login.userCode}
                  </p>
                </div>
                <Button
                  className="min-h-11"
                  variant="secondary"
                  onPress={() =>
                    action.run(async () => {
                      await navigator.clipboard.writeText(login.userCode);
                      setCopied(true);
                    })
                  }
                >
                  {t(copied ? "Kopiert" : "Code kopieren")}
                </Button>
              </Card.Content>
            </Card>
          )}
          <Link
            href={login.verificationUrl}
            target="_blank"
            rel="noopener noreferrer"
            className={buttonVariants({
              className: "min-h-11",
              variant: "primary",
            })}
          >
            {t("Anmeldeseite öffnen ↗")}
          </Link>
          {login.method === "device" ? (
            <p role="status" className="text-sm text-muted">
              {t("Warte auf Anmeldung im Browser …")}
            </p>
          ) : (
            <form
              className="space-y-4 border-t border-border pt-4"
              onSubmit={(event) => {
                event.preventDefault();
                if (!code.trim()) {
                  codeInput.current?.focus();
                  return;
                }
                void action.run(async () => {
                  const result = await api.query<{
                    pollProviderLogin: ProviderLogin;
                  }>(
                    `mutation($id:String!,$code:String!){pollProviderLogin(id:$id,code:$code){${loginFields}}}`,
                    { id: login.id, code: code.trim() },
                  );
                  setLogin(result.pollProviderLogin);
                  if (result.pollProviderLogin.status === "connected")
                    api.refresh();
                  else if (result.pollProviderLogin.error)
                    codeInput.current?.focus();
                });
              }}
            >
              <TextField
                className="gap-2"
                value={code}
                onChange={setCode}
                isRequired
                isInvalid={Boolean(login.error)}
                autoComplete="off"
              >
                <Label>{t("Anmeldecode")}</Label>
                <Input ref={codeInput} className="min-h-11" />
                <FieldError>
                  {login.error ? t(login.error) : undefined}
                </FieldError>
              </TextField>
              <Button
                type="submit"
                className="min-h-11"
                isPending={action.busy}
                isDisabled={!code.trim()}
              >
                {t("Verbinden")}
              </Button>
            </form>
          )}
          {login.expiresAt && (
            <p className="text-xs text-muted">
              {t("Code gültig bis {time}", {
                time: new Date(login.expiresAt).toLocaleTimeString(
                  numberLocale,
                  { hour: "2-digit", minute: "2-digit" },
                ),
              })}
            </p>
          )}
        </>
      )}
      <Failure message={action.error} />
      {login?.status === "connected" && (
        <Alert role="status" status="success">
          <Alert.Indicator />
          <Alert.Content>
            <Alert.Title>{t("Provider verbunden")}</Alert.Title>
            <Alert.Description>
              {t("Verbunden. Die verfügbaren Modelle werden geladen.")}
            </Alert.Description>
          </Alert.Content>
        </Alert>
      )}
      <div className="flex flex-wrap justify-end gap-2 border-t border-border pt-4">
        {failed && (
          <Button
            className="min-h-11"
            onPress={() => setAttempt((value) => value + 1)}
          >
            {t("Anmeldung erneut starten")}
          </Button>
        )}
        <Button className="min-h-11" variant="secondary" onPress={onClose}>
          {t(login?.status === "connected" ? "Modelle ansehen" : "Schließen")}
        </Button>
      </div>
    </div>
  );
  return embedded ? (
    body
  ) : (
    <Inspect
      title={`${t("Provider verbinden")} · ${provider.name}`}
      onClose={onClose}
    >
      {body}
    </Inspect>
  );
}
