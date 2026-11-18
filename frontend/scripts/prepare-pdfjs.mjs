import { createHash } from "node:crypto";
import {
  mkdir,
  mkdtemp,
  readFile,
  rename,
  rm,
  writeFile,
} from "node:fs/promises";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { unzipSync } from "fflate";

// Mozilla's generic release includes the complete viewer UI and locales.
const version = "6.5.438";
const digest =
  "fcbe9ba21d9911843e3502adc178a7f549a524a4055f887bedc942e5490c6ba1";
const publicDirectory = fileURLToPath(new URL("../public/", import.meta.url));
const output = join(publicDirectory, "pdfjs");
const installed = await readFile(join(output, ".sha256"), "utf8").catch(
  (error) => {
    if (error.code !== "ENOENT") throw error;
    return "";
  },
);
if (installed === digest) process.exit(0);

const url = `https://github.com/mozilla/pdf.js/releases/download/v${version}/pdfjs-${version}-dist.zip`;
const response = await fetch(url, { signal: AbortSignal.timeout(90_000) });
if (!response.ok)
  throw new Error(`PDF.js download failed (${response.status})`);
const archive = new Uint8Array(await response.arrayBuffer());
if (createHash("sha256").update(archive).digest("hex") !== digest) {
  throw new Error("PDF.js release checksum mismatch");
}

const files = unzipSync(archive, {
  filter: ({ name }) =>
    !name.endsWith("/") && !name.endsWith(".map") && !name.endsWith(".pdf"),
});
await mkdir(publicDirectory, { recursive: true });
const staging = await mkdtemp(join(dirname(publicDirectory), ".pdfjs-"));
try {
  for (const [name, bytes] of Object.entries(files)) {
    if (
      name.split("/").some((part) => !part || part === ".." || part === ".") ||
      name.includes("\\")
    ) {
      throw new Error(`Unexpected PDF.js archive entry ${name}`);
    }
    const path = join(staging, name);
    await mkdir(dirname(path), { recursive: true });
    await writeFile(path, bytes);
  }
  await writeFile(join(staging, ".sha256"), digest);
  await rm(output, { recursive: true, force: true });
  await rename(staging, output);
} finally {
  await rm(staging, { recursive: true, force: true });
}
