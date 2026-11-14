import { RepositoryPicker, type RepositoryChoice } from "./repository-picker";
import {
  Button,
  Card,
  Chip,
  Label,
  Input,
  TextField,
  Link,
} from "@heroui/react";
import { useEffect, useState } from "react";

import { ActionsMenu } from "./actions-menu";
import { CONFIG, useAPI, useQuery } from "./api";
import { ResourceList, useResourcePage } from "./resource-page";
import { useBatch } from "./batches";
import type { Config } from "./config";
import type { Project, Source } from "./generated";
import { setRouteParameter, useRoute } from "./navigation";
import { useI18n } from "./preferences";
import type { EditorStep, Field } from "./ui";
import {
  Editor,
  Empty,
  Failure,
  Inspect,
  Loading,
  Title,
  useAction,
} from "./ui";
import { Workspace } from "./workspace";
import {
  conversationBootstrapQuery,
  type ConversationBootstrap,
} from "./conversation-bootstrap";
import { SidebarToggle } from "./shell";
import { RemoteEditor } from "./remote-editor";
import { Icon } from "./icons";

function newProject() {
  return {
    id: crypto.randomUUID(),
    name: "",
    machineIds: [],
    poolIds: [],
  };
}

function ProjectRepository({
  value,
  patch,
}: {
  value: Record<string, unknown>;
  patch: (changes: Record<string, unknown>) => void;
}) {
  return (
    <RepositoryPicker
      value={value as Partial<RepositoryChoice>}
      onChange={(choice) => patch(choice)}
    />
  );
}

export function Projects() {
  const { t } = useI18n();

  const resource = useResourcePage();
  const { api, data } = resource;
  const [editing, setEditing] = useState<Record<string, unknown> | null>(null);
  const { params } = useRoute();

  useEffect(() => {
    setEditing(params.get("new") === "1" ? newProject() : null);
  }, [params.get("new")]);

  const [search, setSearch] = useState("");
  const needle = search.trim().toLocaleLowerCase();
  const projects = useBatch(
    (data?.projects ?? []).filter((project) =>
      `${project.name} ${project.directory}`
        .toLocaleLowerCase()
        .includes(needle),
    ),
    needle,
  );
  const fields = projectFields(data, t);
  const create = () => setEditing(newProject());

  return (
    <>
      <Title
        description={t("Wähle ein Projekt und setze eine Unterhaltung fort.")}
        action={
          <Button className="min-h-11" onPress={create}>
            <Icon name="plus" />
            {t("Projekt anlegen")}
          </Button>
        }
      >
        {t("Projekte")}
      </Title>
      <Failure message={resource.error} onRetry={resource.retry} />
      <TextField
        className="mb-5 max-w-sm gap-2"
        value={search}
        onChange={setSearch}
      >
        <Label>{t("Projekte suchen")}</Label>
        <Input
          type="search"
          placeholder={t("Name oder Verzeichnis")}
          className="min-h-11"
        />
      </TextField>
      <ResourceList
        resource={resource}
        batch={projects}
        empty={
          data?.projects.length
            ? t("Keine passenden Projekte")
            : t("Noch keine Projekte")
        }
        className="divide-y divide-separator"
      >
        {(project) => (
          <section
            key={project.id}
            className="flex min-w-0 items-center gap-1 py-1"
          >
            <Link
              href={`#/projects/${encodeURIComponent(project.id)}`}
              aria-label={project.name}
              className="group min-h-20 min-w-0 flex-1 gap-3 rounded-xl px-3 py-4 text-foreground no-underline hover:bg-surface-secondary sm:gap-4"
            >
              <span className="hidden size-10 shrink-0 items-center justify-center text-muted sm:flex">
                <Icon name="projects" className="size-5" />
              </span>
              <div className="min-w-0 flex-1 space-y-1.5">
                <h2 className="break-words font-medium group-hover:underline">
                  {project.name}
                </h2>
                <p className="text-xs font-mono text-muted break-all leading-5">
                  {project.directory ||
                    t("Arbeitsbereich wird automatisch angelegt")}
                </p>
                <div className="flex flex-wrap gap-2 [&_[data-slot=chip]]:max-w-full [&_[data-slot=chip-label]]:whitespace-normal">
                  {project.machineIds.map((id) => (
                    <Chip key={id} size="sm" variant="soft">
                      {data?.machines.find((machine) => machine.id === id)
                        ?.name ?? id}
                    </Chip>
                  ))}
                  {project.poolIds.map((id) => (
                    <Chip key={id} size="sm" variant="soft">
                      {data?.pools.find((pool) => pool.id === id)?.name ?? id}
                    </Chip>
                  ))}
                </div>
              </div>
            </Link>
            <div className="flex shrink-0 items-center gap-1">
              <ActionsMenu
                label={t("Aktionen für {name}", { name: project.name })}
                isDisabled={resource.busy}
                actions={[
                  {
                    id: "edit",
                    label: t("Bearbeiten"),
                    onAction: () => setEditing({ ...project }),
                  },
                  {
                    id: "remove",
                    label: t("Entfernen"),
                    destructive: true,
                    onAction: () => resource.remove("project", project.id),
                  },
                ]}
              />
            </div>
          </section>
        )}
      </ResourceList>
      {editing && (
        <Editor
          placement="top"
          title={
            data?.projects.some((project) => project.id === editing.id)
              ? t("Projekt bearbeiten")
              : t("Neues Projekt")
          }
          fields={fields}
          steps={
            data?.projects.some((project) => project.id === editing.id)
              ? undefined
              : projectCreationSteps(t, data)
          }
          submitLabel={
            data?.projects.some((project) => project.id === editing.id)
              ? undefined
              : t("Projekt anlegen")
          }
          initial={editing}
          renderExtra={(value, patch) => (
            <ProjectRepository value={value} patch={patch} />
          )}
          onClose={() => {
            setEditing(null);
            if (params.get("new")) setRouteParameter("new", "");
          }}
          onSave={(input) =>
            api.mutate(
              "mutation($input:ProjectInput!){saveProject(input:$input){id}}",
              { input },
            )
          }
        />
      )}
    </>
  );
}

