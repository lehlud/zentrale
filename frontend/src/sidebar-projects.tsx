import { Input, Link, TextField, buttonVariants } from "@heroui/react";
import { useState } from "react";

import { ActionsMenu } from "./actions-menu";
import { CONFIG, useAPI, useQuery } from "./api";
import { MoreButton, useBatch } from "./batches";
import type { Config } from "./config";
import type { Agent } from "./generated";
import { Icon } from "./icons";
import { conversationHref, useRoute } from "./navigation";
import { useI18n } from "./preferences";
import { RenameConversation } from "./rename-conversation";
import { Failure, useAction } from "./ui";

export function SidebarProjects({ onNavigate }: { onNavigate?: () => void }) {
  const { t } = useI18n();
  const { projectId, page } = useRoute();
  const api = useAPI();
  const { data, error, loading } = useQuery<Config>(CONFIG);
  const [search, setSearch] = useState("");
  const projects = useBatch(
    (data?.projects ?? []).filter((project) =>
      project.name.toLocaleLowerCase().includes(search.toLocaleLowerCase()),
    ),
    search,
  );

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
      {!loading && !error && !projects.items.length && (
        <p className="px-3 py-3 text-xs leading-5 text-muted">
          {t(search ? "Keine passenden Projekte" : "Noch keine Projekte")}
        </p>
      )}
      {projects.items.map((project) => (
        <div key={project.id}>
          <Link
            href={`#/projects/${encodeURIComponent(project.id)}`}
            onPress={onNavigate}
            className={buttonVariants({
              variant:
                page === "projects" && projectId === project.id
                  ? "secondary"
                  : "ghost",
              className: "min-h-11 min-w-0 w-full justify-start gap-3 px-3",
            })}
          >
            <Icon name="projects" className="size-4 text-muted" />
            <span className="min-w-0 truncate" title={project.name}>
              {project.name}
            </span>
          </Link>
          {page === "projects" && projectId === project.id && (
            <SidebarConversations
              projectId={project.id}
              onNavigate={onNavigate}
            />
          )}
        </div>
      ))}
      <MoreButton batch={projects} />
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
  const conversations = useBatch(
    data?.agents.filter((agent) => agent.role !== "approver") ?? [],
    projectId,
  );
  const activeId =
    params.get("agent") ||
    data?.agents.find((agent) => agent.role === "main")?.id;
  const [renaming, setRenaming] = useState<Agent>();
  const action = useAction();

  return (
    <div className="ml-4 border-l border-border pl-1.5 py-1">
      <Failure message={error || action.error} onRetry={() => api.refresh()} />
      {conversations.items.map((agent) => (
        <div key={agent.id} className="flex min-w-0 items-center">
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
                isDisabled: agent.status === "running",
                onAction: () =>
                  void action.run(async () => {
                    await api.mutate(
                      "mutation($kind:String!,$id:String!){deleteResource(kind:$kind,id:$id)}",
                      { kind: "agent", id: agent.id },
                    );
                    if (activeId === agent.id)
                      location.hash = `#/projects/${encodeURIComponent(projectId)}`;

                    onNavigate?.();
                  }),
              },
            ]}
          />
        </div>
      ))}
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
