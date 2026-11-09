import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: {
    proxy: {
      "/graphql": "http://127.0.0.1:8080",
      "/events": { target: "ws://127.0.0.1:8080", ws: true },
      "/ide": { target: "http://127.0.0.1:8080", ws: true },
    },
  },
});
