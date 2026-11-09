import { Accordion, Chip } from "@heroui/react";

import {
  ActivityContent,
  ActivityRequest,
  toolLabel,
} from "./activity-details";
import type { Agent, Approval, Note } from "./generated";
import { useI18n } from "./preferences";
import { Empty } from "./ui";

export function ApprovalsPanel({ approvals }: { approvals: Approval[] }) {
  const { t } = useI18n();
  return (
    <div className="space-y-3">
      {!approvals.length ? (
        <Empty>{t("Keine Freigabeanfragen")}</Empty>
      ) : (
        approvals.map((approval) => (
          <section
            key={approval.id}
            className="space-y-3 border-b border-border py-4 first:pt-0"
          >
            <div className="flex flex-wrap items-center justify-between gap-3">
              <span className="min-w-0 break-words text-sm font-medium">
                {t(toolLabel(approval.tool))}
              </span>
              <Chip
                size="sm"
                color={approval.allowed ? "success" : "danger"}
                variant="soft"
              >
                {approval.allowed ? t("Genehmigt") : t("Abgelehnt")}
              </Chip>
            </div>
            {approval.reason && (
              <p className="text-sm text-muted">{approval.reason}</p>
            )}
            {approval.blanket && (
              <span className="text-xs text-muted">
                {t("Pauschale Lesefreigabe angefragt")}
              </span>
            )}
            <Accordion hideSeparator>
              <Accordion.Item id={approval.id}>
                <Accordion.Heading>
                  <Accordion.Trigger className="min-h-11 gap-3 text-left text-xs text-muted">
                    <span className="flex-1">{t("Aktionsdetails")}</span>
                    <Accordion.Indicator />
                  </Accordion.Trigger>
                </Accordion.Heading>
                <Accordion.Panel>
                  <Accordion.Body className="pb-3">
                    <ActivityRequest
                      name={approval.tool}
                      arguments={approval.arguments}
                    />
                  </Accordion.Body>
                </Accordion.Panel>
              </Accordion.Item>
            </Accordion>
          </section>
        ))
      )}
    </div>
  );
}

export { JobsPanel } from "./jobs-panel";

export function ContextPanel({
  current,
  notes,
}: {
  current?: Agent;
  notes: Note[];
}) {
  const { t } = useI18n();
  return (
    <div className="space-y-4">
      {!current?.summary && !notes.length && (
        <Empty>{t("Noch kein gespeicherter Kontext")}</Empty>
      )}
      {current?.summary && (
        <section className="border-b border-border pb-5">
          <h3 className="font-medium mb-3">{t("Verdichteter Kontext")}</h3>
          <ActivityContent content={current.summary} />
        </section>
      )}
      {notes.map((note) => (
        <section key={note.id} className="border-b border-border py-4">
          <h3 className="font-medium">{note.title}</h3>
          <p className="text-sm mt-2 text-muted">{note.content}</p>
        </section>
      ))}
    </div>
  );
}
