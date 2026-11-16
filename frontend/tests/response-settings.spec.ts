import { expect, test } from "@playwright/test";
import { installPushFixture } from "./fixtures/push-fixture";
import { config, models, gitConnections } from "./fixtures/visual-data";

test("OpenAI transport and cache choices are saved without replacing credentials", async ({
  page,
}) => {
  await page.addInitScript(() => localStorage.setItem("zentrale-locale", "de"));
  const reads = (query: string) => {
    if (/\bstatus\s*\{/.test(query))
      return { status: { needsSetup: false, authenticated: true } };
    if (query.includes("models(providerId:")) return { models };
    if (query.includes("sidebarAgents")) return { sidebarAgents: [] };
    if (query.includes("gitConnections")) return { gitConnections };
    return {
      ...config,
      providers: config.providers.map((provider) => ({
        ...provider,
        responsesTransport: "",
        promptCacheMode: "",
      })),
    };
  };
  await installPushFixture(page, reads);
  let saved: Record<string, unknown> | undefined;
  await page.route("**/graphql", (route) => {
    const { query, variables = {} } = route.request().postDataJSON();
    if (query.includes("saveProvider(")) {
      saved = variables.input;
      return route.fulfill({
        json: {
          data: {
            saveProvider: { ...config.providers[0], ...saved, connected: true },
          },
        },
      });
    }
    return route.fulfill({ json: { data: reads(query) } });
  });
  await page.goto("/#/providers?provider=openai");
  const account = page.locator("[data-provider-account]:visible").filter({
    has: page.getByRole("heading", {
      name: "OpenAI · Forschung",
      exact: true,
    }),
  });
  await account.getByRole("button", { name: /Aktionen/ }).click();
  await page.getByRole("menuitem", { name: "Bearbeiten", exact: true }).click();
  const dialog = page.locator('[data-slot="modal-dialog"]:not([data-exiting])');
  await expect(
    dialog.getByLabel("Antwortübertragung", { exact: true }),
  ).toContainText("Automatisch");
  await dialog.getByLabel("Antwortübertragung", { exact: true }).click();
  await page.getByRole("option", { name: "WebSocket", exact: true }).click();
  await dialog.getByLabel("Zusätzliche Cache-Grenzen", { exact: true }).click();
  await page.getByRole("option", { name: "Aktivieren", exact: true }).click();
  await dialog.getByRole("button", { name: "Speichern", exact: true }).click();
  await expect.poll(() => saved?.responsesTransport).toBe("websocket");
  expect(saved?.promptCacheMode).toBe("explicit");
  expect(saved?.credential).toBe("");
});
