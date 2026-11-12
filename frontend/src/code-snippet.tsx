import { Button } from "@heroui/react";
import { useEffect, useState } from "react";

import { useI18n } from "./preferences";
import { SourceCode } from "./syntax";

// Preserve original contents for copying, even when the optional JavaScript
// display is formatted. The stored/executed/reviewed tool source never changes.
export function CodeSnippet({
  text,
  label,
  wrap = false,
  formatJavaScript = false,
}: {
  text: string;
  label: string;
  wrap?: boolean;
  formatJavaScript?: boolean;
}) {
  const { t } = useI18n();
  const [copied, setCopied] = useState(false);
  const [failed, setFailed] = useState(false);
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

  return (
    <figure className="my-3 min-w-0 overflow-hidden rounded-lg border border-border bg-background">
      <figcaption className="flex min-w-0 items-center justify-between gap-3 border-b border-border px-3 font-mono text-xs text-muted">
        <span className="min-w-0 break-all">{label}</span>
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
        className={`min-w-0 p-3 font-mono text-xs leading-6 outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-focus sm:p-4 ${wrap ? "whitespace-pre-wrap! break-words [overflow-wrap:anywhere]" : "overflow-x-auto whitespace-pre!"}`}
      >
        <code>
          <SourceCode text={display} language={label} />
        </code>
      </pre>
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
