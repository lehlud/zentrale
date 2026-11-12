import { Accordion, Button } from "@heroui/react";
import { memo, useMemo } from "react";

import {
  parseJSON,
  correlateToolCalls,
  toolOutcome,
  toolSummary,
  type ToolCall,
} from "./agent-activity";
import { ActivityRequest, ActivityResult, toolLabel } from "./activity-details";
import {
  ActivityFields,
  ActivityValue,
  record,
  structured,
} from "./activity-values";
import type { Goal, Message, ToolActivity } from "./generated";
import { AgentGoal } from "./agent-goal";
import {
  ProjectDirectory,
  ProjectIdentifier,
  relativeProjectPath,
  useProjectPath,
} from "./project-path";
import { VirtualTranscriptItem } from "./virtual-transcript-item";
import { FileDiff } from "./file-diff";
import { MessageMarkdown } from "./message-markdown";
import { useI18n } from "./preferences";
import { Icon } from "./icons";
import { QuestionExchange } from "./question-exchange";
import { AttachmentList } from "./attachment-list";
import { attachedFiles } from "./attachments";

const MessageTime = memo(function MessageTime({
  message,
}: {
  message: Message;
}) {
  const { numberLocale } = useI18n();
  return (
    <time
      dateTime={message.createdAt}
      className="shrink-0 font-mono text-[11px] text-muted"
    >
      {new Date(message.createdAt).toLocaleTimeString(numberLocale, {
        hour: "2-digit",
        minute: "2-digit",
      })}
    </time>
  );
});

