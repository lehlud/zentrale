import { Button, Dropdown, Label, Link, buttonVariants } from "@heroui/react";
import type { RefObject, ReactNode } from "react";
import { useState } from "react";

import { useAPI } from "./api";
import { statusLabels } from "./agent-format";
import type { Agent, Project } from "./generated";
import { useI18n } from "./preferences";
import type { useAction } from "./ui";
import { SidebarToggle } from "./shell";
import { AgentSwitcher } from "./agent-switcher";
import { Icon } from "./icons";
import { editorLaunchHref } from "./navigation";
import { TerminalLauncher } from "./terminal-launcher";
import { ConversationModelDialog } from "./conversation-models";
import { RepositoryConnection } from "./repository-connection";

export function AgentControls({
  current,
  project,
  onProjectDialog,
  agents,
  onSelect,
  action,
  headerRef,
  focusAgentSwitcher,
  onAgentSwitcherFocus,
  children: headerContent,
}: {
  current?: Agent;
  project: Project;
  onProjectDialog: (view: string) => void;
  agents: Agent[];
  onSelect: (id: string) => void;
  action: ReturnType<typeof useAction>;
  headerRef: RefObject<HTMLDivElement | null>;
  focusAgentSwitcher: boolean;
  onAgentSwitcherFocus: () => void;
  children?: ReactNode;
}) {
  const { t } = useI18n();
  const api = useAPI();
  const canRemove = current && current.role !== "approver";
  const [modelAgentId, setModelAgentId] = useState("");
  const [repositoryAgentId, setRepositoryAgentId] = useState("");
  const workspaceAgentId =
    current?.role === "approver" ? current.parentId : current?.id;
  const hasRepository = Boolean(
    project.repositorySshUrl || project.repositoryId,
  );

  return (
    <div
      ref={headerRef}
      role="group"
      aria-label={t("Agentensteuerung")}
      className="sticky top-0 z-10 min-w-0 bg-surface py-2"
    >
      <div className="flex min-w-0 items-center justify-between gap-2">
        <SidebarToggle iconOnly />
        <div className="min-w-0 flex-1">
          {current && (
            <AgentSwitcher
              current={current}
              agents={agents}
              onSelect={onSelect}
              autoFocus={focusAgentSwitcher}
              onFocus={onAgentSwitcherFocus}
            />
          )}
        </div>
        <div className="flex shrink-0 items-center gap-1">
          <Link
            href={editorLaunchHref(
              project.id,
              current?.role === "approver" ? current.parentId : current?.id,
            )}
            aria-label={t("VS Code in neuem Tab öffnen ↗")}
            target="_blank"
            rel="noopener noreferrer"
            className={buttonVariants({
              variant: "ghost",
              className: "min-h-11 min-w-11 text-xs",
            })}
          >
            <Icon name="code" className="size-5" />
          </Link>
          <TerminalLauncher
            projectId={project.id}
            projectName={project.name}
            agentId={workspaceAgentId}
            hasRepository={hasRepository}
          />
          {(current || project) && (
            <Dropdown>
              <Button
                isIconOnly
                size="sm"
                variant="ghost"
                className="min-h-11 min-w-11"
                aria-label={t("Projektaktionen")}
                isDisabled={action.busy}
              >
                <Icon name="more" className="size-5" />
              </Button>
              <Dropdown.Popover placement="bottom end">
                <Dropdown.Menu aria-label={t("Projektaktionen")}>
                  {current?.status === "running" &&
                    current.role !== "approver" && (
                      <Dropdown.Item
                        id="stop"
                        className="min-h-11"
                        onAction={() =>
                          action.run(() =>
                            api.mutate(
                              "mutation($agentId:String!){stopAgent(agentId:$agentId)}",
                              { agentId: current.id },
                              { announce: false },
                            ),
                          )
                        }
                      >
                        <Label>{t("Agent stoppen")}</Label>
                      </Dropdown.Item>
                    )}
                  {current && (
                    <Dropdown.Item
                      id="conversation-models"
                      className="min-h-11"
                      onAction={() => setModelAgentId(current.id)}
                    >
                      <Label>{t("Modelle ändern")}</Label>
                    </Dropdown.Item>
                  )}
                  <Dropdown.Item
                    id="sources"
                    className="min-h-11"
                    onAction={() => onProjectDialog("sources")}
                  >
                    <Label>{t("Quellen verwalten")}</Label>
                  </Dropdown.Item>
                  {workspaceAgentId && hasRepository && (
                    <Dropdown.Item
                      id="repository-connection"
                      className="min-h-11"
                      onAction={() => setRepositoryAgentId(workspaceAgentId)}
                    >
                      <Label>{t("Git-Verbindung prüfen")}</Label>
                    </Dropdown.Item>
                  )}
                  <Dropdown.Item
                    id="move"
                    className="min-h-11"
                    onAction={() => onProjectDialog("move")}
                  >
                    <Label>{t("Projekt umziehen")}</Label>
                  </Dropdown.Item>
                  <Dropdown.Item
                    id="reset-vm"
                    className="min-h-11 text-danger"
                    onAction={() =>
                      action.run(() =>
                        api.mutate(
                          "mutation($projectId:String!){resetProjectVM(projectId:$projectId)}",
                          { projectId: project.id },
                        ),
                      )
                    }
                  >
                    <Label>{t("Projekt-VM zurücksetzen")}</Label>
                  </Dropdown.Item>
                  {canRemove && (
                    <Dropdown.Item
                      id="delete"
                      className="min-h-11 text-danger"
                      onAction={() =>
                        action.run(async () => {
                          await api.removeAgent(current.id);
                          onSelect("");
                        })
                      }
                    >
                      <Label>{t("Agent entfernen")}</Label>
                    </Dropdown.Item>
                  )}
                </Dropdown.Menu>
              </Dropdown.Popover>
            </Dropdown>
          )}
        </div>
      </div>
      {current && (
        <span role="status" className="sr-only">
          {t(statusLabels[current.status] ?? current.status)}
        </span>
      )}
      {headerContent}
      {modelAgentId && (
        <ConversationModelDialog
          agentId={modelAgentId}
          onClose={() => setModelAgentId("")}
        />
      )}
      {repositoryAgentId && (
        <RepositoryConnection
          agentId={repositoryAgentId}
          onClose={() => setRepositoryAgentId("")}
        />
      )}
    </div>
  );
}
