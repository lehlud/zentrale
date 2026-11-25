import { Input, Link, TextField, buttonVariants } from "@heroui/react";
import { useState } from "react";

import { ActionsMenu } from "./actions-menu";
import { agentTree, branchContains, type AgentBranch } from "./agent-tree";
import { useAPI, useQuery } from "./api";
import { MoreButton, useBatch } from "./batches";
import type { Agent, Project } from "./generated";
import { Icon } from "./icons";
import { conversationHref, useRoute } from "./navigation";
import { useI18n } from "./preferences";
import { RenameConversation } from "./rename-conversation";
import { Failure, useAction } from "./ui";

type SidebarProject = Pick<Project, "id" | "name" | "archived" | "emoji">;

function ProjectIcon({ emoji }: { emoji?: string | null }) {
  return emoji ? (
    <span aria-hidden="true" className="font-emoji w-4 shrink-0 text-center">
      {emoji}
    </span>
  ) : (
    <Icon name="projects" className="size-4 shrink-0 text-muted" />
  );
}

const projectOrderKey = "zentrale-sidebar-project-order";

function savedProjectOrder(orderKey: string, initialAdmin: boolean): string[] {
  try {
    const stored: unknown = JSON.parse(
      localStorage.getItem(orderKey) ??
        (initialAdmin ? localStorage.getItem(projectOrderKey) : null) ??
        "null",
    );
    return Array.isArray(stored)
      ? stored.filter((id): id is string => typeof id === "string")
      : [];
  } catch {
    return [];
  }
}

function orderProjects(projects: SidebarProject[], order: string[]) {
  const remaining = new Map(projects.map((project) => [project.id, project]));
  const sorted: SidebarProject[] = [];

  for (const id of order) {
    const project = remaining.get(id);
    if (project) {
      sorted.push(project);
      remaining.delete(id);
    }
  }

  // Ignore deleted IDs and append projects not yet in the saved order.
  return [...sorted, ...remaining.values()];
}

