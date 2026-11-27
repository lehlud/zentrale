import { Button } from "@heroui/react";
import { useEffect } from "react";

import { useAPI, useQuery } from "./api";
import { Authentication } from "./authentication";
import { BrowserLoginCallback, isBrowserLoginCallback } from "./browser-login";
import { Keys, Machines, Providers } from "./config";
import { AdminSettings, Settings } from "./settings";
import { SESSION_QUERY, SessionUserContext, type SessionData } from "./session";
import { useRoute } from "./navigation";
import { useI18n } from "./preferences";
import { Projects, ProjectView } from "./projects";
import { ApplicationShell } from "./shell";
import { Failure, Loading } from "./ui";
import { EditorLauncher } from "./editor-launcher";
import { ShortChats } from "./short-chats";
import { OfficePreviews } from "./office-preview";
import { UsageDashboard } from "./usage-dashboard";

export default function App() {
  const { t } = useI18n();

  const api = useAPI();
  const { data, error, loading } = useQuery<SessionData>(SESSION_QUERY);
  const { page, projectId } = useRoute();
  const authenticated = Boolean(data?.status.authenticated);
  const oidc = Boolean(data?.status.oidc);
  const userId = data?.status.user?.id ?? "";
  const redirectToOIDC = !authenticated && oidc;

  useEffect(() => {
    api.setSessionIdentity(userId, oidc);
    if (authenticated) api.connect();
    return () => api.disconnect();
  }, [api, authenticated, oidc, userId]);

  useEffect(() => {
    if (redirectToOIDC) location.replace("/auth/oidc/start");
  }, [redirectToOIDC]);

  if (loading || redirectToOIDC) {
    return <Loading />;
  }

  if (error && !data) {
    return (
      <main className="max-w-lg mx-auto p-8">
        <Failure message={error} />
        <Button className="mt-4 min-h-11" onPress={() => location.reload()}>
          {t("Erneut verbinden")}
        </Button>
      </main>
    );
  }

  if (!authenticated) {
    return <Authentication setup={Boolean(data?.status.needsSetup)} />;
  }

  const admin = data?.status.user?.role === "admin";

  if (page === "editor") {
    return <EditorLauncher projectId={projectId} />;
  }

  if (isBrowserLoginCallback) {
    return <BrowserLoginCallback />;
  }

  return (
    <SessionUserContext.Provider value={data?.status.user}>
      <ApplicationShell key={userId}>
        <OfficePreviews scope={`${page}/${projectId}`}>
          <Failure message={error} onRetry={() => api.refresh()} />
          {page === "projects" &&
            (projectId ? <ProjectView projectId={projectId} /> : <Projects />)}
          {page === "chats" && <ShortChats chatId={projectId} />}
          {page === "usage" &&
            (admin ? (
              <UsageDashboard />
            ) : (
              <Failure message={t("Nur für Administratoren verfügbar.")} />
            ))}
          {admin && page === "machines" && <Machines />}
          {admin && page === "keys" && <Keys />}
          {admin && page === "providers" && <Providers />}
          {page === "settings" && <Settings admin={admin} />}
          {page === "admin-settings" &&
            (admin ? (
              <AdminSettings oidc={Boolean(data?.status.oidc)} />
            ) : (
              <Failure message={t("Nur für Administratoren verfügbar.")} />
            ))}
        </OfficePreviews>
      </ApplicationShell>
    </SessionUserContext.Provider>
  );
}
