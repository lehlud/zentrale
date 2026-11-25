import { Button } from "@heroui/react";
import { useCallback, useEffect, useState } from "react";

import { useAPI, useQuery } from "./api";
import { CostSummary } from "./usage-summary";
import { AgentQuestion } from "./agent-question";
import { ModelResponse } from "./model-response";
import { ActivityOutput } from "./activity-details";
import {
  shortChatTranscriptQuery,
  transcriptDetailsQuery,
  type Transcript,
} from "./agent-transcript";
import type { Agent, Project, SandboxStatus } from "./generated";
import { useMemo } from "react";
import { useTranscriptHistory } from "./transcript-history";
import { HostKeyTrust } from "./ssh-trust";
import { useI18n } from "./preferences";
import { Failure, Inspect, Loading, useAction } from "./ui";
import { JobsPanel, ContextPanel } from "./agent-panels";
import { AgentControls } from "./agent-controls";
import { AgentConversation } from "./agent-conversation";
import { ApproverLog } from "./approver-log";
import { useTranscriptViewport } from "./transcript-viewport";
import { SandboxBadge } from "./sandbox-badge";
import { useRoute } from "./navigation";
import { LiveAssistantResponse } from "./live-assistant-response";
import { AgentComposer } from "./agent-composer";
import { useStableValue } from "./stable-data";
import { AgentGoal } from "./agent-goal";
import { emptyUsage } from "./conversation-bootstrap";
import { SidebarToggle } from "./shell";