export function SidebarProjects({
  onNavigate,
  canCreate,
  userId,
}: {
  onNavigate?: () => void;
  canCreate: boolean;
  userId: string;
}) {
  const { t } = useI18n();
  const { projectId, page } = useRoute();
  const api = useAPI();
  // Sidebar navigation needs no machine/provider/SSH configuration. Keep this
  // parallel read small; conversation rendering never waits for its response.
  const { data, error, loading } = useQuery<{ projects: SidebarProject[] }>(
    `query { projects(limit:500) { id name emoji archived } }`,
  );
  const [search, setSearch] = useState("");
  const orderKey = `${projectOrderKey}:${userId}`;
  const [order, setOrder] = useState(() =>
    savedProjectOrder(orderKey, userId === "initial-admin"),
  );
  const [drag, setDrag] = useState<{ id: string; target?: string }>();
  const sorted = orderProjects(
    (data?.projects ?? []).filter((project) => !project.archived),
    order,
  );
  const filter = search.toLocaleLowerCase();
  const archived = (data?.projects ?? []).filter(
    (project) =>
      project.archived && project.name.toLocaleLowerCase().includes(filter),
  );
  const projects = useBatch(
    sorted.filter((project) =>
      project.name.toLocaleLowerCase().includes(filter),
    ),
    filter,
  );
  const canReorder = projects.items.length > 1;
  const draggedIndex = projects.items.findIndex(
    (project) => project.id === drag?.id,
  );

  function moveProject(id: string, target: string) {
    const from = sorted.findIndex((project) => project.id === id);
    const to = sorted.findIndex((project) => project.id === target);
    if (from < 0 || to < 0 || from === to) return;

    const next = sorted.map((project) => project.id);
    next.splice(from, 1);
    next.splice(to, 0, id);
    setOrder(next);

    try {
      localStorage.setItem(orderKey, JSON.stringify(next));
    } catch {
      // The current sidebar remains sortable if storage is unavailable.
    }
  }

  return (
    <nav
      aria-label={t("Projekte und Unterhaltungen")}
      className="min-w-0 space-y-1"
    >
      <Link
        href="#/projects"
        onPress={onNavigate}
        aria-current={page === "projects" && !projectId ? "page" : undefined}
        className={buttonVariants({
          variant: "ghost",
          className: "min-h-11 w-full justify-start gap-3 px-3",
        })}
      >
        <Icon name="projects" />
        {t("Alle Projekte")}
      </Link>
      <div className="flex items-center justify-between px-3 pt-6 pb-2 text-xs text-muted">
        <span>{t("Projekte")}</span>
        {canCreate && (
          <Link
            href="#/projects?new=1"
            aria-label={t("Projekt anlegen")}
            onPress={onNavigate}
            className={buttonVariants({
              variant: "ghost",
              isIconOnly: true,
              className: "min-h-11 min-w-11",
            })}
          >
            <Icon name="plus" />
          </Link>
        )}
      </div>
      {(data?.projects.length ?? 0) > 8 && (
        <TextField
          aria-label={t("Projekte filtern")}
          value={search}
          onChange={setSearch}
          className="px-1 pb-2"
        >
          <Input
            type="search"
            className="min-h-11 text-sm"
            placeholder={t("Projekte filtern")}
          />
        </TextField>
      )}
      <Failure message={error} onRetry={() => api.refresh()} />
      {loading && !data && (
        <div aria-hidden="true" className="space-y-3 px-3 py-4">
          <div className="h-3 w-3/4 rounded bg-default" />
          <div className="h-3 w-1/2 rounded bg-default" />
        </div>
      )}
      {!loading && !error && !projects.items.length && !archived.length && (
        <p className="px-3 py-3 text-xs leading-5 text-muted">
          {t(filter ? "Keine passenden Projekte" : "Noch keine Projekte")}
        </p>
      )}
      <ul aria-label={t("Projekte")} className="space-y-1">
        {projects.items.map((project, index) => {
          const active = page === "projects" && projectId === project.id;
          const dropTarget = draggedIndex >= 0 && drag?.target === project.id;

          return (
            <li key={project.id}>
              <div
                className="relative flex min-w-0 items-center"
                onDragOver={(event) => {
                  if (!drag || draggedIndex < 0 || drag.id === project.id)
                    return;

                  event.preventDefault();
                  event.dataTransfer.dropEffect = "move";
                  if (drag.target !== project.id) {
                    setDrag({ ...drag, target: project.id });
                  }
                }}
                onDragLeave={(event) => {
                  if (
                    event.relatedTarget instanceof Node &&
                    event.currentTarget.contains(event.relatedTarget)
                  )
                    return;

                  setDrag((current) =>
                    current?.target === project.id
                      ? { id: current.id }
                      : current,
                  );
                }}
                onDrop={(event) => {
                  if (!drag || draggedIndex < 0) return;

                  event.preventDefault();
                  moveProject(drag.id, project.id);
                  setDrag(undefined);
                }}
              >
                <Link
                  href={`#/projects/${encodeURIComponent(project.id)}`}
                  onPress={onNavigate}
                  className={buttonVariants({
                    variant: active ? "secondary" : "ghost",
                    className:
                      "min-h-11 min-w-0 flex-1 justify-start gap-3 px-3",
                  })}
                >
                  <ProjectIcon emoji={project.emoji} />
                  <span className="min-w-0 truncate" title={project.name}>
                    {project.name}
                  </span>
                </Link>
                {canReorder && (
                  <span
                    aria-hidden="true"
                    className="flex h-11 w-8 shrink-0 cursor-grab items-center justify-center text-muted active:cursor-grabbing"
                    title={t("Projekt zum Umsortieren ziehen")}
                    draggable
                    onDragStart={(event) => {
                      event.dataTransfer.effectAllowed = "move";
                      event.dataTransfer.setData(
                        "application/x-zentrale-project",
                        project.id,
                      );
                      setDrag({ id: project.id });
                    }}
                    onDragEnd={() => setDrag(undefined)}
                  >
                    <Icon name="grip" className="pointer-events-none size-4" />
                  </span>
                )}
                {dropTarget && (
                  <span
                    aria-hidden="true"
                    className={`pointer-events-none absolute inset-x-0 h-0.5 rounded-full bg-accent ${draggedIndex < index ? "-bottom-0.5" : "-top-0.5"}`}
                  />
                )}
              </div>
              {active && (
                <SidebarConversations
                  projectId={project.id}
                  onNavigate={onNavigate}
                />
              )}
            </li>
          );
        })}
      </ul>
      <MoreButton batch={projects} />
      {archived.length > 0 && (
        <div className="mt-3 border-t border-separator pt-3">
          <p className="px-3 pb-2 text-xs text-muted">{t("Archiviert")}</p>
          <ul aria-label={t("Archivierte Projekte")} className="space-y-1">
            {archived.map((project) => (
              <li key={project.id}>
                <Link
                  href={`#/projects/${encodeURIComponent(project.id)}`}
                  onPress={onNavigate}
                  className={buttonVariants({
                    variant:
                      page === "projects" && projectId === project.id
                        ? "secondary"
                        : "ghost",
                    className:
                      "min-h-11 w-full min-w-0 justify-start gap-3 px-3",
                  })}
                >
                  <ProjectIcon emoji={project.emoji} />
                  <span className="min-w-0 truncate" title={project.name}>
                    {project.name}
                  </span>
                </Link>
              </li>
            ))}
          </ul>
        </div>
      )}
    </nav>
  );
}

