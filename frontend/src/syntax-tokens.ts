import type { RootContent } from "hast";

import type { SyntaxToken } from "./syntax";
import { highlighter } from "./syntax-language";
import { shellEmbeddings } from "./syntax-shell";

function tokensFor(
  text: string,
  language: string | undefined,
  depth: number,
): SyntaxToken[] {
  if (!language || text.length > 200_000) return [{ text }];

  const tokens: SyntaxToken[] = [];

  function visit(node: RootContent, classes: string[] = []) {
    if (node.type === "text") {
      tokens.push({
        text: node.value,
        className: classes.join(" ") || undefined,
      });
    } else if (node.type === "element") {
      const ownClasses = Array.isArray(node.properties.className)
        ? node.properties.className.map(String)
        : [];

      node.children.forEach((child) =>
        visit(child, [...classes, ...ownClasses]),
      );
    }
  }

  try {
    highlighter
      .highlight(language, text)
      .children.forEach((node) => visit(node));
  } catch {
    return [{ text }];
  }

  if (language !== "bash" || depth >= 3) return tokens;

  const embeddings = shellEmbeddings(text, tokens);
  if (!embeddings.length) return tokens;

  const result: SyntaxToken[] = [];
  let tokenIndex = 0;
  let offset = 0;

  function consumeUntil(end: number, keep: boolean) {
    while (offset < end && tokenIndex < tokens.length) {
      const token = tokens[tokenIndex];
      const length = Math.min(token.text.length, end - offset);

      if (keep && length)
        result.push({ ...token, text: token.text.slice(0, length) });

      offset += length;
      if (length === token.text.length) tokenIndex++;
      else tokens[tokenIndex] = { ...token, text: token.text.slice(length) };
    }
  }

  for (const embedded of embeddings) {
    consumeUntil(embedded.start, true);
    consumeUntil(embedded.end, false);
    result.push(
      ...tokensFor(
        text.slice(embedded.start, embedded.end),
        embedded.language,
        depth + 1,
      ),
    );
  }

  consumeUntil(text.length, true);

  return result;
}

// Splitting happens after all language regions are tokenized, preserving source
// bytes, blank lines and multiline comments/strings across ordinary and diff views.
export function sourceLines(
  text: string,
  language: string | undefined,
): SyntaxToken[][] {
  const lines: SyntaxToken[][] = [[]];

  for (const token of tokensFor(text, language, 0)) {
    token.text.split("\n").forEach((part, index) => {
      if (index) lines.push([]);

      if (part) lines.at(-1)!.push({ ...token, text: part });
    });
  }

  return lines;
}