export function ProjectView({ projectId }: { projectId: string }) {
  const { t } = useI18n();
  const api = useAPI();
  const { params } = useRoute();
  // This survives Workspace's temporary unmount while the next bootstrap loads.
  const [focusAgentId, setFocusAgentId] = useState("");
  const {
    data: result,
    error,
    loading,
  } = useQuery<{ conversationBootstrap: ConversationBootstrap }>(
    conversationBootstrapQuery,
    { projectId, agentId: params.get("agent") ?? "" },
  );
  const data = result?.conversationBootstrap;
  const projectDialog = params.get("view") ?? "";
  const setProjectDialog = (value: string) => setRouteParameter("view", value);
  if (loading) {
    return (
      <>
        <SidebarToggle />
        <Loading />
      </>
    );
  }

  if (!data?.project) {
    return (
      <>
        <SidebarToggle />
        <Failure
          message={error || t("Projekt nicht gefunden")}
          onRetry={() => api.refresh()}
        />
      </>
    );
  }

  const selectedAgent =
    data.agents.find((agent) => agent.id === params.get("agent")) ??
    data.agents.find((agent) => agent.role === "main");
  const editorAgentId =
    selectedAgent?.role === "approver"
      ? selectedAgent.parentId
      : selectedAgent?.id;

  return (
    <div className="min-w-0">
      <Failure message={error} onRetry={() => api.refresh()} />
      <Workspace
        project={data.project}
        agents={data.agents}
        bootstrap={data}
        onProjectDialog={setProjectDialog}
        focusAgentId={focusAgentId}
        onFocusAgent={setFocusAgentId}
      />
      {projectDialog === "settings" && (
        <ProjectSettings
          project={data.project}
          onClose={() => setProjectDialog("")}
        />
      )}
      {projectDialog === "sources" && (
        <Inspect
          title={t("Quellen verwalten")}
          onClose={() => setProjectDialog("")}
        >
          <Sources projectId={projectId} />
        </Inspect>
      )}
      {projectDialog === "editor" && (
        <Inspect
          title={t("Editorverbindung")}
          onClose={() => setProjectDialog("")}
        >
          <RemoteEditor project={data.project} agentId={editorAgentId} />
        </Inspect>
      )}
    </div>
  );
}

