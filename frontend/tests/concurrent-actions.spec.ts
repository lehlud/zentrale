import { expect, test } from "@playwright/test";

import { installPushFixture } from "./fixtures/push-fixture";
import {
  adminStatus,
  config,
  gitConnections,
  project,
} from "./fixtures/visual-data";

test("a pending deletion leaves other project rows and reads usable", async ({
  page,
}) => {
  const second = { ...project, id: "second", name: "Independent project" };
  const state = { ...structuredClone(config), projects: [project, second] };
  const read = (query: string) => {
    if (query.includes("status{")) return { status: adminStatus };
    if (query.includes("models(")) return { models: [] };
    if (query.includes("sidebarAgents")) return { sidebarAgents: [] };
    if (query.includes("gitConnections")) return { ...state, gitConnections };
    return state;
  };
  await installPushFixture(page, read);
  let release = () => {};
  let deleting = false;
  const held = new Promise<void>((resolve) => {
    release = resolve;
  });
  await page.route("**/graphql", async (route) => {
    const { query } = route.request().postDataJSON();
    if (query.includes("deleteResource")) {
      deleting = true;
      await held;
      await route.fulfill({ json: { data: { deleteResource: true } } });
      return;
    }
    await route.fulfill({ json: { data: read(query) } });
  });
  await page.addInitScript(() => localStorage.setItem("zentrale-locale", "de"));
  try {
    await page.goto("/#/projects");
    await page
      .getByRole("button", {
        name: `Aktionen für ${project.name}`,
        exact: true,
      })
      .click();
    await page
      .getByRole("menuitem", { name: "Entfernen", exact: true })
      .click();
    await page
      .getByRole("dialog")
      .getByRole("button", { name: "Entfernen", exact: true })
      .click();
    await expect.poll(() => deleting).toBe(true);

    const independent = page.getByRole("button", {
      name: `Aktionen für ${second.name}`,
      exact: true,
    });
    await expect(independent).toBeEnabled();
    await independent.click();
    await page
      .getByRole("menuitem", { name: "Bearbeiten", exact: true })
      .click();
    const editor = page.getByRole("dialog", {
      name: "Projekt bearbeiten",
      exact: true,
    });
    await expect(editor).toBeVisible();
    await page
      .getByRole("dialog", { name: "Projekt bearbeiten", exact: true })
      .getByRole("button", { name: "Abbrechen", exact: true })
      .click();

    await page.goto("/#/machines");
    await expect(
      page.getByRole("heading", { name: config.machines[0].name, exact: true }),
    ).toBeVisible();
    expect(deleting).toBe(true);
  } finally {
    release();
  }
});
