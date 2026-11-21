import { Button } from "@heroui/react";
import { memo, useEffect, useRef, useState, type Ref } from "react";
import { useAPI } from "./api";
import { useDraft } from "./drafts";
import type { Agent, AgentInput, SandboxStatus } from "./generated";
import { useI18n } from "./preferences";
import { Failure, useAction } from "./ui";
import { AgentInbox } from "./agent-inbox";
import { ChatInput } from "./chat-input";
import { SandboxBadge } from "./sandbox-badge";
import { Icon } from "./icons";
import { useComposerAttachments } from "./composer-attachments";
import { SideConversation } from "./side-conversation";
import { TeamworkBoard } from "./teamwork-board";
import { composerCommand } from "./composer-commands";
import { ComposerCommandMenu } from "./composer-command-menu";

// Keystrokes stay in this small subtree, never in the transcript owner.
export const AgentComposer = memo(function AgentComposer({
  agentId,
  current,
  inputs,
  following,
  latest,
  composerRef,
  autoFocus,
  onDetails,
  sandboxStatus,
  sandboxMode,
}: {
  agentId: string;
  current?: Agent;
  inputs: AgentInput[];
  following: boolean;
  latest: () => void;
  composerRef: Ref<HTMLFormElement>;
  autoFocus: boolean;
  onDetails: () => void;
  sandboxStatus?: SandboxStatus;
  sandboxMode?: string;
}) {
  const { t } = useI18n();
  const api = useAPI();
  const action = useAction();
  const [prompt, setPrompt] = useDraft(`prompt:${agentId}`);
  const attachments = useComposerAttachments(agentId);
  const picker = useRef<HTMLInputElement>(null);
  const sending = useRef(false);
  const [sideQuestion, setSideQuestion] = useState<string | null>(null);
  const [teamworkOpen, setTeamworkOpen] = useState(false);

  useEffect(() => {
    setSideQuestion(null);
    setTeamworkOpen(false);
  }, [agentId]);

  const hasMessage = Boolean(prompt.trim() || attachments.items.length);
  const send = async (mode: "steer" | "queue" = "steer", text = prompt) => {
    if (
      (!text.trim() && !attachments.items.length) ||
      attachments.uploading ||
      sending.current ||
      action.busy
    )
      return;

    sending.current = true;
    try {
      const parsed = composerCommand(text);
      if (parsed) {
        let handled = true;
        await action.run(async () => {
          handled = await parsed.command.execute(
            {
              side: setSideQuestion,
              teamwork: () => setTeamworkOpen(true),
              resume: () =>
                api.mutate(
                  "mutation($agentId:String!){resumeInputs(agentId:$agentId)}",
                  { agentId },
                  { announce: false },
                ),
              stop: () =>
                api.mutate(
                  "mutation($agentId:String!){stopAgent(agentId:$agentId)}",
                  { agentId },
                  { announce: false },
                ),
            },
            parsed.args,
          );

          if (handled) setPrompt("");
        });

        if (handled) return;
      }

      const uploaded = await attachments.upload();
      if (!uploaded) return;

      // /teamwork is an ordinary durable user request understood by the main
      // prompt. Preserve its wording; no routing inference or automatic spawn.
      api.inputs.submit(agentId, text, mode, uploaded);
      setPrompt("");
      attachments.clear();
      latest();
    } finally {
      sending.current = false;
    }
  };

  return (
    <>
      <form
        ref={composerRef}
        onDrop={attachments.drop}
        onDragEnter={attachments.dragEnter}
        onDragLeave={attachments.dragLeave}
        onDragOver={attachments.dragOver}
        className="fixed inset-x-0 bottom-0 z-20 bg-surface px-4 pt-4 pb-[max(0.75rem,env(safe-area-inset-bottom))] sm:px-6 lg:left-[var(--sidebar-width,260px)] lg:px-8"
        onSubmit={(event) => {
          event.preventDefault();
          void send();
        }}
      >
        <div className="relative mx-auto max-w-[96rem]">
          <Failure message={action.error} />
          <Failure message={t(attachments.error)} />
          {attachments.dragging && (
            <p
              role="status"
              className="mb-3 rounded-lg border-2 border-dashed border-accent bg-accent-soft p-3 text-center text-sm"
            >
              {t("Dateien hier ablegen")}
            </p>
          )}
          {attachments.items.length > 0 && (
            <ul
              aria-label={t("Anhänge")}
              className="mb-3 flex max-h-32 flex-wrap gap-2 overflow-y-auto"
            >
              {attachments.items.map((item) => (
                <li
                  key={item.id}
                  className="relative flex min-w-0 max-w-56 items-center gap-2 rounded-lg border border-border bg-surface-secondary py-2 pr-9 pl-2"
                >
                  {item.preview ? (
                    <img
                      src={item.preview}
                      alt={item.file.name}
                      className="size-14 shrink-0 rounded object-cover"
                    />
                  ) : (
                    <Icon name="file" className="size-7" />
                  )}
                  <span className="min-w-0">
                    <span
                      className="block truncate text-xs"
                      title={item.file.name}
                    >
                      {item.file.name}
                    </span>
                    <span className="text-[11px] text-muted">
                      {Math.ceil(
                        (item.uploaded?.size ?? item.file.size) / 1024,
                      )}{" "}
                      KiB
                    </span>
                  </span>
                  <Button
                    isIconOnly
                    size="sm"
                    variant="ghost"
                    className="absolute top-0 right-0 min-h-8 min-w-8"
                    aria-label={t("Anhang entfernen: {name}", {
                      name: item.file.name,
                    })}
                    isDisabled={attachments.uploading}
                    onPress={() => attachments.remove(item.id)}
                  >
                    <Icon name="error" className="size-3.5" />
                  </Button>
                </li>
              ))}
            </ul>
          )}
          {attachments.uploading && (
            <div className="mb-2 flex items-center justify-between gap-3 text-xs">
              <p role="status">
                {t("Anhänge werden hochgeladen …")} {attachments.progress}/
                {attachments.items.length}
              </p>
              <Button variant="ghost" size="sm" onPress={attachments.cancel}>
                {t("Upload abbrechen")}
              </Button>
            </div>
          )}
          <input
            ref={picker}
            type="file"
            multiple
            className="hidden"
            aria-label={t("Dateien auswählen")}
            onChange={(event) => {
              attachments.add(Array.from(event.target.files ?? []));
              event.target.value = "";
            }}
          />
          <AgentInbox
            agentId={agentId}
            inputs={inputs}
            onEdit={(text, files = []) => {
              setPrompt(prompt ? prompt + "\n" + text : text);
              attachments.restore(files);
            }}
          />
          {!following && (
            <Button
              isIconOnly
              size="sm"
              aria-label={t("Zu neuesten Einträgen ↓")}
              className="absolute -top-10 right-1.5 size-8 min-h-8 min-w-8 rounded-full border border-border bg-surface text-muted shadow-none"
              variant="secondary"
              onPress={latest}
            >
              <Icon name="down" className="size-3.5" />
            </Button>
          )}
          <div className="flex items-end gap-3">
            <Button
              isIconOnly
              variant="ghost"
              className="min-h-11 min-w-11 shrink-0"
              aria-label={t("Dateien anhängen")}
              isDisabled={attachments.uploading}
              onPress={() => picker.current?.click()}
            >
              <Icon name="attachment" />
            </Button>
            <ComposerCommandMenu
              value={prompt}
              onChange={setPrompt}
              onCommand={(name) => void send("steer", `/${name}`)}
            >
              {(commandsOpen) => (
                <ChatInput
                  value={prompt}
                  onChange={setPrompt}
                  autoFocus={autoFocus}
                  isDisabled={attachments.uploading}
                  label={t("Aufgabe oder neue Instruktion")}
                  placeholder={t("Was soll der Agent erledigen?")}
                  onPaste={attachments.paste}
                  onKeyDown={(event) => {
                    if (
                      commandsOpen &&
                      [
                        "ArrowDown",
                        "ArrowUp",
                        "Enter",
                        "Tab",
                        "Escape",
                      ].includes(event.key)
                    )
                      return;

                    if (
                      event.key === "Tab" &&
                      !event.shiftKey &&
                      !event.ctrlKey &&
                      !event.metaKey &&
                      !event.altKey &&
                      !event.nativeEvent.isComposing &&
                      hasMessage
                    ) {
                      event.preventDefault();
                      send("queue");
                    } else if (
                      event.key === "Enter" &&
                      !event.shiftKey &&
                      !event.nativeEvent.isComposing
                    ) {
                      event.preventDefault();
                      if (hasMessage) void send();
                    }
                  }}
                />
              )}
            </ComposerCommandMenu>
          </div>
          <div className="mt-2 flex min-h-7 min-w-0 flex-wrap items-center justify-center gap-x-4 gap-y-1 px-2 text-[11px] text-muted">
            <Button
              size="sm"
              variant="ghost"
              aria-label={
                current?.modelId
                  ? t("Agentendetails für {model}", {
                      model: current.modelId,
                    })
                  : t("Agentendetails")
              }
              className="min-h-7 min-w-0 max-w-[45%] px-2 text-[11px] text-muted"
              onPress={onDetails}
            >
              <span className="truncate">
                {current?.modelId || t("Agentendetails")}
              </span>
            </Button>
            {current?.machineId && (
              <SandboxBadge
                compact
                mode={sandboxMode ?? sandboxStatus?.mode ?? "unknown"}
                status={
                  sandboxStatus?.machineId === current.machineId
                    ? sandboxStatus
                    : undefined
                }
              />
            )}
          </div>
        </div>
      </form>
      {sideQuestion !== null && (
        <SideConversation
          agentId={agentId}
          initialQuestion={sideQuestion}
          onClose={() => setSideQuestion(null)}
        />
      )}
      {teamworkOpen && (
        <TeamworkBoard
          agentId={agentId}
          onClose={() => setTeamworkOpen(false)}
        />
      )}
    </>
  );
});