function Sources({ projectId }: { projectId: string }) {
  const { t } = useI18n();

  const api = useAPI();
  const { data, error, loading } = useQuery<{
    sources: Source[];
    sshKeys: {
      id: number;
      purpose: string;
      purposeId: string;
      canDelete: boolean;
    }[];
  }>(
    `query($id:String!){sources(projectId:$id,limit:500){id projectId name url keyId readOnly bindingId repositoryName projectRoot} sshKeys{id purpose purposeId canDelete}}`,
    { id: projectId },
  );
  const [editing, setEditing] = useState<Record<string, unknown> | null>(null);
  const [repositoryDialog, setRepositoryDialog] = useState(false);
  const [repositoryChoice, setRepositoryChoice] = useState<RepositoryChoice>();
  const action = useAction();
  const fields: Field[] = [
    { key: "name", label: t("Verzeichnisname"), required: true },
    {
      key: "url",
      label: "SSH-URL",
      required: true,
      placeholder: "git@github.com:owner/repository.git",
    },
    {
      key: "keyId",
      label: t("SSH-Schlüssel"),
      type: "select",
      required: true,
      options: data?.sshKeys
        .filter(
          (key) =>
            key.canDelete ||
            (key.purpose === "source" && key.purposeId === editing?.id) ||
            key.id === Number(editing?.keyId),
        )
        .map((key) => ({
          key: String(key.id),
          label: t("Schlüssel {id}", { id: key.id ?? "" }),
        })),
    },
    { key: "readOnly", label: t("Nur lesend einbinden"), type: "check" },
  ];
  if (loading) {
    return <Loading />;
  }

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap gap-2">
        <Button className="min-h-11" onPress={() => setRepositoryDialog(true)}>
          {t("Repository verbinden")}
        </Button>
        <Button
          variant="ghost"
          className="min-h-11"
          onPress={() =>
            setEditing({ id: crypto.randomUUID(), projectId, readOnly: false })
          }
        >
          {t("Quelle manuell hinzufügen")}
        </Button>
      </div>
      <Failure message={error || action.error} onRetry={() => api.refresh()} />
      {!data && error ? null : !data?.sources.length ? (
        <Empty>{t("Keine Quellen")}</Empty>
      ) : (
        data.sources.map((source) => (
          <Card key={source.id} variant="secondary">
            <Card.Content className="flex-row flex-wrap items-center justify-between gap-4">
              <div className="min-w-0 flex-1 basis-64">
                <h2 className="font-medium">{source.name}</h2>
                <p className="text-sm text-muted mt-1 break-all">
                  {source.url}
                </p>
                {source.readOnly && (
                  <Chip size="sm" variant="soft" className="mt-3">
                    {t("Nur lesend")}
                  </Chip>
                )}
              </div>
              <ActionsMenu
                label={t("Aktionen für {name}", { name: source.name })}
                isDisabled={action.busy}
                actions={[
                  {
                    id: "edit",
                    label: t("Bearbeiten"),
                    onAction: () =>
                      setEditing({ ...source, keyId: String(source.keyId) }),
                  },
                  {
                    id: "remove",
                    label: t("Entfernen"),
                    destructive: true,
                    onAction: () =>
                      action.run(() =>
                        api.mutate(
                          "mutation($kind:String!,$id:String!){deleteResource(kind:$kind,id:$id)}",
                          { kind: "source", id: source.id },
                        ),
                      ),
                  },
                ]}
              />
            </Card.Content>
          </Card>
        ))
      )}
      {repositoryDialog && (
        <Inspect
          title={t("Repository verbinden")}
          placement="top"
          onClose={() => setRepositoryDialog(false)}
          isBusy={action.busy}
        >
          <div className="space-y-5">
            <RepositoryPicker
              value={repositoryChoice ?? {}}
              onChange={setRepositoryChoice}
            />
            <Failure message={action.error} />
            <div className="flex justify-end gap-2">
              <Button
                variant="ghost"
                className="min-h-11"
                isDisabled={action.busy}
                onPress={() => setRepositoryDialog(false)}
              >
                {t("Abbrechen")}
              </Button>
              <Button
                className="min-h-11"
                isPending={action.busy}
                isDisabled={!repositoryChoice?.repositoryId}
                onPress={() =>
                  action.run(async () => {
                    await api.mutate(
                      "mutation($projectId:String!,$connectionId:String!,$repositoryId:String!){connectRepository(projectId:$projectId,connectionId:$connectionId,repositoryId:$repositoryId){id}}",
                      {
                        projectId,
                        connectionId: repositoryChoice?.gitConnectionId,
                        repositoryId: repositoryChoice?.repositoryId,
                      },
                    );
                    setRepositoryDialog(false);
                    setRepositoryChoice(undefined);
                  })
                }
              >
                {t("Verbinden")}
              </Button>
            </div>
          </div>
        </Inspect>
      )}
      {editing && (
        <Editor
          title={t("Quelle konfigurieren")}
          fields={
            editing.bindingId
              ? fields.filter(
                  (field) => field.key === "name" || field.key === "readOnly",
                )
              : fields
          }
          initial={editing}
          onClose={() => setEditing(null)}
          onSave={(value) =>
            api.mutate(
              "mutation($input:SourceInput!){saveSource(input:$input){id}}",
              { input: { ...value, keyId: Number(value.keyId) } },
            )
          }
        />
      )}
    </div>
  );
}

