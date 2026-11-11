import { Chip } from "@heroui/react";

import { MoreButton, useBatch } from "./batches";
import { CodeSnippet } from "./code-snippet";
import {
  ActivityFields,
  ActivityStatus,
  ActivityValue,
  record,
  structured,
} from "./activity-values";
import { useI18n } from "./preferences";
import { MessageMarkdown } from "./message-markdown";
import { MCPRequest, MCPResult } from "./mcp-activity";
import { goalStatusLabels } from "./goal-format";

const toolLabels: Record<string, string> = {
  exec: "Befehl ausführen",
  write_stdin: "Prozess fortsetzen",
  python: "Python ausführen",
  read_file: "Datei lesen",
  write_file: "Datei schreiben",
  apply_patch: "Dateien ändern",
  code_mode: "Werkzeuge kombinieren",
  tool_search: "Werkzeug suchen",
  create_goal: "Ziel setzen",
  get_goal: "Ziel ansehen",
  update_goal: "Zielstatus anfordern",
  goal_completion: "Zielabschluss prüfen",
  mcp_connect: "MCP-Server verbinden",
  mcp_list: "MCP-Verbindungen prüfen",
  mcp_tools: "MCP-Werkzeuge entdecken",
  mcp_call: "MCP-Werkzeug ausführen",
  mcp_disconnect: "MCP-Verbindung beenden",
  instructions_read: "Originalanweisungen lesen",
  update_plan: "Plan aktualisieren",
  delete_file: "Datei entfernen",
  list_files: "Dateien auflisten",
  packages_install: "Pakete installieren",
  microvm_info: "MicroVM prüfen",
  microvm_reset: "MicroVM zurücksetzen",
  venv_create: "Python-Umgebung erstellen",
  venv_cleanup: "Python-Umgebung entfernen",
  container: "Container ausführen",
  websearch: "Im Web suchen",
  webfetch: "Webseite abrufen",
  github: "GitHub-Anfrage",
  clone_source: "Quelle klonen",
  source_git: "Git ausführen",
  machine_list: "Maschinen auflisten",
  machine_select: "Maschine auswählen",
  resource_list: "Rechenressourcen prüfen",
  storage_list: "Projektspeicher prüfen",
  storage_quota: "Speicherquota abfragen",
  note_save: "Notiz speichern",
  note_list: "Notizen auflisten",
  note_read: "Notiz lesen",
  note_delete: "Notiz entfernen",
  context_clear: "Kontext leeren",
  context_rotate: "Kontext verschieben",
  context_compact: "Kontext verdichten",
  subagent: "Unteragent starten",
  steer_agent: "Unteragent anweisen",
  inspect_agent: "Unteragent prüfen",
  instructions: "Nutzeranweisungen lesen",
  history: "Verlauf lesen",
  grants: "Lesefreigaben prüfen",
  tool_output: "Weitere Ausgabe lesen",
  question_ask: "Rückfrage stellen",
  question_list: "Rückfragen auflisten",
  question_withdraw: "Rückfrage zurückziehen",
  job_status: "Job prüfen",
  job_logs: "Job-Ausgabe lesen",
  job_wait: "Auf Job warten",
  job_stop: "Job beenden",
};

export function toolLabel(name: string, input?: unknown) {
  if (name === "write_stdin" && input !== undefined) {
    const args = record(structured(input));
    if (!args.chars && !args.eof) {
      return "Prozessausgabe abrufen";
    }
  }
  return toolLabels[name] ?? name.replaceAll("_", " ");
}

