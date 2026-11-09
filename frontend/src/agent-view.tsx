import { Button, Label, TextArea, TextField } from "@heroui/react";
import { useEffect, useState } from "react";

import { useAPI, useQuery } from "./api";
import { CostSummary } from "./usage-summary";
import { AgentQuestion } from "./agent-question";
import { ModelResponse } from "./model-response";
import { ActivityOutput } from "./activity-details";
import { transcriptQuery, type Transcript } from "./agent-transcript";
import type { Agent, Project, Message, SandboxStatus } from "./generated";
import { HostKeyTrust } from "./ssh-trust";
import { useI18n } from "./preferences";
import { Failure, Inspect, Loading, useAction } from "./ui";
import { JobsPanel, ContextPanel } from "./agent-panels";
import { useDraft } from "./drafts";
import { AgentControls } from "./agent-controls";
import { AgentConversation } from "./agent-conversation";
import { ApproverLog } from "./approver-log";
import { useTranscriptViewport } from "./transcript-viewport";
import { Icon } from "./icons";
import { SandboxBadge } from "./sandbox-badge";
import { useRoute } from "./navigation";
import { RenameConversation } from "./rename-conversation";
import { LiveAssistantResponse } from "./live-assistant-response";
import { AgentInbox } from "./agent-inbox";
import { AgentGoal } from "./agent-goal";

