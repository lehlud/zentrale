import { expect, test } from "@playwright/test";

import { bootstrapData, isTranscriptQuery } from "./fixtures/bootstrap-data";
import { agents, config, project, transcript } from "./fixtures/visual-data";

test("drag projects directly in the sidebar and keep their order after reload", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.addInitScript(() => localStorage.setItem("zentrale-locale", "de"));
  await page.routeWebSocket("**/events?*", () => {});

  const alpha = { ...project, name: "Alpha" };
  const beta = { ...project, id: "beta", name: "Beta" };
  const gamma = { ...project, id: "gamma", name: "Gamma" };
  const delta = { ...project, id: "delta", name: "Delta" };
  let listed = [alpha, beta, gamma];

  await page.route("**/graphql", (route) => {
    const { query } = route.request().postDataJSON();
    let data: unknown = { ...config, projects: listed };

    if (query.includes("status{")) {
      data = {
        status: {
          needsSetup: false,
          authenticated: true,
          oidc: false,
          user: { id: "initial-admin", username: "admin", role: "admin" },
        },
      };
    } else if (isTranscriptQuery(query)) {
      data = transcript;
    } else if (query.includes("agents(projectId:")) {
      data = { project: alpha, agents };
    }

    return route.fulfill({ json: { data: bootstrapData(query, data) } });
  });

  await page.goto("/#/projects");

  const sidebar = page.getByRole("navigation", {
    name: "Projekte und Unterhaltungen",
  });
  const list = sidebar.getByRole("list", { name: "Projekte", exact: true });
  const rows = list.locator(":scope > li");
  const row = (name: string) =>
    rows.filter({ has: page.getByRole("link", { name, exact: true }) });
  const names = rows.locator(":scope > div > a");
  const drag = (from: string, to: string) =>
    row(from)
      .locator("[draggable=true]")
      .dragTo(row(to).getByRole("link", { name: to, exact: true }));

  await expect(names).toHaveText(["Alpha", "Beta", "Gamma"]);
  await drag("Gamma", "Alpha");
  await expect(names).toHaveText(["Gamma", "Alpha", "Beta"]);
  await drag("Gamma", "Beta");
  await expect(names).toHaveText(["Alpha", "Beta", "Gamma"]);
  await drag("Beta", "Alpha");
  await expect(names).toHaveText(["Beta", "Alpha", "Gamma"]);
  await expect(page).toHaveURL(/#\/projects$/);

  await page.reload();
  await expect(names).toHaveText(["Beta", "Alpha", "Gamma"]);

  listed = [delta, { ...alpha, name: "Alpha umbenannt" }, beta];
  await page.reload();
  await expect(names).toHaveText(["Beta", "Alpha umbenannt", "Delta"]);
  await row("Alpha umbenannt")
    .getByRole("link", { name: "Alpha umbenannt" })
    .click();
  await expect(page).toHaveURL(new RegExp(`#/projects/${alpha.id}(?:\\?|$)`));
});
