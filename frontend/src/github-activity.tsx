import { useState } from "react";
import { ActivityStatus, record, structured } from "./activity-values";
import { MoreButton, useBatch } from "./batches";
import { useI18n } from "./preferences";

function text(value: unknown) {
  return typeof value === "string" ? value : "";
}
function webURL(value: unknown) {
  if (typeof value !== "string") return "";
  try {
    const url = new URL(value);
    return ["https:", "http:"].includes(url.protocol) ? url.href : "";
  } catch {
    return "";
  }
}

// GitHub envelopes contain repeated repository/owner/link metadata. Summarize
// actual resources instead of recursively turning every API field into a table.
export function GitHubResult({ value }: { value: unknown }) {
  const { t } = useI18n();
  const envelope = record(value);
  const body = structured(envelope.body ?? value);
  const result = record(body);
  const collection = [
    "workflow_runs",
    "jobs",
    "check_runs",
    "pull_requests",
    "issues",
    "items",
    "repositories",
    "commits",
    "releases",
  ]
    .map((key) => result[key])
    .find(Array.isArray);
  const rows: unknown[] = Array.isArray(body)
    ? body
    : Array.isArray(collection)
      ? collection
      : body && typeof body === "object"
        ? [body]
        : [];
  const batch = useBatch(rows);
  const [rawOpen, setRawOpen] = useState(false);
  return (
    <div className="min-w-0 space-y-3" aria-label={t("GitHub-Ergebnis")}>
      <div className="flex flex-wrap items-center gap-3 text-xs text-muted">
        {typeof envelope.status === "number" && (
          <span className="font-mono">HTTP {envelope.status}</span>
        )}
        {(Array.isArray(body) || collection) && (
          <span>
            {typeof result.total_count === "number"
              ? result.total_count
              : rows.length}{" "}
            {t("Einträge")}
          </span>
        )}
      </div>
      {rows.length > 0 && (
        <ul className="space-y-2">
          {batch.items.map((item, index) => {
            const row = record(item);
            const title =
              text(row.display_title) ||
              text(row.title) ||
              text(row.name) ||
              text(row.full_name) ||
              text(row.message) ||
              text(record(row.commit).message) ||
              text(row.sha).slice(0, 12) ||
              t("GitHub-Ergebnis");
            const href = webURL(row.html_url);
            const status =
              text(row.conclusion) || text(row.state) || text(row.status);
            const branch = text(row.head_branch) || text(record(row.head).ref);
            const sha = text(row.head_sha) || text(row.sha);
            const author = text(record(row.user ?? row.actor).login);
            return (
              <li
                key={String(row.id ?? row.sha ?? index)}
                className="min-w-0 rounded-lg border border-border p-3"
              >
                <div className="flex min-w-0 flex-wrap items-start justify-between gap-2">
                  <div className="min-w-0 flex-1 break-words font-medium [overflow-wrap:anywhere]">
                    {typeof row.number === "number" && (
                      <span className="mr-2 text-muted">#{row.number}</span>
                    )}
                    {href ? (
                      <a
                        href={href}
                        target="_blank"
                        rel="noopener noreferrer"
                        className="text-accent underline-offset-2 hover:underline"
                      >
                        {title}
                      </a>
                    ) : (
                      title
                    )}
                  </div>
                  {status && (
                    <ActivityStatus
                      status={
                        status === "success"
                          ? "completed"
                          : status === "failure"
                            ? "failed"
                            : status
                      }
                    />
                  )}
                </div>
                {(branch || sha || author) && (
                  <div className="mt-2 flex min-w-0 flex-wrap gap-x-3 gap-y-1 text-xs text-muted">
                    {branch && <code className="break-all">{branch}</code>}
                    {sha && <code title={sha}>{sha.slice(0, 8)}</code>}
                    {author && <span>@{author}</span>}
                  </div>
                )}
              </li>
            );
          })}
        </ul>
      )}
      <MoreButton batch={batch} />
      {body === "" && (
        <p className="text-sm text-muted">{t("Keine Antwortdaten")}</p>
      )}
      <details onToggle={(event) => setRawOpen(event.currentTarget.open)}>
        <summary className="cursor-pointer text-xs text-muted">
          {t("Vollständige API-Antwort")}
        </summary>
        {rawOpen && (
          <pre className="mt-2 max-h-96 overflow-auto rounded border border-border p-3 text-xs">
            {JSON.stringify(value, null, 2)}
          </pre>
        )}
      </details>
    </div>
  );
}
