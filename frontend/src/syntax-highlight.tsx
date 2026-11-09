import { Fragment, useMemo, type ReactNode } from "react";
import type { Element, Root, RootContent } from "hast";
import { common, createLowlight } from "lowlight";

import { SyntaxTokens, type DiffSourceLine, type SyntaxToken } from "./syntax";
import "./syntax.css";

const highlighter = createLowlight(common);

const extensions: Record<string, string> = {
  py: "python",
  pyw: "python",
  go: "go",
  rs: "rust",
  js: "javascript",
  jsx: "javascript",
  mjs: "javascript",
  cjs: "javascript",
  ts: "typescript",
  tsx: "typescript",
  mts: "typescript",
  cts: "typescript",
  json: "json",
  jsonc: "json",
  yaml: "yaml",
  yml: "yaml",
  html: "xml",
  htm: "xml",
  xml: "xml",
  svg: "xml",
  css: "css",
  scss: "scss",
  less: "less",
  sh: "bash",
  bash: "bash",
  zsh: "bash",
  md: "markdown",
  mdx: "markdown",
  sql: "sql",
  java: "java",
  kt: "kotlin",
  kts: "kotlin",
  c: "c",
  h: "c",
  cpp: "cpp",
  hpp: "cpp",
  cc: "cpp",
  cs: "csharp",
  rb: "ruby",
  php: "php",
  swift: "swift",
  lua: "lua",
  r: "r",
  toml: "ini",
  ini: "ini",
  conf: "ini",
  graphql: "graphql",
  gql: "graphql",
  diff: "diff",
  patch: "diff",
};

function languageFor(label: string) {
  const name = label.toLowerCase().split(/[\\/]/).at(-1) ?? "";
  if (["command", "befehl", ".bashrc", ".zshrc"].includes(name)) {
    return "bash";
  }

  if (["makefile", "gnumakefile"].includes(name)) {
    return "makefile";
  }

  if (name === ".env" || name.startsWith(".env.")) {
    return "ini";
  }

  const language = extensions[name.split(".").at(-1) ?? ""] ?? name;

  return highlighter.registered(language) ? language : undefined;
}

// Split the token tree into lines without losing multiline strings or comments.
// React renders text nodes, so source code is never inserted as HTML.
function sourceLines(text: string, label: string): SyntaxToken[][] {
  const plain = () => text.split("\n").map((line) => [{ text: line }]);
  const language = languageFor(label);
  if (!language || text.length > 200_000) {
    return plain();
  }

  let tree: Root;

  try {
    tree = highlighter.highlight(language, text);
  } catch {
    return plain();
  }

  const lines: SyntaxToken[][] = [[]];

  function visit(node: RootContent, classes: string[] = []) {
    if (node.type === "text") {
      node.value.split("\n").forEach((part, index) => {
        if (index) lines.push([]);
        if (part)
          lines
            .at(-1)!
            .push({ text: part, className: classes.join(" ") || undefined });
      });
    } else if (node.type === "element") {
      const element = node as Element;
      const ownClasses = Array.isArray(element.properties.className)
        ? element.properties.className.map(String)
        : [];
      element.children.forEach((child) =>
        visit(child, [...classes, ...ownClasses]),
      );
    }
  }
  tree.children.forEach((node) => visit(node));

  return lines;
}

export function HighlightedSource({
  text,
  language,
}: {
  text: string;
  language: string;
}) {
  const lines = useMemo(() => sourceLines(text, language), [text, language]);
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

function diffLines(lines: DiffSourceLine[], language: string): SyntaxToken[][] {
  const result = lines.map((line) => [{ text: line.text }]);
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
      language,
    );
    const after = sourceLines(
      group
        .filter((line) => ["context", "added"].includes(line.kind))
        .map((line) => line.text)
        .join("\n"),
      language,
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
