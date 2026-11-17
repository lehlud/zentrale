import { expect, test } from "@playwright/test";

import { installPushFixture } from "./fixtures/push-fixture";
import { config, gitConnections } from "./fixtures/visual-data";

for (const width of [1440, 390]) {
  test(`settings show summaries and keep credential drafts inside dialogs: ${width}`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height: 900 });
    await page.addInitScript(() =>
      localStorage.setItem("zentrale-locale", "de"),
    );
    const writes: string[] = [];
    const resolve = (query: string) => {
      if (query.includes("status{"))
        return { status: { authenticated: true, needsSetup: false } };
      if (query.includes("gitConnections")) return { gitConnections };
      if (query.includes("huggingFaceConnection"))
        return {
          huggingFaceConnection: {
            connected: false,
            userName: "",
            authMode: "",
            checkedAt: "",
            oauthConfigured: false,
            oauthClientId: "",
            oauthConfidential: false,
            login: { id: "", authorizationUrl: "", expiresAt: "" },
          },
        };
      return config;
    };
    await installPushFixture(page, resolve);
    await page.route("**/graphql", async (route) => {
      const { query, variables } = route.request().postDataJSON();
      if (query.includes("setGitHubToken")) {
        writes.push(variables.token);
        return route.fulfill({ json: { data: { setGitHubToken: true } } });
      }
      return route.fulfill({ json: { data: resolve(query) } });
    });
    await page.goto("/#/settings");
    const identity = page.getByRole("region", {
      name: "Standard-Identität für Commits",
      exact: true,
    });
    await expect(
      identity.getByText(config.commitIdentity.name, { exact: true }),
    ).toBeVisible();
    await expect(page.locator('input[type="password"]')).toHaveCount(0);
    await page.screenshot({
      path: `artifacts/ui/settings-overview-${width}.png`,
      fullPage: true,
      animations: "disabled",
    });

    await identity
      .getByRole("button", { name: "Bearbeiten", exact: true })
      .click();
    let dialog = page.getByRole("dialog", {
      name: "Standard-Identität für Commits",
      exact: true,
    });
    await dialog
      .getByLabel("Name für Commits", { exact: true })
      .fill("Discard this draft");
    await dialog
      .getByRole("button", { name: "Schließen", exact: true })
      .click();
    await identity
      .getByRole("button", { name: "Bearbeiten", exact: true })
      .click();
    await expect(
      dialog.getByLabel("Name für Commits", { exact: true }),
    ).toHaveValue(config.commitIdentity.name);
    await dialog
      .getByRole("button", { name: "Schließen", exact: true })
      .click();

    const openAccess = page.getByRole("button", {
      name: "Erweiterter API-Zugang",
      exact: true,
    });
    await openAccess.click();
    dialog = page.getByRole("dialog", {
      name: "GitHub-API-Zugang",
      exact: true,
    });
    const token = dialog.getByLabel("GitHub-Token", { exact: true });
    await token.fill("discarded-sensitive-draft");
    await dialog
      .getByRole("button", { name: "Abbrechen", exact: true })
      .click();
    expect(writes).toEqual([]);
    await openAccess.click();
    await expect(token).toHaveValue("");
    await token.fill("saved-sensitive-token");
    await token.press("Enter");
    await expect(dialog).toHaveCount(0);
    expect(writes).toEqual(["saved-sensitive-token"]);
    expect(
      await page.evaluate(() =>
        JSON.stringify({ ...localStorage, ...sessionStorage }),
      ),
    ).not.toContain("sensitive");
    await expect(page.locator("body")).not.toContainText(
      "saved-sensitive-token",
    );
    await openAccess.click();
    await expect(token).toHaveValue("");
    await dialog
      .getByRole("button", {
        name: "Gespeicherten API-Token entfernen",
        exact: true,
      })
      .click();
    await expect(dialog).toHaveCount(0);
    expect(writes).toEqual(["saved-sensitive-token", ""]);
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth - innerWidth,
      ),
    ).toBeLessThanOrEqual(1);
  });
}
