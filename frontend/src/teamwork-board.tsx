import { useQuery } from "./api";
import type { Teamwork } from "./generated";
import { useI18n } from "./preferences";
import { Failure, Inspect, Loading } from "./ui";

const boardQuery = `query($agentId:String!) {
  teamwork(agentId:$agentId) {
    id agentId objective pattern status revision maxWorkers evidence
    tasks { id title objective acceptance kind dependsOn writePaths ownerId status result evidence }
  }
}`;

const statusLabels: Record<string, string> = {
  active: "Aktiv",
  pending: "Offen",
  claimed: "In Arbeit",
  blocked: "Blockiert",
  done: "Abgeschlossen",
  cancelled: "Abgebrochen",
};

// Only the open modal subscribes. The model supplies structured data, never UI
// code or HTML; this native, read-only view owns rendering and interactions.
export function TeamworkBoard({
  agentId,
  onClose,
}: {
  agentId: string;
  onClose: () => void;
}) {
  const { t } = useI18n();
  const { data, loading, error } = useQuery<{ teamwork: Teamwork | null }>(
    boardQuery,
    { agentId },
  );
  const board = data?.teamwork;

  return (
    <Inspect title={t("Teamwork")} onClose={onClose}>
      {loading && <Loading />}
      <Failure message={error} />
      {data && !board && (
        <p>
          {t(
            "Noch kein Teamwork-Board. Mit /teamwork <Aufgabe> kannst du gezielte Zusammenarbeit anfordern. Agenten werden nicht automatisch gestartet.",
          )}
        </p>
      )}
      {board && (
        <section aria-label={t("Teamwork-Board")} className="space-y-4">
          <p className="whitespace-pre-wrap">{board.objective}</p>
          <p className="text-sm text-muted">
            {t(statusLabels[board.status] ?? board.status)} · {board.pattern} ·{" "}
            {t("Worker-Limit")}: {board.maxWorkers}
          </p>
          <p className="text-sm text-muted">
            {t(
              "Deklarierte Schreibbereiche koordinieren die Arbeit; sie sind keine Dateisperren oder Berechtigungen. Ein Board-Abbruch beendet keine laufenden Agenten oder Jobs.",
            )}
          </p>
          <ol className="space-y-3">
            {board.tasks.map((task) => (
              <li key={task.id} className="rounded-lg border border-border p-3">
                <details>
                  <summary className="cursor-pointer">
                    {task.title} · {t(statusLabels[task.status] ?? task.status)}
                    {task.kind === "verify" && ` · ${t("Verifikation")}`}
                  </summary>
                  <dl className="mt-3 space-y-3 whitespace-pre-wrap text-sm">
                    {[
                      ["Aufgabe", task.objective],
                      ["Abnahme", task.acceptance],
                      ["Zuständig", task.ownerId],
                      ["Abhängigkeiten", task.dependsOn.join(", ")],
                      ["Schreibbereiche", task.writePaths.join(", ")],
                      ["Ergebnis", task.result],
                      ["Belege", task.evidence],
                    ].map(([label, value]) =>
                      value ? (
                        <div key={label}>
                          <dt className="font-medium">{t(label)}</dt>
                          <dd className="break-words text-muted">{value}</dd>
                        </div>
                      ) : null,
                    )}
                  </dl>
                </details>
              </li>
            ))}
          </ol>
          {board.evidence && (
            <div className="whitespace-pre-wrap text-sm">
              <h3 className="font-medium">
                {t("Gesamtprüfung / Abbruchgrund")}
              </h3>
              <p>{board.evidence}</p>
            </div>
          )}
        </section>
      )}
    </Inspect>
  );
}
