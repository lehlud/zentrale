import { defineConfig } from "@playwright/test";

export default defineConfig({
  testDir: "./tests",
  workers: 1,
  timeout: 60000,
  use: {
    baseURL: process.env.ZENTRALE_E2E_URL ?? "http://127.0.0.1:18080",
    headless: true,
    colorScheme: "dark",
    launchOptions: {
      executablePath: process.env.CHROMIUM_PATH,
      args: ["--no-sandbox", "--disable-dev-shm-usage"],
    },
    screenshot: "only-on-failure",
  },
  webServer: process.env.ZENTRALE_E2E_URL
    ? undefined
    : {
        command:
          "npm run build && cd ../backend && go run . -addr 127.0.0.1:18080 -db :memory: -frontend ../frontend/dist",
        url: "http://127.0.0.1:18080",
        reuseExistingServer: false,
        timeout: 120000,
      },
});
