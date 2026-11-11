import { expect, test } from "@playwright/test";
import { config, project } from "./fixtures/visual-data";

for (const locale of ["de", "en"]) {
  test(`project deletion warns about its cascade and supports cancellation (${locale})`, async ({
    page,
  }) => {
    const english = locale === "en";
    const label = (de: string, en: string) => (english ? en : de);
    let removed = 0;
    await page.addInitScript(
      (language) => localStorage.setItem("zentrale-locale", language),
      locale,
    );
    await page.routeWebSocket("**/events?*", () => {});
    await page.route("**/graphql", async (route) => {
      const { query, variables = {} } = route.request().postDataJSON();
      if (query.includes("deleteResource")) {
        expect(variables).toEqual({ kind: "project", id: project.id });
        removed++;
        return route.fulfill({ json: { data: { deleteResource: true } } });
      }
      if (query.includes("status{"))
        return route.fulfill({
          json: {
            data: { status: { needsSetup: false, authenticated: true } },
          },
        });
      return route.fulfill({
        json: { data: { ...config, projects: removed ? [] : [project] } },
      });
    });
    await page.goto("/#/projects");
    const remove = async () => {
      await page
        .getByRole("button", {
          name: label(
            `Aktionen für ${project.name}`,
            `Actions for ${project.name}`,
          ),
          exact: true,
        })
        .click();
      await page
        .getByRole("menuitem", {
          name: label("Entfernen", "Remove"),
          exact: true,
        })
        .click();
    };
    await remove();
    const dialog = page.getByRole("dialog");
    await expect(dialog).toContainText(
      label(
        "Projekt mit allen Agenten, Verläufen und Quellen dauerhaft entfernen? Repository-Dateien bleiben erhalten.",
        "Permanently remove this project with all agents, history and sources? Repository files will be preserved.",
      ),
    );
    await dialog
      .getByRole("button", { name: label("Abbrechen", "Cancel"), exact: true })
      .click();
    await expect(dialog).not.toBeVisible();
    expect(removed).toBe(0);
    await remove();
    await dialog
      .getByRole("button", { name: label("Entfernen", "Remove"), exact: true })
      .click();
    await expect.poll(() => removed).toBe(1);
    await expect(
      page.getByRole("button", {
        name: label(
          `Aktionen für ${project.name}`,
          `Actions for ${project.name}`,
        ),
        exact: true,
      }),
    ).toHaveCount(0);
  });
}
