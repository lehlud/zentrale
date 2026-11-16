import { expect, test } from "@playwright/test";

import { languageFor } from "../src/syntax-language";
import { sourceLines } from "../src/syntax-tokens";
import { pythonHereDoc } from "./fixtures/embedded-source";

function highlighted(text: string, label = "bash") {
  const lines = sourceLines(text, languageFor(label, text));
  expect(
    lines.map((line) => line.map((token) => token.text).join("")).join("\n"),
  ).toBe(text);

  return lines.flat();
}

function keywords(text: string, label = "bash") {
  return highlighted(text, label)
    .filter((token) => token.className?.split(" ").includes("hljs-keyword"))
    .map((token) => token.text);
}

test("the user's virtualenv command highlights Python inside its Shell wrapper", () => {
  for (const label of ["bash", "sh", "shell", "Befehl", "script.sh"]) {
    const tokens = highlighted(pythonHereDoc, label);
    expect(keywords(pythonHereDoc, label)).toEqual(
      expect.arrayContaining(["import", "async", "def", "await"]),
    );
    expect(
      tokens
        .filter((token) => token.className?.includes("hljs-string"))
        .map((token) => token.text),
    ).toContain("'PY'");
    expect(
      tokens.some(
        (token) => token.text === "15" && token.className === "hljs-number",
      ),
    ).toBe(true);
  }
});

for (const header of [
  "python3 - <<EOF",
  "python - <<'EOF'",
  'python - <<"EOF"',
  "python - <<\\EOF",
  "MODE='two words' /opt/venv/bin/python3.13t -I - <<EOF",
  '"/opt/my venv/bin/python" - <<EOF',
  "env -i MODE=test python - <<EOF",
  "env -u OLD python - <<EOF",
  "env -S python - <<EOF",
  "printf input | python - <<EOF",
  "true && python - <<EOF",
  "python - 0<<EOF",
]) {
  test(`literal stdin interpreter: ${header}`, () => {
    expect(
      keywords(
        `${header}\nasync def check():\n    return True\nEOF\necho done\n`,
      ),
    ).toContain("def");
  });
}

for (const [header, body, keyword] of [
  ["node --input-type=module - <<'JS'", "const answer = 42;", "const"],
  ["deno run - <<'TS'", "const answer: number = 42;", "const"],
  ["ruby <<'RB'", "def greet\n  true\nend", "def"],
  ["lua - <<'LUA'", "local answer = 42", "local"],
  ["cat <<'JSON'", '{"answer": true}', "true"],
] as const) {
  test(`embedded language: ${header}`, () => {
    const delimiter = header.match(/'([^']+)'$/)![1];
    expect(keywords(`${header}\n${body}\n${delimiter}\n`)).toContain(keyword);
  });
}

test("the interpreter takes precedence over a misleading delimiter", () => {
  expect(
    keywords("python - <<'JSON'\ndef greet():\n    return True\nJSON\n"),
  ).toContain("def");
});

test("tab-stripped delimiters and CRLF preserve every source character", () => {
  const text =
    "python - <<-'EOF'\r\n\tdef greet():\r\n\t    return True\r\n\tEOF\r\necho done\r\n";
  expect(keywords(text)).toContain("def");
  expect(
    highlighted(text).some(
      (token) => token.text === "echo" && token.className === "hljs-built_in",
    ),
  ).toBe(true);
});

test("independent here-docs and nested Shell/Python bodies retain their languages", () => {
  const text =
    "bash <<'SHELL'\npython - <<'PY'\ndef greet():\n    return True\nPY\nSHELL\nnode - <<'JS'\nconst answer = 42;\nJS\n";
  expect(keywords(text)).toEqual(expect.arrayContaining(["def", "const"]));
});

test("an unfinished here-doc can highlight a streaming body", () => {
  expect(keywords("python - <<'EOF'\ndef greet():\n")).toContain("def");
});

for (const text of [
  "# python - <<'PY'\ndef greet():\nPY\n",
  'echo "python - <<PY\ndef greet():\nPY\n"\n',
  "echo 'python - <<PY\ndef greet():\nPY\n'\n",
  "echo python - <<'EOF'\ndef greet():\nEOF\n",
  "unknown-wrapper python - <<'PY'\ndef greet():\nPY\n",
  "python helper.py <<'PY'\ndef greet():\nPY\n",
  "python -c 'print(1)' <<'PY'\ndef greet():\nPY\n",
  "python -m helper <<'PY'\ndef greet():\nPY\n",
  "node --eval='console.log(1)' <<'PY'\ndef greet():\nPY\n",
  "python - 2<<'PY'\ndef greet():\nPY\n",
  "cat <<'EOF'\ndef greet():\nEOF\n",
  "python - <<'PY'X\ndef greet():\nPYX\n",
  "python - <<< 'PY'\ndef greet():\nPY\n",
  "python - <<ONE <<TWO\ndef first():\nONE\ndef second():\nTWO\n",
]) {
  test(`ambiguous or non-code input stays with the Bash grammar: ${text.split("\n")[0]}`, () => {
    expect(keywords(text)).not.toContain("def");
  });
}

test("large and unknown-language sources keep the bounded plaintext fallback", () => {
  for (const [text, language] of [
    [pythonHereDoc.repeat(400), "bash"],
    [pythonHereDoc, "missing-grammar"],
  ]) {
    const lines = sourceLines(text, language);
    expect(lines.flat().every((token) => !token.className)).toBe(true);
    expect(
      lines.map((line) => line.map((token) => token.text).join("")).join("\n"),
    ).toBe(text);
  }
});
