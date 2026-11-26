import { Chip, Link } from "@heroui/react";

import { MoreButton, useBatch } from "./batches";
import { parseJSON } from "./agent-activity";
import { useI18n } from "./preferences";
import { useProjectPath } from "./project-path";

export function record(value: unknown): Record<string, unknown> {
  return value !== null && typeof value === "object" && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : {};
}

export function structured(value: unknown): unknown {
  if (typeof value !== "string") {
    return value;
  }
  return parseJSON(value) ?? value;
}

const fieldLabels: Record<string, string> = {
  readOnly: "Schreibgeschützt",
  characters: "Zeichen",
  target: "Ziel",
  counters: "Zähler",
  command: "Befehl",
  output: "Ausgabe",
  content: "Inhalt",
  code: "Code",
  path: "Pfad",
  filename: "Datei",
  directory: "Verzeichnis",
  name: "Name",
  title: "Titel",
  description: "Beschreibung",
  status: "Status",
  error: "Fehler",
  reason: "Begründung",
  justification: "Begründung",
  network: "Netzwerkzugriff",
  outsideSandbox: "Außerhalb der Sandbox",
  guestRoot: "Administration im Gast",
  packages: "Pakete",
  sandbox: "Sandbox",
  machineId: "Maschine",
  machineIds: "Maschinen",
  projectId: "Projekt",
  project: "Projekt",
  agentId: "Agent",
  parentId: "Übergeordneter Agent",
  mainAgentId: "Hauptagent",
  approverId: "Auto-Approver",
  providerId: "Provider",
  modelId: "Modell",
  id: "Kennung",
  jobId: "Job",
  sourceId: "Quelle",
  source: "Quelle",
  note: "Hinweis",
  unavailableSources: "Nicht erreichbare Quellen",
  sources: "Quellen",
  host: "Host",
  port: "Port",
  user: "Benutzer",
  keyId: "SSH-Schlüssel",
  jumps: "Sprungserver",
  singleUser: "Exklusive Nutzung",
  containers: "Container",
  properties: "Eigenschaften",
  maxMachines: "Maximale Maschinenanzahl",
  sharedHome: "Gemeinsames Home",
  workspaceRoot: "Projektverzeichnis",
  managedWorkspace: "Automatisch verwalteter Arbeitsbereich",
  os: "Betriebssystem",
  version: "Version",
  bootId: "VM-Startkennung",
  running: "VM läuft",
  persistent: "Dauerhafte Umgebung",
  projectFilesPreserved: "Projektdateien bleiben erhalten",
  createdAt: "Erstellt am",
  startedAt: "Gestartet am",
  finishedAt: "Beendet am",
  allowed: "Genehmigt",
  blanket: "Pauschale Lesefreigabe",
  saved: "Gespeichert",
  written: "Gespeichert",
  deleted: "Gelöscht",
  connected: "Verbunden",
  selected: "Ausgewählt",
  query: "Suchanfrage",
  url: "Adresse",
  address: "Adresse",
  method: "Methode",
  body: "Inhalt",
  raw: "Originalinhalt",
  runtime: "Laufzeitumgebung",
  arguments: "Parameter",
  action: "Aktion",
  result: "Ergebnis",
  entries: "Dateien",
  symlink: "Verknüpfung",
  count: "Anzahl",
  total: "Gesamt",
  exitCode: "Exit-Code",
  exitStatus: "Exit-Code",
  role: "Rolle",
  summary: "Zusammenfassung",
  options: "Antwortmöglichkeiten",
  answer: "Antwort",
  task: "Aufgabe",
  venv: "Python-Umgebung",
  offset: "Position",
  nextOffset: "Nächste Position",
  limit: "Limit",
  totalBytes: "Größe in Bytes",
  hasMore: "Weitere Inhalte vorhanden",
  truncated: "Gekürzt",
  preview: "Vorschau",
  userInstructions: "Nutzeranweisungen",
  hash: "Prüfsumme",
  sha256: "Prüfsumme",
};

const values: Record<string, string> = {
  idle: "Bereit",
  running: "Arbeitet",
  waiting: "Wartet",
  completed: "Abgeschlossen",
  failed: "Fehlgeschlagen",
  timed_out: "Zeitlimit erreicht",
  error: "Fehler",
  interrupted: "Unterbrochen",
  stopped: "Gestoppt",
  open: "Offen",
  answered: "Beantwortet",
  withdrawn: "Zurückgezogen",
  microvm: "MicroVM",
  bwrap: "Bubblewrap",
  none: "Ohne Sandbox",
  auto: "Automatisch",
  main: "Hauptagent",
  subagent: "Unteragent",
  approver: "Auto-Approver",
  user: "Du",
  assistant: "Agent",
  event: "System",
  tool: "Werkzeug",
  fedora: "Fedora",
  reset: "Zurückgesetzt",
  stop: "Gestoppt",
  remove: "Entfernt",
};

export function fieldLabel(key: string) {
  return (
    fieldLabels[key] ??
    key.replace(/([a-z])([A-Z])/g, "$1 $2").replaceAll("_", " ")
  );
}

