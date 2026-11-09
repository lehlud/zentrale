import { Button } from "@heroui/react";
import { useEffect } from "react";

import { useAPI, useQuery } from "./api";
import { Authentication } from "./authentication";
import { Keys, Machines, Providers, Settings } from "./config";
import type { Status } from "./generated";
import { useRoute } from "./navigation";
import { useI18n } from "./preferences";
import { Projects, ProjectView } from "./projects";
import { ApplicationShell } from "./shell";
import { Failure, Loading } from "./ui";
import { EditorLauncher } from "./editor-launcher";

export default function App() {
  const { t } = useI18n();

  const api = useAPI();
  const { data, error, loading } = useQuery<{ status: Status }>(
    "query{status{needsSetup authenticated githubConnected}}",
  );
  const { page, projectId } = useRoute();
  const authenticated = Boolean(data?.status.authenticated);

  useEffect(() => {
    if (authenticated) api.connect();
    return () => api.disconnect();
  }, [api, authenticated]);

  if (loading) {
    return <Loading />;
  }

  if (error && !data) {
    return (
      <main className="max-w-lg mx-auto p-8">
        <Failure message={error} />
        <Button className="mt-4 min-h-11" onPress={() => api.refresh()}>
          {t("Erneut verbinden")}
        </Button>
      </main>
    );
  }

  if (!authenticated) {
    return <Authentication setup={Boolean(data?.status.needsSetup)} />;
  }

  if (page === "editor") {
    return <EditorLauncher projectId={projectId} />;
  }

  return (
    <ApplicationShell>
      <Failure message={error} onRetry={() => api.refresh()} />
      {page === "projects" &&
        (projectId ? <ProjectView projectId={projectId} /> : <Projects />)}
      {page === "machines" && <Machines />}
      {page === "keys" && <Keys />}
      {page === "providers" && <Providers />}
      {page === "settings" && <Settings />}
    </ApplicationShell>
  );
}
