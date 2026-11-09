import { Button, Link, Spinner } from "@heroui/react";
import { useEffect, useState } from "react";

import { useAPI, useQuery } from "./api";
import type { EditorStartResult, Project, SSHHostKey } from "./generated";
import { useI18n } from "./preferences";
import { HostKeyTrust } from "./ssh-trust";
import { Failure, Loading } from "./ui";
import { useRoute } from "./navigation";

export function EditorLauncher({ projectId }: { projectId: string }) {
  const api = useAPI();
  const { t, locale, theme } = useI18n();
  const agentId = useRoute().params.get("agent") ?? "";
  const { data, error } = useQuery<{ project: Project }>(
    "query($id:String!){project(id:$id){id name directory machineIds poolIds commitName commitEmail}}",
    { id: projectId },
  );
  const [attempt, setAttempt] = useState(0);
  const [failure, setFailure] = useState("");
  const [challenge, setChallenge] = useState<SSHHostKey>();
  const [starting, setStarting] = useState(true);

  useEffect(() => {
    if (!data?.project) {
      return;
    }

    const controller = new AbortController();
    setStarting(true);
    setFailure("");
    setChallenge(undefined);
    api
      .query<{ startEditor: EditorStartResult }>(
        "mutation($projectId:String!,$agentId:String){startEditor(projectId:$projectId,agentId:$agentId){session{id projectId agentId sandbox machineId machineName url} error hostKey{host fingerprint publicKey}}}",
        { projectId, agentId },
        controller.signal,
      )
      .then(({ startEditor: result }) => {
        if (controller.signal.aborted) {
          return;
        }
        if (result.hostKey.publicKey) setChallenge(result.hostKey);
        else if (result.error) setFailure(result.error);
        else {
          const url = new URL(result.session.url, location.origin);
          url.searchParams.set("locale", locale);
          url.searchParams.set("theme", theme);
          // The workbench replaces this preparation page and owns the whole tab.
          location.replace(url);
        }
      })
      .catch((error: Error) => {
        if (!controller.signal.aborted) setFailure(error.message);
      })
      .finally(() => {
        if (!controller.signal.aborted) setStarting(false);
      });

    return () => controller.abort();
  }, [api, projectId, agentId, data?.project?.id, attempt, locale, theme]);

  return (
    <main className="mx-auto max-w-xl space-y-6 px-6 py-12">
      <h1 className="text-2xl font-medium">{t("VS Code öffnen")}</h1>
      {data?.project && (
        <p className="break-words text-muted">{data.project.name}</p>
      )}
      {!data && !error && <Loading />}
      {data && starting && (
        <div role="status" className="flex items-center gap-3">
          <Spinner size="sm" />
          <span>
            {t(
              "Die Weboberfläche wird in Zentrale vorbereitet und der Remote-Agent über SSH verbunden. Der erste Start kann einige Minuten dauern.",
            )}
          </span>
        </div>
      )}
      <Failure
        message={error || failure}
        onRetry={error ? () => api.refresh() : undefined}
      />
      {challenge && (
        <HostKeyTrust
          challenge={challenge}
          onTrusted={async () => setAttempt((value) => value + 1)}
        />
      )}
      {!starting && !challenge && failure && (
        <Button
          className="min-h-11"
          onPress={() => setAttempt((value) => value + 1)}
        >
          {t("Erneut versuchen")}
        </Button>
      )}
      <Link
        className="min-h-11"
        href={`#/projects/${encodeURIComponent(projectId)}?view=editor${agentId ? `&agent=${encodeURIComponent(agentId)}` : ""}`}
      >
        {t("Zurück zum Projekt")}
      </Link>
    </main>
  );
}
