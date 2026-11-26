import { Link, buttonVariants } from "@heroui/react";
import { useRef } from "react";

import { AgentControls } from "./agent-controls";
import { AgentView } from "./agent-view";
import { WorkspaceFiles } from "./workspace-files";
import {
  bootstrapTranscript,
  type ConversationBootstrap,
} from "./conversation-bootstrap";
import type { Agent, Project } from "./generated";
import { setRouteParameter, useRoute } from "./navigation";
import { useI18n } from "./preferences";
import { Empty, useAction } from "./ui";
import { conversationHref } from "./navigation";
import { useSessionUser } from "./session";
import { SidebarToggle } from "./shell";

export function Workspace({
  project,
  agents,
  onProjectDialog,
  bootstrap,
  focusAgentId,
  onFocusAgent,
}: {
  project: Project;
  agents: Agent[];
  onProjectDialog: (view: string) => void;
  bootstrap: ConversationBootstrap;
  focusAgentId: string;
  onFocusAgent: (id: string) => void;
}) {
  const { t } = useI18n();

  const admin = useSessionUser()?.role === "admin";
  const header = useRef<HTMLDivElement>(null);
  const action = useAction();
  const { params } = useRoute();
  const selected = params.get("agent") ?? "";
  // Each agent remounts its history and draft. Restore keyboard focus when the
  // newly selected session has loaded and its compact switcher is available.
  const setSelected = (id: string) => {
    onFocusAgent(id);
    setRouteParameter("agent", id);
  };
  const mainAgents = agents.filter((agent) => agent.role === "main");
  const activeId = bootstrap.agent?.id ?? selected;
  const defaultsReady = Boolean(
    bootstrap.modelDefaults?.main.modelId &&
    bootstrap.modelDefaults?.approver.modelId,
  );

  if (project.archived) {
    return (
      <>
        <SidebarToggle iconOnly />
        <nav
          className="flex flex-wrap gap-2 pb-4"
          aria-label={t("Unterhaltungen")}
        >
          {agents
            .filter((agent) => agent.role !== "approver")
            .map((agent) => (
              <Link
                key={agent.id}
                href={conversationHref(project.id, agent.id)}
                aria-current={agent.id === activeId ? "page" : undefined}
              >
                {agent.name}
              </Link>
            ))}
        </nav>
        {activeId && (
          <AgentView
            key={activeId}
            agentId={activeId}
            project={project}
            initialTranscript={bootstrapTranscript(bootstrap)}
            managedTranscript
            readOnly
          />
        )}
      </>
    );
  }

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
            headerRef={header}
            focusAgentSwitcher={false}
            onAgentSwitcherFocus={() => {}}
          />
          <div className="mx-auto max-w-xl py-16">
            <Empty
              action={
                !defaultsReady &&
                admin && (
                  <Link
                    href="#/admin-settings"
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
                : admin
                  ? t(
                      "Wähle zuerst die Standardmodelle für Aufgaben und Freigaben.",
                    )
                  : t(
                      "Ein Admin muss zuerst Standardmodelle für Projekte festlegen.",
                    )}
            </Empty>
          </div>
        </div>
      ) : (
        activeId && (
          <WorkspaceFiles
            key={activeId}
            agentId={
              agents.find(
                (agent) => agent.id === activeId && agent.role === "approver",
              )?.parentId || activeId
            }
            projectId={project.id}
            directory={project.directory}
          >
            <AgentView
              agentId={activeId}
              project={project}
              onProjectDialog={onProjectDialog}
              agents={agents}
              onSelect={setSelected}
              focusAgentSwitcher={focusAgentId === activeId}
              onAgentSwitcherFocus={() => onFocusAgent("")}
              initialTranscript={bootstrapTranscript(bootstrap)}
              managedTranscript
              sandboxMode={bootstrap.sandboxMode}
              sandboxStatus={bootstrap.sandboxStatus ?? undefined}
            />
          </WorkspaceFiles>
        )
      )}
    </div>
  );
}
