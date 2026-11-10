import { Accordion } from "@heroui/react";
import { MoreButton } from "./batches";
import { useState } from "react";

import type { FileChange } from "./generated";
import { useI18n } from "./preferences";
import { useProjectPath } from "./project-path";
import { DiffSyntax, SyntaxTokens, type SyntaxToken } from "./syntax";

type DiffLine = {
  kind: "context" | "added" | "removed" | "hunk" | "note";
  text: string;
  oldLine?: number;
  newLine?: number;
};

// Hunk coordinates, rather than array indexes, determine the two line numbers.
// File headers are metadata; a code line starting with +++ is still an addition.
function patchLines(patch: string): DiffLine[] {
  let oldLine = 0;
  let newLine = 0;
  let inHunk = false;
  const lines = patch.split("\n");
  if (lines.at(-1) === "") lines.pop();

  return lines.flatMap((line): DiffLine[] => {
    const hunk = /^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@/.exec(line);
    if (hunk) {
      oldLine = Number(hunk[1]);
      newLine = Number(hunk[2]);
      inHunk = true;

      return [{ kind: "hunk", text: line }];
    }

    if (!inHunk) {
      return [];
    }

    if (line.startsWith("+")) {
      return [{ kind: "added", text: line.slice(1), newLine: newLine++ }];
    }

    if (line.startsWith("-")) {
      return [{ kind: "removed", text: line.slice(1), oldLine: oldLine++ }];
    }

    if (line.startsWith(" ")) {
      return [
        {
          kind: "context",
          text: line.slice(1),
          oldLine: oldLine++,
          newLine: newLine++,
        },
      ];
    }

    return [{ kind: "note", text: line }];
  });
}

const notices: Record<string, string> = {
  binary: "Für Binärdateien ist kein Text-Diff verfügbar.",
  too_large: "Die Datei ist für einen Text-Diff zu groß.",
  unavailable: "Der vorherige Dateiinhalt konnte nicht gelesen werden.",
  directory:
    "Verzeichnis entfernt. Für Verzeichnisse ist kein Text-Diff verfügbar.",
};

const kinds: Record<string, string> = {
  created: "Erstellt",
  modified: "Geändert",
  deleted: "Gelöscht",
  unchanged: "Unverändert",
};

export function FileDiff({ change }: { change: FileChange }) {
  const { t } = useI18n();
  const projectPath = useProjectPath();
  const [visibleLines, setVisibleLines] = useState(80);
  const lines = patchLines(change.patch);
  const renderRows = (highlighted?: SyntaxToken[][]) =>
    lines.slice(0, visibleLines).map((line, index) => (
      <tr
        key={index}
        className={
          line.kind === "added"
            ? "bg-success-soft text-success-soft-foreground"
            : line.kind === "removed"
              ? "bg-danger-soft text-danger-soft-foreground"
              : line.kind === "hunk"
                ? "bg-accent/5 text-accent"
                : "text-foreground"
        }
      >
        <td className="w-10 min-w-10 select-none px-2 text-right">
          {line.oldLine}
        </td>
        <td className="w-10 min-w-10 select-none px-2 text-right">
          {line.newLine}
        </td>
        <td className="w-5 min-w-5 select-none text-center">
          {line.kind === "added" ? "+" : line.kind === "removed" ? "−" : ""}
        </td>
        <td className="whitespace-pre pr-4">
          {line.kind === "note" &&
          line.text === "\\ No newline at end of file" ? (
            t("Kein Zeilenumbruch am Dateiende")
          ) : (
            <code
              className={
                ["context", "added", "removed"].includes(line.kind)
                  ? "syntax-highlight"
                  : undefined
              }
            >
              {highlighted?.[index] ? (
                <SyntaxTokens tokens={highlighted[index]} />
              ) : (
                line.text || " "
              )}
            </code>
          )}
        </td>
      </tr>
    ));

  return (
    <Accordion
      hideSeparator
      defaultExpandedKeys={["diff"]}
      className="my-2 min-w-0 rounded-lg border border-border bg-background"
    >
      <Accordion.Item id="diff">
        <Accordion.Heading>
          <Accordion.Trigger className="min-h-11 min-w-0 gap-3 px-3 py-2 font-mono text-xs">
            <span
              title={change.path}
              className="min-w-0 flex-1 break-all text-left font-medium"
            >
              {projectPath(change.path)}
            </span>
            <span className="sr-only">
              {t(kinds[change.kind] ?? "Geändert")}
            </span>
            <span className="shrink-0 rounded bg-success-soft px-1.5 py-0.5 text-success-soft-foreground">
              +{change.added}
            </span>
            <span className="shrink-0 rounded bg-danger-soft px-1.5 py-0.5 text-danger-soft-foreground">
              −{change.removed}
            </span>
            <Accordion.Indicator className="shrink-0 text-muted" />
          </Accordion.Trigger>
        </Accordion.Heading>
        <Accordion.Panel>
          <Accordion.Body className="min-w-0 p-0">
            {lines.length > 0 && (
              <div
                role="region"
                aria-label={t("Dateiänderungen: {path}", { path: change.path })}
                tabIndex={0}
                className="min-w-0 overflow-x-auto border-t border-border outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-focus"
              >
                <table className="w-max min-w-full border-collapse font-mono text-xs leading-6">
                  <caption className="sr-only">
                    {t("Dateiänderungen: {path}", { path: change.path })}
                  </caption>
                  <thead className="sr-only">
                    <tr>
                      <th>{t("Alte Zeile")}</th>
                      <th>{t("Neue Zeile")}</th>
                      <th>{t("Änderung")}</th>
                      <th>{t("Inhalt")}</th>
                    </tr>
                  </thead>
                  <tbody>
                    <DiffSyntax lines={lines} language={change.path}>
                      {renderRows}
                    </DiffSyntax>
                  </tbody>
                </table>
              </div>
            )}
            <MoreButton
              batch={{
                hasMore: visibleLines < lines.length,
                showMore: () => setVisibleLines((count) => count + 80),
              }}
            />
            {(change.notice || change.truncated || !lines.length) && (
              <p
                className="border-t border-border px-3 py-2 text-xs leading-6 text-muted"
                role="note"
              >
                {change.notice
                  ? t(notices[change.notice] ?? "Kein Text-Diff verfügbar.")
                  : change.truncated
                    ? t(
                        "Diff gekürzt. Die Zeilenzähler gelten für die gesamte Änderung.",
                      )
                    : t(
                        change.kind === "unchanged"
                          ? "Dateiinhalt unverändert."
                          : "Leere Datei.",
                      )}
              </p>
            )}
          </Accordion.Body>
        </Accordion.Panel>
      </Accordion.Item>
    </Accordion>
  );
}
