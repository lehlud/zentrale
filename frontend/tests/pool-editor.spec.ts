import { expect, test } from "@playwright/test";
import { mkdir, writeFile } from "node:fs/promises";
import { config } from "./fixtures/visual-data";

const yaml = `id: training
name: Training
max_machines: 1
shared_home: false
workspace_root: /srv/projects
machines:
  - id: worker
    name: Worker
    host: worker.example.org
    user: researcher
`;

for (const english of [false, true]) {
  test(`Monaco pool editor preserves YAML and keyboard navigation: ${english ? "mobile-en-dark" : "desktop-de-light"}`, async ({
    page,
    context,
  }) => {
    const label = (de: string, en: string) => (english ? en : de);
    const errors: string[] = [];
    page.on("pageerror", (error) => errors.push(error.message));
    await page.setViewportSize({ width: english ? 390 : 1440, height: 900 });
    await page.addInitScript(
      ({ english }) => {
        localStorage.setItem("zentrale-locale", english ? "en" : "de");
        localStorage.setItem("zentrale-theme", english ? "dark" : "light");
      },
      { english },
    );
    let saved = "";
    let attempts = 0;
    let imported = false;
    const poolKey = {
      id: 89,
      publicKey: "ecdsa-sha2-nistp521 MANAGED_POOL_PUBLIC_KEY",
      fingerprint: "SHA256:ManagedPoolKey",
      purpose: "pool",
      purposeId: "training",
      canDelete: false,
      canRevoke: false,
      usages: [
        { kind: "pool", id: "training", name: "Training", projectId: "" },
      ],
    };
    await page.routeWebSocket("**/events?*", () => {});
    await page.route("**/graphql", async (route) => {
      const { query, variables = {} } = route.request().postDataJSON();
      const send = (data: unknown) => route.fulfill({ json: { data } });
      if (query.includes("status{"))
        return send({ status: { needsSetup: false, authenticated: true } });
      if (query.includes("savePool")) {
        saved = variables.yaml;
        expect(saved).not.toMatch(/key_id|keyId/);
        attempts++;
        if (attempts === 1)
          return route.fulfill({
            json: {
              errors: [
                {
                  message:
                    "decode pool configuration: yaml: line 3: invalid test configuration",
                },
              ],
            },
          });
        imported = true;
        return send({ savePool: { id: "training", keyId: poolKey.id } });
      }
      return send(
        imported
          ? {
              ...config,
              pools: [
                {
                  ...config.pools[0],
                  id: "training",
                  name: "Training",
                  keyId: poolKey.id,
                },
              ],
              sshKeys: [...config.sshKeys, poolKey],
            }
          : config,
      );
    });
    await page.goto("/#/machines");
    // Opening the route must not load the large optional editor.
    expect(
      await page.evaluate(() =>
        performance
          .getEntriesByType("resource")
          .some((entry) => /monaco-runtime/.test(entry.name)),
      ),
    ).toBe(false);
    await page.getByRole("tab", { name: "Pools", exact: true }).click();
    const trigger = page.getByRole("button", {
      name: label("YAML importieren / aktualisieren", "Import / update YAML"),
    });
    await trigger.click();
    const dialog = page.getByRole("dialog");
    const editor = dialog.locator(".monaco-editor");
    await expect(editor).toBeVisible();
    await expect(editor).toHaveClass(new RegExp(english ? "vs-dark" : "vs"));
    const input = dialog.getByRole("textbox", { name: "YAML", exact: true });
    await expect(editor).not.toContainText("key_id");
    await expect(dialog.getByRole("combobox")).toHaveCount(0);
    await expect(input).toHaveAttribute("aria-required", "true");
    await input.focus();
    await page.keyboard.press("ControlOrMeta+a");
    await page.keyboard.press("Backspace");
    await dialog
      .getByRole("button", { name: label("Speichern", "Save"), exact: true })
      .click();
    await expect(input).toBeFocused();
    await expect(input).toHaveAttribute("aria-invalid", "true");
    expect(attempts).toBe(0);
    await context.grantPermissions(["clipboard-read", "clipboard-write"]);
    await page.evaluate((value) => navigator.clipboard.writeText(value), yaml);
    await page.keyboard.press("ControlOrMeta+v");
    await expect(input).toHaveAttribute("aria-invalid", "false");
    await expect(
      editor.locator(".margin-view-overlays .line-numbers").first(),
    ).toHaveText("1");
    await expect
      .poll(() =>
        editor
          .locator(".view-line")
          .first()
          .locator("span[class^=mtk]")
          .evaluateAll(
            (tokens) => new Set(tokens.map((token) => token.className)).size,
          ),
      )
      .toBeGreaterThan(1);
    await page.keyboard.press("ControlOrMeta+f");
    await expect(
      dialog.getByRole("textbox", {
        name: label("Suchen", "Find"),
        exact: true,
      }),
    ).toBeVisible();
    await page.keyboard.press("Escape");
    await expect(dialog).toBeVisible();
    await expect(input).toBeFocused();
    await page.keyboard.press("Tab");
    await expect(
      dialog.getByRole("button", {
        name: label("Abbrechen", "Cancel"),
        exact: true,
      }),
    ).toBeFocused();
    await dialog
      .getByRole("button", { name: label("Speichern", "Save"), exact: true })
      .click();
    await expect(input).toBeFocused();
    await expect(input).toHaveAttribute("aria-invalid", "true");
    expect(saved).toBe(yaml);
    await expect(dialog.getByRole("alert")).toContainText(
      "invalid test configuration",
    );
    const description = await input.getAttribute("aria-describedby");
    expect(description).toContain("error");
    await mkdir("artifacts/ui", { recursive: true });
    await page.screenshot({
      path: `artifacts/ui/monaco-${english ? "mobile-en-dark" : "desktop-de-light"}.png`,
      animations: "disabled",
    });
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth - innerWidth,
      ),
    ).toBeLessThanOrEqual(1);
    await dialog
      .getByRole("button", { name: label("Speichern", "Save"), exact: true })
      .click();
    await expect(dialog).toHaveCount(0);
    await expect(trigger).toBeFocused();
    expect(saved).toBe(yaml);
    expect(attempts).toBe(2);
    const copy = page.getByRole("button", {
      name: label("Öffentlichen Schlüssel kopieren", "Copy public key"),
      exact: true,
    });
    await expect(copy).toBeVisible();
    await expect(
      page.getByText(poolKey.fingerprint, { exact: true }),
    ).toBeVisible();
    await copy.click();
    expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(
      poolKey.publicKey,
    );
    const variant = english ? "mobile-en-dark" : "desktop-de-light";
    const screenshot = `pool-managed-key-${variant}.png`;
    await page.screenshot({
      path: `artifacts/ui/${screenshot}`,
      animations: "disabled",
    });
    await writeFile(
      `artifacts/ui/pool-managed-key-${variant}.json`,
      JSON.stringify({
        captures: [{ name: "managed-pool-key", file: screenshot }],
      }),
    );
    await trigger.click();
    await expect(dialog.locator(".monaco-editor")).toBeVisible();
    await page.keyboard.press("Escape");
    await expect(dialog).toHaveCount(0);
    expect(errors).toEqual([]);
  });
}

