import { Button, Input, Label, TextField } from "@heroui/react";
import { useState } from "react";

import { useAPI, useQuery } from "./api";
import type { Project } from "./generated";
import { useI18n } from "./preferences";
import { Failure, Inspect, useAction } from "./ui";

type ProjectDialog = { project: Project; onClose: () => void };

export function ProjectAccess({ project, onClose }: ProjectDialog) {
  const { t } = useI18n();
  const api = useAPI();
  const action = useAction();
  const [username, setUsername] = useState("");
  const { data, error } = useQuery<{
    projectMembers: { username: string; name: string }[];
  }>("query($id:String!){projectMembers(projectId:$id){username name}}", {
    id: project.id,
  });

  const change = (name: string, add: boolean) =>
    action.run(async () => {
      await api.mutate(
        "mutation($id:String!,$username:String!,$add:Boolean!){setProjectMember(projectId:$id,username:$username,add:$add)}",
        { id: project.id, username: name.trim(), add },
      );
      if (add) setUsername("");
    });

  return (
    <Inspect
      title={t("Mitglieder · {name}", { name: project.name })}
      onClose={onClose}
    >
      <div className="space-y-4">
        <Failure message={error || action.error} />
        <p className="text-sm text-muted">
          {t(
            "Mitglieder können in diesem Projekt arbeiten. Git-Verbindungen bleiben beim Projektersteller.",
          )}
        </p>
        <form
          className="flex items-end gap-2"
          onSubmit={(event) => {
            event.preventDefault();
            void change(username, true);
          }}
        >
          <TextField
            className="min-w-0 flex-1"
            value={username}
            onChange={setUsername}
          >
            <Label>{t("Benutzername")}</Label>
            <Input autoComplete="off" />
          </TextField>
          <Button
            type="submit"
            isDisabled={!username.trim()}
            isPending={action.busy}
          >
            {t("Hinzufügen")}
          </Button>
        </form>
        <ul className="divide-y divide-separator">
          {data?.projectMembers.map((member) => (
            <li
              key={member.username}
              className="flex items-center justify-between gap-2 py-2"
            >
              <span className="min-w-0 truncate">
                {member.name || member.username}{" "}
                <small className="text-muted">@{member.username}</small>
              </span>
              <Button
                size="sm"
                variant="ghost"
                isDisabled={action.busy}
                onPress={() => change(member.username, false)}
              >
                {t("Entfernen")}
              </Button>
            </li>
          ))}
        </ul>
      </div>
    </Inspect>
  );
}

export function ProjectArchive({ project, onClose }: ProjectDialog) {
  const { t } = useI18n();
  const api = useAPI();
  const action = useAction();

  return (
    <Inspect
      title={t("{name} archivieren", { name: project.name })}
      onClose={onClose}
    >
      <div className="space-y-4">
        <p className="text-sm text-muted">
          {t(
            "Unterhaltungen bleiben lesbar. Wenn alle Git-Arbeitskopien sauber sind, werden VMs und Projektdaten auf den Fernmaschinen entfernt.",
          )}
        </p>
        <Failure message={action.error} />
        <Button
          variant="danger"
          isPending={action.busy}
          onPress={() =>
            action.run(async () => {
              await api.mutate(
                "mutation($id:String!){archiveProject(projectId:$id)}",
                { id: project.id },
              );
              onClose();
            })
          }
        >
          {t(project.archivePending ? "Aufräumen wiederholen" : "Archivieren")}
        </Button>
      </div>
    </Inspect>
  );
}
