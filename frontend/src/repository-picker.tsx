import {
  Button,
  Input,
  Label,
  ListBox,
  Select,
  TextField,
} from "@heroui/react";
import {
  useDeferredValue,
  useEffect,
  useRef,
  useState,
  useSyncExternalStore,
} from "react";

import { useAPI, useQuery } from "./api";
import type {
  GitConnection,
  GitNamespace,
  GitRepository,
  GitRepositoryPage,
} from "./generated";
import { GitConnect, gitConnectionsQuery, gitName } from "./git-connections";
import { useI18n } from "./preferences";
import { Editor, Empty, Failure, Loading } from "./ui";
import { Icon } from "./icons";

export type RepositoryChoice = {
  gitConnectionId: string;
  repositoryId: string;
  repositoryName: string;
};

const repositoryFields =
  "id identity name fullName sshUrl webUrl private canManageDeployKeys";

const repositoriesQuery = `query($connectionId:String!,$search:String!,$offset:Int!){gitRepositories(connectionId:$connectionId,search:$search,offset:$offset){items{${repositoryFields}} hasMore nextOffset}}`;

export function RepositoryPicker({
  value,
  onChange,
}: {
  value: Partial<RepositoryChoice>;
  onChange: (choice: RepositoryChoice) => void;
}) {
  const { t } = useI18n();
  const api = useAPI();
  const { data, error, loading } = useQuery<{
    gitConnections: GitConnection[];
  }>(gitConnectionsQuery);
  const [kind, setKind] = useState(value.gitConnectionId || "");
  const connections = data?.gitConnections ?? [];
  const connection =
    connections.find((entry) => entry.id === kind) ??
    connections.find((entry) => entry.connected) ??
    connections[0];
  const [connecting, setConnecting] = useState(false);
  const [creating, setCreating] = useState(false);
  const [expanded, setExpanded] = useState(!value.repositoryId);
  const choose = (repository: GitRepository) => {
    if (connection) {
      onChange({
        gitConnectionId: connection.id,
        repositoryId: repository.id,
        repositoryName: repository.fullName,
      });
      setExpanded(false);
    }
  };

  return (
    <section
      className="min-w-0 space-y-4"
      aria-label={t("Repository auswählen")}
    >
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h3 className="text-sm font-medium">{t("Repository (optional)")}</h3>
        {value.repositoryId && (
          <div className="flex gap-1">
            {!expanded && (
              <Button
                size="sm"
                variant="ghost"
                className="min-h-11"
                onPress={() => setExpanded(true)}
              >
                {t("Repository ändern")}
              </Button>
            )}
            <Button
              size="sm"
              variant="ghost"
              className="min-h-11"
              onPress={() => {
                onChange({
                  gitConnectionId: "",
                  repositoryId: "",
                  repositoryName: "",
                });
                setExpanded(true);
              }}
            >
              {t("Auswahl entfernen")}
            </Button>
          </div>
        )}
      </div>
      {value.repositoryName && (
        <p role="status" className="break-all text-sm">
          <span className="text-muted">{t("Ausgewählt")}: </span>
          {value.repositoryName}
        </p>
      )}
      <Failure message={error} onRetry={() => api.refresh()} />
      {!expanded ? null : loading ? (
        <Loading />
      ) : (
        connection && (
          <>
            <Select
              value={connection.id}
              onChange={(next) => setKind(String(next ?? ""))}
              className="gap-2"
            >
              <Label>{t("Git-Dienst")}</Label>
              <Select.Trigger className="min-h-11">
                <Select.Value />
                <Select.Indicator />
              </Select.Trigger>
              <Select.Popover>
                <ListBox>
                  {connections.map((entry) => (
                    <ListBox.Item
                      id={entry.id}
                      key={entry.id}
                      textValue={gitName(entry.kind)}
                      className="min-h-11"
                    >
                      <Label>{gitName(entry.kind)}</Label>
                      <ListBox.ItemIndicator />
                    </ListBox.Item>
                  ))}
                </ListBox>
              </Select.Popover>
            </Select>
            {connection.connected ? (
              <>
                <RepositoryList
                  connection={connection}
                  selected={
                    value.gitConnectionId === connection.id
                      ? value.repositoryId
                      : ""
                  }
                  onChoose={choose}
                />
                <Button
                  variant="secondary"
                  className="min-h-11"
                  onPress={() => setCreating(true)}
                >
                  <Icon name="plus" />
                  {t("Repository erstellen")}
                </Button>
              </>
            ) : (
              <Button
                variant="secondary"
                className="min-h-11"
                onPress={() => setConnecting(true)}
              >
                {t("{name} verbinden", { name: gitName(connection.kind) })}
              </Button>
            )}
          </>
        )
      )}
      <p className="text-xs leading-5 text-muted">
        {t(
          "Ein eigener Deploy-Key erlaubt Lesen und Schreiben. Vorhandene Repo-Schlüssel werden wiederverwendet.",
        )}
      </p>
      {connection && connecting && (
        <GitConnect
          connection={connection}
          onClose={() => setConnecting(false)}
        />
      )}
      {connection && creating && (
        <CreateRepository
          connection={connection}
          onCreated={choose}
          onClose={() => setCreating(false)}
        />
      )}
    </section>
  );
}

