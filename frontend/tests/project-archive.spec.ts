import { expect, test } from "@playwright/test";
import { installPushFixture } from "./fixtures/push-fixture";
import { config, project } from "./fixtures/visual-data";

test("archive keeps a project in the separate sidebar list", async ({
  page,
}) => {
  const status = {
    authenticated: true,
    needsSetup: false,
    oidc: false,
    user: { id: "initial-admin", username: "admin", role: "admin" },
  };
  let archived = false;
  const resolve = (query: string) =>
    query.includes("status{")
      ? { status }
      : { ...config, projects: [{ ...project, archived }] };

  const push = await installPushFixture(page, resolve);
  await page.route("**/graphql", async (route) => {
    const { query, variables } = route.request().postDataJSON();
    if (query.includes("archiveProject")) {
      expect(variables).toEqual({ id: project.id });
      archived = true;
      await push.push();

      return route.fulfill({ json: { data: { archiveProject: true } } });
    }

    return route.fulfill({ json: { data: resolve(query) } });
  });
  await page.goto("/#/projects");

  await page
    .getByRole("button", { name: `Aktionen für ${project.name}`, exact: true })
    .click();
  await page
    .getByRole("menuitem", { name: "Archivieren", exact: true })
    .click();
  const dialog = page.getByRole("dialog", {
    name: `${project.name} archivieren`,
  });
  await expect(dialog).toContainText("Unterhaltungen bleiben lesbar.");
  await expect(dialog).toContainText(
    "Projektdaten auf den Fernmaschinen entfernt.",
  );
  await dialog
    .getByRole("button", { name: "Archivieren", exact: true })
    .click();

  const sidebar = page.getByRole("navigation", {
    name: "Projekte und Unterhaltungen",
  });
  const archives = sidebar.getByRole("list", { name: "Archivierte Projekte" });
  await expect(
    archives.getByRole("link", { name: project.name }),
  ).toBeVisible();
  await expect(dialog).not.toBeVisible();
});