export function AgentView({
  agentId,
  project,
  onProjectDialog,
  agents,
  onSelect,
  sandboxStatus,
  sandboxMode,
  machineName,
  onCreateAgent,
  canCreateAgent,
  focusAgentSwitcher,
  onAgentSwitcherFocus,
}: {
  agentId: string;
  project: Project;
  onProjectDialog: (view: string) => void;
  agents: Agent[];
  onSelect: (id: string) => void;
  sandboxStatus?: SandboxStatus;
  sandboxMode?: string;
  machineName?: string;
  onCreateAgent: () => void;
  canCreateAgent: boolean;
  focusAgentSwitcher: boolean;
  onAgentSwitcherFocus: () => void;
}) {
  const { t } = useI18n();

  const api = useAPI();
  const { params } = useRoute();
  const [renaming, setRenaming] = useState(false);
  const [offset, setOffset] = useState(0);
  const [detailsOpen, setDetailsOpen] = useState(false);
  const [visibleMessages, setVisibleMessages] = useState<Message[]>([]);
  const { data, error, loading } = useQuery<Transcript>(transcriptQuery, {
    id: agentId,
    offset,
  });

  useEffect(() => {
    if (!data) {
      return;
    }
    setVisibleMessages((previous) => {
      const messages = new Map(
        previous.map((message) => [message.id, message]),
      );
      for (const message of [...data.messages, ...data.recentMessages])
        messages.set(message.id, message);

      return [...messages.values()].sort(
        (left, right) =>
          left.createdAt.localeCompare(right.createdAt) ||
          left.id.localeCompare(right.id),
      );
    });
  }, [data]);

  const [prompt, setPrompt] = useDraft(`prompt:${agentId}`);
  const [inspection, setInspection] = useState<{
    title: string;
    content: string;
    kind?: "model";
  } | null>(null);
  const action = useAction();
  const connectionChallenge = data?.agentRunState?.hostKey.publicKey
    ? data.agentRunState.hostKey
    : undefined;
  const current = data?.agent ?? agents.find((agent) => agent.id === agentId);
  const viewport = useTranscriptViewport(
    detailsOpen || renaming || Boolean(inspection),
    Boolean(data),
  );

  useEffect(() => {
    if (!data) {
      return;
    }
    api.inputs.acknowledge(
      new Set([
        ...(data.agentInputs ?? []).map((input) => input.id),
        ...data.messages.map((message) => message.id),
        ...data.recentMessages.map((message) => message.id),
      ]),
    );
  }, [api, data]);

  const send = (mode: "steer" | "queue" = "steer") => {
    if (!prompt.trim()) {
      return;
    }

    viewport.latest();
    api.inputs.submit(agentId, prompt, mode);
    setPrompt("");
  };
  const resume = () =>
    action.run(() =>
      api.mutate(
        "mutation($agentId:String!){resumeInputs(agentId:$agentId)}",
        { agentId },
        { announce: false },
      ),
    );
  const inspectionDialog = inspection && (
    <Inspect title={inspection.title} onClose={() => setInspection(null)}>
      {inspection.kind === "model" ? (
        <ModelResponse content={inspection.content} />
      ) : (
        <ActivityOutput content={inspection.content} />
      )}
    </Inspect>
  );

  return (
    <section
      ref={viewport.root}
      className="min-w-0 [&_*]:scroll-mb-[var(--composer-space,12rem)] [&_*]:scroll-mt-[var(--chat-header-space,6rem)]"
      aria-label={current?.name || t("Agent")}
    >
      <AgentControls
        project={project}
        onProjectDialog={onProjectDialog}
        headerRef={viewport.header}
        onCreateAgent={onCreateAgent}
        canCreateAgent={canCreateAgent}
        focusAgentSwitcher={focusAgentSwitcher}
        onAgentSwitcherFocus={onAgentSwitcherFocus}
        current={current}
        agents={agents}
        onSelect={onSelect}
        action={action}
        onDetails={() => setDetailsOpen(true)}
        onRename={() => setRenaming(true)}
      />
      <div
        role="region"
        aria-label={t("Agentenverlauf")}
        className="mx-auto min-w-0 max-w-3xl py-6 sm:py-8"
      >
        {loading && !data ? (
          <Loading />
        ) : current?.role === "approver" ? (
          <ApproverLog
            messages={visibleMessages}
            parentId={current.parentId}
            working={current.status === "running"}
            onInspect={(content) =>
              setInspection({
                title: t("Schrittdetails"),
                content,
                kind: "model",
              })
            }
          />
        ) : !visibleMessages.length && !error ? (
          <div className="flex min-h-[calc(100dvh-19rem)] flex-col items-center justify-center gap-3 px-3 py-8 text-center">
            <h2 className="text-2xl font-medium tracking-tight">
              {t("Woran möchtest du arbeiten?")}
            </h2>
            <p className="max-w-md text-sm leading-7 text-muted">
              {t(
                "Beschreibe dein Ziel. Der Agent kümmert sich um die nächsten Schritte.",
              )}
            </p>
          </div>
        ) : (
          <AgentConversation
            messages={visibleMessages}
            working={current?.status === "running"}
            onInspect={(content) =>
              setInspection({
                title: t("Schrittdetails"),
                content,
                kind: "model",
              })
            }
          />
        )}
        {(data?.messages.length ?? 0) === 100 && (
          <Button
            className="my-3 min-h-11"
            variant="secondary"
            onPress={() => setOffset(offset + 100)}
          >
            {t("Mehr anzeigen")}
          </Button>
        )}
        {data?.questions
          .filter((question) => question.status === "open")
          .map((question) => (
            <AgentQuestion key={question.id} question={question} />
          ))}
        {data?.jobs.some((job) => job.status === "running") && (
          <section className="my-5 border-t border-border pt-4">
            <h2 className="mb-3 font-mono text-xs text-muted">
              {t("Laufende Jobs")}
            </h2>
            <JobsPanel
              jobs={data.jobs.filter((job) => job.status === "running")}
            />
          </section>
        )}
        {connectionChallenge && (
          <div className="my-4">
            <HostKeyTrust
              challenge={connectionChallenge}
              onTrusted={async () => {
                await resume();
              }}
            />
          </div>
        )}
        {data?.goal && current?.role !== "approver" && (
          <AgentGoal
            goal={data.goal}
            working={current?.status === "running"}
            reviewing={data.agentRunState?.phase === "goal_review"}
          />
        )}
        <Failure
          message={
            error ||
            (!connectionChallenge && action.error) ||
            (current?.error !== data?.goal?.reason
              ? current?.error
              : undefined) ||
            undefined
          }
          onRetry={
            error ? () => api.refresh() : current?.error ? resume : undefined
          }
        />
        {current?.status === "stopped" &&
          Boolean(data?.agentInputs?.length) && (
            <Button
              variant="ghost"
              size="sm"
              isPending={action.busy}
              onPress={resume}
            >
              {t("Verarbeitung fortsetzen")}
            </Button>
          )}
        {current?.role !== "approver" && (
          <LiveAssistantResponse
            agentId={agentId}
            messages={visibleMessages}
            working={current?.status === "running"}
            phase={data?.agentRunState?.phase}
          />
        )}
      </div>
      {current?.role !== "approver" && (
        <form
          ref={viewport.composer}
          className="fixed inset-x-0 bottom-0 z-20 bg-surface px-4 pt-4 pb-[max(0.75rem,env(safe-area-inset-bottom))] sm:px-6 lg:left-[var(--sidebar-width,260px)] lg:px-8"
          onSubmit={(event) => {
            event.preventDefault();
            void send();
          }}
        >
          <div className="relative mx-auto max-w-3xl">
            <AgentInbox
              agentId={agentId}
              inputs={data?.agentInputs ?? []}
              onEdit={(text) => setPrompt(prompt ? prompt + "\n" + text : text)}
            />
            {!viewport.following && (
              <Button
                isIconOnly
                size="sm"
                aria-label={t("Zu neuesten Einträgen ↓")}
                className="absolute -top-10 right-1.5 size-8 min-h-8 min-w-8 rounded-full border border-border bg-surface text-muted shadow-none"
                variant="secondary"
                onPress={viewport.latest}
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
                autoFocus={params.get("compose") === "1" && !focusAgentSwitcher}
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
                onPress={() => setDetailsOpen(true)}
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
      )}
      {current?.role === "approver" && (
        <footer className="flex flex-wrap items-center justify-between gap-2 border-t border-border py-3 text-xs text-muted">
          <p>{t("Dieser Verlauf ist schreibgeschützt.")}</p>
          {data && <CostSummary totals={data.usageSummary} />}
        </footer>
      )}
      {renaming && current && (
        <RenameConversation
          agent={current}
          onClose={() => setRenaming(false)}
        />
      )}
      {detailsOpen && (
        <Inspect
          title={t("Agentendetails")}
          onClose={() => setDetailsOpen(false)}
        >
          <div className="space-y-8">
            <section className="space-y-3">
              <h2 className="text-base font-medium">
                {t("Ausführungsumgebung")}
              </h2>
              <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-2 text-sm">
                <dt className="text-muted">{t("Modell")}</dt>
                <dd className="break-all">{current?.modelId || "—"}</dd>
                <dt className="text-muted">{t("Maschine")}</dt>
                <dd className="break-words">
                  {machineName || current?.machineId || "—"}
                </dd>
              </dl>
              <SandboxBadge
                mode={sandboxMode ?? sandboxStatus?.mode ?? "unknown"}
                status={
                  sandboxStatus?.machineId === current?.machineId
                    ? sandboxStatus
                    : undefined
                }
              />
              {data && <CostSummary totals={data.usageSummary} />}
            </section>
            <section>
              <h2 className="mb-4 text-base font-medium">
                {t("Laufende Jobs")}
              </h2>
              <JobsPanel
                jobs={(data?.jobs ?? []).filter(
                  (job) => job.status === "running",
                )}
              />
            </section>
            <section>
              <h2 className="mb-4 text-base font-medium">{t("Kontext")}</h2>
              <ContextPanel current={current} notes={data?.notes ?? []} />
            </section>
          </div>
          {inspectionDialog}
        </Inspect>
      )}
      {!detailsOpen && inspectionDialog}
    </section>
  );
}
