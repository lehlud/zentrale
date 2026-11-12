import { strict as assert } from "node:assert";
import { annotation, failures } from "./report_browser_tests.mjs";

const report = {
  suites: [
    {
      file: "test.ts",
      specs: [
        {
          title: "bad",
          line: 42,
          tests: [
            { status: "expected", results: [{ status: "passed" }] },
            {
              status: "unexpected",
              results: [
                {
                  errors: [
                    { message: "\u001b[31mfail\u001b[0m\n::error::spoof" },
                  ],
                },
              ],
            },
          ],
        },
      ],
      suites: [
        {
          specs: [
            {
              file: "nested.ts",
              line: 1,
              title: "nested",
              tests: [
                { status: "unexpected", results: [{ status: "timedOut" }] },
              ],
            },
          ],
        },
      ],
    },
  ],
};
const entries = failures(report);
assert.equal(entries.length, 2);
assert.match(entries[0], /^test.ts:42: bad\nfail/);
assert.match(entries[1], /nested.ts:1: nested\ntimedOut/);
assert.ok(!entries[0].includes("\u001b"));
assert.ok(annotation(entries[0]).includes("%0A::error::spoof"));
assert.ok(!annotation(entries[0]).includes("\n"));
assert.ok(annotation("100%\r").includes("100%25%0D"));
console.log("browser report parsing and annotation escaping passed");
