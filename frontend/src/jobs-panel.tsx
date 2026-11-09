import { Accordion, Button } from "@heroui/react";
import { useAPI } from "./api";
import { ActivityOutput } from "./activity-details";
import { statusLabels } from "./agent-format";
import type { Job } from "./generated";
import { Icon } from "./icons";
import { useI18n } from "./preferences";
import { Empty, Failure, useAction } from "./ui";

function JobRow({ job }: { job: Job }) {
  const { t } = useI18n();
  const api = useAPI();
  const action = useAction();
  const running = job.status === "running";
  return (
    <li className="min-w-0 border-b border-border last:border-0">
      <div className="flex min-w-0 items-start gap-1">
        <Accordion hideSeparator className="min-w-0 flex-1">
          <Accordion.Item id={job.id}>
            <Accordion.Heading>
              <Accordion.Trigger
                aria-label={t("Ausgabe öffnen: {command}", {
                  command: job.command,
                })}
                className="min-h-11 min-w-0 gap-2 py-2 text-left"
              >
                <Icon name="terminal" className="size-4 text-muted" />
                <code
                  className="min-w-0 flex-1 truncate text-xs"
                  title={job.command}
                >
                  {job.command}
                </code>
                <span
                  className={
                    running
                      ? "shrink-0 text-[11px] text-accent"
                      : "shrink-0 text-[11px] text-muted"
                  }
                >
                  {t(
                    running
                      ? "Läuft"
                      : (statusLabels[job.status] ?? job.status),
                  )}
                </span>
                <Accordion.Indicator className="size-3.5 shrink-0 text-muted" />
              </Accordion.Trigger>
            </Accordion.Heading>
            <Accordion.Panel>
              <Accordion.Body className="min-w-0 pb-3 pl-6">
                <p className="mb-2 break-all font-mono text-xs text-muted">
                  {job.command}
                </p>
                {job.error && (
                  <p className="mb-2 break-words text-xs text-danger">
                    {job.error}
                  </p>
                )}
                <ActivityOutput
                  content={job.output || t("Noch keine Ausgabe")}
                />
              </Accordion.Body>
            </Accordion.Panel>
          </Accordion.Item>
        </Accordion>
        {running && (
          <Button
            isIconOnly
            size="sm"
            variant="ghost"
            className="min-h-11 min-w-11 shrink-0 text-muted"
            aria-label={t("Job beenden: {command}", { command: job.command })}
            isPending={action.busy}
            onPress={() =>
              action.run(() =>
                api.mutate(
                  "mutation($id:String!){stopJob(id:$id)}",
                  { id: job.id },
                  { announce: false },
                ),
              )
            }
          >
            <Icon name="stop" className="size-3.5" />
          </Button>
        )}
      </div>
      <Failure message={action.error} />
    </li>
  );
}

export function JobsPanel({ jobs }: { jobs: Job[] }) {
  const { t } = useI18n();
  return !jobs.length ? (
    <Empty>{t("Keine Jobs")}</Empty>
  ) : (
    <ul className="min-w-0" aria-label={t("Jobs")}>
      {jobs.map((job) => (
        <JobRow key={job.id} job={job} />
      ))}
    </ul>
  );
}
