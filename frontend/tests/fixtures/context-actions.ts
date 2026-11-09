import type { Locator, Page } from "@playwright/test";

export async function chooseProviderAccount(page: Page, name: string) {
  const selector = page.getByRole("button", {
    name: /Provider-Zugang|Provider account/,
  });
  if (await selector.isVisible()) {
    await selector.click();
    await page.getByRole("option", { name, exact: true }).click();
  }

  return page.locator("[data-provider-account]:visible").filter({
    has: page.getByRole("heading", { name, exact: true }),
  });
}

export async function openProviderModels(page: Page, name: string) {
  const account = await chooseProviderAccount(page, name);
  const view = account.getByRole("button", {
    name: /^(Modelle ansehen|View models)$/,
  });
  // Narrow layouts show the selected account's catalog directly below its status.
  if (await view.isVisible()) await view.click();
}

export async function chooseContextAction(
  page: Page,
  owner: Locator,
  name: string,
) {
  const trigger = owner.getByRole("button", {
    name: /^(Aktionen für|Actions for) /,
  });
  const label = await trigger.getAttribute("aria-label");
  await trigger.click();
  await page
    .getByRole("menu", { name: label!, exact: true })
    .getByRole("menuitem", { name, exact: true })
    .click();

  return trigger;
}

export async function setLogDiagnostics(page: Page, enabled: boolean) {
  const settings = page.getByRole("button", {
    name: /^(Einstellungen öffnen|Open settings)$/,
  });
  if (!(await settings.isVisible()))
    await page
      .getByRole("button", {
        name: /^(Menü öffnen|Open menu|Seitenleiste öffnen|Open sidebar)$/,
      })
      .click();

  await settings.click();
  await page
    .getByRole("menuitem", {
      name: /^(Darstellung und Sprache|Appearance and language)$/,
    })
    .click();
  const toggle = page.getByRole("switch", {
    name: /^(Diagnosedetails im Verlauf|Show diagnostics in activity logs)$/,
  });
  if ((await toggle.isChecked()) !== enabled) {
    await toggle.focus();
    await page.keyboard.press("Space");
  }

  await page.keyboard.press("Escape");
  await page
    .locator('[data-slot="modal-dialog"]')
    .waitFor({ state: "detached" });
  await page
    .locator('[data-slot="drawer-dialog"]')
    .waitFor({ state: "detached" });
}
