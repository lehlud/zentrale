import { expect, test } from "@playwright/test";
import type { FileShareConfig } from "../src/generated";
import { installPushFixture } from "./fixtures/push-fixture";
import { adminStatus, config } from "./fixtures/visual-data";

test("tests the DomainShare draft as an eligible user without saving it", async ({
  page,
}) => {
  await page.addInitScript(() => localStorage.setItem("zentrale-locale", "de"));

  const users = [
    { id: "alice", name: "Alice", username: "alice", disabled: false },
    { id: "bob", name: "Bob", username: "bob", disabled: false },
    { id: "carol", name: "Carol", username: "carol", disabled: true },
  ];
  const checks: Record<string, unknown>[] = [];
  let saved: FileShareConfig | undefined;
  const resolve = (query: string) => {
    if (query.includes("fileShares{")) {
      return { fileShares: saved ? [saved] : [], users };
    }
    return { ...config, status: adminStatus, users };
  };

  const push = await installPushFixture(page, resolve);
  await page.route("**/graphql", async (route) => {
    const { query, variables } = route.request().postDataJSON();
    if (query.includes("checkFileShare(")) {
      checks.push(variables);
      const json =
        variables.userId === "bob"
          ? { data: { checkFileShare: { entries: 0 } } }
          : { errors: [{ message: "Token does not allow this user" }] };
      await route.fulfill({ json });
      return;
    }

    if (query.includes("saveFileShare(")) {
      saved = { ...variables.input, id: "team-share" };
      await push.push();
      await route.fulfill({
        json: { data: { saveFileShare: { id: saved!.id } } },
      });
      return;
    }

    await route.fulfill({ json: { data: resolve(query) } });
  });

  await page.goto("/#/admin-settings");
  await page.getByRole("button", { name: "Freigabe hinzufügen" }).click();

  const dialog = page.getByRole("dialog");
  await dialog.getByLabel("Name", { exact: true }).fill("Teamdokumente");
  await dialog.getByLabel("Protokoll").selectOption("domainshare");
  await dialog.getByLabel("Adresse").fill("https://files.example.org");
  await dialog.getByLabel(/Agenten-Token/).fill("fixture-token");
  await dialog.getByLabel("Verfügbar für").selectOption("selected");

  const check = dialog.getByRole("button", { name: "Verbindung prüfen" });
  const save = dialog.getByRole("button", { name: "Speichern", exact: true });
  await expect(check).toBeDisabled();
  await expect(save).toBeDisabled();

  await dialog.getByRole("checkbox", { name: "Carol (carol)" }).check();
  await expect(check).toBeDisabled();
  await dialog.getByRole("checkbox", { name: "Carol (carol)" }).uncheck();
  await dialog.getByRole("checkbox", { name: "Alice (alice)" }).check();
  await dialog.getByRole("checkbox", { name: "Bob (bob)" }).check();

  const testUser = dialog.getByLabel("Verbindung prüfen als");
  await testUser.selectOption("bob");
  await check.click();
  await expect(dialog.getByRole("status")).toHaveText(
    "Verbindung OK. Für diesen Benutzer sind keine Einträge sichtbar.",
  );
  expect(checks).toEqual([
    expect.objectContaining({
      userId: "bob",
      password: "fixture-token",
      input: expect.objectContaining({
        id: "",
        name: "Teamdokumente",
        protocol: "domainshare",
        endpoint: "https://files.example.org",
        restricted: true,
        userIds: ["alice", "bob"],
      }),
    }),
  ]);
  expect(saved).toBeUndefined();

  await testUser.selectOption("alice");
  await expect(dialog.getByText(/Verbindung OK/)).toHaveCount(0);
  await check.click();
  await expect(dialog.getByRole("alert")).toContainText(
    "Token does not allow this user",
  );
  expect(saved).toBeUndefined();

  await save.click();
  await expect(dialog).toHaveCount(0);
  await expect(
    page.getByRole("button", { name: /Teamdokumente.*Ausgewählte Benutzer/ }),
  ).toBeVisible();
  expect(saved).toMatchObject({ restricted: true, userIds: ["alice", "bob"] });
});
