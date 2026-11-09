import { Accordion, Chip } from "@heroui/react";
import { useMemo } from "react";

import { useAPI, useQuery } from "./api";
import { parseJSON, toolSummary } from "./agent-activity";
import { ActivityRequest, toolLabel } from "./activity-details";
import { record, structured } from "./activity-values";
import { AgentConversation } from "./agent-conversation";
import type { Approval, Message, GoalReview } from "./generated";
import { goalStatusLabels } from "./goal-format";
import { useI18n } from "./preferences";
import { Empty, Failure } from "./ui";

type Decision = { allowed: boolean; reason?: string; blanket?: boolean };

type Review = {
  id: string;
  request?: Message;
  context: Record<string, unknown>;
  steps: Message[];
  decision?: Decision;
  finishedAt?: string;
};

function decisionData(content: string): Record<string, unknown> {
  const text = content
    .trim()
    .replace(/^```(?:json)?\s*/, "")
    .replace(/\s*```$/, "");
  return record(parseJSON(text));
}

function decisionFrom(content: string): Decision | undefined {
  const value = decisionData(content);
  if (
    typeof value.allowed !== "boolean" ||
    (value.reason !== undefined && typeof value.reason !== "string") ||
    (value.blanket !== undefined && typeof value.blanket !== "boolean")
  ) {
    return;
  }

  return value as Decision;
}

function reviewsFrom(messages: Message[]): Review[] {
  const reviews: Review[] = [];
  let current: Review | undefined;
  for (const message of messages) {
    const context = decisionData(message.content);
    if (message.role === "event" && typeof context.action === "string") {
      current = { id: message.id, request: message, context, steps: [] };
      reviews.push(current);
    } else {
      if (
        current?.context.action === "goal_completion" &&
        message.role === "assistant" &&
        typeof context.status === "string" &&
        ["complete", "continue", "blocked", "paused"].includes(context.status)
      ) {
        current.finishedAt = message.createdAt;
        current = undefined;
        continue;
      }

      const decision =
        message.role === "assistant"
          ? decisionFrom(message.content)
          : undefined;
      if (decision) {
        if (!current) {
          current = { id: message.id, context: {}, steps: [] };
          reviews.push(current);
        }

        current.decision = decision;
        current.finishedAt = message.createdAt;
        current = undefined;
      } else if (current) current.steps.push(message);
    }
  }

  return reviews;
}

function canonical(value: unknown): string {
  function normalize(item: unknown): unknown {
    if (Array.isArray(item)) {
      return item.map(normalize);
    }

    if (item && typeof item === "object") {
      return Object.fromEntries(
        Object.entries(item)
          .sort(([a], [b]) => a.localeCompare(b))
          .map(([key, child]) => [key, normalize(child)]),
      );
    }

    return item;
  }
  return JSON.stringify(normalize(structured(value)));
}

// A model proposal is not an effective permission. Match only decisions actually
// recorded by the harness, including the action, arguments and request interval.
function recordedDecisions(reviews: Review[], approvals: Approval[]) {
  const assigned = new Set<string>();
  const matches = new Map<string, Approval>();
  for (const [index, review] of reviews.entries()) {
    if (!review.request) {
      continue;
    }

    const start = Date.parse(review.request.createdAt);
    const next = reviews.slice(index + 1).find((item) => item.request)?.request;
    const end = next ? Date.parse(next.createdAt) : Infinity;
    const approval = approvals.find(
      (item) =>
        !assigned.has(item.id) &&
        item.tool === review.context.action &&
        Date.parse(item.createdAt) >= start &&
        Date.parse(item.createdAt) <= end &&
        canonical(item.arguments) === canonical(review.context.arguments),
    );
    if (approval) {
      assigned.add(approval.id);
      matches.set(review.id, approval);
    }
  }

  return matches;
}