function RepositoryList({
  connection,
  selected,
  onChoose,
}: {
  connection: GitConnection;
  selected?: string;
  onChoose: (repository: GitRepository) => void;
}) {
  const { t } = useI18n();
  const api = useAPI();
  const version = useSyncExternalStore(api.subscribe, api.snapshot);
  const [search, setSearch] = useState("");
  const needle = useDeferredValue(search.trim());
  const requestKey = `${connection.id}:${needle}`;
  const [result, setResult] = useState<{
    key: string;
    page: GitRepositoryPage;
  }>();
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [retry, setRetry] = useState(0);
  const page = result?.key === requestKey ? result.page : undefined;
  const visibleCount = useRef({ key: "", count: 0 });
  visibleCount.current = { key: requestKey, count: page?.items.length ?? 0 };

  useEffect(() => {
    const controller = new AbortController();
    setError("");
    const retainCount =
      visibleCount.current.key === requestKey ? visibleCount.current.count : 0;

    async function refresh() {
      let refreshed = (
        await api.query<{ gitRepositories: GitRepositoryPage }>(
          repositoriesQuery,
          { connectionId: connection.id, search: needle, offset: 0 },
          controller.signal,
        )
      ).gitRepositories;
      while (refreshed.hasMore && refreshed.items.length < retainCount) {
        const next = (
          await api.query<{ gitRepositories: GitRepositoryPage }>(
            repositoriesQuery,
            {
              connectionId: connection.id,
              search: needle,
              offset: refreshed.nextOffset,
            },
            controller.signal,
          )
        ).gitRepositories;
        refreshed = { ...next, items: [...refreshed.items, ...next.items] };
      }

      if (!controller.signal.aborted)
        setResult({ key: requestKey, page: refreshed });
    }
    void refresh().catch((failure: Error) => {
      if (!controller.signal.aborted) setError(failure.message);
    });

    return () => controller.abort();
  }, [api, connection.id, needle, requestKey, version, retry]);

  async function more() {
    if (!page || busy) {
      return;
    }

    setBusy(true);

    try {
      const next = await api.query<{ gitRepositories: GitRepositoryPage }>(
        repositoriesQuery,
        {
          connectionId: connection.id,
          search: needle,
          offset: page.nextOffset,
        },
      );
      setResult((current) =>
        current?.key !== requestKey
          ? current
          : {
              key: requestKey,
              page: {
                ...next.gitRepositories,
                items: [
                  ...current.page.items,
                  ...next.gitRepositories.items.filter(
                    (item) =>
                      !current.page.items.some(
                        (existing) => existing.id === item.id,
                      ),
                  ),
                ],
              },
            },
      );
      setError("");
    } catch (failure) {
      setError((failure as Error).message);
    } finally {
      setBusy(false);
    }
  }

  async function refreshCatalog() {
    if (busy) {
      return;
    }

    setBusy(true);

    try {
      await api.mutate(
        "mutation($connectionId:String!){refreshGitRepositories(connectionId:$connectionId)}",
        { connectionId: connection.id },
      );
      setError("");
    } catch (failure) {
      setError((failure as Error).message);
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-end gap-2">
        <TextField value={search} onChange={setSearch} className="gap-2">
          <Label>{t("Repositories suchen")}</Label>
          <Input
            type="search"
            placeholder={t("Name oder Organisation")}
            className="min-h-11"
          />
        </TextField>
        <Button
          variant="ghost"
          className="min-h-11"
          aria-label={t("Repositories aktualisieren")}
          isPending={busy}
          onPress={() => void refreshCatalog()}
        >
          {t("Aktualisieren")}
        </Button>
      </div>
      <Failure message={error} onRetry={() => setRetry((value) => value + 1)} />
      {!page && !error ? (
        <Loading />
      ) : page?.items.length === 0 ? (
        <Empty>
          {t("Keine passenden Repositories mit Berechtigung für Deploy-Keys")}
        </Empty>
      ) : (
        <ul aria-label={t("Repositories")} className="space-y-1">
          {page?.items.map((repository) => (
            <li key={repository.id}>
              <Button
                variant={selected === repository.id ? "secondary" : "ghost"}
                className="min-h-11 w-full justify-between whitespace-normal text-start"
                aria-pressed={selected === repository.id}
                onPress={() => onChoose(repository)}
              >
                <span className="min-w-0 break-all">{repository.fullName}</span>
                <span className="shrink-0 text-xs text-muted">
                  {t(repository.private ? "Privat" : "Öffentlich")}
                </span>
              </Button>
            </li>
          ))}
        </ul>
      )}
      {page?.hasMore && (
        <Button
          variant="secondary"
          className="min-h-11"
          isPending={busy}
          onPress={() => void more()}
        >
          {t("Mehr anzeigen")}
        </Button>
      )}
    </div>
  );
}

function CreateRepository({
  connection,
  onCreated,
  onClose,
}: {
  connection: GitConnection;
  onCreated: (repository: GitRepository) => void;
  onClose: () => void;
}) {
  const { t } = useI18n();
  const api = useAPI();
  const { data, error, loading } = useQuery<{ gitNamespaces: GitNamespace[] }>(
    "query($connectionId:String!){gitNamespaces(connectionId:$connectionId){id name personal}}",
    { connectionId: connection.id },
  );

  return (
    <Editor
      title={t("Repository erstellen")}
      initial={{
        connectionId: connection.id,
        name: "",
        namespaceId: "",
        description: "",
        private: true,
      }}
      fields={[
        {
          key: "name",
          label: t("Repository-Name"),
          required: true,
          placeholder: "mein-projekt",
        },
        {
          key: "namespaceId",
          label: t("Konto oder Organisation"),
          type: "select",
          required: true,
          options: data?.gitNamespaces.map((namespace) => ({
            key: namespace.id,
            label: namespace.name,
          })),
        },
        { key: "description", label: t("Beschreibung (optional)") },
        { key: "private", label: t("Privates Repository"), type: "check" },
      ]}
      renderExtra={() => (
        <>
          {loading && <Loading />}
          <Failure message={error} onRetry={() => api.refresh()} />
        </>
      )}
      onSave={async (input) => {
        const result = await api.mutate<{ createGitRepository: GitRepository }>(
          `mutation($input:GitRepositoryInputInput!){createGitRepository(input:$input){${repositoryFields}}}`,
          { input },
          { announce: false },
        );
        onCreated(result.createGitRepository);
      }}
      submitLabel={t("Repository erstellen")}
      onClose={onClose}
    />
  );
}
