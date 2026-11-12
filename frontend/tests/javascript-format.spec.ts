import { expect, test } from "@playwright/test";
import { formatJavaScriptForDisplay } from "../src/javascript-format";

test("formats compact combined tools with top-level await", async () => {
  const source =
    'for(const x of [1,2]){await tools.exec({cmd:"printf x",timeout_ms:1000});}';
  expect(await formatJavaScriptForDisplay(source)).toBe(
    'for (const x of [1, 2]) {\n  await tools.exec({ cmd: "printf x", timeout_ms: 1000 });\n}\n',
  );
});

test("formatting parses without executing or resolving imports", async () => {
  const source =
    'globalThis.__zentraleFormatterExecuted=true;await import("https://invalid.example/never-fetch.js");';
  expect(await formatJavaScriptForDisplay(source)).toContain(
    "globalThis.__zentraleFormatterExecuted = true;",
  );
  expect(Object.hasOwn(globalThis, "__zentraleFormatterExecuted")).toBe(false);
});

test("invalid and oversized snippets retain their exact original contents", async () => {
  for (const source of ["for(const", "//" + "a".repeat(65536)]) {
    expect(await formatJavaScriptForDisplay(source)).toBe(source);
  }
});