function projectFields(
  data: Config | undefined,
  t: ReturnType<typeof useI18n>["t"],
): Field[] {
  return [
    { key: "name", label: "Name", required: true },
    {
      key: "machineIds",
      label: t("Fernmaschinen"),
      type: "multi",
      options: data?.machines.map((machine) => ({
        key: machine.id,
        label: machine.name,
      })),
    },
    {
      key: "poolIds",
      label: "Pools",
      type: "multi",
      options: data?.pools.map((pool) => ({ key: pool.id, label: pool.name })),
    },
    {
      key: "commitName",
      label: "Commit-Name (optional)",
      placeholder: data?.commitIdentity?.name,
      description: t(
        "Beide Felder leer lassen, um die globale Commit-Identität zu verwenden.",
      ),
    },
    {
      key: "commitEmail",
      label: "Commit-E-Mail (optional)",
      type: "email",
      placeholder: data?.commitIdentity?.email || "dev@example.org",
    },
  ];
}

function projectCreationSteps(
  t: ReturnType<typeof useI18n>["t"],
  data: Config | undefined,
): EditorStep[] {
  return [
    {
      id: "project",
      title: t("Wie heißt dein Projekt?"),
      description: t(
        "Der Arbeitsbereich wird automatisch auf der gewählten Maschine oder im Pool angelegt.",
      ),
      fields: ["name"],
    },
    {
      id: "machines",
      title: t("Wo sollen die Agenten arbeiten?"),
      description: t("Wähle mindestens eine Fernmaschine oder einen Pool."),
      fields: ["machineIds", "poolIds"],
      validate: (value): Record<string, string> =>
        ((value.machineIds as string[])?.length ?? 0) +
          ((value.poolIds as string[])?.length ?? 0) >
        0
          ? {}
          : {
              machineIds: "Wähle mindestens eine Fernmaschine oder einen Pool.",
            },
    },
    {
      id: "repository",
      title: t("Welches Repository gehört dazu?"),
      description: t(
        "Optional. Du kannst auch ohne Repository fortfahren und es später verbinden.",
      ),
      fields: [],
      showExtra: true,
    },
    {
      id: "identity",
      title: t("Wer soll als Commit-Autor erscheinen?"),
      description: data?.commitIdentity?.name
        ? t("Globale Identität: {name} <{email}>.", data.commitIdentity)
        : t(
            "Beide Felder leer lassen, um die globale Commit-Identität zu verwenden.",
          ),
      fields: [],
      advancedFields: ["commitName", "commitEmail"],
      advancedLabel: t("Commit-Identität anpassen"),
    },
  ];
}

function ProjectSettings({
  project,
  onClose,
}: {
  project: Project;
  onClose: () => void;
}) {
  const { t } = useI18n();
  const api = useAPI();
  const { data } = useQuery<Config>(CONFIG);

  return (
    <Editor
      title={t("Projekteinstellungen")}
      placement="top"
      fields={projectFields(data, t)}
      initial={{ ...project }}
      renderExtra={(value, patch) => (
        <ProjectRepository value={value} patch={patch} />
      )}
      onClose={onClose}
      onSave={(input) =>
        api.mutate(
          "mutation($input:ProjectInput!){saveProject(input:$input){id}}",
          { input },
        )
      }
    />
  );
}
