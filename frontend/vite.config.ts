import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";
import { transcriptViewer } from "./scripts/transcript-viewer";

export default defineConfig({
  plugins: [react(), tailwindcss(), transcriptViewer()],
  build: {
    // Serve fonts as files, including small Noto Emoji subsets. The app's
    // self-only font policy intentionally does not allow data: URLs.
    assetsInlineLimit: 0,
  },
  server: {
    proxy: {
      "/graphql": "http://127.0.0.1:8080",
      "/transcript-export": "http://127.0.0.1:8080",
      "/office": "http://127.0.0.1:8080",
      "/workspace-file": "http://127.0.0.1:8080",
      "/events": { target: "ws://127.0.0.1:8080", ws: true },
      "/terminal": { target: "ws://127.0.0.1:8080", ws: true },
      "/ide": { target: "http://127.0.0.1:8080", ws: true },
    },
  },
});
