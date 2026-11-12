import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { fileURLToPath } from "node:url";

// A separate step avoids GitHub's ten-annotation-per-step limit hiding all but
// the first few failing tests. The complete JSON remains an uploaded artifact.
export function failures(report) {
  const output = [];
  const clean = (text) => String(text ?? "").replace(/\x1b\[[0-9;]*m/g, "");
  function visit(suite) {
    for (const spec of suite.specs ?? []) {
      for (const test of spec.tests ?? []) {
        if (test.status !== "unexpected") continue;
        const result = test.results?.at(-1);
        const error =
          result?.errors
            ?.map((error) => error.message ?? error.value)
            .join("\n") ||
          result?.status ||
          "failed";
        output.push(
          `${spec.file ?? suite.file}:${spec.line ?? 0}: ${spec.title}\n${clean(error).slice(0, 900)}`,
        );
      }
    }
    for (const child of suite.suites ?? []) visit(child);
  }
  for (const suite of report.suites ?? []) visit(suite);
  for (const error of report.errors ?? [])
    output.push(`Runner: ${clean(error.message).slice(0, 900)}`);
  return output;
}

export function annotation(text) {
  return `::error title=Browser failure summary::${text.replaceAll("%", "%25").replaceAll("\r", "%0D").replaceAll("\n", "%0A")}`;
}

if (
  process.argv[1] &&
  resolve(process.argv[1]) === fileURLToPath(import.meta.url)
) {
  const entries = failures(JSON.parse(readFileSync(process.argv[2], "utf8")));
  // Keep every failed test title, with bounded diagnostic excerpts.
  const batchSize = Math.max(1, Math.ceil(entries.length / 8));
  for (let offset = 0; offset < entries.length; offset += batchSize) {
    const batch = entries.slice(offset, offset + batchSize).join("\n\n");
    console.log(annotation(batch));
  }
  if (!entries.length)
    console.log(
      "No test-level diagnostics recorded; inspect runner logs and report artifact.",
    );
}
