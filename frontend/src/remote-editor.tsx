import {
  Button,
  Card,
  Input,
  Label,
  Link,
  TextField,
  buttonVariants,
} from "@heroui/react";
import { useState } from "react";

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
  const [previewPort, setPreviewPort] = useState("1234");
  const portNumber = Number(previewPort);
  const validPort =
    /^\d+$/.test(previewPort) &&
    portNumber >= 1 &&
    portNumber <= 65535 &&
    portNumber !== 9842;

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
        {session && (
          <div className="space-y-3 border-t border-border pt-4">
            <h3 className="text-sm font-medium">{t("Webapp-Vorschau")}</h3>
            <p className="text-sm text-muted">
              {t(
                "Starte den Dev-Server im VS-Code-Terminal und öffne seinen Port hier. Die Vorschau verwendet dieselbe HTTPS-Adresse wie Zentrale.",
              )}
            </p>
            <div className="flex flex-wrap items-end gap-3">
              <TextField
                value={previewPort}
                onChange={setPreviewPort}
                className="w-32 gap-2"
                isInvalid={!validPort}
              >
                <Label>{t("Dev-Server-Port")}</Label>
                <Input
                  inputMode="numeric"
                  maxLength={5}
                  className="min-h-11 font-mono"
                />
              </TextField>
              <Link
                className={buttonVariants({
                  variant: "secondary",
                  className: "min-h-11 no-underline",
                })}
                isDisabled={!validPort}
                href={`/preview/${encodeURIComponent(project.id)}/${encodeURIComponent(session.id)}/${portNumber}/`}
                target="_blank"
                rel="noopener noreferrer"
              >
                {t("Vorschau öffnen ↗")}
              </Link>
            </div>
          </div>
        )}
        <Failure
          message={error || action.error}
          onRetry={() => api.refresh()}
        />
      </Card.Content>
    </Card>
  );
}
