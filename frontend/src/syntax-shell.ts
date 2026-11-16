import type { SyntaxToken } from "./syntax";
import {
  highlighter,
  interpreterCommand,
  languageFor,
} from "./syntax-language";

export type EmbeddedSource = { start: number; end: number; language: string };

// This is deliberately not a shell evaluator. Only literal words in simple
// commands are understood; substitutions and compound syntax keep the fallback.
function commandWords(source: string): string[] | undefined {
  const words: string[] = [];
  let word = "";
  let quote = "";
  let started = false;

  for (let index = 0; index < source.length; index++) {
    const character = source[index];

    if (character === "\\" && quote !== "'") {
      if (index + 1 === source.length) return;

      word += source[++index];
      started = true;
      continue;
    }

    if (quote) {
      if (character === quote) quote = "";
      else word += character;

      continue;
    }

    if (character === "'" || character === '"') {
      quote = character;
      started = true;
      continue;
    }

    if (/[ \t]/.test(character)) {
      if (started) words.push(word);

      word = "";
      started = false;
      continue;
    }

    if (/[;&|]/.test(character)) {
      words.length = 0;
      word = "";
      started = false;
      continue;
    }

    if (/[()<>`\r\n]/.test(character) || (character === "#" && !started)) {
      return;
    }

    word += character;
    started = true;
  }

  if (quote) return;

  if (started) words.push(word);

  while (/^[A-Za-z_][A-Za-z0-9_]*=/.test(words[0] ?? "")) words.shift();

  return words;
}

function bodyLanguage(prefix: string, delimiter: string, body: string) {
  const words = commandWords(prefix);
  if (!words?.length) return;

  const interpreter = interpreterCommand(words);
  if (!interpreter) {
    // Explicit delimiter names (JSON, SQL, PY, ...) also describe literal files.
    const executable = words[0].split("/").at(-1);
    return executable === "cat" || executable === "tee"
      ? languageFor(delimiter, body)
      : undefined;
  }

  const { language } = interpreter;
  let args = interpreter.args;

  if (language === "typescript" && args[0] === "run") args = args.slice(1);

  if (
    ![
      "bash",
      "python",
      "javascript",
      "typescript",
      "ruby",
      "perl",
      "php",
      "lua",
      "r",
    ].includes(language)
  ) {
    return;
  }

  // A script filename or -c/-e-style argument consumes code elsewhere: its stdin
  // is data, even when the here-doc happens to have a familiar delimiter name.
  const codeOptions: Record<string, string> = {
    bash: "c",
    python: "cm",
    javascript: "ep",
    typescript: "ep",
    ruby: "eE",
    perl: "eE",
    php: "rf",
    lua: "e",
    r: "e",
  };
  const inlineOption = new RegExp(`^-[^-]*[${codeOptions[language]}]`);

  if (
    args.some(
      (arg) =>
        (arg !== "-" && !arg.startsWith("-")) ||
        inlineOption.test(arg) ||
        /^--(?:eval|print|command|module)(?:=|$)/.test(arg),
    )
  ) {
    return;
  }

  return highlighter.registered(language) ? language : undefined;
}

export function shellEmbeddings(
  text: string,
  tokens: SyntaxToken[],
): EmbeddedSource[] {
  // Reuse the Bash grammar's lexical context instead of mistaking examples in
  // comments or quoted strings for real redirections (including multiline ones).
  const protectedText = new Uint8Array(text.length);
  let offset = 0;

  for (const token of tokens) {
    if (/\bhljs-(?:string|comment)\b/.test(token.className ?? "")) {
      protectedText.fill(1, offset, offset + token.text.length);
    }

    offset += token.text.length;
  }

  const opener =
    /(?<![<\\])<<(-?)(?!<)[ \t]*(?:'([^'\r\n]+)'|"([^"$`\\\r\n]+)"|\\?([A-Za-z_][\w.-]*))/g;
  const result: EmbeddedSource[] = [];
  let match: RegExpExecArray | null;

  while ((match = opener.exec(text))) {
    if (protectedText[match.index]) continue;

    // Concatenated, expanded or otherwise non-literal delimiter words are not
    // covered by this small parser. Never reinterpret only their first fragment.
    if (
      opener.lastIndex < text.length &&
      !/[ \t\r\n;&|<>]/.test(text[opener.lastIndex])
    )
      continue;

    const lineStart = text.lastIndexOf("\n", match.index - 1) + 1;
    const lineEnd = text.indexOf("\n", opener.lastIndex);
    if (lineEnd < 0) break;

    const pending = [match];
    let next: RegExpExecArray | null;

    while ((next = opener.exec(text)) && next.index < lineEnd) {
      // Bash may already mark the rest of this header as a here-doc string.
      // Count every candidate conservatively: multiple redirects stay fallback.
      pending.push(next);
    }

    let bodyStart = lineEnd + 1;

    for (const opening of pending) {
      const delimiter = opening[2] ?? opening[3] ?? opening[4];
      let bodyEnd = bodyStart;
      let afterDelimiter = text.length;

      while (bodyEnd < text.length) {
        const newline = text.indexOf("\n", bodyEnd);
        const end = newline < 0 ? text.length : newline;
        const line = text.slice(bodyEnd, end).replace(/\r$/, "");
        const candidate = opening[1] ? line.replace(/^\t+/, "") : line;

        if (candidate === delimiter) {
          afterDelimiter = newline < 0 ? end : end + 1;
          break;
        }

        bodyEnd = newline < 0 ? text.length : newline + 1;
      }

      const prefix = text.slice(lineStart, opening.index);
      const descriptor = /(?:^|[ \t])(\d+)$/.exec(prefix);
      const inputPrefix = descriptor
        ? prefix.slice(0, descriptor.index)
        : prefix;
      const language =
        pending.length === 1 && (!descriptor || descriptor[1] === "0")
          ? bodyLanguage(inputPrefix, delimiter, text.slice(bodyStart, bodyEnd))
          : undefined;

      if (language && bodyEnd > bodyStart) {
        result.push({ start: bodyStart, end: bodyEnd, language });
      }

      bodyStart = afterDelimiter;
    }

    opener.lastIndex = bodyStart;
  }

  return result;
}
