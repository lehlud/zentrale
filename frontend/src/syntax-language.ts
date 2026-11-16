import { common, createLowlight } from "lowlight";

export const highlighter = createLowlight(common);

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

const interpreters: Record<string, string> = {
  sh: "bash",
  bash: "bash",
  dash: "bash",
  ash: "bash",
  ksh: "bash",
  zsh: "bash",
  python: "python",
  pypy: "python",
  node: "javascript",
  nodejs: "javascript",
  bun: "typescript",
  deno: "typescript",
  "ts-node": "typescript",
  "ts-node-esm": "typescript",
  tsx: "typescript",
  ruby: "ruby",
  jruby: "ruby",
  perl: "perl",
  php: "php",
  "php-cgi": "php",
  lua: "lua",
  luajit: "lua",
  rscript: "r",
  swift: "swift",
  kotlin: "kotlin",
  java: "java",
};

function interpreterLanguage(executable: string) {
  const name = executable.split("/").at(-1)?.toLowerCase() ?? "";
  const unversioned = name.replace(/\d+(?:\.\d+)*t?$/, "");

  return Object.hasOwn(interpreters, unversioned)
    ? interpreters[unversioned]
    : undefined;
}

export function interpreterCommand(words: string[]) {
  function command(executable: string, argumentsStart: number) {
    const language = interpreterLanguage(executable);

    return language
      ? { language, args: words.slice(argumentsStart) }
      : undefined;
  }

  const executable = words[0] ?? "";
  if (executable.split("/").at(-1) !== "env") {
    return command(executable, 1);
  }

  for (let index = 1; index < words.length; index++) {
    const word = words[index];
    if (word === "--") {
      return command(words[index + 1] ?? "", index + 2);
    }

    if (["-i", "--ignore-environment", "-S", "--split-string"].includes(word)) {
      continue;
    }

    if (["-u", "--unset", "-C", "--chdir"].includes(word)) {
      index++;
      continue;
    }

    if (/^(?:[A-Za-z_][A-Za-z0-9_]*=|--(?:unset|chdir)=|-[uC].)/.test(word)) {
      continue;
    }

    if (word.startsWith("--split-string=")) {
      return command(word.slice("--split-string=".length), index + 1);
    }

    if (word.startsWith("-S") && word.length > 2) {
      return command(word.slice(2), index + 1);
    }

    // Unknown env options may take arguments. Do not guess from their values.
    if (word.startsWith("-")) {
      return;
    }

    return command(word, index + 1);
  }
}

function shebangLanguage(text: string) {
  const firstLine = text.split("\n", 1)[0];
  if (!firstLine.startsWith("#!")) {
    return;
  }

  return interpreterCommand(firstLine.slice(2).trim().split(/\s+/))?.language;
}

// A recognized shebang describes execution, so it outranks the filename.
export function languageFor(label: string, text = "", startLine = 1) {
  const interpreter = startLine === 1 ? shebangLanguage(text) : undefined;
  if (interpreter && highlighter.registered(interpreter)) {
    return interpreter;
  }

  const name = label.toLowerCase().split(/[\\/]/).at(-1) ?? "";
  if (["command", "befehl", "shell", ".bashrc", ".zshrc"].includes(name)) {
    return "bash";
  }

  if (["makefile", "gnumakefile"].includes(name)) {
    return "makefile";
  }

  if (name === ".env" || name.startsWith(".env.")) {
    return "ini";
  }

  const extension = name.split(".").at(-1) ?? "";
  const language = Object.hasOwn(extensions, extension)
    ? extensions[extension]
    : name;

  return highlighter.registered(language) ? language : undefined;
}
