import { Button, Dropdown, Label } from "@heroui/react";
import { ChevronLeft, ChevronRight, GitFork, Pencil } from "lucide-react";
import { useState, type ReactNode } from "react";
import type { Agent, Message } from "./generated";
import { useAPI, useQuery } from "./api";
import { conversationHref } from "./navigation";
import { useI18n } from "./preferences";
import { Failure, useAction } from "./ui";

type Branch = Pick<
  Agent,
  "id" | "name" | "branchParentId" | "branchMessageLineageId"
>;

export function useConversationBranches(agentId: string, enabled: boolean) {
  const { data } = useQuery<{ conversationBranches: Branch[] }>(
    "query($id:String!){conversationBranches(agentId:$id){id name branchParentId branchMessageLineageId}}",
    { id: agentId },
    { enabled },
  );

  return data?.conversationBranches ?? [];
}

function openBranch(projectId: string, id: string) {
  location.hash = projectId
    ? conversationHref(projectId, id)
    : `#/chats/${encodeURIComponent(id)}`;
}

function versionsAt(branches: Branch[], agentId: string, lineageId: string) {
  const byId = new Map(branches.map((branch) => [branch.id, branch]));
  const visited = new Set<string>();
  let base = byId.get(agentId);
  while (base?.branchMessageLineageId === lineageId && !visited.has(base.id)) {
    visited.add(base.id);
    const parent = byId.get(base.branchParentId);
    if (!parent) break;
    base = parent;
  }
  if (!base) return [];

  // Creation order guarantees that a parent comes before its variants.
  const versions = new Set([base.id]);
  for (const branch of branches) {
    if (
      branch.branchMessageLineageId === lineageId &&
      versions.has(branch.branchParentId)
    )
      versions.add(branch.id);
  }

  return [...versions];
}

export function BranchSwitcher({
  branches,
  current,
  projectId,
}: {
  branches: Branch[];
  current: string;
  projectId: string;
}) {
  const { t } = useI18n();
  if (branches.length < 2) return null;

  return (
    <Dropdown>
      <Button
        isIconOnly
        variant="ghost"
        size="sm"
        aria-label={t("Unterhaltungszweige")}
      >
        <GitFork size={17} />
      </Button>
      <Dropdown.Popover placement="bottom end">
        <Dropdown.Menu
          aria-label={t("Unterhaltungszweige")}
          selectionMode="single"
          selectedKeys={[current]}
          onAction={(id) => openBranch(projectId, String(id))}
        >
          {branches.map((branch, index) => (
            <Dropdown.Item
              key={branch.id}
              id={branch.id}
              textValue={branch.name}
            >
              <Label>
                {index + 1} · {branch.name || t("Unterhaltung")}
              </Label>
            </Dropdown.Item>
          ))}
        </Dropdown.Menu>
      </Dropdown.Popover>
    </Dropdown>
  );
}

export function BranchableMessage({
  message,
  renderBubble,
  branches,
  projectId,
  disabled,
}: {
  message: Message;
  renderBubble: (content?: ReactNode) => ReactNode;
  branches: Branch[];
  projectId: string;
  disabled: boolean;
}) {
  const api = useAPI();
  const { t } = useI18n();
  const action = useAction();
  const [edit, setEdit] = useState<string | null>(null);
  const versions = versionsAt(
    branches,
    message.agentId,
    message.lineageId || message.id,
  );
  const index = versions.indexOf(message.agentId);
  const canSwitch = index >= 0 && versions.length > 1;

  function fork(text: string | null) {
    void action.run(
      async () => {
        const { branchConversation } = await api.mutate<{
          branchConversation: { id: string };
        }>(
          "mutation($id:String!,$message:String!,$text:String){branchConversation(agentId:$id,messageId:$message,text:$text){id}}",
          { id: message.agentId, message: message.id, text },
          { announce: false },
        );
        openBranch(projectId, branchConversation.id);
      },
      "branch",
      { announce: false },
    );
  }

  return (
    <>
      {edit === null
        ? renderBubble()
        : renderBubble(
            <form
              className="grid min-w-0 gap-2 sm:min-w-80"
              onSubmit={(event) => {
                event.preventDefault();
                fork(edit);
              }}
            >
              <textarea
                autoFocus
                className="min-h-24 min-w-0 resize-y rounded-lg border border-border bg-surface p-2"
                aria-label={t("Nachricht bearbeiten")}
                value={edit}
                onChange={(event) => setEdit(event.target.value)}
              />
              <div className="flex justify-end gap-2">
                <Button
                  type="button"
                  size="sm"
                  variant="ghost"
                  isDisabled={action.busy}
                  onPress={() => setEdit(null)}
                >
                  {t("Abbrechen")}
                </Button>
                <Button
                  type="submit"
                  size="sm"
                  isPending={action.busy}
                  isDisabled={!edit.trim() && !message.attachments?.length}
                >
                  {t("Speichern und senden")}
                </Button>
              </div>
            </form>,
          )}
      {edit === null && (canSwitch || !disabled) && (
        <div className="mt-1 flex items-center justify-end gap-1 text-xs text-muted">
          {canSwitch && (
            <div
              className="flex items-center"
              aria-label={t("Unterhaltungszweige")}
              role="group"
            >
              <Button
                isIconOnly
                size="sm"
                variant="ghost"
                isDisabled={index === 0}
                onPress={() => openBranch(projectId, versions[index - 1])}
                aria-label={t("Vorheriger Zweig")}
              >
                <ChevronLeft size={15} />
              </Button>
              <span>
                {index + 1}/{versions.length}
              </span>
              <Button
                isIconOnly
                size="sm"
                variant="ghost"
                isDisabled={index === versions.length - 1}
                onPress={() => openBranch(projectId, versions[index + 1])}
                aria-label={t("Nächster Zweig")}
              >
                <ChevronRight size={15} />
              </Button>
            </div>
          )}
          {!disabled && (
            <>
              <Button
                isIconOnly
                size="sm"
                variant="ghost"
                isDisabled={action.busy}
                onPress={() => setEdit(message.content)}
                aria-label={t("Nachricht bearbeiten")}
              >
                <Pencil size={15} />
              </Button>
              <Button
                isIconOnly
                size="sm"
                variant="ghost"
                isPending={action.busy}
                onPress={() => fork(null)}
                aria-label={t("Hier abzweigen")}
              >
                <GitFork size={15} />
              </Button>
            </>
          )}
        </div>
      )}
      <Failure message={action.error} />
    </>
  );
}
