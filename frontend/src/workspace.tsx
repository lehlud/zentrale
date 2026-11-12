import { Button, Link, buttonVariants } from "@heroui/react";
import { useRef, useState } from "react";

import { AgentControls } from "./agent-controls";
import { AgentView } from "./agent-view";
import {
  bootstrapTranscript,
  type ConversationBootstrap,
} from "./conversation-bootstrap";
import type { Agent, Project } from "./generated";
import { setRouteParameter, useRoute } from "./navigation";
import { useI18n } from "./preferences";
import { Empty, useAction } from "./ui";
import { useSidebar } from "./shell";

export function Workspace({
  project,
  agents,
  onProjectDialog,
  bootstrap,
}: {
  project: Project;
  agents: Agent[];
  onProjectDialog: (view: string) => void;
  bootstrap: ConversationBootstrap;
}) {
  const { t } = useI18n();

  const sidebar = useSidebar();
  const header = useRef<HTMLDivElement>(null);
  const action = useAction();
  const { params } = useRoute();
  const selected = params.get("agent") ?? "";
  // Each agent remounts its history and draft. Restore keyboard focus when the
  // newly selected session has loaded and its compact switcher is available.
  const [focusAgentId, setFocusAgentId] = useState("");
  const setSelected = (id: string) => {
    setFocusAgentId(id);
    setRouteParameter("agent", id);
  };
  const mainAgents = agents.filter((agent) => agent.role === "main");
  const activeId = bootstrap.agent?.id ?? selected;
  const defaultsReady = Boolean(
    bootstrap.modelDefaults?.main.modelId &&
    bootstrap.modelDefaults?.approver.modelId,
  );

  return (
    <div className="min-w-0">
      {!mainAgents.length ? (
        <div>
          <AgentControls
            project={project}
            onProjectDialog={onProjectDialog}
            agents={agents}
            onSelect={setSelected}
            action={action}
            onDetails={() => {}}
            headerRef={header}
            onCreateAgent={() => sidebar?.newConversation(project.id)}
            canCreateAgent={defaultsReady}
            focusAgentSwitcher={false}
            onAgentSwitcherFocus={() => {}}
          />
          <div className="mx-auto max-w-xl py-16">
            <Empty
              action={
                defaultsReady ? (
                  <Button
                    className="min-h-11"
                    onPress={() => sidebar?.newConversation(project.id)}
                  >
                    {t("Neue Unterhaltung")}
                  </Button>
                ) : (
                  <Link
                    href="#/settings"
                    className={buttonVariants({
                      variant: "secondary",
                      className: "min-h-11",
                    })}
                  >
                    {t("Standardmodelle auswählen")}
                  </Link>
                )
              }
            >
              {defaultsReady
                ? t("Noch keine Agenten")
                : t(
                    "Wähle zuerst die Standardmodelle für Aufgaben und Freigaben.",
                  )}
            </Empty>
          </div>
        </div>
      ) : (
        activeId && (
          <AgentView
            key={activeId}
            agentId={activeId}
            project={project}
            onProjectDialog={onProjectDialog}
            agents={agents}
            onSelect={setSelected}
            onCreateAgent={() => sidebar?.newConversation(project.id)}
            canCreateAgent={defaultsReady}
            focusAgentSwitcher={focusAgentId === activeId}
            onAgentSwitcherFocus={() => setFocusAgentId("")}
            initialTranscript={bootstrapTranscript(bootstrap)}
            managedTranscript
            sandboxMode={bootstrap.sandboxMode}
            sandboxStatus={bootstrap.sandboxStatus ?? undefined}
            machineName={bootstrap.machineName}
          />
        )
      )}
    </div>
  );
}
