import { Fragment, useMemo, type ReactNode } from "react";

import { SyntaxTokens, type DiffSourceLine, type SyntaxToken } from "./syntax";
import { languageFor } from "./syntax-language";
import { sourceLines } from "./syntax-tokens";
import "./syntax.css";

export function HighlightedSource({
  text,
  language,
  startLine = 1,
}: {
  text: string;
  language: string;
  startLine?: number;
}) {
  const lines = useMemo(
    () => sourceLines(text, languageFor(language, text, startLine)),
    [text, language, startLine],
  );
  return (
    <span className="syntax-highlight">
      {lines.map((tokens, index) => (
        <Fragment key={index}>
          <SyntaxTokens tokens={tokens} />
          {index < lines.length - 1 ? "\n" : ""}
        </Fragment>
      ))}
    </span>
  );
}

function diffLines(lines: DiffSourceLine[], label: string): SyntaxToken[][] {
  const result = lines.map((line) => [{ text: line.text }]);

  // Only actual file line 1 can supply a shebang. Keep each version's language
  // across hunks, even when a patch changes or removes the interpreter.
  const beforeLanguage = languageFor(
    label,
    lines.find((line) => line.oldLine === 1)?.text ?? "",
  );
  const afterLanguage = languageFor(
    label,
    lines.find((line) => line.newLine === 1)?.text ?? "",
  );

  // Each hunk has its own context. Highlight both versions, then map tokens back
  // onto their rows; deleted lines never affect the new file's syntax state.
  let start = 0;
  while (start < lines.length) {
    if (lines[start].kind === "hunk") {
      start++;
      continue;
    }

    let end = start;
    while (end < lines.length && lines[end].kind !== "hunk") end++;

    const group = lines.slice(start, end);
    const before = sourceLines(
      group
        .filter((line) => ["context", "removed"].includes(line.kind))
        .map((line) => line.text)
        .join("\n"),
      beforeLanguage,
    );
    const after = sourceLines(
      group
        .filter((line) => ["context", "added"].includes(line.kind))
        .map((line) => line.text)
        .join("\n"),
      afterLanguage,
    );
    let oldIndex = 0,
      newIndex = 0;
    group.forEach((line, index) => {
      if (line.kind === "removed")
        result[start + index] = before[oldIndex++] ?? [];
      else if (line.kind === "added")
        result[start + index] = after[newIndex++] ?? [];
      else if (line.kind === "context") {
        result[start + index] = after[newIndex++] ?? [];
        oldIndex++;
      }
    });
    start = end;
  }

  return result;
}

export function HighlightedDiff({
  lines,
  language,
  children,
}: {
  lines: DiffSourceLine[];
  language: string;
  children: (tokens?: SyntaxToken[][]) => ReactNode;
}) {
  const highlighted = useMemo(
    () => diffLines(lines, language),
    [lines, language],
  );
  return children(highlighted);
}
