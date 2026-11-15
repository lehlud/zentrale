import { expect, test } from "@playwright/test";
import { languageFor } from "../src/syntax-language";

for (const [command, language] of [
  ["/bin/sh", "bash"],
  ["/usr/bin/bash -eu", "bash"],
  ["/bin/dash", "bash"],
  ["/bin/ash", "bash"],
  ["/usr/local/bin/zsh", "bash"],
  ["/usr/bin/python3.12 -I", "python"],
  ["/usr/bin/python3.13t", "python"],
  ["/usr/bin/pypy3", "python"],
  ["/usr/bin/node", "javascript"],
  ["/usr/bin/nodejs", "javascript"],
  ["/usr/bin/deno run", "typescript"],
  ["/usr/bin/bun", "typescript"],
  ["/usr/bin/ts-node", "typescript"],
  ["/usr/bin/tsx", "typescript"],
  ["/usr/bin/ruby3.3", "ruby"],
  ["/usr/bin/perl5.40", "perl"],
  ["/usr/bin/php8.4", "php"],
  ["/usr/bin/lua5.4", "lua"],
  ["/usr/bin/luajit", "lua"],
  ["/usr/bin/Rscript", "r"],
  ["/usr/bin/swift", "swift"],
  ["/usr/bin/kotlin", "kotlin"],
  ["/usr/bin/java --source 21", "java"],
  ["/usr/bin/env bash", "bash"],
  ["/usr/bin/env python3", "python"],
  ["/usr/bin/env -S python3.12 -I", "python"],
  ["/usr/bin/env --split-string=node --trace-warnings", "javascript"],
  ["/usr/bin/env -Sbash -eu", "bash"],
  ["/usr/bin/env -S -i MODE=test bash -eu", "bash"],
  ["/usr/bin/env -S -u python3 bash", "bash"],
  ["/usr/bin/env -S --unset=python3 bash", "bash"],
  ["/usr/bin/env -S -C python3 bash", "bash"],
  ["/usr/bin/env -S --chdir=python3 bash", "bash"],
  ["/usr/bin/env -- python3", "python"],
] as const) {
  test(`recognized shebang overrides the filename: ${command}`, () => {
    expect(languageFor("misleading.json", `#!${command}\nignored body`)).toBe(
      language,
    );
    expect(languageFor("extensionless", `#!${command}`)).toBe(language);
  });
}

for (const [label, content, language] of [
  ["script.py", "#!/opt/custom-wrapper\nprint(1)", "python"],
  ["script.sh", "#!/bin/fish\necho hello", "bash"],
  ["script.js", "#!/usr/bin/custom-python3", "javascript"],
  ["script.js", "#!/usr/bin/constructor", "javascript"],
  ["script.js", "#!/usr/bin/__proto__", "javascript"],
  ["constructor", "", undefined],
  ["__proto__", "", undefined],
  ["script.py", "#!/usr/bin/env unknown python3", "python"],
  ["script.sh", "#!/usr/bin/env --unknown-option python3", "bash"],
  ["script.sh", "#!/usr/bin/env -u python3", "bash"],
  ["script.py", "#!/usr/bin/env", "python"],
  ["script.py", "#!", "python"],
  ["script.py", "\n#!/usr/bin/env bash", "python"],
  ["script.py", " #!/usr/bin/env bash", "python"],
  ["script.py", "# comment\n#!/bin/bash", "python"],
  ["script.py", "\ufeff#!/bin/bash", "python"],
  ["script.py", "print(1)", "python"],
  ["src/COMPONENT.TSX", "", "typescript"],
  ["C:\\repo\\main.go", "", "go"],
  ["Makefile", "", "makefile"],
  [".env.production", "", "ini"],
  [".bashrc", "", "bash"],
  ["Befehl", "", "bash"],
  ["javascript", "", "javascript"],
  ["unknown", "#!/opt/custom-wrapper", undefined],
  ["", "", undefined],
] as const) {
  test(`filename fallback: ${label} / ${JSON.stringify(content)}`, () => {
    expect(languageFor(label, content)).toBe(language);
  });
}

test("shebang whitespace and CRLF are accepted without inspecting later lines", () => {
  expect(languageFor("wrong.go", "#! \t/usr/bin/env bash\r\necho hello")).toBe(
    "bash",
  );
});

test("known file offsets distinguish a shebang from a later comment", () => {
  const source = "#!/usr/bin/env bash\ndef greet():\n    return 1";

  expect(languageFor("script.py", source, 1)).toBe("bash");
  expect(languageFor("script.py", source, 201)).toBe("python");
});
