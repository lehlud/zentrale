import { Button } from "@heroui/react";
import { useEffect, useRef, useState } from "react";

import { useAPI } from "./api";
import { useI18n } from "./preferences";
import { Failure, Loading } from "./ui";

export function browserLoginRedirectURI() {
  const base =
    (globalThis as typeof globalThis & { __ZENTRALE_PREVIEW_BASE__?: string })
      .__ZENTRALE_PREVIEW_BASE__ ?? "/";

  return new URL(`${base.replace(/\/$/, "")}/oauth/callback`, location.origin)
    .href;
}

function captureCallback() {
  if (!location.pathname.endsWith("/oauth/callback")) {
    return undefined;
  }

  const parameters = new URLSearchParams(location.search);
  const code = parameters.getAll("code");
  const state = parameters.getAll("state");

  // Remove codes and provider-supplied error text before rendering or making
  // any application requests. Never persist the grant or replay it on reload.
  history.replaceState(null, "", location.pathname);

  if (
    parameters.has("error") ||
    code.length !== 1 ||
    state.length !== 1 ||
    !code[0] ||
    !state[0] ||
    code[0].length > 4096 ||
    state[0].length > 1024
  ) {
    return { valid: false, code: "", state: "" };
  }

  return { valid: true, code: code[0], state: state[0] };
}

const callback = captureCallback();

export const isBrowserLoginCallback = callback !== undefined;

export function BrowserLoginCallback() {
  const api = useAPI();
  const { t } = useI18n();
  const request = useRef<Promise<boolean> | undefined>(undefined);
  const [status, setStatus] = useState<"waiting" | "complete" | "error">(
    "waiting",
  );

  useEffect(() => {
    let active = true;
    if (!request.current) {
      request.current = callback?.valid
        ? api
            .query<{ completeBrowserLogin: boolean }>(
              "mutation($state:String!,$code:String!){completeBrowserLogin(state:$state,code:$code)}",
              { state: callback.state, code: callback.code },
            )
            .then((result) => result.completeBrowserLogin)
        : Promise.resolve(false);
    }

    void request.current.then(
      (complete) => {
        if (active) setStatus(complete ? "complete" : "error");
      },
      () => {
        if (active) setStatus("error");
      },
    );

    return () => {
      active = false;
    };
  }, [api]);

  return (
    <main className="mx-auto max-w-lg space-y-5 p-8">
      <h1 className="text-xl font-medium">{t("Browser-Anmeldung")}</h1>
      {status === "waiting" && <Loading />}
      {status === "complete" && (
        <p role="status">
          {t("Anmeldung übermittelt. Kehre zum ursprünglichen Fenster zurück.")}
        </p>
      )}
      {status === "error" && (
        <Failure
          message={t(
            "Diese Anmeldung konnte nicht abgeschlossen werden. Starte sie im ursprünglichen Fenster erneut.",
          )}
        />
      )}
      <Button
        variant="secondary"
        className="min-h-11"
        onPress={() => window.close()}
      >
        {t("Fenster schließen")}
      </Button>
    </main>
  );
}
