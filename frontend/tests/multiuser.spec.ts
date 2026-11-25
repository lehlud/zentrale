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

    await page.getByRole("link", { name: "Kurzchats", exact: true }).click();
    await page.getByRole("button", { name: "Neuer Chat", exact: true }).click();
    await expect(page).toHaveURL(/#\/chats\/[a-zA-Z0-9_-]+/);
    await expect(
      page.getByPlaceholder("Schreib eine Nachricht …"),
    ).toBeVisible();

    const { shortChats } = await graphql<{ shortChats: { id: string }[] }>(
      page.request,
      "{shortChats{id}}",
    );
    expect(shortChats).toHaveLength(1);

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
