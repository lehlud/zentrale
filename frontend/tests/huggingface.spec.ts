import { expect, test, type Page } from "@playwright/test";

import { installPushFixture } from "./fixtures/push-fixture";
import { config, gitConnections } from "./fixtures/visual-data";

async function connectionFixture(page: Page, locale = "de") {
  await page.addInitScript((language) => {
    localStorage.setItem("zentrale-locale", language);
  }, locale);

  const state = {
    connection: {
      connected: false,
      userName: "",
      checkedAt: "",
      authMode: "",
      oauthClientId: "",
      oauthConfigured: false,
      oauthConfidential: false,
      login: { id: "", authorizationUrl: "", expiresAt: "" },
    },
    failLogin: false,
    failRead: false,
    writes: [] as string[],
    oauthWrites: [] as {
      clientId: string;
      clientSecret: string;
      publicClient: boolean;
    }[],
    begins: [] as string[],
    ends: [] as string[],
    completions: [] as string[],
  };
  const resolve = (query: string) => {
    if (/\bstatus\s*\{/.test(query)) {
      return { status: { needsSetup: false, authenticated: true } };
    }

    if (query.includes("huggingFaceConnection")) {
      if (state.failRead) throw new Error("HF connection metadata unavailable");

      return { huggingFaceConnection: state.connection };
    }

    if (query.includes("gitConnections")) return { gitConnections };

    return config;
  };
  const push = await installPushFixture(page, resolve);
  await page.route("**/graphql", async (route) => {
    const { query, variables } = route.request().postDataJSON();

    if (query.includes("configureHuggingFaceOAuth")) {
      state.oauthWrites.push(variables.value);
      state.connection.oauthClientId = variables.value.clientId;
      state.connection.oauthConfigured = Boolean(variables.value.clientId);
      state.connection.oauthConfidential = !variables.value.publicClient;
      await route.fulfill({
        json: { data: { configureHuggingFaceOAuth: state.connection } },
      });
      await push.push();
      return;
    }

    if (query.includes("beginHuggingFaceLogin")) {
      state.begins.push(variables.redirectUri);
      state.connection.login = {
        id: "hf-login",
        authorizationUrl:
          "https://huggingface.co/oauth/authorize?state=huggingface:hf-login:random-state",
        expiresAt: new Date(Date.now() + 600000).toISOString(),
      };
      await route.fulfill({
        json: { data: { beginHuggingFaceLogin: state.connection.login } },
      });
      await push.push();
      return;
    }

    if (query.includes("endHuggingFaceLogin")) {
      state.ends.push(variables.id);
      state.connection.login = { id: "", authorizationUrl: "", expiresAt: "" };
      await route.fulfill({ json: { data: { endHuggingFaceLogin: true } } });
      await push.push();
      return;
    }

    if (query.includes("completeBrowserLogin")) {
      state.completions.push(variables.code);
      state.connection = {
        ...state.connection,
        connected: true,
        userName: "oauth-owner",
        authMode: "oauth",
        login: { id: "", authorizationUrl: "", expiresAt: "" },
      };
      await route.fulfill({ json: { data: { completeBrowserLogin: true } } });
      return;
    }

    if (query.includes("connectHuggingFace(")) {
      state.writes.push(variables.token);

      if (state.failLogin) {
        return route.fulfill({
          json: {
            errors: [{ message: "Hugging Face access unavailable (HTTP 403)" }],
          },
        });
      }

      state.connection = {
        ...state.connection,
        connected: true,
        userName: "dataset-owner",
        checkedAt: "2026-11-15T12:00:00Z",
        authMode: "token",
        login: { id: "", authorizationUrl: "", expiresAt: "" },
      };
      await route.fulfill({
        json: { data: { connectHuggingFace: state.connection } },
      });
      await push.push();
      return;
    }

    if (query.includes("disconnectHuggingFace")) {
      state.connection = {
        ...state.connection,
        connected: false,
        userName: "",
        checkedAt: "",
        authMode: "",
        login: { id: "", authorizationUrl: "", expiresAt: "" },
      };
      await route.fulfill({ json: { data: { disconnectHuggingFace: true } } });
      await push.push();
      return;
    }

    try {
      await route.fulfill({ json: { data: resolve(query) } });
    } catch (error) {
      await route.fulfill({
        json: { errors: [{ message: (error as Error).message }] },
      });
    }
  });

  await page.goto("/#/settings");
  const section = page.getByRole("region", {
    name: "Hugging Face",
    exact: true,
  });
  await expect(section).toBeVisible();

  return { state, push, section };
}

for (const locale of ["de", "en"]) {
  test(`Hugging Face token connection, replacement and disconnect: ${locale}`, async ({
    page,
  }) => {
    const { state, section } = await connectionFixture(page, locale);
    const token = section.getByLabel(
      locale === "de" ? "Hugging-Face-Token" : "Hugging Face token",
    );
    const secret = "hf_DummyBrowserCredential123456789";

    await expect(token).toHaveAttribute("type", "password");
    await token.fill(secret);
    await token.press("Enter");

    await expect(
      section.getByText("dataset-owner", { exact: true }),
    ).toBeVisible();
    await expect(token).toHaveValue("");
    expect(state.writes).toEqual([secret]);
    expect(
      await page.evaluate(() =>
        JSON.stringify({ ...localStorage, ...sessionStorage }),
      ),
    ).not.toContain(secret);
    await expect(page.locator("body")).not.toContainText(secret);

    await token.fill("hf_DummyReplacementCredential123456789");
    await section
      .getByRole("button", {
        name: locale === "de" ? "Token ersetzen" : "Replace token",
        exact: true,
      })
      .click();
    await expect(token).toHaveValue("");
    expect(state.writes).toHaveLength(2);

    const disconnect = locale === "de" ? "Trennen" : "Disconnect";
    await section
      .getByRole("button", { name: disconnect, exact: true })
      .click();
    await page
      .getByRole("dialog")
      .getByRole("button", { name: disconnect, exact: true })
      .click();
    await expect(
      section.getByText(locale === "de" ? "Nicht verbunden" : "Not connected", {
        exact: true,
      }),
    ).toBeVisible();
    await expect(
      section.getByText("dataset-owner", { exact: true }),
    ).toHaveCount(0);

    const link = section.locator(
      'a[href="https://huggingface.co/settings/tokens"]',
    );
    await expect(link).toHaveAttribute(
      "href",
      "https://huggingface.co/settings/tokens",
    );
    await expect(link).toHaveAttribute("rel", "noopener noreferrer");
  });
}

test("Hugging Face permission failure is not a Zentrale logout and permits retry", async ({
  page,
}) => {
  const { state, section } = await connectionFixture(page);
  const token = section.getByLabel("Hugging-Face-Token");
  state.failLogin = true;

  await token.fill("hf_DummyDeniedCredential123456789");
  await section.getByRole("button", { name: "Verbinden", exact: true }).click();
  await expect(
    section.getByText("Hugging Face access unavailable (HTTP 403)"),
  ).toBeVisible();
  await expect(token).toHaveValue("hf_DummyDeniedCredential123456789");
  await expect(page).toHaveURL(/#\/settings$/);

  state.failLogin = false;
  await token.press("Enter");
  await expect(
    section.getByText("dataset-owner", { exact: true }),
  ).toBeVisible();
  await expect(token).toHaveValue("");

  await token.fill("hf_UnsavedDraftMustNotPersist12345");
  await page.reload();
  await expect(section.getByLabel("Hugging-Face-Token")).toHaveValue("");
});

test("Hugging Face status push is authoritative and errors preserve the snapshot", async ({
  page,
}) => {
  const { state, section, push } = await connectionFixture(page);
  state.connection = {
    ...state.connection,
    connected: true,
    userName: "other-tab",
    checkedAt: "2026-11-15T12:00:00Z",
  };
  await push.push();
  await expect(section.getByText("other-tab", { exact: true })).toBeVisible();

  state.failRead = true;
  await push.push();
  await expect(
    section.getByText("HF connection metadata unavailable"),
  ).toBeVisible();
  await expect(section.getByText("other-tab", { exact: true })).toBeVisible();
});

for (const locale of ["de", "en"]) {
  test(`Hugging Face offers OAuth and PAT with secret-safe app setup: ${locale}`, async ({
    page,
  }) => {
    const { state, section } = await connectionFixture(page, locale);
    const german = locale === "de";
    const signIn = german
      ? "Mit Hugging Face anmelden"
      : "Sign in with Hugging Face";
    const appID = section.getByLabel(
      german ? "Hugging-Face-OAuth-Client-ID" : "Hugging Face OAuth client ID",
    );
    const publicLabel = german
      ? "Öffentliche OAuth-App ohne Client-Secret"
      : "Public OAuth app without a client secret";

    await expect(
      section.getByRole("button", { name: signIn, exact: true }),
    ).toBeDisabled();
    await appID.fill("hf-browser-app");
    await section.getByText(publicLabel, { exact: true }).click();
    await expect(section.getByRole("checkbox")).not.toBeChecked();

    const secret = section.getByLabel(
      german
        ? "Hugging-Face-OAuth-Client-Secret"
        : "Hugging Face OAuth client secret",
    );
    await expect(secret).toHaveAttribute("type", "password");
    await secret.fill("private-browser-app-secret");
    await section
      .getByRole("button", {
        name: german ? "OAuth-App speichern" : "Save OAuth app",
        exact: true,
      })
      .click();

    await expect(secret).toHaveValue("");
    expect(state.oauthWrites).toEqual([
      {
        clientId: "hf-browser-app",
        clientSecret: "private-browser-app-secret",
        publicClient: false,
      },
    ]);
    await expect(
      section.getByRole("button", { name: signIn, exact: true }),
    ).toBeEnabled();
    await section.getByRole("button", { name: signIn, exact: true }).click();

    const link = section.getByRole("link", {
      name: german
        ? "Hugging-Face-Anmeldung öffnen"
        : "Open Hugging Face sign-in",
    });
    await expect(link).toHaveAttribute(
      "href",
      /^https:\/\/huggingface\.co\/oauth\/authorize\?/,
    );
    expect(state.begins).toEqual([new URL("/oauth/callback", page.url()).href]);
    await expect(link).toHaveAttribute("rel", "noopener noreferrer");
    await section
      .getByRole("button", {
        name: german ? "Abbrechen" : "Cancel",
        exact: true,
      })
      .click();
    await expect(link).toHaveCount(0);
    expect(state.ends).toEqual(["hf-login"]);

    await section.getByRole("button", { name: signIn, exact: true }).click();
    await page.goto(
      "/oauth/callback?state=huggingface:hf-login:random-state&code=private-browser-grant",
    );
    await expect(page).toHaveURL(/\/oauth\/callback$/);
    await expect(page.getByRole("status")).toContainText(
      german ? "Anmeldung übermittelt" : "Sign-in submitted",
    );
    expect(state.completions).toEqual(["private-browser-grant"]);

    await page.goto("/#/settings");
    await expect(
      section.getByText("oauth-owner", { exact: true }),
    ).toBeVisible();
    await expect(
      section.getByText(
        german ? "Über OAuth verbunden" : "Connected through OAuth",
        { exact: true },
      ),
    ).toBeVisible();

    const token = section.getByLabel(
      german ? "Hugging-Face-Token" : "Hugging Face token",
    );
    await token.fill("hf_PatAfterBrowserLogin123456789");
    await token.press("Enter");
    await expect(
      section.getByText("dataset-owner", { exact: true }),
    ).toBeVisible();
    await expect(
      section.getByText(
        german ? "Über OAuth verbunden" : "Connected through OAuth",
        { exact: true },
      ),
    ).toHaveCount(0);
    expect(state.connection.oauthConfigured).toBe(true);

    const storage = await page.evaluate(() =>
      JSON.stringify({ ...localStorage, ...sessionStorage }),
    );
    expect(storage).not.toContain("private-browser");
    await expect(page.locator("body")).not.toContainText(
      "private-browser-app-secret",
    );
  });
}
