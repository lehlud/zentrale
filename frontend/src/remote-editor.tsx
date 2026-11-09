import { Button, Card, Link, buttonVariants } from "@heroui/react";
import { useEffect } from "react";

import { useAPI, useQuery } from "./api";
import type { EditorSession, Project } from "./generated";
import { useI18n } from "./preferences";
import { Failure, useAction } from "./ui";
import { editorLaunchHref } from "./navigation";

export function RemoteEditor({
  project,
  agentId = "",
}: {
  project: Project;
  agentId?: string;
}) {
  const api = useAPI();
  const { t } = useI18n();
  const { data, error } = useQuery<{ editorSession: EditorSession }>(
    "query($projectId:String!,$agentId:String){editorSession(projectId:$projectId,agentId:$agentId){id projectId agentId sandbox machineId machineName url}}",
    { projectId: project.id, agentId },
  );
  const session = data?.editorSession?.id ? data.editorSession : undefined;
  const action = useAction();

  useEffect(() => {
    const refresh = () => api.refresh();
    window.addEventListener("focus", refresh);

    return () => window.removeEventListener("focus", refresh);
  }, [api]);

  return (
    <Card variant="secondary">
      <Card.Content className="gap-4">
        <h2 className="font-medium">{t("VS Code")}</h2>
        <p className="text-sm text-muted">
          {t(
            "VS Code öffnet sich in einem eigenen Tab. Die Zentrale stellt die Oberfläche bereit. Terminal und Extensions verwenden die Umgebung des gewählten Agenten, einschließlich seiner Fedora-MicroVM.",
          )}
        </p>
        {session && (
          <p role="status" className="text-sm">
            {t("Remote-Agent verbunden mit {machine}", {
              machine: session.machineName,
            })}
            {session.sandbox === "microvm" && " · Fedora MicroVM"}
          </p>
        )}
        <div className="flex flex-wrap items-center gap-3">
          <Link
            className={buttonVariants({
              variant: "primary",
              className: "min-h-11 text-accent-foreground no-underline",
            })}
            href={editorLaunchHref(project.id, agentId)}
            target="_blank"
            rel="noopener noreferrer"
          >
            {t("VS Code in neuem Tab öffnen ↗")}
          </Link>
          {session && (
            <Button
              className="min-h-11"
              variant="ghost"
              isPending={action.busy}
              onPress={() =>
                action.run(() =>
                  api.mutate(
                    "mutation($projectId:String!,$sessionId:String!){stopEditor(projectId:$projectId,sessionId:$sessionId)}",
                    { projectId: project.id, sessionId: session.id },
                  ),
                )
              }
            >
              {t("Editor beenden")}
            </Button>
          )}
        </div>
        <Failure
          message={error || action.error}
          onRetry={() => api.refresh()}
        />
      </Card.Content>
    </Card>
  );
}
