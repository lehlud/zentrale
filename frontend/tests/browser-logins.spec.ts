import { expect, test, type Page } from "@playwright/test";

import { installPushFixture } from "./fixtures/push-fixture";
import { adminStatus, config, gitConnections } from "./fixtures/visual-data";

async function loginFixture(page: Page, locale = "de") {
  await page.addInitScript(
    (language) => localStorage.setItem("zentrale-locale", language),
    locale,
  );

  const provider = {
    ...config.providers[0],
    id: "google-api",
    name: "Google API",
    kind: "gemini",
    authMode: "oauth",
    connected: false,
    modelCount: 0,
    catalogStatus: "disconnected",
    oauthClientId: "own-web-app",
    oauthProjectId: "api-project",
  };
  const connection = {
    id: "forgejo-private",
    kind: "forgejo",
    name: "Private forge",
    baseUrl: "https://git.example.org",
    clientId: "own-forge-app",
    configured: true,
    connected: false,
    privateNetwork: true,
    userName: "",
    reauthorizationRequired: false,
  };
  const gitLogin = {
    id: "git-login",
    connectionId: connection.id,
    method: "browser",
    status: "pending",
    verificationUrl: "https://git.example.org/login/oauth/authorize",
    userCode: "",
    interval: 5,
    expiresAt: "2099-01-01T00:00:00Z",
    error: "",
  };
  const providerLogin = {
    ...gitLogin,
    id: "google-login",
    providerId: provider.id,
    method: "oauth",
    verificationUrl: "https://accounts.google.com/o/oauth2/v2/auth",
  };
  const state = {
    completions: 0,
    begins: 0,
    configuredGit: undefined as Record<string, unknown> | undefined,
    configuredProvider: undefined as Record<string, unknown> | undefined,
  };
  const resolve = (query: string) => {
    if (/\bstatus\s*\{/.test(query)) {
      return { status: adminStatus };
    }

    if (query.includes("gitLogin(")) return { gitLogin };
    if (query.includes("providerLogin(")) return { providerLogin };
    if (query.includes("gitConnections"))
      return { gitConnections: [...gitConnections, connection] };
    if (query.includes("huggingFaceConnection"))
      return {
        huggingFaceConnection: {
          connected: false,
          userName: "",
          checkedAt: "",
        },
      };
    if (query.includes("sidebarAgents")) return { sidebarAgents: [] };

    return {
      ...config,
      providers: [provider],
      providerKinds: [
        {
          id: "gemini",
          name: "Google / Gemini",
          authMethods: ["api", "oauth"],
        },
      ],
    };
  };
  await installPushFixture(page, resolve);
  await page.route("**/graphql", async (route) => {
    const { query, variables = {} } = route.request().postDataJSON();
    const send = (data: unknown) => route.fulfill({ json: { data } });

    if (query.includes("completeBrowserLogin(")) {
      state.completions++;
      expect(page.url()).not.toContain("fixture-code");
      return send({ completeBrowserLogin: true });
    }

    if (query.includes("configureGitConnection(")) {
      state.configuredGit = variables.input;
      gitLogin.verificationUrl = `${variables.input.baseUrl}/login/oauth/authorize`;
      return send({
        configureGitConnection: {
          ...connection,
          ...variables.input,
          id: connection.id,
          configured: true,
        },
      });
    }

    if (query.includes("saveProvider(")) {
      state.configuredProvider = variables.input;
      return send({ saveProvider: provider });
    }

    if (query.includes("beginGitLogin(")) {
      state.begins++;
      expect(variables.redirectUri).toContain("/oauth/callback");
      return send({ beginGitLogin: gitLogin });
    }

    if (query.includes("beginProviderLogin(")) {
      state.begins++;
      expect(variables.redirectUri).toContain("/oauth/callback");
      return send({ beginProviderLogin: providerLogin });
    }

    if (query.includes("cancelGitLogin("))
      return send({ cancelGitLogin: true });
    if (query.includes("cancelProviderLogin("))
      return send({ cancelProviderLogin: true });

    return send(resolve(query));
  });

  return state;
}

for (const locale of ["de", "en"]) {
  test(`browser callback scrubs query and exchanges only once: ${locale}`, async ({
    page,
  }) => {
    const state = await loginFixture(page, locale);
    await page.goto(
      "/oauth/callback?code=fixture-code&state=provider:login:random-state",
    );

    await expect(page.getByRole("status")).toContainText(
      locale === "de" ? "Anmeldung übermittelt" : "Sign-in submitted",
    );
    expect(new URL(page.url()).search).toBe("");
    expect(state.completions).toBe(1);
    expect(
      await page.evaluate(() =>
        JSON.stringify({ ...localStorage, ...sessionStorage }),
      ),
    ).not.toContain("fixture-code");

    await page.reload();
    await expect(page.getByRole("alert")).toContainText(
      locale === "de" ? "nicht abgeschlossen" : "could not be completed",
    );
    expect(state.completions).toBe(1);
  });
}

test("duplicate callback parameters and provider errors are never exchanged or echoed", async ({
  page,
}) => {
  const state = await loginFixture(page);
  for (const query of [
    "code=one&code=two&state=provider:login:state",
    "code=one&state=a&state=b",
    "error=access_denied&error_description=private-provider-error",
  ]) {
    await page.goto(`/oauth/callback?${query}`);
    await expect(page.getByRole("alert")).toBeVisible();
    expect(new URL(page.url()).search).toBe("");
    await expect(page.locator("body")).not.toContainText(
      "private-provider-error",
    );
  }

  expect(state.completions).toBe(0);
});

test("Forgejo instance setup explains broad access and uses browser consent without device code", async ({
  page,
}) => {
  const state = await loginFixture(page);
  await page.goto("/#/settings");
  await page
    .getByRole("button", { name: "Forgejo hinzufügen", exact: true })
    .click();
  const dialog = page.locator('[data-slot="modal-dialog"]:not([data-exiting])');
  await expect(dialog).toContainText("breiten Account-Zugriff");
  await expect(dialog).toContainText("/oauth/callback");
  await dialog.getByLabel("Name", { exact: true }).fill("Company forge");
  await dialog
    .getByLabel("Instanz-Adresse", { exact: true })
    .fill("https://git.company.example");
  await dialog
    .getByLabel("OAuth-App Client-ID", { exact: true })
    .fill("company-app");
  await dialog
    .getByText("Verbindungen ins private Netzwerk erlauben", { exact: true })
    .click();
  await expect(
    dialog.getByRole("checkbox", {
      name: "Verbindungen ins private Netzwerk erlauben",
      exact: true,
    }),
  ).toBeChecked();
  await dialog
    .getByRole("button", { name: "Weiter zur Anmeldung", exact: true })
    .click();

  await expect(
    dialog.getByRole("link", { name: "Anmeldeseite öffnen ↗" }),
  ).toHaveAttribute("href", /git\.company\.example/);
  await expect(dialog).toContainText("Auf der Anmeldeseite zustimmen");
  await expect(dialog).not.toContainText("Anmeldecode");
  expect(state.begins).toBe(1);
  expect(state.configuredGit).toMatchObject({
    kind: "forgejo",
    name: "Company forge",
    privateNetwork: true,
  });
});

test("Google app configuration submits private secret without browser persistence and keeps API billing notice", async ({
  page,
}) => {
  const state = await loginFixture(page);
  await page.goto("/#/providers?provider=google-api");
  await page
    .getByRole("button", { name: "Aktionen für Google API", exact: true })
    .click();
  await page.getByRole("menuitem", { name: "Bearbeiten", exact: true }).click();
  const dialog = page.locator('[data-slot="modal-dialog"]:not([data-exiting])');
  await expect(dialog).toContainText("nicht dein AI-Pro-/Ultra-Abo");
  await expect(
    dialog.getByLabel("Google OAuth Client-Secret", { exact: true }),
  ).toHaveAttribute("type", "password");
  await dialog
    .getByLabel("Google OAuth Client-Secret", { exact: true })
    .fill("private-browser-app-secret");
  await dialog.getByRole("button", { name: "Speichern", exact: true }).click();

  await expect(
    dialog.getByRole("link", { name: "Anmeldeseite öffnen ↗" }),
  ).toHaveAttribute("href", /accounts\.google\.com/);
  await expect(
    dialog.getByRole("textbox", { name: "Anmeldecode", exact: true }),
  ).toHaveCount(0);
  expect(state.begins).toBe(1);
  expect(state.configuredProvider).toMatchObject({
    oauthClientId: "own-web-app",
    oauthProjectId: "api-project",
    oauthClientSecret: "private-browser-app-secret",
  });
  expect(
    await page.evaluate(() =>
      JSON.stringify({ ...localStorage, ...sessionStorage }),
    ),
  ).not.toContain("private-browser-app-secret");
});