test("a failed Monaco download keeps YAML editable and savable", async ({
  page,
}) => {
  await page.addInitScript(() => localStorage.setItem("zentrale-locale", "en"));
  await page.routeWebSocket("**/events?*", () => {});
  await page.route("**/assets/monaco-runtime-*.js", (route) => route.abort());
  let saved = "";
  await page.route("**/graphql", async (route) => {
    const { query, variables = {} } = route.request().postDataJSON();
    let data: unknown = config;
    if (query.includes("status{"))
      data = { status: { needsSetup: false, authenticated: true } };
    if (query.includes("savePool")) {
      saved = variables.yaml;
      data = { savePool: { id: "training" } };
    }
    return route.fulfill({ json: { data } });
  });
  await page.goto("/#/machines");
  await page.getByRole("tab", { name: "Pools", exact: true }).click();
  await page.getByRole("button", { name: "Import / update YAML" }).click();
  const dialog = page.getByRole("dialog");
  await expect(dialog.getByRole("status")).toContainText(
    "You can still edit YAML",
  );
  await dialog.getByRole("textbox", { name: "YAML", exact: true }).fill(yaml);
  await dialog.getByRole("button", { name: "Save", exact: true }).click();
  await expect(dialog).toHaveCount(0);
  expect(saved).toBe(yaml);
});
