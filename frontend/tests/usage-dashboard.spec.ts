import { expect, test } from "@playwright/test";
import { adminStatus, config } from "./fixtures/visual-data";

test("usage chart includes empty UTC days and stacks both metrics by the selected dimension", async ({
  page,
}, testInfo) => {
  await page.clock.setFixedTime(new Date("2024-03-05T15:00:00Z"));
  await page.addInitScript(() => localStorage.setItem("zentrale-locale", "de"));
  await page.routeWebSocket("**/events?*", () => {});
  const entries = [
    {
      userId: "u1",
      projectId: "p1",
      providerId: "pr1",
      modelId: "m1",
      uncachedTokens: 10,
      realCost: 0.25,
    },
    {
      userId: "u2",
      projectId: "",
      providerId: "pr2",
      modelId: "m2",
      uncachedTokens: 30,
      realCost: 0.5,
    },
  ].map((row) => ({
    ...row,
    day: "2024-02-29",
    purpose: "main",
    cachedTokens: 2,
    outputTokens: 3,
    requestCount: 1,
  }));

  await page.route("**/graphql", (route) => {
    const { query, variables } = route.request().postDataJSON();
    if (query.includes("status{"))
      return route.fulfill({ json: { data: { status: adminStatus } } });
    const data = query.includes("usageReport(")
      ? {
          usageReport: entries.filter(
            ({ day }) => variables.from <= day && day < variables.to,
          ),
          users: [
            { id: "u1", name: "Ada" },
            { id: "u2", name: "Lin" },
          ],
          projects: [{ id: "p1", name: "Atlas" }],
          modelProviders: [
            { id: "pr1", name: "Alpha" },
            { id: "pr2", name: "Beta" },
          ],
        }
      : config;
    return route.fulfill({ json: { data } });
  });

  await page.goto("/#/usage");
  const chart = page.getByRole("region", { name: "Verbrauch pro Tag" });
  const bars = chart.getByRole("img");
  await expect(bars).toHaveCount(30);
  await expect(bars.first()).toHaveAccessibleName("2024-02-05: 0");
  await expect(bars.last()).toHaveAccessibleName("2024-03-05: 0");

  const activeDay = bars.filter({ has: page.locator("[data-usage-segment]") });
  await expect(activeDay).toHaveCount(1);
  const group = chart.getByRole("combobox", { name: "Aufschlüsselung" });
  const legend = chart.getByRole("list", { name: "Legende" });

  for (const [value, labels] of [
    ["userId", ["Ada", "Lin"]],
    ["projectId", ["Atlas", "Ohne Projekt"]],
    ["modelId", ["m1", "m2"]],
    ["providerId", ["Alpha", "Beta"]],
  ] as const) {
    await group.selectOption(value);
    await expect(activeDay.locator("[data-usage-segment]")).toHaveCount(2);
    await expect(legend.getByRole("listitem")).toHaveText([...labels]);
    await expect(activeDay).toHaveAccessibleName(
      `2024-02-29: 50 · ${labels[0]}: 15 · ${labels[1]}: 35`,
    );
  }

  await chart.getByRole("button", { name: "Kosten", exact: true }).click();
  await expect(activeDay).toHaveAccessibleName(
    /2024-02-29: 0,75.*Alpha: 0,25.*Beta: 0,50/,
  );
  await page.screenshot({
    path: testInfo.outputPath("usage-stacked.png"),
    animations: "disabled",
  });

  await page.getByLabel("Bis (exklusiv)").fill("2024-02-08");
  await expect(bars).toHaveCount(3);
  await expect(chart.locator("[data-usage-segment]")).toHaveCount(0);
  await expect(legend).toHaveCount(0);
  await expect(
    page.getByText("Für diesen Zeitraum gibt es keine Nutzung."),
  ).toBeVisible();
});