const ToolEntry = memo(function ToolEntry({
  message,
  call,
  onInspect,
  hasParentChanges = false,
}: {
  message: Pick<
    Message,
    "id" | "toolName" | "content" | "change" | "changes" | "activities"
  >;
  call?: ToolCall;
  onInspect: (wire: string) => void;
  hasParentChanges?: boolean;
}) {
  const { t, showDiagnostics } = useI18n();
  const projectPath = useProjectPath();
  const outcome = toolOutcome(message.content);
  const resultRecord = record(outcome.result);

  const hasJob = Boolean(
    resultRecord.jobId || resultRecord.id || record(resultRecord.job).id,
  );
  const pending =
    !outcome.error &&
    !hasJob &&
    ["running", "waiting"].includes(outcome.status);

  const denied = outcome.error.startsWith("auto-approval denied:");
  const errorMessage = denied
    ? outcome.error.slice("auto-approval denied:".length).trim()
    : outcome.error.startsWith("MCP tool reported an error:")
      ? outcome.error.slice("MCP tool reported an error:".length).trim()
      : outcome.error;

  const job = record(resultRecord.job ?? resultRecord);
  const processOutput = typeof job.output === "string" ? job.output.trim() : "";
  // Process failures often repeat stdout/stderr inside their error string.
  // Keep the short failure visible; the complete log remains in the disclosure.
  const errorSummary = processOutput
    ? errorMessage.split("\n")[0]
    : errorMessage;

  const changes = message.changes?.length
    ? message.changes
    : message.change
      ? [message.change]
      : [];

  return (
    <li className="my-1 min-w-0 border-l border-border pl-3 sm:pl-4">
      <Accordion hideSeparator className="min-w-0">
        <Accordion.Item id={message.id}>
          <Accordion.Heading>
            <Accordion.Trigger className="min-h-11 min-w-0 gap-2 py-1 font-mono text-xs">
              <span
                aria-hidden="true"
                className={
                  outcome.error
                    ? "text-danger"
                    : pending
                      ? "text-accent"
                      : "text-success-soft-foreground"
                }
              >
                <Icon
                  name={outcome.error ? "error" : pending ? "working" : "check"}
                  className="size-3.5"
                />
              </span>
              <span className="shrink-0 font-medium text-accent">
                {t(call ? toolLabel(call.name, call.arguments) : "Werkzeug")}
              </span>
              <span className="min-w-0 flex-1 truncate text-left font-normal text-muted">
                {projectPath(toolSummary(call))}
              </span>
              <span
                className={
                  outcome.error
                    ? "shrink-0 text-danger"
                    : pending
                      ? "shrink-0 text-accent"
                      : "sr-only"
                }
              >
                {t(
                  denied
                    ? "Abgelehnt"
                    : outcome.error
                      ? "Fehlgeschlagen"
                      : pending
                        ? "Arbeitet"
                        : call &&
                            ["create_goal", "get_goal", "update_goal"].includes(
                              call.name,
                            )
                          ? "Ausgeführt"
                          : "Abgeschlossen",
                )}
              </span>
              <Accordion.Indicator className="shrink-0 text-muted" />
            </Accordion.Trigger>
          </Accordion.Heading>
          <Accordion.Panel>
            <Accordion.Body className="space-y-3 pb-3 pl-5">
              {call &&
                !(
                  call.name === "create_goal" &&
                  typeof record(record(outcome.result).goal ?? outcome.result)
                    .objective === "string"
                ) && (
                  <ActivityRequest
                    name={call.name}
                    arguments={call.arguments}
                    network={call.network}
                    justification={call.justification}
                    hasChanges={changes.length > 0 || hasParentChanges}
                  />
                )}
              <ActivityResult
                name={call?.name || message.toolName}
                value={outcome.result}
                failed={Boolean(outcome.error)}
              />
              {outcome.error && (
                <pre className="min-w-0 whitespace-pre-wrap break-words font-mono text-xs leading-6 text-danger [overflow-wrap:anywhere]">
                  {t(errorSummary)}
                </pre>
              )}
              {message.activities?.length > 0 && (
                <CodeModeActivities
                  activities={message.activities}
                  hasChanges={changes.length > 0}
                  parentId={message.id}
                  onInspect={onInspect}
                />
              )}
              {(call?.name === "apply_patch" || call?.name === "code_mode") &&
                changes.map((change) => (
                  <FileDiff key={change.id} change={change} />
                ))}
              {showDiagnostics && call?.wire && (
                <Button
                  className="min-h-11"
                  size="sm"
                  variant="ghost"
                  onPress={() => onInspect(call.wire)}
                >
                  {t("Schrittdetails")}
                </Button>
              )}
            </Accordion.Body>
          </Accordion.Panel>
        </Accordion.Item>
      </Accordion>
      {call?.name !== "apply_patch" &&
        call?.name !== "code_mode" &&
        changes.map((change) => <FileDiff key={change.id} change={change} />)}
    </li>
  );
});

function CodeModeActivities({
  activities,
  hasChanges,
  parentId,
  onInspect,
}: {
  activities: ToolActivity[];
  hasChanges: boolean;
  parentId: string;
  onInspect: (wire: string) => void;
}) {
  const { t } = useI18n();
  return (
    <ol className="space-y-3" aria-label={t("Ausgeführte Werkzeuge")}>
      {activities.map((activity, index) => {
        const id = `${parentId}-step-${index}`;
        return (
          <ToolEntry
            key={id}
            message={{
              id,
              toolName: activity.name,
              content: activity.content,
              change: null,
              changes: [],
              activities: [],
            }}
            call={{
              id,
              name: activity.name,
              providerTool: activity.name,
              arguments: parseJSON(activity.arguments),
              wire: "",
              network: activity.network,
              justification: activity.justification,
            }}
            hasParentChanges={hasChanges}
            onInspect={onInspect}
          />
        );
      })}
    </ol>
  );
}

