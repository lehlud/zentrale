import { Button, Label, TextArea, TextField } from "@heroui/react";
import { memo, type Ref } from "react";
import { useAPI } from "./api";
import { useDraft } from "./drafts";
import type { Agent, AgentInput, SandboxStatus } from "./generated";
import { useI18n } from "./preferences";
import { Failure, useAction } from "./ui";
import { AgentInbox } from "./agent-inbox";
import { SandboxBadge } from "./sandbox-badge";
import { Icon } from "./icons";

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
  const send = (mode: "steer" | "queue" = "steer") => {
    if (!prompt.trim()) return;
    latest();
    api.inputs.submit(agentId, prompt, mode);
    setPrompt("");
  };
  return (
    <form
      ref={composerRef}
      className="fixed inset-x-0 bottom-0 z-20 bg-surface px-4 pt-4 pb-[max(0.75rem,env(safe-area-inset-bottom))] sm:px-6 lg:left-[var(--sidebar-width,260px)] lg:px-8"
      onSubmit={(event) => {
        event.preventDefault();
        void send();
      }}
    >
      <div className="relative mx-auto max-w-3xl">
        <Failure message={action.error} />
        <AgentInbox
          agentId={agentId}
          inputs={inputs}
          onEdit={(text) => setPrompt(prompt ? prompt + "\n" + text : text)}
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
          <TextField
            fullWidth
            className="min-w-0 flex-1"
            value={prompt}
            onChange={setPrompt}
            autoFocus={autoFocus}
          >
            <Label className="sr-only">
              {t("Aufgabe oder neue Instruktion")}
            </Label>
            <TextArea
              className="min-h-16 max-h-40 resize-none [field-sizing:content]"
              placeholder={t("Was soll der Agent erledigen?")}
              rows={2}
              onKeyDown={(event) => {
                if (
                  event.key === "Tab" &&
                  !event.shiftKey &&
                  !event.ctrlKey &&
                  !event.metaKey &&
                  !event.altKey &&
                  !event.nativeEvent.isComposing &&
                  prompt.trim()
                ) {
                  event.preventDefault();
                  send("queue");
                } else if (
                  event.key === "Enter" &&
                  !event.shiftKey &&
                  !event.nativeEvent.isComposing
                ) {
                  event.preventDefault();
                  if (prompt.trim()) send();
                }
              }}
            />
          </TextField>
          <Button
            isIconOnly
            className="min-h-11 min-w-11 shrink-0"
            aria-label={
              current?.status === "running" && !prompt.trim()
                ? t("Agent stoppen")
                : `${current?.status === "running" ? t("Instruktion senden") : t("Starten")} ↑`
            }
            type={
              current?.status === "running" && !prompt.trim()
                ? "button"
                : "submit"
            }
            isPending={action.busy}
            isDisabled={!prompt.trim() && current?.status !== "running"}
            onPress={
              current?.status === "running" && !prompt.trim()
                ? () =>
                    action.run(() =>
                      api.mutate(
                        "mutation($agentId:String!){stopAgent(agentId:$agentId)}",
                        { agentId },
                        { announce: false },
                      ),
                    )
                : undefined
            }
          >
            <Icon
              name={
                current?.status === "running" && !prompt.trim()
                  ? "stop"
                  : "arrow"
              }
              className={
                current?.status === "running" && !prompt.trim()
                  ? "size-4 fill-current"
                  : "size-5 -rotate-90"
              }
            />
          </Button>
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
  );
});
