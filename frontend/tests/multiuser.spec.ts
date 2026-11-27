import { expect, test, type APIRequestContext } from "@playwright/test";

async function graphql<T>(client: APIRequestContext, query: string) {
  const response = await client.post("/graphql", { data: { query } });
  expect(response.ok()).toBe(true);
  const result = await response.json();
  expect(result.errors).toBeUndefined();
  return result.data as T;
}

test("a member can create a private short chat, not a project", async ({
  browser,
  page,
  baseURL,
}) => {
  const administrator = await browser.newContext({ baseURL });
  try {
    const { status } = await graphql<{ status: { needsSetup: boolean } }>(
      administrator.request,
      "{status{needsSetup}}",
    );
    await graphql(
      administrator.request,
      status.needsSetup
        ? `mutation{setup(password:"browser test password",commitIdentity:{name:"Browser Test Agent",email:"browser-agent@example.test"})}`
        : `mutation{login(password:"browser test password")}`,
    );
    await graphql(
      administrator.request,
      `
        mutation {
          saveUser(
            input: { username: "short-chat-tester", role: "user" }
            password: "short chat password"
          ) {
            id
          }
        }
      `,
    );

    await page.goto("/#/projects");
    await page
      .getByLabel("Benutzername", { exact: true })
      .fill("short-chat-tester");
    await page
      .getByLabel("Passwort", { exact: true })
      .fill("short chat password");
    await page.getByRole("button", { name: "Anmelden", exact: true }).click();
    await expect(
      page.getByRole("heading", { name: "Projekte", exact: true }),
    ).toBeVisible();
    await expect(
      page.getByRole("link", { name: "Projekt anlegen", exact: true }),
    ).toHaveCount(0);

    await expect(
      page.getByRole("button", { name: "Admin", exact: true }),
    ).toHaveCount(0);
    const denied = await page.request.post("/graphql", {
      data: {
        query:
          "{shortChatSettings{retentionDays} shortChatLocalAvailable shortChatLocalIssue}",
      },
    });
    expect((await denied.json()).errors).toHaveLength(3);
    await page
      .getByRole("link", { name: "Neue Unterhaltung", exact: true })
      .click();
    await expect(page).toHaveURL(/#\/chats$/);
    expect(await graphql(page.request, "{shortChats{id}}")).toEqual({
      shortChats: [],
    });
    const composer = page.getByPlaceholder("Schreib eine Nachricht …");
    await expect(composer).toBeFocused();
    await composer.fill("/");
    await expect(page.getByRole("listbox", { name: "Befehle" })).toHaveCount(0);

    for (const text of [
      "/side Eine Nebenfrage",
      "/teamwork",
      "/continue",
      "/stop",
    ]) {
      await composer.fill(text);
      const submitted = page.waitForResponse(
        (response) =>
          response.url().endsWith("/graphql") &&
          response.request().postDataJSON()?.query?.includes("submitInput("),
      );
      await composer.press("Enter");

      const response = await submitted;
      expect(response.request().postDataJSON().variables.text).toBe(text);
      expect((await response.json()).errors).toBeUndefined();
      await expect(page).toHaveURL(/#\/chats\/[a-zA-Z0-9_-]+/);
      await expect(composer).toHaveValue("");
      await expect(page.getByRole("dialog")).toHaveCount(0);
    }

    const { shortChats } = await graphql<{ shortChats: { id: string }[] }>(
      page.request,
      "{shortChats{id}}",
    );
    expect(shortChats).toHaveLength(1);

    const name = "/side Eine Nebenfrage";
    const pinActions = page.getByRole("button", {
      name: `Aktionen für ${name}`,
      exact: true,
    });
    const pinned = page.getByRole("list", { name: "Angeheftet", exact: true });
    const history = page.getByRole("list", {
      name: "Unterhaltungsverlauf",
      exact: true,
    });
    await expect(
      history.getByRole("link", { name, exact: true }),
    ).toBeVisible();
    await pinActions.click();
    await page.getByRole("menuitem", { name: "Anheften", exact: true }).click();
    await expect(pinned.getByRole("link", { name, exact: true })).toBeVisible();
    await page.reload();
    await expect(pinned.getByRole("link", { name, exact: true })).toBeVisible();
    await pinActions.click();
    await page.getByRole("menuitem", { name: "Loslösen", exact: true }).click();
    await expect(
      history.getByRole("link", { name, exact: true }),
    ).toBeVisible();
    await expect(pinned).toHaveCount(0);

    const adminPage = await administrator.newPage();
    await adminPage.goto(`/#/chats/${shortChats[0].id}`);
    await expect(
      adminPage.getByText("project not found or not available", {
        exact: true,
      }),
    ).toBeVisible();
  } finally {
    await administrator.close();
  }
});

test("an admin can configure short-chat and VM retention", async ({ page }) => {
  const { status } = await graphql<{ status: { needsSetup: boolean } }>(
    page.request,
    "{status{needsSetup}}",
  );
  await graphql(
    page.request,
    status.needsSetup
      ? 'mutation{setup(password:"browser test password",commitIdentity:{name:"Browser Test Agent",email:"browser-agent@example.test"})}'
      : 'mutation{login(password:"browser test password")}',
  );

  const { shortChatLocalIssue } = await graphql<{
    shortChatLocalIssue: string | null;
  }>(page.request, "{shortChatLocalIssue}");
  await page.goto("/#/admin-settings");
  const configure = page.getByRole("button", {
    name: "Kurzchats konfigurieren",
  });
  await configure.click();
  const dialog = page.getByRole("dialog", { name: "Kurzchats konfigurieren" });
  const retention = dialog.getByLabel("Chat-Aufbewahrung (Tage)");
  const lifetime = dialog.getByLabel("VM-Lebensdauer (Tage)");
  await expect(retention).toHaveValue("45");
  await expect(lifetime).toHaveValue("7");
  if (shortChatLocalIssue !== null) {
    await expect(
      dialog.getByText(
        `Zentrale (lokal) ist nicht verfügbar: ${shortChatLocalIssue}`,
        {
          exact: true,
        },
      ),
    ).toBeVisible();
  }

  await dialog.getByRole("button", { name: "Codeausführung" }).click();
  await expect(
    page.getByRole("option", { name: "Zentrale (lokal)" }),
  ).toHaveCount(shortChatLocalIssue === null ? 1 : 0);
  await page.getByRole("option", { name: "Deaktiviert" }).click();
  await retention.fill("60");
  await lifetime.fill("14");
  await dialog.getByRole("button", { name: "Speichern", exact: true }).click();
  await expect(dialog).not.toBeVisible();

  await page.reload();
  await configure.click();
  await expect(retention).toHaveValue("60");
  await expect(lifetime).toHaveValue("14");
  await graphql(
    page.request,
    "mutation{saveShortChatSettings(input:{retentionDays:45,vmLifetimeDays:7}){retentionDays vmLifetimeDays}}",
  );
});

test("an admin can extend the short-chat prompt and manage references", async ({
  page,
}) => {
  const { status } = await graphql<{ status: { needsSetup: boolean } }>(
    page.request,
    "{status{needsSetup}}",
  );
  await graphql(
    page.request,
    status.needsSetup
      ? 'mutation{setup(password:"browser test password",commitIdentity:{name:"Browser Test Agent",email:"browser-agent@example.test"})}'
      : 'mutation{login(password:"browser test password")}',
  );
  await page.goto("/#/admin-settings");
  const promptButton = page.getByRole("button", {
    name: "Systemprompt ergänzen",
  });
  await promptButton.click();
  const prompt = page.getByRole("dialog", { name: "Systemprompt ergänzen" });
  const instructions = prompt.getByLabel(
    "Zusätzliche Anweisungen für Kurzchats",
  );
  await instructions.fill("Verwende die Begriffe aus dem Handbuch.");
  await prompt.getByRole("button", { name: "Speichern", exact: true }).click();
  await expect(prompt).not.toBeVisible();
  await promptButton.click();
  await expect(instructions).toHaveValue(
    "Verwende die Begriffe aus dem Handbuch.",
  );
  await instructions.fill("");
  await prompt.getByRole("button", { name: "Speichern", exact: true }).click();
  await expect(prompt).not.toBeVisible();

  await page.getByRole("button", { name: "Wissensbasis verwalten" }).click();
  const knowledge = page.getByRole("dialog", { name: "Wissensbasis" });
  const chosen = page.waitForEvent("filechooser");
  await knowledge
    .getByRole("button", { name: "Datei zur Wissensbasis hinzufügen" })
    .click();
  await (
    await chosen
  ).setFiles({
    name: "Handbuch.md",
    mimeType: "text/markdown",
    buffer: Buffer.from("# Handbuch\nDer Startknopf leuchtet grün."),
  });
  const document = knowledge.getByRole("link", { name: "Handbuch.md" });
  await expect(document).toBeVisible();
  const download = page.waitForEvent("download");
  await document.click();
  expect((await download).suggestedFilename()).toBe("Handbuch.md");
  await knowledge.getByRole("button", { name: "Entfernen" }).click();
  await page
    .getByRole("dialog", { name: "Nachschlagewerk entfernen" })
    .getByRole("button", { name: "Entfernen" })
    .click();
  await expect(document).toHaveCount(0);
});
