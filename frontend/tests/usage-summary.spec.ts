import { expect, test } from "@playwright/test";

import {
  agent,
  agents,
  config,
  project,
  transcript,
} from "./fixtures/visual-data";

test("usage shows reported cache counts separately from missing measurements", async ({
  page,
}) => {
  await page.addInitScript(() => localStorage.setItem("zentrale-locale", "de"));
  await page.routeWebSocket("**/events?*", () => {});
  await page.route("**/graphql", (route) => {
    const { query } = route.request().postDataJSON();
    if (query.includes("recentMessages")) {
      expect(query).toContain("cacheInputTokens");
      expect(query).toContain("unknownTokenCounts");

      return route.fulfill({
        json: {
          data: {
            ...transcript,
            usageSummary: {
              ...transcript.usageSummary,
              unknownTokenCounts: 2,
              unknownCacheWrites: 3,
            },
          },
        },
      });
    }

    return route.fulfill({
      json: {
        data: query.includes("status{")
          ? { status: { needsSetup: false, authenticated: true } }
          : query.includes("agents(projectId:")
            ? { project, agents }
            : config,
      },
    });
  });
  await page.goto(`/#/projects/${project.id}?agent=${agent.id}`);
  await page.getByRole("button", { name: /^Agentendetails für / }).click();
  const summary = page
    .locator("details")
    .filter({ has: page.locator("summary", { hasText: "Tokens" }) })
    .first();
  await summary.locator("summary").click();

  await expect(summary).toContainText("75% Cache gelesen (gemeldete Tokens)");
  await expect(summary).toContainText("6.000 Cache-Tokens geschrieben");
  await expect(summary).toContainText(
    "Tokenzahlen geschätzt oder unbekannt: 2 Anfragen",
  );
  await expect(summary).toContainText(
    "Cache-Schreibdaten fehlen für 3 Anfragen",
  );
  await expect(summary.locator("summary")).toContainText("≈");
});