export function ActivityRequest({
  name,
  arguments: input,
  network,
  justification,
  hasChanges = false,
}: {
  name: string;
  arguments: unknown;
  network?: boolean;
  justification?: string;
  hasChanges?: boolean;
}) {
  const { t, showDiagnostics } = useI18n();
  const decoded = structured(input);
  const args = record(decoded);
  if (name === "tool_search" || name === "job_stop") {
    return (
      <div className="min-w-0 space-y-2 text-sm">
        {name === "tool_search" ? (
          <ActivityFields value={{ query: args.query }} />
        ) : (
          <dl>
            <dt className="text-xs text-muted">{t("Job")}</dt>
            <dd className="break-all font-mono">{String(args.id ?? "")}</dd>
          </dl>
        )}
        {justification && <p className="text-muted">{justification}</p>}
      </div>
    );
  }
  if (["create_goal", "get_goal", "update_goal"].includes(name)) {
    return (
      <div className="space-y-2 text-sm">
        {typeof args.objective === "string" && (
          <p className="whitespace-pre-wrap break-words">{args.objective}</p>
        )}
        {typeof args.status === "string" && (
          <p>{t(goalStatusLabels[args.status] ?? args.status)}</p>
        )}
        {typeof args.reason === "string" && (
          <p className="whitespace-pre-wrap text-muted">{args.reason}</p>
        )}
      </div>
    );
  }

  if (name === "write_stdin") {
    const input = typeof args.chars === "string" ? args.chars : "";
    const visible = input
      .replaceAll("\u0003", "[Ctrl+C]")
      .replaceAll("\u0004", "[Ctrl+D]")
      .replaceAll("\u001b", "[Esc]");

    return (
      <div className="min-w-0 space-y-3">
        {visible && (
          <CodeSnippet text={visible} label={t("Prozesseingabe")} wrap />
        )}
        {args.eof === true && (
          <p className="text-sm">{t("Eingabe beenden (EOF)")}</p>
        )}
        {!visible && !args.eof && (
          <p className="text-sm text-muted">
            {t("Neue Prozessausgabe abrufen")}
          </p>
        )}
        {showDiagnostics && (
          <ActivityFields value={args} omit={["chars", "eof"]} />
        )}
      </div>
    );
  }

  if (name.startsWith("mcp_")) {
    return <MCPRequest name={name} args={args} network={network} />;
  }

  const packages = Array.isArray(args.packages) ? args.packages : undefined;
  const content = typeof args.content === "string" ? args.content : undefined;
  const command = typeof args.command === "string" ? args.command : undefined;
  const code = typeof args.code === "string" ? args.code : undefined;
  const patch =
    name === "apply_patch" && typeof args.input === "string"
      ? args.input
      : undefined;
  const remaining = Object.fromEntries(
    Object.entries(args).filter(
      ([key]) => !["command", "code", "content", "packages"].includes(key),
    ),
  );

  return (
    <div
      className="min-w-0 space-y-3"
      aria-label={t("Aktionsdetails: {action}", {
        action: t(toolLabel(name, args)),
      })}
    >
      {showDiagnostics && justification && (
        <p className="text-sm leading-6 text-muted">{justification}</p>
      )}
      {command !== undefined && (
        <CodeSnippet text={command} label={t("Befehl")} wrap />
      )}
      {code !== undefined && (
        <CodeSnippet
          text={code}
          label={name === "code_mode" ? "JavaScript" : "Python"}
        />
      )}
      {patch !== undefined && (!hasChanges || showDiagnostics) && (
        <CodeSnippet text={patch} label="Patch" />
      )}
      <ActivityFields
        value={{
          ...(showDiagnostics ? remaining : {}),
          ...(network !== undefined && (showDiagnostics || network || packages)
            ? { network }
            : {}),
        }}
      />
      {packages && (
        <div className="space-y-2">
          <p className="text-xs text-muted">{t("Pakete")}</p>
          <div className="flex flex-wrap gap-2">
            {packages.map((item, index) => (
              <Chip key={index} size="sm" variant="soft">
                <span className="break-all font-mono">{String(item)}</span>
              </Chip>
            ))}
          </div>
        </div>
      )}
      {content !== undefined && (
        <CodeSnippet
          text={content}
          label={typeof args.path === "string" ? args.path : t("Inhalt")}
        />
      )}
      {typeof decoded === "string" && decoded && (
        <p className="whitespace-pre-wrap break-words text-sm">
          {/^[\s]*[\[{]/.test(decoded)
            ? t("Die Parameter konnten nicht gelesen werden.")
            : decoded}
        </p>
      )}
      {!Object.keys(args).length &&
        !justification &&
        network === undefined &&
        typeof decoded !== "string" && (
          <p className="text-xs text-muted">
            {t("Keine Parameter erforderlich.")}
          </p>
        )}
    </div>
  );
}

function TaskPlan({ steps }: { steps: unknown[] }) {
  const { t } = useI18n();
  return (
    <ol aria-label={t("Arbeitsplan")} className="space-y-2">
      {steps.map((value, index) => {
        const step = record(value);
        return (
          <li
            key={index}
            className="flex min-w-0 flex-wrap items-baseline gap-x-3 gap-y-1 text-sm"
          >
            <span className="min-w-0 flex-1 break-words">
              {index + 1}. {String(step.step ?? "")}
            </span>
            <span className="text-xs text-muted">
              {t(
                step.status === "completed"
                  ? "Abgeschlossen"
                  : step.status === "in_progress"
                    ? "In Arbeit"
                    : "Ausstehend",
              )}
            </span>
          </li>
        );
      })}
    </ol>
  );
}

function FileEntries({ entries }: { entries: unknown[] }) {
  const { t } = useI18n();
  const batch = useBatch(entries, "", 30);

  return (
    <div className="min-w-0">
      <p className="mb-2 text-xs text-muted">
        {t("{count} Einträge", { count: entries.length })}
      </p>
      {!entries.length && (
        <p className="text-sm text-muted">
          {t("Dieses Verzeichnis ist leer.")}
        </p>
      )}
      {entries.length > 0 && (
        <ul
          aria-label={t("Dateien")}
          className="divide-y divide-border rounded-lg border border-border"
        >
          {batch.items.map((entry, index) => {
            const file = record(entry);
            const name =
              typeof entry === "string"
                ? entry
                : String(file.name ?? file.path ?? "");
            const kind = file.symlink
              ? t("Verknüpfung")
              : file.directory
                ? t("Verzeichnis")
                : t("Datei");

            return (
              <li
                key={index}
                className="flex min-w-0 items-start gap-3 px-3 py-2 text-xs leading-6"
              >
                <span aria-hidden="true" className="text-muted">
                  {file.directory ? "▸" : "·"}
                </span>
                <span className="min-w-0 flex-1 break-all font-mono">
                  {name}
                </span>
                <span className="shrink-0 text-muted">{kind}</span>
              </li>
            );
          })}
        </ul>
      )}
      <MoreButton batch={batch} />
    </div>
  );
}

function JobResult({
  value,
  name,
}: {
  value: Record<string, unknown>;
  name: string;
}) {
  const { t, showDiagnostics } = useI18n();
  if (
    name === "packages_install" &&
    !showDiagnostics &&
    !value.error &&
    !value.exitCode
  ) {
    return null;
  }

  return (
    <div className="min-w-0 space-y-3">
      {name.startsWith("job_") && typeof value.command === "string" && (
        <CodeSnippet text={value.command} label={t("Befehl")} wrap />
      )}
      <div className="flex flex-wrap items-center gap-3">
        {typeof value.status === "string" &&
          (showDiagnostics ||
            ["running", "waiting", "failed", "interrupted"].includes(
              value.status,
            )) && <ActivityStatus status={value.status} />}
        {typeof value.exitCode === "number" &&
          (showDiagnostics || value.exitCode !== 0) && (
            <span className="font-mono text-xs text-muted">
              {t("Exit-Code")} {value.exitCode}
            </span>
          )}
      </div>
      {showDiagnostics && (
        <ActivityFields
          value={value}
          omit={["command", "output", "error", "status", "exitCode", "agentId"]}
        />
      )}
      {typeof value.output === "string" &&
        (value.output ? (
          <ActivityOutput content={value.output} />
        ) : (
          <p className="text-xs text-muted">{t("Keine Ausgabe")}</p>
        ))}
    </div>
  );
}

function ToolSearchResults({ items }: { items: unknown[] }) {
  const { t } = useI18n();
  return (
    <ul aria-label={t("Gefundene Werkzeuge")} className="min-w-0 space-y-2">
      {items.map((item, index) => {
        const signature = typeof item === "string" ? item : "";
        const match = signature.match(/^([\w.:-]+)\s+(\{.*\})(?:\s+(.*))?$/s);
        if (!match) {
          return (
            <li key={index}>
              <ActivityValue value={item} />
            </li>
          );
        }
        const [, name, parameters, description] = match;
        return (
          <li
            key={index}
            className="min-w-0 space-y-2 rounded-lg border border-border p-3"
          >
            <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
              <span className="text-sm font-medium">
                {t(toolLabel(name, {}))}
              </span>
              <code className="break-all text-xs text-muted">{name}</code>
            </div>
            {description && (
              <p className="break-words text-sm text-muted">{description}</p>
            )}
            <details className="text-xs">
              <summary className="cursor-pointer py-1">
                {t("Parameter")}
              </summary>
              <code className="block whitespace-pre-wrap break-words pt-1 [overflow-wrap:anywhere]">
                {parameters}
              </code>
            </details>
          </li>
        );
      })}
    </ul>
  );
}

export function ActivityResult({
  name,
  value,
  failed = false,
}: {
  name: string;
  value: unknown;
  failed?: boolean;
}) {
  const { t, showDiagnostics } = useI18n();
  const decoded = structured(value);
  const result = record(decoded);
  if (name === "tool_search" && Array.isArray(decoded)) {
    return <ToolSearchResults items={decoded} />;
  }
  if (name === "job_stop" && decoded === true) {
    return <p className="text-sm">{t("Stopp angefordert.")}</p>;
  }
  if (["create_goal", "get_goal", "update_goal"].includes(name)) {
    const goal = record(result.goal ?? decoded);
    return (
      <div className="space-y-2 text-sm">
        {typeof goal.objective === "string" ? (
          <p className="whitespace-pre-wrap break-words">{goal.objective}</p>
        ) : (
          <p>{t("Noch kein Ziel gesetzt")}</p>
        )}
        {typeof goal.status === "string" && (
          <p className="text-xs text-muted">
            {t(goalStatusLabels[goal.status] ?? goal.status)}
          </p>
        )}
        {result.reviewRequired === true && (
          <p>{t("Die unabhängige Abschlussprüfung steht noch aus.")}</p>
        )}
        {typeof goal.reason === "string" && goal.reason && (
          <p className="whitespace-pre-wrap">{t(goal.reason)}</p>
        )}
        {showDiagnostics && <ActivityFields value={goal} />}
      </div>
    );
  }

  if (name.startsWith("mcp_")) {
    return <MCPResult name={name} value={decoded} />;
  }

  const metadata = showDiagnostics && (
    <ActivityFields
      value={result}
      omit={[
        "content",
        "entries",
        "output",
        "job",
        "error",
        "written",
        "deleted",
        "saved",
        "_change",
      ]}
    />
  );
  if (name === "update_plan" && Array.isArray(result.plan)) {
    return <TaskPlan steps={result.plan} />;
  }

  if (name === "apply_patch" && !showDiagnostics) {
    return null;
  }

  if (result.job && typeof result.job === "object") {
    return (
      <div className="space-y-3">
        {showDiagnostics && (
          <ActivityFields value={result} omit={["job", "error"]} />
        )}
        <JobResult value={record(result.job)} name={name} />
      </div>
    );
  }

  if (typeof result.output === "string") {
    return <JobResult value={result} name={name} />;
  }

  if (Array.isArray(result.entries)) {
    return (
      <div className="space-y-3">
        {metadata}
        <FileEntries entries={result.entries} />
      </div>
    );
  }

  if (name === "read_file" && typeof result.content === "string") {
    return (
      <div className="space-y-3">
        {metadata}
        {typeof result.offset === "number" &&
          typeof result.totalLines === "number" && (
            <p className="text-xs text-muted">
              {result.content
                ? t("Ab Zeile {start} · {total} Zeilen insgesamt", {
                    start: result.offset,
                    total: result.totalLines,
                  })
                : t("Keine Zeilen in diesem Bereich.")}
            </p>
          )}
        <CodeSnippet
          text={result.content}
          label={
            typeof result.path === "string" ? result.path : t("Dateiinhalt")
          }
        />
      </div>
    );
  }

  if (
    result.written === true ||
    result.deleted === true ||
    result.saved === true
  ) {
    return !showDiagnostics ? null : (
      <div className="space-y-3">
        <p className="text-sm">
          {t(
            result.deleted
              ? "Datei entfernt."
              : result.written
                ? "Datei gespeichert."
                : "Gespeichert.",
          )}
        </p>
        {metadata}
      </div>
    );
  }

  if (decoded === true) {
    return !showDiagnostics ? null : (
      <p className="text-sm">{t("Aktion abgeschlossen.")}</p>
    );
  }

  if (decoded === false) {
    return <p className="text-sm">{t("Aktion nicht ausgeführt.")}</p>;
  }

  if (failed && !Object.keys(result).filter((key) => key !== "error").length) {
    return null;
  }

  if (
    decoded === undefined ||
    decoded === null ||
    (typeof decoded === "object" &&
      !Array.isArray(decoded) &&
      !Object.keys(result).length)
  ) {
    return <p className="text-xs text-muted">{t("Keine weiteren Angaben.")}</p>;
  }

  return (
    <div className="min-w-0 text-sm">
      <ActivityValue
        value={
          typeof decoded === "object" && !Array.isArray(decoded)
            ? Object.fromEntries(
                Object.entries(result).filter(
                  ([key]) =>
                    key !== "error" &&
                    (showDiagnostics ||
                      ![
                        "id",
                        "jobId",
                        "agentId",
                        "machineId",
                        "sandbox",
                        "startedAt",
                        "createdAt",
                        "finishedAt",
                        "status",
                      ].includes(key)),
                ),
              )
            : decoded
        }
      />
    </div>
  );
}

export function ActivityContent({ content }: { content: string }) {
  const { t } = useI18n();
  const value = structured(content);
  const decision = record(value);
  if (typeof decision.allowed === "boolean") {
    return (
      <div className="space-y-2">
        <Chip
          size="sm"
          variant="soft"
          color={decision.allowed ? "success" : "danger"}
        >
          {t(decision.allowed ? "Genehmigt" : "Abgelehnt")}
        </Chip>
        {typeof decision.reason === "string" && decision.reason && (
          <p className="text-sm leading-6">{decision.reason}</p>
        )}
      </div>
    );
  }

  return typeof value === "string" ? (
    <MessageMarkdown content={value} />
  ) : (
    <ActivityValue value={value} />
  );
}

export function ActivityOutput({ content }: { content: string }) {
  const { t } = useI18n();
  const value = structured(content);
  if (value !== null && typeof value === "object") {
    return <ActivityValue value={value} />;
  }

  // The Git helper appends a structured receipt after ordinary terminal output.
  const lines = content.trimEnd().split("\n");
  const receipt = record(structured(lines.at(-1)));
  if (
    typeof receipt.directory === "string" &&
    typeof receipt.readOnly === "boolean"
  ) {
    return (
      <div className="space-y-3">
        {lines.length > 1 && (
          <CodeSnippet
            text={lines.slice(0, -1).join("\n")}
            label={t("Ausgabe")}
            wrap
          />
        )}
        <ActivityFields value={receipt} />
      </div>
    );
  }

  return <CodeSnippet text={content} label={t("Ausgabe")} wrap />;
}
