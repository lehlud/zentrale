import { Button, Link, buttonVariants } from "@heroui/react";
import { useRef, useState } from "react";

import { CONFIG, useQuery } from "./api";
import { AgentControls } from "./agent-controls";
import { AgentView } from "./agent-view";
import type { Config } from "./config";
import type { Agent, Project } from "./generated";
import { setRouteParameter, useRoute } from "./navigation";
import { useI18n } from "./preferences";
import { Empty, Loading, useAction } from "./ui";
import { useSidebar } from "./shell";

export function Workspace({
  project,
  agents,
  onProjectDialog,
}: {
  project: Project;
  agents: Agent[];
  onProjectDialog: (view: string) => void;
}) {
  const { t } = useI18n();

  const sidebar = useSidebar();
  const header = useRef<HTMLDivElement>(null);
  const action = useAction();
  const { data: config } = useQuery<Config>(CONFIG);
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
  const activeId = selected || mainAgents[0]?.id || "";
  const machineId = agents.find((agent) => agent.id === activeId)?.machineId;
  const sandboxStatus = config?.sandboxStatuses?.find(
    (status) => status.machineId === machineId,
  );
  const machine =
    config?.machines.find((machine) => machine.id === machineId) ??
    config?.pools
      .flatMap((pool) =>
        pool.machines.map((machine) => ({
          ...machine,
          id: `${pool.id}/${machine.id}`,
        })),
      )
      .find((machine) => machine.id === machineId);
  const defaultsReady = Boolean(
    config?.modelDefaults.main.modelId &&
    config?.modelDefaults.approver.modelId,
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
            {!config ? (
              <Loading />
            ) : (
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
            )}
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
            sandboxStatus={sandboxStatus}
            sandboxMode={machine?.sandbox}
            machineName={machine?.name}
          />
        )
      )}
    </div>
  );
}
