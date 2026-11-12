import { Button } from "@heroui/react";
import { useSyncExternalStore } from "react";

import { useAPI } from "./api";
import type { AgentInput } from "./generated";
import type { OutgoingInput } from "./input-delivery";
import { useI18n } from "./preferences";
import { Failure, useAction } from "./ui";
import { Icon } from "./icons";
import { attachedFiles, type Attachment } from "./attachments";

export function AgentInbox({
  agentId,
  inputs,
  onEdit,
}: {
  agentId: string;
  inputs: AgentInput[];
  onEdit: (text: string, attachments?: Attachment[]) => void;
}) {
  const api = useAPI();
  const { t } = useI18n();
  const action = useAction();
  useSyncExternalStore(api.inputs.subscribe, api.inputs.snapshot);
  const known = new Set(inputs.map((input) => input.id));
  const outgoing = api.inputs
    .forAgent(agentId)
    .filter((input) => !known.has(input.id));
  const waiting = inputs.filter(
    (input) =>
      input.status === "queued" ||
      input.status === "pending" ||
      (["blocked", "paused"].includes(input.status) &&
        input.deliveredAt.startsWith("0001-")),
  );
  const entries: Array<AgentInput | OutgoingInput> = [...waiting, ...outgoing];
  if (!entries.length) {
    return null;
  }

  return (
    <section
      aria-label={t("Nachrichtenwarteschlange")}
      className="mb-3 max-h-40 overflow-y-auto rounded-lg border border-border bg-surface-secondary px-3 py-2"
    >
      <p className="mb-1 text-xs text-muted">
        {t("Wartende Nachrichten")} · {entries.length}
      </p>
      <ol>
        {entries.map((input) => (
          <li key={input.id} className="flex min-w-0 items-center gap-2 py-1">
            <div className="min-w-0 flex-1">
              <p className="truncate text-sm" title={input.text}>
                {input.text}
                {attachedFiles(input).length > 0 && (
                  <span className="ml-2 text-xs text-muted">
                    {attachedFiles(input)
                      .map((file) => file.name)
                      .join(", ")}
                  </span>
                )}
              </p>
              <p role="status" className="text-xs text-muted">
                {t(
                  input.status === "sending"
                    ? "Wird gesendet …"
                    : input.status === "unconfirmed"
                      ? "Übernahme noch nicht bestätigt. Wird erneut versucht."
                      : input.status === "failed"
                        ? "Noch nicht übernommen"
                        : input.status === "queued" || input.mode === "queue"
                          ? "Auf dem Server eingereiht"
                          : "Vom Server übernommen",
                )}
              </p>
            </div>
            {["unconfirmed", "failed"].includes(input.status) && (
              <Button
                size="sm"
                variant="ghost"
                aria-label={t("Nachricht erneut senden")}
                onPress={() => api.inputs.retry(input.id)}
              >
                <Icon name="refresh" className="size-4" />
              </Button>
            )}
            {"deliveredAt" in input && input.status === "queued" && (
              <>
                <Button
                  size="sm"
                  variant="ghost"
                  aria-label={t("Wartende Nachricht bearbeiten")}
                  isDisabled={action.busy}
                  onPress={() =>
                    action.run(async () => {
                      await api.mutate(
                        "mutation($agentId:String!,$id:String!){cancelInput(agentId:$agentId,id:$id)}",
                        { agentId, id: input.id },
                        { announce: false },
                      );
                      onEdit(input.text, attachedFiles(input));
                    })
                  }
                >
                  <Icon name="compose" className="size-4" />
                </Button>
                <Button
                  size="sm"
                  variant="ghost"
                  aria-label={t("Wartende Nachricht entfernen")}
                  isDisabled={action.busy}
                  onPress={() =>
                    action.run(() =>
                      api.mutate(
                        "mutation($agentId:String!,$id:String!){cancelInput(agentId:$agentId,id:$id)}",
                        { agentId, id: input.id },
                        { announce: false },
                      ),
                    )
                  }
                >
                  <Icon name="error" className="size-4" />
                </Button>
              </>
            )}
          </li>
        ))}
      </ol>
      {outgoing
        .filter((input) => input.status === "failed")
        .map((input) => (
          <Failure key={input.id} message={input.error} />
        ))}
      <Failure message={action.error} />
    </section>
  );
}