const MessageContent = memo(function MessageContent({
  message,
}: {
  message: Message;
}) {
  const { t } = useI18n();
  if (message.role === "event") {
    const feedback = record(parseJSON(message.content));
    if (
      feedback.kind === "subagent_question" ||
      feedback.kind === "parent_question_answer"
    ) {
      return <QuestionExchange value={feedback} />;
    }
    if (feedback.kind === "goal_feedback") {
      return (
        <div className="space-y-2 text-sm leading-6">
          <p className="font-medium">
            {t("Der Auto-Approver hat noch offene Arbeit festgestellt.")}
          </p>
          {typeof feedback.reason === "string" && <p>{feedback.reason}</p>}
          {typeof feedback.nextStep === "string" && (
            <p className="text-muted">{feedback.nextStep}</p>
          )}
        </div>
      );
    }
  }

  if (message.role === "user") {
    return (
      <>
        <p className="whitespace-pre-wrap break-words leading-7 [overflow-wrap:anywhere]">
          {message.content}
        </p>
        <AttachmentList
          attachments={attachedFiles(message)}
          agentId={message.agentId}
        />
      </>
    );
  }

  const value = parseJSON(message.content);
  if (
    value &&
    typeof value === "object" &&
    "allowed" in value &&
    typeof value.allowed === "boolean"
  ) {
    if (
      ("reason" in value && typeof value.reason !== "string") ||
      ("blanket" in value && typeof value.blanket !== "boolean")
    ) {
      return (
        <p className="text-sm text-muted">
          {t("Keine gültige Prüfentscheidung erhalten.")}
        </p>
      );
    }
    return (
      <p className="text-sm leading-7">
        {t(value.allowed ? "Genehmigt" : "Abgelehnt")}
        {"reason" in value && typeof value.reason === "string"
          ? ` · ${value.reason}`
          : ""}
      </p>
    );
  }

  if (
    value &&
    typeof value === "object" &&
    "action" in value &&
    typeof value.action === "string"
  ) {
    const request = record(value);
    return (
      <Accordion hideSeparator className="min-w-0">
        <Accordion.Item id="request">
          <Accordion.Heading>
            <Accordion.Trigger className="min-h-11 gap-3 text-left text-sm">
              <span className="min-w-0 flex-1">
                {t(toolLabel(String(request.action)))}
              </span>
              <Accordion.Indicator />
            </Accordion.Trigger>
          </Accordion.Heading>
          <Accordion.Panel>
            <Accordion.Body className="space-y-4 pb-3">
              <ActivityRequest
                name={String(request.action)}
                arguments={structured(request.arguments)}
                network={
                  typeof request.network === "boolean"
                    ? request.network
                    : undefined
                }
                justification={
                  typeof request.justification === "string"
                    ? request.justification
                    : undefined
                }
              />
              <ActivityFields
                value={request}
                omit={[
                  "action",
                  "arguments",
                  "network",
                  "justification",
                  "mainAgentId",
                ]}
              />
            </Accordion.Body>
          </Accordion.Panel>
        </Accordion.Item>
      </Accordion>
    );
  }

  if (value !== null && typeof value === "object") {
    return <ActivityValue value={value} />;
  }

  return <MessageMarkdown content={message.content} />;
});