function SidebarConversations({
  projectId,
  onNavigate,
}: {
  projectId: string;
  onNavigate?: () => void;
}) {
  const { t } = useI18n();
  const api = useAPI();
  const { params } = useRoute();
  const { data, error } = useQuery<{ agents: Agent[] }>(
    "query($id:String!){agents(projectId:$id,limit:500){id projectId parentId approverId name role status}}",
    { id: projectId },
  );
  const conversations = useBatch(agentTree(data?.agents ?? []), projectId);
  const activeId =
    params.get("agent") ||
    data?.agents.find((agent) => agent.role === "main")?.id;
  const [renaming, setRenaming] = useState<Agent>();
  const action = useAction();

  const renderConversation = (branch: AgentBranch) => {
    const { agent, children } = branch;
    return (
      <li key={agent.id} className="min-w-0">
        <div className="flex min-w-0 items-center">
          <Link
            href={conversationHref(projectId, agent.id)}
            onPress={onNavigate}
            aria-current={activeId === agent.id ? "page" : undefined}
            className={buttonVariants({
              variant: activeId === agent.id ? "secondary" : "ghost",
              className: "min-h-11 min-w-0 flex-1 justify-start gap-2 px-2",
            })}
          >
            <span
              aria-hidden="true"
              className={
                agent.status === "running" ? "text-accent" : "text-muted"
              }
            >
              {agent.status === "running" ? "◉" : "·"}
            </span>
            <span className="min-w-0 truncate" title={agent.name}>
              {agent.name}
            </span>
            {agent.status === "running" && (
              <span className="sr-only">{t("Arbeitet")}</span>
            )}
          </Link>
          <ActionsMenu
            label={t("Aktionen für {name}", { name: agent.name })}
            isDisabled={action.busy}
            actions={[
              {
                id: "rename",
                label: t("Umbenennen"),
                isDisabled:
                  agent.status === "running" || agent.status === "waiting",
                onAction: () => setRenaming(agent),
              },
              {
                id: "delete",
                label: t("Entfernen"),
                destructive: true,
                onAction: () =>
                  void action.run(async () => {
                    await api.removeAgent(agent.id);
                    if (branchContains(branch, activeId ?? ""))
                      location.hash = `#/projects/${encodeURIComponent(projectId)}`;
                    onNavigate?.();
                  }),
              },
            ]}
          />
        </div>
        {children.length > 0 && (
          <ul
            aria-label={t("Subagenten von {name}", { name: agent.name })}
            className="ml-4 border-l border-border pl-1.5"
          >
            {children.map(renderConversation)}
          </ul>
        )}
      </li>
    );
  };

  return (
    <div className="ml-4 border-l border-border pl-1.5 py-1">
      <Failure message={error || action.error} onRetry={() => api.refresh()} />
      <ul aria-label={t("Unterhaltungen")}>
        {conversations.items.map(renderConversation)}
      </ul>
      <MoreButton batch={conversations} />
      {renaming && (
        <RenameConversation
          agent={renaming}
          onClose={() => setRenaming(undefined)}
        />
      )}
    </div>
  );
}
