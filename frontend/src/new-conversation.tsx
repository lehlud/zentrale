import {
  Alert,
  Button,
  Label,
  Link,
  ListBox,
  Select,
  Spinner,
} from "@heroui/react";
import { useEffect, useRef, useState } from "react";
import { CONFIG, useAPI, useQuery } from "./api";
import type { Config } from "./config";
import type { Agent } from "./generated";
import { conversationHref } from "./navigation";
import { useI18n } from "./preferences";
import { Empty, Failure, Inspect, Loading, useAction } from "./ui";

export function NewConversation({
  projectId,
  onClose,
}: {
  projectId: string;
  onClose: () => void;
}) {
  const { t } = useI18n();
  const api = useAPI();
  const { data, error, loading } = useQuery<Config>(CONFIG);
  const [selected, setSelected] = useState(projectId);
  const started = useRef(false);
  const active = useRef(true);
  const action = useAction();
  useEffect(() => {
    active.current = true;
    return () => {
      active.current = false;
    };
  }, []);
  const ready = Boolean(
    data?.modelDefaults.main.modelId && data?.modelDefaults.approver.modelId,
  );
  const create = async (id: string) => {
    const result = await api.mutate<{ createAgent: Agent }>(
      "mutation($projectId:String!,$name:String!){createAgent(projectId:$projectId,name:$name){id}}",
      { projectId: id, name: t("Neue Unterhaltung") },
    );
    if (!active.current) return;
    location.hash = conversationHref(id, result.createAgent.id) + "&compose=1";
    onClose();
  };
  useEffect(() => {
    if (!data || !ready || started.current) return;
    const id =
      projectId || (data.projects.length === 1 ? data.projects[0].id : "");
    if (!id) return;
    started.current = true;
    setSelected(id);
    void action.run(() => create(id));
  }, [data, ready, projectId]);
  // A selected project can open directly. Reserve the dialog for choices and
  // recovery so creating a conversation does not steal focus from its composer.
  const automatic = ready && Boolean(projectId || data?.projects.length === 1);
  if (!error && !action.error && (loading || automatic))
    return (
      <Alert
        role="status"
        className="fixed right-4 top-4 z-40 w-auto max-w-[calc(100vw-2rem)]"
      >
        <Alert.Indicator>
          <Spinner size="sm" aria-hidden="true" />
        </Alert.Indicator>
        <Alert.Content>
          <Alert.Title>{t("Unterhaltung wird erstellt …")}</Alert.Title>
        </Alert.Content>
      </Alert>
    );
  return (
    <Inspect
      title={t("Neue Unterhaltung")}
      onClose={onClose}
      isBusy={action.busy}
    >
      <div className="space-y-5">
        <Failure
          message={error || action.error}
          onRetry={error ? () => api.refresh() : undefined}
        />
        {loading || action.busy ? (
          <Loading />
        ) : !data?.projects.length ? (
          <Empty
            action={
              <Link href="#/projects?new=1" onPress={onClose}>
                {t("Projekt anlegen")}
              </Link>
            }
          >
            {t("Lege zuerst ein Projekt mit einer Arbeitsmaschine an.")}
          </Empty>
        ) : !ready ? (
          <Empty
            action={
              <Link href="#/settings" onPress={onClose}>
                {t("Standardmodelle auswählen")}
              </Link>
            }
          >
            {t("Wähle zuerst die Standardmodelle für Aufgaben und Freigaben.")}
          </Empty>
        ) : (
          <form
            className="space-y-5"
            onSubmit={(event) => {
              event.preventDefault();
              if (selected) void action.run(() => create(selected));
            }}
          >
            <p className="text-sm leading-6 text-muted">
              {t("In welchem Projekt möchtest du arbeiten?")}
            </p>
            <Select
              value={selected || null}
              onChange={(id) => setSelected(String(id ?? ""))}
              className="gap-2"
              isRequired
            >
              <Label>{t("Projekt")}</Label>
              <Select.Trigger className="min-h-11">
                <Select.Value />
                <Select.Indicator />
              </Select.Trigger>
              <Select.Popover>
                <ListBox>
                  {data.projects.map((project) => (
                    <ListBox.Item
                      id={project.id}
                      key={project.id}
                      textValue={project.name}
                    >
                      <Label>{project.name}</Label>
                      <ListBox.ItemIndicator />
                    </ListBox.Item>
                  ))}
                </ListBox>
              </Select.Popover>
            </Select>
            <div className="flex justify-end">
              <Button type="submit" isDisabled={!selected} className="min-h-11">
                {t("Unterhaltung starten")}
              </Button>
            </div>
          </form>
        )}
      </div>
    </Inspect>
  );
}