export const AgentConversation = memo(function AgentConversation({
  messages,
  working,
  onInspect,
  projectDirectory = "",
  projectId = "",
}: {
  messages: Message[];
  working: boolean;
  onInspect: (wire: string) => void;
  projectDirectory?: string;
  projectId?: string;
}) {
  const { t, showDiagnostics } = useI18n();
  const { callsByMessage, resultCalls, completed } = useMemo(
    () => correlateToolCalls(messages),
    [messages],
  );
  const timeline = useMemo(
    () =>
      [
        ...messages
          .filter((message) => {
            if (message.content || message.role === "tool") return true;
            const calls = callsByMessage.get(message.id) ?? [];
            return (
              calls.some((call) => !completed.has(call)) ||
              (!calls.length && Boolean(message.wire) && showDiagnostics)
            );
          })
          .map((message) => ({
            message,
            goal:
              message.role === "goal"
                ? (JSON.parse(message.content) as Goal)
                : undefined,
            time: message.createdAt,
            id: message.id,
          })),
      ].sort(
        (left, right) =>
          left.time.localeCompare(right.time) ||
          left.id.localeCompare(right.id),
      ),
    [messages, callsByMessage, completed, showDiagnostics],
  );

  return (
    <ProjectDirectory.Provider value={projectDirectory}>
      <ProjectIdentifier.Provider value={projectId}>
        <ol
          className="min-w-0"
          aria-label={t("Nachrichten und Werkzeugaktivität")}
        >
          {timeline.map((entry) => (
            <VirtualTranscriptItem
              key={entry.id}
              estimate={entry.message?.role === "tool" ? 48 : 140}
            >
              {() => {
                if (entry.goal)
                  return (
                    <li key={`goal-${entry.id}`} data-goal-id={entry.goal.id}>
                      <AgentGoal
                        goal={entry.goal}
                        working={false}
                        reviewing={false}
                      />
                    </li>
                  );
                const message = entry.message!;
                const calls = callsByMessage.get(message.id) ?? [];
                if (message.role === "tool") {
                  return (
                    <ToolEntry
                      key={message.id}
                      message={message}
                      call={resultCalls.get(message.id)}
                      onInspect={onInspect}
                    />
                  );
                }

                if (!message.content) {
                  const pending = calls.filter((call) => !completed.has(call));
                  if (pending.length) {
                    return (
                      <li
                        key={message.id}
                        className="space-y-1 border-l border-border py-2 pl-4 font-mono text-xs"
                      >
                        {pending.map((call, index) => (
                          <p
                            key={call.id || index}
                            className="flex min-h-8 items-center gap-2"
                          >
                            <span aria-hidden="true" className="text-accent">
                              ›
                            </span>
                            <span className="text-accent">
                              {t(toolLabel(call.name, call.arguments))}
                            </span>
                            <span className="min-w-0 flex-1 truncate text-muted">
                              {relativeProjectPath(
                                toolSummary(call),
                                projectDirectory,
                                projectId,
                              )}
                            </span>
                            <span className="text-muted">
                              {t(working ? "Arbeitet" : "Werkzeugaufruf")}
                            </span>
                          </p>
                        ))}
                        {showDiagnostics && (
                          <Button
                            className="min-h-11"
                            size="sm"
                            variant="ghost"
                            onPress={() => onInspect(message.wire)}
                          >
                            {t("Schrittdetails")}
                          </Button>
                        )}
                      </li>
                    );
                  }

                  if (calls.length || !message.wire || !showDiagnostics) {
                    return null;
                  }

                  return (
                    <li key={message.id}>
                      <Button
                        className="min-h-11 font-mono text-xs"
                        size="sm"
                        variant="ghost"
                        onPress={() => onInspect(message.wire)}
                      >
                        {t("Schrittdetails")}
                      </Button>
                    </li>
                  );
                }

                const user = message.role === "user";

                return (
                  <li
                    key={message.id}
                    data-message-id={message.id}
                    className={
                      user
                        ? "my-6 ml-auto w-fit min-w-0 max-w-[90%] rounded-3xl bg-surface-secondary px-5 py-3 first:mt-0"
                        : "min-w-0 py-5"
                    }
                  >
                    <div
                      className={
                        message.role === "event"
                          ? "mb-2 text-xs text-muted"
                          : "sr-only"
                      }
                    >
                      <span>
                        {t(
                          user
                            ? "Du"
                            : message.role === "event"
                              ? "System"
                              : "Agent",
                        )}
                      </span>
                      <span className="sr-only">
                        <MessageTime message={message} />
                      </span>
                    </div>
                    <MessageContent message={message} />
                    {showDiagnostics && !user && message.wire && (
                      <Button
                        className="mt-1 min-h-11 text-xs text-muted"
                        size="sm"
                        variant="ghost"
                        aria-label={t("Schrittdetails")}
                        onPress={() => onInspect(message.wire)}
                      >
                        {t("Schrittdetails")}
                      </Button>
                    )}
                  </li>
                );
              }}
            </VirtualTranscriptItem>
          ))}
        </ol>
      </ProjectIdentifier.Provider>
    </ProjectDirectory.Provider>
  );
});
