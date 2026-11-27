import { createHash } from "node:crypto";
import { fileURLToPath } from "node:url";
import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import { build, type Plugin } from "vite";

const entry = fileURLToPath(
  new URL("../src/transcript-viewer.tsx", import.meta.url),
);
const filename = "assets/transcript-viewer.html";

async function viewerTemplate() {
  const result = await build({
    configFile: false,
    publicDir: false,
    plugins: [react(), tailwindcss()],
    define: { "process.env.NODE_ENV": JSON.stringify("production") },
    resolve: {
      alias: {
        "./javascript-format.worker?worker": `${fileURLToPath(new URL("../src/javascript-format.worker.ts", import.meta.url))}?worker&inline`,
      },
    },
    build: {
      write: false,
      lib: { entry, name: "ZentraleTranscript", formats: ["iife"] },
    },
  });

  const assets = (Array.isArray(result) ? result : [result]).flatMap(
    (bundle) => ("output" in bundle ? bundle.output : []),
  );
  const js = assets.find((item) => item.type === "chunk");
  const css = assets.find(
    (item) => item.type === "asset" && item.fileName.endsWith(".css"),
  );

  if (js?.type !== "chunk" || css?.type !== "asset" || assets.length !== 2) {
    throw new Error(
      "The transcript viewer must build to one JS bundle and one CSS bundle",
    );
  }

  const script = js.code.replaceAll(/<\/script/gi, "<\\/script");
  const hash = createHash("sha256").update(script).digest("base64");
  const policy = `default-src 'none'; script-src 'sha256-${hash}'; style-src 'unsafe-inline'; img-src data:; font-src data:; worker-src blob: data:; base-uri 'none'; form-action 'none'`;

  return `<!doctype html>
<html lang="de"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="referrer" content="no-referrer">
<meta http-equiv="Content-Security-Policy" content="${policy}">
<title>Zentrale · HTML</title><style>${css.source}</style></head>
<body><div id="root"></div>
<script id="transcript-data" type="application/json">__ZENTRALE_TRANSCRIPT_DATA__</script>
<script>${script}</script></body></html>`;
}

export function transcriptViewer(): Plugin {
  return {
    name: "zentrale-transcript-viewer",
    async generateBundle() {
      const source = await viewerTemplate();
      this.emitFile({ type: "asset", fileName: filename, source });
    },
    configureServer(server) {
      server.middlewares.use(`/${filename}`, (_request, response, next) => {
        viewerTemplate().then((template) => {
          response.setHeader("Content-Type", "text/html; charset=utf-8");
          response.end(template);
        }, next);
      });
    },
  };
}