export function AgentView({
  agentId,
  project,
  onProjectDialog,
  agents,
  onSelect,
  sandboxStatus,
  sandboxMode,
  machineName,
  focusAgentSwitcher,
  onAgentSwitcherFocus,
  initialTranscript,
  managedTranscript = false,
  readOnly = false,
}: {
  agentId: string;
  project?: Project;
  onProjectDialog?: (view: string) => void;
  agents?: Agent[];
  onSelect?: (id: string) => void;
  sandboxStatus?: SandboxStatus;
  sandboxMode?: string;
  machineName?: string;
  focusAgentSwitcher?: boolean;
  onAgentSwitcherFocus?: () => void;
  initialTranscript?: Transcript;
  managedTranscript?: boolean;
  readOnly?: boolean;
}) {
  const { t } = useI18n();

  const api = useAPI();
  const { params } = useRoute();
  const [detailsOpen, setDetailsOpen] = useState(false);
  const {
    data: core,
    error,
    loading,
  } = useQuery<Transcript>(
    shortChatTranscriptQuery,
    {
      id: agentId,
    },
    { enabled: !managedTranscript, initialData: initialTranscript },
  );
  const details = useQuery<
    Pick<Transcript, "jobs" | "approvals" | "notes" | "usageSummary"> & {
      agent: Pick<Agent, "id" | "summary"> | null;
    }
  >(transcriptDetailsQuery, { id: agentId }, { enabled: detailsOpen });
  const data = useMemo(
    () =>
      core
        ? {
            ...core,
            approvals: core.approvals ?? [],
            notes: core.notes ?? [],
            usageSummary: core.usageSummary ?? emptyUsage,
            ...details.data,
            agent: { ...core.agent, ...details.data?.agent },
          }
        : undefined,
    [core, details.data],
  );

  const [inspection, setInspection] = useState<{
    title: string;
    content: string;
    kind?: "model";
  } | null>(null);
  const action = useAction();
  const connectionChallenge = data?.agentRunState?.hostKey.publicKey
    ? data.agentRunState.hostKey
    : undefined;
  const current = useStableValue(
    data?.agent ?? agents?.find((agent) => agent.id === agentId),
  );
  const agentError = current?.error;
  const agentFailure =
    agentError !== data?.goal?.reason &&
    agentError !== "service shut down" &&
    agentError !== "service restarted"
      ? agentError
      : undefined;
  const canContinue =
    current?.role === "main" &&
    ((current.status === "interrupted" && !agentFailure) ||
      (current.status === "stopped" && Boolean(data?.agentInputs?.length)));
  const inputs = useStableValue(data?.agentInputs ?? []);
  const viewport = useTranscriptViewport(
    detailsOpen || Boolean(inspection),
    Boolean(data),
  );
  const incoming = useMemo(
    () =>
      data ? [...data.messages, ...(data.recentMessages ?? [])] : undefined,
    [data],
  );
  const history = useTranscriptHistory(agentId, incoming, viewport.following);
  const visibleMessages = history.messages;

  useEffect(() => {
    if (!data) {
      return;
    }
    api.inputs.acknowledge(
      new Set([
        ...(data.agentInputs ?? []).map((input) => input.id),
        ...data.messages.map((message) => message.id),
        ...(data.recentMessages ?? []).map((message) => message.id),
      ]),
    );
  }, [api, data]);

  const openDetails = useCallback(() => setDetailsOpen(true), []);
  const detailsTitle = t("Schrittdetails");
  const inspectModel = useCallback(
    (content: string) =>
      setInspection({ title: detailsTitle, content, kind: "model" }),
    [detailsTitle],
  );
  const resume = () =>
    action.run(
      () =>
        api.mutate(
          "mutation($agentId:String!){resumeInputs(agentId:$agentId)}",
          { agentId },
          { announce: false },
        ),
      "default",
      { announce: false },
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
      {project &&
      onProjectDialog &&
      agents &&
      onSelect &&
      onAgentSwitcherFocus ? (
        <AgentControls
          project={project}
          onProjectDialog={onProjectDialog}
          headerRef={viewport.header}
          focusAgentSwitcher={Boolean(focusAgentSwitcher)}
          onAgentSwitcherFocus={onAgentSwitcherFocus}
          current={current}
          agents={agents}
          onSelect={onSelect}
          action={action}
        >
          {data?.goal &&
            !["complete", "cancelled", "superseded"].includes(
              data.goal.status,
            ) &&
            current?.role !== "approver" && (
              <AgentGoal
                compact
                goal={data.goal}
                working={current?.status === "running"}
                reviewing={data.agentRunState?.phase === "goal_review"}
                allowDiscard={current?.role === "main"}
              />
            )}
        </AgentControls>
      ) : (
        <header
          ref={viewport.header}
          className="sticky top-0 z-10 flex min-h-16 items-center gap-3 border-b border-border bg-surface"
        >
          <SidebarToggle iconOnly />
          <h1 className="min-w-0 flex-1 truncate text-lg font-medium">
            {current?.name || t("Kurzchat")}
          </h1>
          {current?.status === "running" && (
            <Button
              variant="ghost"
              onPress={() =>
                action.run(() =>
                  api.mutate(
                    "mutation($agentId:String!){stopAgent(agentId:$agentId)}",
                    { agentId },
                    { announce: false },
                  ),
                )
              }
            >
              {t("Stoppen")}
            </Button>
          )}
        </header>
      )}
      <div
        role="region"
        aria-label={t("Agentenverlauf")}
        className="mx-auto min-w-0 max-w-[96rem] py-6 sm:py-8"
      >
        <div ref={history.sentinel} aria-hidden="true" className="h-px" />
        {history.loading && <Loading />}
        <Failure message={history.error} onRetry={history.retry} />
        {loading && !data ? (
          <Loading />
        ) : current?.role === "approver" ? (
          <ApproverLog
            messages={visibleMessages}
            parentId={current.parentId}
            working={current.status === "running"}
            onInspect={inspectModel}
          />
        ) : !visibleMessages.length && !error ? (
          <div className="flex min-h-[calc(100dvh-19rem)] flex-col items-center justify-center gap-3 px-3 py-8 text-center">
            <h2 className="text-2xl font-medium tracking-tight">
              {t(
                project
                  ? "Woran möchtest du arbeiten?"
                  : "Was möchtest du wissen?",
              )}
            </h2>
            <p className="max-w-md text-sm leading-7 text-muted">
              {t(
                project
                  ? "Beschreibe dein Ziel. Der Agent kümmert sich um die nächsten Schritte."
                  : "Stell eine Frage oder füge eine Datei hinzu.",
              )}
            </p>
          </div>
        ) : (
          <AgentConversation
            messages={visibleMessages}
            projectDirectory={project?.directory}
            projectId={project?.id}
            working={current?.status === "running"}
            onInspect={inspectModel}
          />
        )}
        {data?.questions
          .filter(
            (question) =>
              question.status === "open" && current?.role === "main",
          )
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
        <Failure
          message={
            error ||
            (!connectionChallenge && action.error) ||
            agentFailure ||
            undefined
          }
          onRetry={
            error
              ? () => api.refresh()
              : agentFailure && current?.role !== "approver"
                ? resume
                : undefined
          }
        />
        {canContinue && (
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
            repositoryStep={data?.agentRunState?.repositoryStep}
            repositoryPercent={data?.agentRunState?.repositoryPercent}
            repositoryDetail={data?.agentRunState?.repositoryDetail}
          />
        )}
      </div>
      {!readOnly && current?.role === "main" && (
        <AgentComposer
          key={agentId}
          agentId={agentId}
          current={current}
          inputs={inputs}
          following={viewport.following}
          latest={viewport.latest}
          composerRef={viewport.composer}
          autoFocus={params.get("compose") === "1" && !focusAgentSwitcher}
          onDetails={openDetails}
          shortChat={!project}
          sandboxStatus={sandboxStatus}
          sandboxMode={sandboxMode}
        />
      )}
      {(readOnly || current?.role !== "main") && (
        <footer
          ref={viewport.readonlyBar}
          className="fixed inset-x-0 bottom-0 z-20 flex flex-wrap items-center justify-between gap-2 border-t border-border bg-surface px-4 pt-3 pb-[max(0.75rem,env(safe-area-inset-bottom))] text-xs text-muted sm:px-6 lg:left-[var(--sidebar-width,260px)] lg:px-8"
        >
          <p>{t("Dieser Verlauf ist schreibgeschützt.")}</p>
          {!readOnly && current?.role === "subagent" && (
            <Button
              size="sm"
              variant="secondary"
              isPending={action.busy}
              onPress={
                current.status === "running"
                  ? () =>
                      action.run(() =>
                        api.mutate(
                          "mutation($agentId:String!){stopAgent(agentId:$agentId)}",
                          { agentId },
                          { announce: false },
                        ),
                      )
                  : resume
              }
            >
              {t(
                current.status === "running"
                  ? "Agent stoppen"
                  : "Agent fortsetzen",
              )}
            </Button>
          )}
          {data && details.data && (
            <CostSummary totals={data.usageSummary} agentId={agentId} />
          )}
        </footer>
      )}
      {detailsOpen && (
        <Inspect
          title={t("Agentendetails")}
          onClose={() => setDetailsOpen(false)}
        >
          <div className="space-y-8">
            {details.loading && <Loading />}
            <Failure message={details.error} onRetry={() => api.refresh()} />
            <section className="space-y-3">
              <h2 className="text-base font-medium">
                {t("Ausführungsumgebung")}
              </h2>
              <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-2 text-sm">
                <dt className="text-muted">{t("Modell")}</dt>
                <dd className="break-all">{current?.modelId || "—"}</dd>
                {project && (
                  <>
                    <dt className="text-muted">{t("Maschine")}</dt>
                    <dd className="break-words">
                      {machineName || current?.machineId || "—"}
                    </dd>
                  </>
                )}
              </dl>
              {project && (
                <SandboxBadge
                  mode={sandboxMode ?? sandboxStatus?.mode ?? "unknown"}
                  status={
                    sandboxStatus?.machineId === current?.machineId
                      ? sandboxStatus
                      : undefined
                  }
                />
              )}
              {data && details.data && (
                <CostSummary totals={data.usageSummary} agentId={agentId} />
              )}
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