export function ActivityStatus({ status }: { status: string }) {
  const { t } = useI18n();
  const color = ["failed", "error", "interrupted", "timed_out"].includes(status)
    ? "danger"
    : status === "completed"
      ? "success"
      : "default";

  return (
    <Chip size="sm" variant="soft" color={color}>
      {t(values[status] ?? status)}
    </Chip>
  );
}

// Future tool fields still get readable labels, lists and values. Transport
// wrappers and provider signatures never reach this fallback.
export function ActivityValue({
  value,
  field = "",
}: {
  value: unknown;
  field?: string;
}) {
  const { t, numberLocale } = useI18n();
  const projectPath = useProjectPath();
  if (value === null || value === undefined || value === "") {
    return <span className="text-muted">{t("Keine Angabe")}</span>;
  }

  if (typeof value === "boolean") {
    return <span>{t(value ? "Ja" : "Nein")}</span>;
  }

  if (typeof value === "number") {
    return (
      <span className="font-mono tabular-nums">
        {value.toLocaleString(numberLocale)}
      </span>
    );
  }

  if (typeof value === "string") {
    if (field === "status") {
      return <ActivityStatus status={value} />;
    }

    if (/At$/.test(field) && !Number.isNaN(Date.parse(value))) {
      return (
        <time dateTime={value}>
          {new Date(value).toLocaleString(numberLocale)}
        </time>
      );
    }

    if (
      (field === "url" || field === "html_url") &&
      /^https?:\/\//i.test(value)
    ) {
      return (
        <Link
          href={value}
          target="_blank"
          rel="noopener noreferrer"
          className="break-all text-sm"
        >
          {value}
        </Link>
      );
    }

    {
      const decoded = structured(value);
      if (decoded !== null && typeof decoded === "object") {
        return <ActivityValue value={decoded} />;
      }
    }

    return (
      <span
        className={`whitespace-pre-wrap break-words [overflow-wrap:anywhere] ${["path", "filename", "host", "id", "jobId", "bootId"].includes(field) ? "font-mono text-xs" : ""}`}
      >
        {["sandbox", "role", "os", "action"].includes(field)
          ? t(values[value] ?? value)
          : ["path", "filename", "workspace", "directory"].includes(field)
            ? projectPath(value)
            : value}
      </span>
    );
  }

  if (Array.isArray(value)) {
    return <ActivityList items={value} />;
  }

  return <ActivityFields value={record(value)} />;
}

function ActivityList({ items }: { items: unknown[] }) {
  const { t } = useI18n();
  const batch = useBatch(items, "", 20);
  if (!items.length) {
    return <span className="text-muted">{t("Keine Einträge")}</span>;
  }

  return (
    <div className="min-w-0">
      <ul className="divide-y divide-border">
        {batch.items.map((item, index) => (
          <li key={index} className="min-w-0 py-2 first:pt-0 last:pb-0">
            <ActivityValue value={item} />
          </li>
        ))}
      </ul>
      <MoreButton batch={batch} />
    </div>
  );
}

export function ActivityFields({
  value,
  omit = [],
}: {
  value: Record<string, unknown>;
  omit?: string[];
}) {
  const { t, showDiagnostics } = useI18n();
  const fields = Object.entries(value).filter(
    ([key, item]) =>
      !omit.includes(key) &&
      (showDiagnostics ||
        ![
          "id",
          "agentId",
          "mainAgentId",
          "parentId",
          "approverId",
          "providerId",
          "machineId",
          "jobId",
          "keyId",
          "bootId",
          "sandbox",
          "createdAt",
          "startedAt",
          "finishedAt",
          "checkedAt",
        ].includes(key)) &&
      !key.startsWith("_") &&
      ![
        "wire",
        "encrypted_content",
        "signature",
        "thoughtSignature",
        "readMore",
      ].includes(key) &&
      item !== undefined &&
      item !== null &&
      item !== "",
  );
  if (!fields.length) {
    return null;
  }

  return (
    <dl className="flex min-w-0 flex-wrap items-start gap-x-6 gap-y-2 text-sm">
      {fields.map(([key, item]) => {
        const decoded = structured(item);
        const group = decoded !== null && typeof decoded === "object";
        const multiline =
          typeof item === "string" && (item.length > 80 || item.includes("\n"));

        return (
          <div
            key={key}
            className={
              group || multiline
                ? "w-full min-w-0 space-y-0.5"
                : "flex min-w-0 max-w-full flex-wrap items-baseline gap-x-2"
            }
          >
            <dt className="break-words text-xs font-medium leading-6 text-muted">
              {t(fieldLabel(key))}
            </dt>
            <dd
              className={
                group
                  ? "min-w-0 rounded-lg bg-surface-secondary p-2.5"
                  : "min-w-0 leading-6"
              }
            >
              <ActivityValue value={group ? decoded : item} field={key} />
            </dd>
          </div>
        );
      })}
    </dl>
  );
}