export function ApproverLog({
  messages,
  parentId,
  working,
  onInspect,
}: {
  messages: Message[];
  parentId: string;
  working: boolean;
  onInspect: (wire: string) => void;
}) {
  const { t, numberLocale } = useI18n();
  const api = useAPI();
  const { data, error } = useQuery<{
    approvals: Approval[];
    goalReviews?: GoalReview[];
  }>(
    "query($id:String!){approvals(agentId:$id,limit:500){id tool arguments allowed reason blanket createdAt} goalReviews(agentId:$id,limit:500){id agentId goalId approverId messageId status reason evidence nextStep blocker createdAt}}",
    { id: parentId },
  );
  const reviews = useMemo(() => reviewsFrom(messages), [messages]);
  const recorded = useMemo(
    () => recordedDecisions(reviews, data?.approvals ?? []),
    [reviews, data],
  );
  if (!reviews.length) {
    return <Empty>{t("Noch keine Aktionen geprüft.")}</Empty>;
  }

  return (
    <div className="space-y-4">
      <Failure message={error} onRetry={() => api.refresh()} />
      <p className="text-sm text-muted">
        {t("{count} Prüfungen im angezeigten Verlauf", {
          count: reviews.length,
        })}
      </p>
      <ol
        className="divide-y divide-separator"
        aria-label={t("Freigabeprüfungen")}
      >
        {reviews.map((review, index) => {
          if (review.context.action === "goal_completion") {
            const audit = data?.goalReviews?.find(
              (item) => item.id === review.id,
            );
            const goal = record(review.context.goal);

            return (
              <li key={review.id} className="min-w-0 space-y-3 py-5 first:pt-0">
                <div className="flex flex-wrap items-center justify-between gap-3">
                  <h3 className="text-sm font-medium">
                    {t("Zielabschluss prüfen")}
                  </h3>
                  <Chip
                    size="sm"
                    variant="soft"
                    color={audit?.status === "complete" ? "success" : "warning"}
                  >
                    {t(
                      audit
                        ? (goalStatusLabels[audit.status] ?? audit.status)
                        : working && index === reviews.length - 1
                          ? "Wird geprüft"
                          : "Keine Abschlussprüfung erfasst",
                    )}
                  </Chip>
                </div>
                {typeof goal.objective === "string" && (
                  <p className="whitespace-pre-wrap break-words text-sm">
                    {goal.objective}
                  </p>
                )}
                {audit?.reason && <p className="text-sm">{audit.reason}</p>}
                {audit?.evidence && (
                  <div className="space-y-1">
                    <p className="text-xs text-muted">{t("Geprüfte Belege")}</p>
                    <p className="whitespace-pre-wrap text-sm">
                      {audit.evidence}
                    </p>
                  </div>
                )}
                {audit?.nextStep && (
                  <p className="whitespace-pre-wrap text-sm">
                    {audit.nextStep}
                  </p>
                )}
                {review.steps.length > 0 && (
                  <Accordion hideSeparator>
                    <Accordion.Item id="goal-inspection">
                      <Accordion.Heading>
                        <Accordion.Trigger className="min-h-11 text-sm">
                          <span className="flex-1 text-left">
                            {t("Prüfschritte")}
                          </span>
                          <Accordion.Indicator />
                        </Accordion.Trigger>
                      </Accordion.Heading>
                      <Accordion.Panel>
                        <Accordion.Body>
                          <AgentConversation
                            messages={review.steps}
                            working={!audit && working}
                            onInspect={onInspect}
                          />
                        </Accordion.Body>
                      </Accordion.Panel>
                    </Accordion.Item>
                  </Accordion>
                )}
              </li>
            );
          }

          const approval = recorded.get(review.id);
          const pending =
            !approval &&
            !review.decision &&
            working &&
            index === reviews.length - 1;
          const action =
            typeof review.context.action === "string"
              ? review.context.action
              : "";
          const args = record(structured(review.context.arguments));
          const summary = toolSummary({ arguments: args });
          const reason =
            approval?.reason || (!approval ? review.decision?.reason : "");
          const time =
            approval?.createdAt ||
            review.finishedAt ||
            review.request?.createdAt;

          return (
            <li key={review.id} className="min-w-0 space-y-3 py-5 first:pt-0">
              <div className="flex flex-wrap items-center justify-between gap-3">
                <h3 className="text-sm font-medium">
                  {action
                    ? t(toolLabel(action))
                    : t("Anfrage aus älterem Verlauf")}
                </h3>
                <div className="flex items-center gap-3">
                  {time && (
                    <time dateTime={time} className="text-xs text-muted">
                      {new Date(time).toLocaleTimeString(numberLocale, {
                        hour: "2-digit",
                        minute: "2-digit",
                      })}
                    </time>
                  )}
                  <Chip
                    size="sm"
                    color={
                      approval
                        ? approval.allowed
                          ? "success"
                          : "danger"
                        : "warning"
                    }
                    variant="soft"
                  >
                    {t(
                      approval
                        ? approval.allowed
                          ? "Genehmigt"
                          : "Abgelehnt"
                        : pending
                          ? "Wird geprüft"
                          : "Keine Freigabe erfasst",
                    )}
                  </Chip>
                </div>
              </div>
              {summary && (
                <code className="block whitespace-pre-wrap break-all text-sm">
                  {summary}
                </code>
              )}
              {reason && (
                <p className="whitespace-pre-wrap break-words text-sm leading-6">
                  {reason}
                </p>
              )}
              {approval?.blanket && (
                <p className="text-xs text-muted">
                  {t("Wiederholtes Lesen erlaubt")}
                </p>
              )}
              <Accordion hideSeparator>
                {review.request && (
                  <Accordion.Item id="request">
                    <Accordion.Heading>
                      <Accordion.Trigger className="min-h-11 text-sm">
                        <span className="flex-1 text-left">
                          {t("Anfrage ansehen")}
                        </span>
                        <Accordion.Indicator />
                      </Accordion.Trigger>
                    </Accordion.Heading>
                    <Accordion.Panel>
                      <Accordion.Body className="pb-3">
                        {typeof review.context.justification === "string" &&
                          review.context.justification && (
                            <p className="mb-3 text-sm text-muted">
                              {review.context.justification}
                            </p>
                          )}
                        <ActivityRequest
                          name={action}
                          arguments={args}
                          network={
                            typeof review.context.network === "boolean"
                              ? review.context.network
                              : undefined
                          }
                        />
                      </Accordion.Body>
                    </Accordion.Panel>
                  </Accordion.Item>
                )}
                {review.steps.length > 0 && (
                  <Accordion.Item id="steps">
                    <Accordion.Heading>
                      <Accordion.Trigger className="min-h-11 text-sm">
                        <span className="flex-1 text-left">
                          {t("Prüfschritte")} ·{" "}
                          {review.steps.filter(
                            (message) => message.role === "tool",
                          ).length || review.steps.length}
                        </span>
                        <Accordion.Indicator />
                      </Accordion.Trigger>
                    </Accordion.Heading>
                    <Accordion.Panel>
                      <Accordion.Body className="pb-3">
                        <AgentConversation
                          messages={review.steps}
                          working={pending}
                          onInspect={onInspect}
                        />
                      </Accordion.Body>
                    </Accordion.Panel>
                  </Accordion.Item>
                )}
              </Accordion>
            </li>
          );
        })}
      </ol>
    </div>
  );
}
