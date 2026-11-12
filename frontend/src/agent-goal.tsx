import { Accordion, Button, Card, Chip } from "@heroui/react";

import type { Goal } from "./generated";
import { useAPI } from "./api";
import { goalStatusLabels } from "./goal-format";

// A goal moves from the live footer into the immutable timeline on completion.
// Preserve a reader's disclosure choice across that move and virtualization.
const disclosures = new WeakMap<object, Set<string>>();
import { useI18n } from "./preferences";
import { Failure, useAction } from "./ui";

export function AgentGoal({
  goal,
  working,
  reviewing,
  allowDiscard = true,
  compact = false,
}: {
  goal: Goal;
  working: boolean;
  reviewing: boolean;
  allowDiscard?: boolean;
  compact?: boolean;
}) {
  const { t } = useI18n();
  const api = useAPI();
  if (!disclosures.has(api)) disclosures.set(api, new Set());
  const expanded = disclosures.get(api)!;
  const action = useAction();
  const status =
    reviewing && goal.status === "active" ? "reviewing" : goal.status;
  const terminal = ["complete", "cancelled", "superseded"].includes(
    goal.status,
  );

  return (
    <Card
      variant="secondary"
      className={`p-1 ${
        compact
          ? "mx-auto mt-2 max-h-[35dvh] max-w-[96rem] overflow-y-auto"
          : "my-4"
      }`}
      role="region"
      aria-label={t("Zielstatus")}
    >
      <Card.Content className="py-0">
        <Accordion
          hideSeparator
          defaultExpandedKeys={expanded.has(goal.id) ? [goal.id] : []}
          onExpandedChange={(keys) => {
            if (keys.has(goal.id)) expanded.add(goal.id);
            else expanded.delete(goal.id);
          }}
        >
          <Accordion.Item id={goal.id}>
            <Accordion.Heading>
              <Accordion.Trigger className="min-h-11 min-w-0 gap-3 text-sm">
                <span className="shrink-0 text-muted">{t("Arbeitsziel")}</span>
                <span className="min-w-0 flex-1 truncate text-left font-normal">
                  {goal.objective.slice(0, 180)}
                </span>
                <Chip
                  size="sm"
                  variant="soft"
                  color={
                    status === "complete"
                      ? "success"
                      : status === "blocked"
                        ? "warning"
                        : "default"
                  }
                >
                  {t(goalStatusLabels[status] ?? status)}
                </Chip>
                <Accordion.Indicator />
              </Accordion.Trigger>
            </Accordion.Heading>
            <Accordion.Panel>
              <Accordion.Body className="space-y-3 pb-4">
                <p className="whitespace-pre-wrap break-words text-sm leading-6">
                  {goal.objective}
                </p>
                {goal.reason && goal.status !== "blocked" && (
                  <p className="whitespace-pre-wrap text-sm">
                    {t(goal.reason)}
                  </p>
                )}
                {goal.evidence && (
                  <div className="space-y-1">
                    <p className="text-xs text-muted">{t("Geprüfte Belege")}</p>
                    <p className="whitespace-pre-wrap text-sm">
                      {goal.evidence}
                    </p>
                  </div>
                )}
                {!terminal && (
                  <div className="flex flex-wrap gap-2">
                    {goal.status === "active" && (
                      <Button
                        size="sm"
                        variant="secondary"
                        isPending={action.busy}
                        onPress={() =>
                          action.run(() =>
                            api.mutate(
                              "mutation($agentId:String!){stopAgent(agentId:$agentId)}",
                              { agentId: goal.agentId },
                              { announce: false },
                            ),
                          )
                        }
                      >
                        {t("Ziel pausieren")}
                      </Button>
                    )}
                    {!working && (
                      <Button
                        size="sm"
                        variant="secondary"
                        isPending={action.busy}
                        onPress={() =>
                          action.run(() =>
                            api.mutate(
                              "mutation($agentId:String!){resumeInputs(agentId:$agentId)}",
                              { agentId: goal.agentId },
                              { announce: false },
                            ),
                          )
                        }
                      >
                        {t("Ziel fortsetzen")}
                      </Button>
                    )}
                    {!working && allowDiscard && (
                      <Button
                        size="sm"
                        variant="ghost"
                        isDisabled={action.busy}
                        onPress={() =>
                          action.run(() =>
                            api.mutate(
                              "mutation($agentId:String!,$goalId:String!){cancelGoal(agentId:$agentId,goalId:$goalId)}",
                              { agentId: goal.agentId, goalId: goal.id },
                              { announce: false },
                            ),
                          )
                        }
                      >
                        {t("Ziel verwerfen")}
                      </Button>
                    )}
                  </div>
                )}
                <Failure message={action.error} />
              </Accordion.Body>
            </Accordion.Panel>
          </Accordion.Item>
        </Accordion>
        {goal.status === "blocked" && (
          <p role="status" className="pb-3 text-sm text-muted">
            {goal.reason}
          </p>
        )}
      </Card.Content>
    </Card>
  );
}
