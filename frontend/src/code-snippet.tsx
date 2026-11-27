import { Button } from "@heroui/react";
import { useEffect, useState } from "react";

import { MoreButton } from "./batches";
import { DiffStats, diffTone } from "./diff-presentation";
import { useI18n } from "./preferences";
import { SourceCode } from "./syntax";

function describePatch(text: string) {
  const source = text.split(/\r?\n/);
  if (source.at(-1) === "") source.pop();

  let added = 0;
  let removed = 0;
  // Submitted patches contain explicit edit lines but no absolute line numbers.
  // Keep the original headers/context, including malformed input for diagnosis.
  const lines = source.map((line) => {
    if (line.startsWith("+")) {
      added++;
      return { line, kind: "added" } as const;
    }
    if (line.startsWith("-")) {
      removed++;
      return { line, kind: "removed" } as const;
    }
    if (line === "@@" || line.startsWith("@@ ")) {
      return { line, kind: "hunk" } as const;
    }
    return {
      line,
      kind: line.startsWith("*** ") ? "note" : "context",
    } as const;
  });

  return { lines, added, removed };
}

// Preserve original contents for copying, even when the optional JavaScript
// display is formatted. The stored/executed/reviewed tool source never changes.
export function CodeSnippet({
  text,
  label,
  startLine,
  wrap = false,
  formatJavaScript = false,
  highlightPatch = false,
}: {
  text: string;
  label: string;
  startLine?: number;
  wrap?: boolean;
  formatJavaScript?: boolean;
  highlightPatch?: boolean;
}) {
  const { t } = useI18n();
  const [copied, setCopied] = useState(false);
  const [failed, setFailed] = useState(false);
  const [visiblePatchLines, setVisiblePatchLines] = useState(80);
  const [formatted, setFormatted] = useState<{
    source: string;
    text: string;
  }>();
  useEffect(() => {
    if (!formatJavaScript) return;
    let disposed = false;
    void import("./format-javascript")
      .then((formatter) => formatter.formatJavaScript(text))
      .catch(() => text)
      .then((display) => {
        if (!disposed) setFormatted({ source: text, text: display });
      });
    return () => {
      disposed = true;
    };
  }, [formatJavaScript, text]);
  const formatting = formatJavaScript && formatted?.source !== text;
  const display =
    formatJavaScript && formatted?.source === text ? formatted.text : text;
  const patch = highlightPatch ? describePatch(text) : undefined;
  const layout = patch
    ? "py-2 overflow-x-auto whitespace-pre!"
    : wrap
      ? "p-3 sm:p-4 whitespace-pre-wrap! break-words [overflow-wrap:anywhere]"
      : "p-3 sm:p-4 overflow-x-auto whitespace-pre!";

  return (
    <figure className="my-3 min-w-0 overflow-hidden rounded-lg border border-border bg-background">
      <figcaption className="flex min-w-0 items-center justify-between gap-3 border-b border-border px-3 font-mono text-xs text-muted">
        <span className="min-w-0 flex-1 break-all">{label}</span>
        {patch && <DiffStats added={patch.added} removed={patch.removed} />}
        <Button
          size="sm"
          variant="ghost"
          className="min-h-11 shrink-0 font-sans"
          onPress={async () => {
            try {
              await navigator.clipboard.writeText(text);
              setCopied(true);
              setFailed(false);
            } catch {
              setFailed(true);
            }
          }}
        >
          {t(copied ? "Kopiert" : "Kopieren")}
        </Button>
      </figcaption>
      <pre
        aria-busy={formatting || undefined}
        tabIndex={0}
        aria-label={t("Codeblock: {language}", { language: label })}
        className={`min-w-0 font-mono text-xs leading-6 outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-focus ${layout}`}
      >
        {patch ? (
          <code className="block w-max min-w-full">
            {patch.lines
              .slice(0, visiblePatchLines)
              .map(({ line, kind }, index) => (
                <span
                  key={index}
                  className={`block min-h-6 px-3 ${diffTone[kind]}`}
                >
                  {line}
                </span>
              ))}
          </code>
        ) : (
          <code>
            <SourceCode text={display} language={label} startLine={startLine} />
          </code>
        )}
      </pre>
      {patch && (
        <MoreButton
          batch={{
            hasMore: visiblePatchLines < patch.lines.length,
            showMore: () => setVisiblePatchLines((count) => count + 80),
          }}
        />
      )}
      {failed && (
        <p role="status" className="px-4 pb-3 text-xs text-muted">
          {t(
            "Kopieren nicht möglich. Den Code markieren und manuell kopieren.",
          )}
        </p>
      )}
      <span role="status" className="sr-only">
        {copied ? t("Code kopiert") : ""}
      </span>
    </figure>
  );
}
