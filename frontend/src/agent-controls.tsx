import { Button, Dropdown, Label, Link, buttonVariants } from "@heroui/react";
import type { RefObject, ReactNode } from "react";

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

export function AgentControls({
  current,
  project,
  onProjectDialog,
  agents,
  onSelect,
  action,
  onDetails,
  onRename,
  headerRef,
  onCreateAgent,
  canCreateAgent,
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
  onDetails: () => void;
  onRename?: () => void;
  headerRef: RefObject<HTMLDivElement | null>;
  onCreateAgent: () => void;
  canCreateAgent: boolean;
  focusAgentSwitcher: boolean;
  onAgentSwitcherFocus: () => void;
  children?: ReactNode;
}) {
  const { t } = useI18n();
  const api = useAPI();
  const parent = agents.find((agent) => agent.id === current?.parentId);
  const children = agents.filter(
    (agent) => agent.parentId === current?.id && agent.role !== "approver",
  );
  const canRemove =
    Boolean(current) &&
    current?.role !== "approver" &&
    current?.status !== "running";

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
          <h1
            className="truncate px-2 text-xs font-normal leading-5 text-muted"
            title={project.name}
          >
            {project.name}
          </h1>
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
            agentId={
              current?.role === "approver" ? current.parentId : current?.id
            }
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
                  <Dropdown.Item
                    id="create"
                    className="min-h-11"
                    isDisabled={!canCreateAgent}
                    onAction={onCreateAgent}
                  >
                    <Label>{t("Neue Unterhaltung")}</Label>
                  </Dropdown.Item>
                  {current && (
                    <Dropdown.Item
                      id="details"
                      className="min-h-11"
                      onAction={onDetails}
                    >
                      <Label>{t("Agentendetails")}</Label>
                    </Dropdown.Item>
                  )}
                  {current && onRename && (
                    <Dropdown.Item
                      id="rename"
                      className="min-h-11"
                      isDisabled={
                        current.status === "running" ||
                        current.status === "waiting"
                      }
                      onAction={onRename}
                    >
                      <Label>{t("Umbenennen")}</Label>
                    </Dropdown.Item>
                  )}
                  <Dropdown.Item
                    id="settings"
                    className="min-h-11"
                    onAction={() => onProjectDialog("settings")}
                  >
                    <Label>{t("Projekteinstellungen")}</Label>
                  </Dropdown.Item>
                  <Dropdown.Item
                    id="sources"
                    className="min-h-11"
                    onAction={() => onProjectDialog("sources")}
                  >
                    <Label>{t("Quellen verwalten")}</Label>
                  </Dropdown.Item>
                  <Dropdown.Item
                    id="editor"
                    className="min-h-11"
                    onAction={() => onProjectDialog("editor")}
                  >
                    <Label>{t("Editorverbindung")}</Label>
                  </Dropdown.Item>
                  {current?.role !== "approver" && current?.approverId && (
                    <Dropdown.Item
                      id="approver"
                      className="min-h-11"
                      onAction={() => onSelect(current.approverId)}
                    >
                      <Label>Auto-Approver ↗</Label>
                    </Dropdown.Item>
                  )}
                  {parent && (
                    <Dropdown.Item
                      id="parent"
                      className="min-h-11"
                      onAction={() => onSelect(parent.id)}
                    >
                      <Label>← {parent.name}</Label>
                    </Dropdown.Item>
                  )}
                  {children.map((child) => (
                    <Dropdown.Item
                      id={child.id}
                      key={child.id}
                      className="min-h-11"
                      onAction={() => onSelect(child.id)}
                    >
                      <Label>{child.name} ↗</Label>
                    </Dropdown.Item>
                  ))}
                  {canRemove && (
                    <Dropdown.Item
                      id="delete"
                      className="min-h-11 text-danger"
                      onAction={() =>
                        action.run(async () => {
                          await api.mutate(
                            "mutation($kind:String!,$id:String!){deleteResource(kind:$kind,id:$id)}",
                            { kind: "agent", id: current!.id },
                          );
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
    </div>
  );
}
