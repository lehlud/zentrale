import { Button } from "@heroui/react";
import { useCallback, useEffect, useState } from "react";

import { useAPI, useQuery } from "./api";
import { CostSummary } from "./usage-summary";
import { AgentQuestion } from "./agent-question";
import { ModelResponse } from "./model-response";
import { ActivityOutput } from "./activity-details";
import { transcriptQuery, type Transcript } from "./agent-transcript";
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
import { RenameConversation } from "./rename-conversation";
import { LiveAssistantResponse } from "./live-assistant-response";
import { AgentComposer } from "./agent-composer";
import { useStableValue } from "./stable-data";
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
  const [detailsOpen, setDetailsOpen] = useState(false);
  const { data, error, loading } = useQuery<Transcript>(transcriptQuery, {
    id: agentId,
  });

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
    data?.agent ?? agents.find((agent) => agent.id === agentId),
  );
  const inputs = useStableValue(data?.agentInputs ?? []);
  const viewport = useTranscriptViewport(
    detailsOpen || renaming || Boolean(inspection),
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
            projectDirectory={project.directory}
            projectId={project.id}
            working={current?.status === "running"}
            onInspect={inspectModel}
          />
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
        {data?.goal &&
          !["complete", "cancelled", "superseded"].includes(data.goal.status) &&
          current?.role !== "approver" && (
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
          sandboxStatus={sandboxStatus}
          sandboxMode={sandboxMode}
        />
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
