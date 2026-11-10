import { expect, test, type Page } from "@playwright/test";

import {
  chooseContextAction,
  chooseProviderAccount,
} from "./fixtures/context-actions";
import type { Server } from "node:http";
import { createServer } from "node:http";

import { startSSHPeer, type SSHPeer } from "./fixtures/ssh-peer";

async function openNavigation(page: Page) {
  const drawer = page.locator('[data-slot="drawer-dialog"]');
  if (await drawer.isVisible()) {
    return;
  }

  if ((page.viewportSize()?.width ?? 1280) >= 1024) {
    return;
  }

  const trigger = page.getByRole("button", {
    name: /^(Menü öffnen|Open menu)$/,
  });

  await expect(trigger).toBeVisible();

  await trigger.click();

  await expect(drawer).toBeVisible();
}

async function closeNavigation(page: Page) {
  const drawer = page.locator('[data-slot="drawer-dialog"]');
  if (await drawer.isVisible()) {
    await page.keyboard.press("Escape");
    await expect(drawer).not.toBeVisible();
  }
}

async function navigateToPage(page: Page, name: string) {
  await openNavigation(page);
  if (name === "Projekte" || name === "Projects") {
    await page
      .getByRole("link", { name: /^(Alle Projekte|All projects)$/ })
      .click();
  } else {
    const labels: Record<string, string> = {
      Schlüssel: "Schlüssel",
      Keys: "Keys",
      Provider: "Provider und Modelle",
      Einstellungen: "Allgemeine Einstellungen",
      Machines: "Machines",
      Maschinen: "Maschinen",
    };
    const english = (await page.locator("html").getAttribute("lang")) === "en";
    const englishLabels: Record<string, string> = {
      Keys: "Keys",
      Provider: "Providers and models",
      Settings: "General settings",
      Machines: "Machines",
    };
    await page
      .getByRole("button", {
        name: /^(Einstellungen öffnen|Open settings)$/,
      })
      .click();
    await page
      .getByRole("menuitem", {
        name: english
          ? (englishLabels[name] ?? labels[name] ?? name)
          : (labels[name] ?? name),
        exact: true,
      })
      .click();
  }

  await expect(page.locator('[data-slot="drawer-dialog"]')).not.toBeVisible();
}

async function selectPreference(page: Page, label: RegExp, value: string) {
  const control = page.getByRole("button", { name: label });
  let appearance = false;
  if (!(await control.isVisible())) {
    await openNavigation(page);
    await page
      .getByRole("button", {
        name: /^(Einstellungen öffnen|Open settings)$/,
      })
      .click();
    await page
      .getByRole("menuitem", {
        name: /^(Darstellung und Sprache|Appearance and language)$/,
      })
      .click();
    appearance = true;
  }

  const controlId = await control.getAttribute("id");
  await control.click();
  await page
    .locator('[data-slot="select-popover"]:not([data-exiting])')
    .getByRole("option", { name: value, exact: true })
    .click();

  await expect(page.locator('[data-slot="select-popover"]')).toHaveCount(0);
  await expect(page.locator(`[id="${controlId}"]`)).toBeFocused();

  if (appearance) {
    await page.keyboard.press("Escape");
    await expect(page.locator('[data-slot="modal-dialog"]')).toHaveCount(0);
  }

  await closeNavigation(page);
}

let provider: Server;

let providerURL: string;

let sshPeer: SSHPeer;

test.beforeAll(async () => {
  sshPeer = await startSSHPeer();
  provider = createServer(async (request, response) => {
    if (request.method === "GET" && request.url?.startsWith("/models")) {
      response.setHeader("Content-Type", "application/json");
      response.end(
        JSON.stringify({
          data: [
            {
              id: "fixture-main",
              name: "Fixture Main",
              context_length: 128000,
            },
            {
              id: "fixture-approver",
              name: "Fixture Approver",
              context_length: 64000,
            },
          ],
        }),
      );

      return;
    }

    if (request.method === "GET" && request.url === "/large/models") {
      response.setHeader("Content-Type", "application/json");
      response.end(
        JSON.stringify({
          data: Array.from({ length: 650 }, (_, index) => ({
            id: `catalog-${String(index).padStart(4, "0")}`,
            name: `Catalog model ${String(index).padStart(4, "0")}`,
            context_length: 64000,
          })),
        }),
      );

      return;
    }

    const chunks: Buffer[] = [];
    for await (const chunk of request) chunks.push(Buffer.from(chunk));

    const body = JSON.parse(Buffer.concat(chunks).toString());
    const isApprover = body.instructions?.includes("independent auto-approval");
    const hasResults = body.input?.some(
      (item: { type?: string }) => item.type === "function_call_output",
    );
    let output;
    if (body.instructions?.includes("goal_completion_review")) {
      output = [
        {
          type: "message",
          role: "assistant",
          content: [
            {
              type: "output_text",
              text: JSON.stringify({
                status: "complete",
                reason: "Fixture task finished",
                evidence: "The fixture actions and saved note were checked",
              }),
            },
          ],
        },
      ];
    } else if (isApprover) {
      output = [
        {
          type: "message",
          role: "assistant",
          content: [
            {
              type: "output_text",
              text: JSON.stringify({
                allowed: false,
                reason: "Nicht durch den Auftrag gedeckt",
                blanket: false,
              }),
            },
          ],
        },
      ];
    } else if (hasResults) {
      output = [
        {
          type: "message",
          role: "assistant",
          content: [
            { type: "output_text", text: "Die Aufgabe ist abgeschlossen." },
          ],
        },
      ];
    } else {
      output = [
        {
          type: "function_call",
          call_id: "note-call",
          name: "harness",
          arguments: JSON.stringify({
            action: "note_save",
            arguments: JSON.stringify({
              title: "Arbeitsplan",
              content: "Dieser Plan überlebt die Kontextverdichtung.",
            }),
          }),
        },
        {
          type: "function_call",
          call_id: "command-call",
          name: "harness",
          arguments: JSON.stringify({
            action: "exec",
            arguments: JSON.stringify({ command: "echo test" }),
          }),
        },
      ];
    }

    response.setHeader("Content-Type", "application/json");
    response.end(
      JSON.stringify({
        output,
        usage: {
          input_tokens: 100,
          output_tokens: 20,
          input_tokens_details: { cached_tokens: 60 },
        },
      }),
    );
  });
  await new Promise<void>((resolve) =>
    provider.listen(0, "127.0.0.1", resolve),
  );
  const address = provider.address();
  if (!address || typeof address === "string") {
    throw new Error("No mock provider address");
  }

  providerURL = `http://127.0.0.1:${address.port}`;
});

test.afterAll(async () => {
  await sshPeer?.close();
  await new Promise<void>((resolve, reject) =>
    provider.close((error) => (error ? reject(error) : resolve())),
  );
});

test("setup, configuration, agents, approvals and responsive navigation", async ({
  page,
}, testInfo) => {
  const errors: string[] = [];
  page.on("pageerror", (error) => errors.push(error.message));
  await page.goto("/");

  await expect(
    page.getByRole("heading", { name: "Zentrale einrichten" }),
  ).toBeVisible();

  await page
    .getByLabel("Passwort", { exact: true })
    .fill("browser test password");
  await page.getByLabel("Passwort wiederholen").fill("browser test password");
  await page.getByRole("button", { name: "Weiter", exact: true }).click();
  await page
    .getByLabel("Name für Commits", { exact: true })
    .fill("Browser Test Agent");
  await page
    .getByLabel("E-Mail für Commits", { exact: true })
    .fill("browser-agent@example.test");
  await page.getByRole("button", { name: "Einrichtung abschließen" }).click();

  await expect(
    page.getByRole("heading", { name: "Projekte", exact: true }),
  ).toBeVisible();

  await page.request.post("/graphql", {
    data: {
      query:
        "mutation($publicKey:String!,$trust:String!){trustHostKey(publicKey:$publicKey,trust:$trust)}",
      variables: { publicKey: sshPeer.publicKey, trust: "trust" },
    },
  });

  await navigateToPage(page, "Schlüssel");
  await page.getByRole("button", { name: "SSH-Schlüssel erzeugen" }).click();

  await expect(
    page.getByRole("heading", { name: "Schlüssel 1" }),
  ).toBeVisible();

  await navigateToPage(page, "Maschinen");
  await page.getByRole("button", { name: "Maschine hinzufügen" }).click();
  let dialog = page.locator('[data-slot="modal-dialog"]');
  await dialog.getByLabel("Name", { exact: true }).fill("Testserver");
  await dialog.getByLabel("Host", { exact: true }).fill(sshPeer.host);
  await dialog
    .getByLabel("SSH-Port", { exact: true })
    .fill(String(sshPeer.port));
  await dialog.getByLabel("Benutzer", { exact: true }).fill("developer");
  await dialog.getByRole("button", { name: "Weiter", exact: true }).click();
  await dialog
    .getByRole("button", { name: /SSH-Schlüssel/ })
    .and(dialog.locator('[data-slot="select-trigger"]'))
    .click();
  await page
    .locator('[data-slot="select-popover"]:not([data-exiting])')
    .getByRole("option", { name: "Schlüssel 1" })
    .click();
  await dialog.getByRole("button", { name: "Weiter", exact: true }).click();
  await dialog.getByRole("button", { name: "Speichern", exact: true }).click();

  await expect(dialog).not.toBeVisible();
  await expect(page.getByRole("heading", { name: "Testserver" })).toBeVisible();

  await page
    .getByRole("button", { name: "Verbindung prüfen", exact: true })
    .click();

  await expect(
    page.getByText("Sandbox: Automatisch → Ohne Sandbox", { exact: true }),
  ).toBeVisible();

  await page.reload();

  await expect(
    page.getByText("Sandbox: Automatisch → Ohne Sandbox", { exact: true }),
  ).toBeVisible();

  await navigateToPage(page, "Provider");
  await page.getByRole("button", { name: "Provider hinzufügen" }).click();
  dialog = page.locator('[data-slot="modal-dialog"]');
  await dialog.getByLabel("Name", { exact: true }).fill("Testmodell");

  await expect(dialog.getByLabel("Modell-ID")).toHaveCount(0);

  await dialog
    .getByRole("button", { name: "Erweiterte Einstellungen" })
    .click();
  await dialog.getByLabel("API-Basis-URL").fill(providerURL);
  await dialog.getByRole("button", { name: "Speichern", exact: true }).click();

  await expect(dialog).not.toBeVisible();

  await expect(
    page.getByText("2 Sprachmodelle", { exact: true }),
  ).toBeVisible();

  await navigateToPage(page, "Einstellungen");
  for (const [role, model] of [
    ["Hauptmodell", "Fixture Main"],
    ["Auto-Approver", "Fixture Approver"],
  ]) {
    const group = page.getByRole("group", { name: role, exact: true });
    await group.getByRole("button", { name: "Auswählen", exact: true }).click();
    await page
      .getByRole("dialog")
      .getByRole("option", { name: new RegExp(model) })
      .click();
    await page
      .getByRole("dialog")
      .getByRole("button", { name: "Auswahl übernehmen" })
      .click();
  }

  await page.getByRole("button", { name: "Modell-Defaults speichern" }).click();

  await navigateToPage(page, "Projekte");
  await page.getByRole("button", { name: "Projekt anlegen" }).click();
  dialog = page.locator('[data-slot="modal-dialog"]');
  await dialog.getByLabel("Name", { exact: true }).fill("Mein Projekt");

  await expect(
    dialog.getByLabel("Projektverzeichnis auf der Fernmaschine"),
  ).toHaveCount(0);

  await dialog.getByRole("button", { name: "Weiter", exact: true }).click();
  await dialog.getByRole("button", { name: /Fernmaschinen/ }).click();
  await page
    .locator('[data-slot="select-popover"]:not([data-exiting])')
    .getByRole("option", { name: "Testserver" })
    .click();
  await page.keyboard.press("Escape");
  await dialog.getByRole("button", { name: "Weiter", exact: true }).click();
  await dialog.getByRole("button", { name: "Weiter", exact: true }).click();
  await dialog
    .getByRole("button", { name: "Projekt anlegen", exact: true })
    .click();

  await expect(dialog).not.toBeVisible();

  await page
    .getByRole("main")
    .getByRole("link", { name: "Mein Projekt", exact: true })
    .click();

  await expect(
    page.getByRole("heading", { name: "Mein Projekt" }),
  ).toBeVisible();

  await page
    .getByRole("main")
    .getByRole("button", { name: "Neue Unterhaltung", exact: true })
    .click();

  await expect(page.getByLabel("Aufgabe oder neue Instruktion")).toBeVisible();

  await page
    .getByLabel("Aufgabe oder neue Instruktion")
    .fill("Prüfe den Ablauf und speichere eine Notiz.");
  await page.getByRole("button", { name: "Starten" }).click();

  await expect(
    page.getByText("Die Aufgabe ist abgeschlossen.", { exact: true }),
  ).toBeVisible();
  await expect(
    page.getByText("Nicht durch den Auftrag gedeckt", { exact: true }),
  ).toBeHidden();
  await page
    .getByRole("button", { name: /Befehl ausführen.*echo test.*Abgelehnt/ })
    .click();
  await expect(
    page.getByText("Nicht durch den Auftrag gedeckt", { exact: true }),
  ).toBeVisible();

  await page.getByRole("button", { name: /^Agentendetails/ }).click();
  const detailsDialog = page.locator('[data-slot="modal-dialog"]').last();

  await expect(
    detailsDialog.getByText("Nicht durch den Auftrag gedeckt", { exact: true }),
  ).toHaveCount(0);
  await expect(
    detailsDialog.getByRole("heading", { name: "Arbeitsplan", exact: true }),
  ).toBeVisible();

  await page.keyboard.press("Escape");
  await page
    .getByRole("button", { name: "Projektaktionen", exact: true })
    .click();
  await page
    .getByRole("menuitem", { name: "Auto-Approver ↗", exact: true })
    .click();

  await expect(
    page.getByLabel("Aufgabe oder neue Instruktion"),
  ).not.toBeVisible();

  await page
    .getByRole("button", { name: "Projektaktionen", exact: true })
    .click();

  await expect(
    page.getByRole("menuitem", {
      name: "← Prüfe den Ablauf und speichere eine Notiz.",
      exact: true,
    }),
  ).toBeVisible();

  await page.keyboard.press("Escape");
  await page.screenshot({
    path: testInfo.outputPath("desktop.png"),
    fullPage: true,
  });

  await page.setViewportSize({ width: 390, height: 844 });
  await navigateToPage(page, "Projekte");

  await expect(
    page.getByRole("heading", { name: "Projekte", exact: true }),
  ).toBeVisible();
  expect(
    await page.evaluate(() => {
      if (document.documentElement.scrollWidth > window.innerWidth) {
        return [...document.querySelectorAll("body *")]
          .filter(
            (element) =>
              element.getBoundingClientRect().right > window.innerWidth + 1,
          )
          .slice(0, 12)
          .map((element) => ({
            tag: element.tagName,
            class: element.className,
            width: element.getBoundingClientRect().width,
          }));
      }
      return [];
    }),
  ).toEqual([]);

  await page.screenshot({
    path: testInfo.outputPath("mobile.png"),
    fullPage: true,
  });
  // Preferences keep the current task and survive browser navigation.
  await selectPreference(page, /Sprache/, "English");

  await expect(
    page.getByRole("heading", { name: "Projects", exact: true }),
  ).toBeVisible();

  await navigateToPage(page, "Machines");
  await page.goBack();

  await expect(
    page.getByRole("heading", { name: "Projects", exact: true }),
  ).toBeVisible();

  await selectPreference(page, /Theme/, "Light");

  await expect(page.locator("html")).toHaveClass(/light/);
  await expect(page.locator('[data-slot="select-popover"]')).toHaveCount(0);

  await page.evaluate(() => {
    document.body.style.zoom = "2";
  });

  expect(
    await page.evaluate(() => {
      if (document.documentElement.scrollWidth > window.innerWidth) {
        return [...document.querySelectorAll("body *")]
          .filter(
            (element) =>
              element.getBoundingClientRect().right > window.innerWidth + 1,
          )
          .slice(0, 12)
          .map((element) => ({
            tag: element.tagName,
            class: element.className,
            width: element.getBoundingClientRect().width,
          }));
      }
      return [];
    }),
  ).toEqual([]);

  await page.evaluate(() => {
    document.body.style.zoom = "";
  });
  await navigateToPage(page, "Keys");
  const usedKey = page
    .locator("section")
    .filter({ has: page.getByRole("heading", { name: "Key 1", exact: true }) });
  await usedKey
    .getByRole("button", { name: "Actions for Key 1", exact: true })
    .click();

  await expect(
    page.getByRole("menuitem", { name: "Remove", exact: true }),
  ).toBeDisabled();

  await page.keyboard.press("Escape");
  await page
    .getByRole("button", { name: "Generate SSH key", exact: true })
    .click();
  const unusedKey = page
    .locator("section")
    .filter({ has: page.getByRole("heading", { name: "Key 2", exact: true }) });

  await expect(unusedKey).toBeVisible();

  await chooseContextAction(page, unusedKey, "Remove");

  await expect(page.locator('[data-slot="modal-dialog"]')).toBeVisible();

  await page.getByRole("button", { name: "Cancel", exact: true }).click();

  await expect(page.locator('[data-slot="modal-dialog"]')).not.toBeVisible();
  await expect(unusedKey).toBeVisible();

  await navigateToPage(page, "Projects");
  await page
    .getByRole("main")
    .getByRole("link", { name: "Mein Projekt", exact: true })
    .click();
  await page
    .getByRole("button", { name: "Project actions", exact: true })
    .click();
  await page
    .getByRole("menuitem", { name: "Remove agent", exact: true })
    .click();
  await page
    .locator('[data-slot="modal-dialog"]')
    .getByRole("button", { name: "Remove", exact: true })
    .click();

  await expect(page.getByText("No agents yet", { exact: true })).toBeVisible();

  await openNavigation(page);
  await page
    .getByRole("button", { name: "Open settings", exact: true })
    .click();
  await page.getByRole("menuitem", { name: "Sign out", exact: true }).click();

  await expect(
    page.getByRole("heading", { name: "Sign in", exact: true }),
  ).toBeVisible();

  await page.screenshot({
    path: testInfo.outputPath("login-mobile.png"),
    fullPage: true,
  });

  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBe(true);
  expect(errors).toEqual([]);
});

async function authenticate(page: import("@playwright/test").Page) {
  const status = await page.request.post("/graphql", {
    data: { query: "query{status{needsSetup}}" },
  });
  const body = await status.json();
  const method = body.data.status.needsSetup ? "setup" : "login";
  await page.request.post("/graphql", {
    data: {
      query:
        method === "setup"
          ? 'mutation{setup(password:"browser test password",commitIdentity:{name:"Browser Test Agent",email:"browser-agent@example.test"})}'
          : 'mutation{login(password:"browser test password")}',
    },
  });
  const accounts = await page.request.post("/graphql", {
    data: { query: "query{providers{id name}}" },
  });
  const accountData = await accounts.json();
  const existing = accountData.data.providers.find(
    (provider: { id: string; name: string }) => provider.name === "Testmodell",
  );
  await page.request.post("/graphql", {
    data: {
      query: "mutation($input:ProviderInput!){saveProvider(input:$input){id}}",
      variables: {
        input: {
          id: existing?.id ?? "fixture-provider",
          name: "Testmodell",
          kind: "openai",
          authMode: "api",
          baseUrl: providerURL,
        },
      },
    },
  });
  await page.request.post("/graphql", {
    data: {
      query: "mutation($id:String!){refreshModels(providerId:$id)}",
      variables: { id: existing?.id ?? "fixture-provider" },
    },
  });
  await page.goto("/");
}

test("provider forms preserve errors, guided login and inline model catalog", async ({
  page,
}) => {
  await authenticate(page);
  await navigateToPage(page, "Provider");
  await page
    .getByRole("button", { name: "Provider hinzufügen", exact: true })
    .click();
  const dialog = page.getByRole("dialog");
  await dialog
    .getByLabel("Name", { exact: true })
    .fill("Invalid endpoint test");
  await dialog
    .getByRole("button", { name: "Erweiterte Einstellungen" })
    .click();
  await dialog.getByLabel("API-Basis-URL").fill("http://remote.example/v1");
  await dialog.getByRole("button", { name: "Speichern", exact: true }).click();

  await expect(dialog.getByLabel("API-Basis-URL")).toBeFocused();
  await expect(dialog.getByLabel("Name", { exact: true })).toHaveValue(
    "Invalid endpoint test",
  );
  await expect(
    dialog.getByText("Für entfernte Provider eine HTTPS-Adresse verwenden."),
  ).toBeVisible();

  await page.keyboard.press("Escape");

  await expect(dialog).not.toBeVisible();
  await expect(
    page.getByRole("button", { name: "Provider hinzufügen", exact: true }),
  ).toBeFocused();

  await page.route("**/graphql", async (route) => {
    const request = route.request().postDataJSON();
    if (request.query.includes("beginProviderLogin"))
      await route.fulfill({
        json: {
          data: {
            beginProviderLogin: {
              id: "fixture-login",
              providerId: request.variables.providerId,
              method: "device",
              status: "pending",
              verificationUrl: "https://auth.openai.com/codex/device",
              userCode: "ABCD-1234",
              interval: 5,
              expiresAt: new Date(Date.now() + 60000).toISOString(),
              error: "",
            },
          },
        },
      });
    else await route.continue();
  });
  await page
    .getByRole("button", { name: "Provider hinzufügen", exact: true })
    .click();
  await dialog.getByLabel("Name", { exact: true }).fill("ChatGPT subscription");
  await dialog.getByText("ChatGPT-Konto", { exact: true }).click();

  await expect(dialog.getByLabel("API-Basis-URL")).toHaveCount(0);
  await expect(dialog.getByLabel("Modell-ID")).toHaveCount(0);

  await dialog.getByRole("button", { name: "Speichern", exact: true }).click();

  await expect(dialog.getByText("ABCD-1234", { exact: true })).toBeVisible();
  await expect(
    dialog.getByRole("link", { name: "Anmeldeseite öffnen ↗" }),
  ).toHaveAttribute("href", "https://auth.openai.com/codex/device");

  await page.keyboard.press("Escape");

  await expect(dialog).not.toBeVisible();

  const account = await chooseProviderAccount(page, "ChatGPT subscription");
  await account.getByRole("button", { name: "Anmelden", exact: true }).click();

  await expect(dialog.getByText("ABCD-1234", { exact: true })).toBeVisible();

  await page.keyboard.press("Escape");

  await expect(
    account.getByRole("button", { name: "Anmelden", exact: true }),
  ).toBeFocused();

  await page.unroute("**/graphql");
  await page
    .getByRole("button", { name: "Modelle ansehen", exact: true })
    .first()
    .click();
  const catalog = page.getByRole("region", {
    name: "Sprachmodelle",
    exact: true,
  });

  await expect(
    catalog.getByRole("heading", { name: "Fixture Main", exact: true }),
  ).toBeVisible();
  await expect(dialog).toHaveCount(0);

  const modelActions = await chooseContextAction(
    page,
    catalog.locator('[data-slot="card"]').filter({
      has: page.getByRole("heading", { name: "Fixture Main", exact: true }),
    }),
    "Metadaten ergänzen",
  );
  await dialog.getByLabel("Kontextfenster (Tokens)").fill("200000");
  await dialog.getByRole("button", { name: "Speichern", exact: true }).click();

  await expect(dialog).toHaveCount(0);
  await expect(catalog.getByText(/200.000|200,000/)).toBeVisible();
  await expect(modelActions).toBeFocused();
});

test("routes, themes, languages, keyboard and recovery at desktop and mobile widths", async ({
  page,
}, testInfo) => {
  await authenticate(page);
  const consoleErrors: string[] = [];
  page.on("pageerror", (error) => consoleErrors.push(error.message));
  for (const language of ["de", "en"]) {
    await page.evaluate(
      (locale) => localStorage.setItem("zentrale:locale", locale),
      language,
    );
    // Use the actual preference controls so app state and browser language agree.
    await selectPreference(
      page,
      /Sprache|Language/,
      language === "de" ? "Deutsch" : "English",
    );
    for (const theme of ["dark", "light"]) {
      await selectPreference(
        page,
        /Design|Theme/,
        language === "de"
          ? theme === "dark"
            ? "Dunkel"
            : "Hell"
          : theme === "dark"
            ? "Dark"
            : "Light",
      );
      for (const width of [1280, 390]) {
        await page.setViewportSize({ width, height: 900 });
        for (const route of [
          "projects",
          "machines",
          "keys",
          "providers",
          "settings",
        ]) {
          await page.goto(`/#/${route}`);

          await expect(page.locator("main h1")).toBeVisible();
          await expect(page.getByRole("main").getByRole("alert")).toHaveCount(
            0,
          );
          expect(
            await page.evaluate(
              () => document.documentElement.scrollWidth <= innerWidth,
            ),
          ).toBe(true);
        }
      }
    }
  }

  await page.screenshot({
    path: testInfo.outputPath("settings-mobile-light-english.png"),
    fullPage: true,
  });
  // A failed refresh keeps already visible results and offers a recovery action.
  await page.goto("/#/providers");

  await expect(
    page.getByRole("heading", { name: "Testmodell", exact: true }),
  ).toBeVisible();

  await page.route("**/graphql", async (route) => {
    if (route.request().postDataJSON().query.includes("machines(limit:500)"))
      await route.fulfill({
        json: { errors: [{ message: "Temporary connection failure" }] },
      });
    else await route.continue();
  });
  await chooseContextAction(
    page,
    await chooseProviderAccount(page, "Testmodell"),
    "Refresh models",
  );

  await expect(
    page
      .getByRole("main")
      .getByRole("alert")
      .filter({ hasText: "Temporary connection failure" }),
  ).toBeVisible();
  await expect(
    page.getByRole("heading", { name: "Testmodell", exact: true }),
  ).toBeVisible();

  await page.unroute("**/graphql");
  await page.getByRole("button", { name: "Try again" }).first().click();

  await expect(page.getByRole("main").getByRole("alert")).toHaveCount(0);

  await page.keyboard.press("Tab");

  expect(await page.evaluate(() => document.activeElement?.tagName)).not.toBe(
    "BODY",
  );
  expect(consoleErrors).toEqual([]);
});

test("model batches preserve results beyond 500 entries and reset on search", async ({
  page,
}) => {
  await authenticate(page);
  const saved = await page.request.post("/graphql", {
    data: {
      query: "mutation($input:ProviderInput!){saveProvider(input:$input){id}}",
      variables: {
        input: {
          id: "large-catalog",
          name: "Large catalog",
          kind: "openai",
          baseUrl: providerURL + "/large",
        },
      },
    },
  });

  expect((await saved.json()).errors).toBeUndefined();

  await page.request.post("/graphql", {
    data: { query: 'mutation{refreshModels(providerId:"large-catalog")}' },
  });
  await page.goto("/#/providers");
  const account = await chooseProviderAccount(page, "Large catalog");
  await account
    .getByRole("button", { name: "Modelle ansehen", exact: true })
    .click();
  const dialog = page.getByRole("region", {
    name: "Sprachmodelle",
    exact: true,
  });
  const headings = dialog.getByRole("heading", { name: /Catalog model / });

  await expect(headings).toHaveCount(50);

  for (let count = 100; count <= 650; count += 50) {
    await dialog
      .getByRole("button", { name: "Mehr anzeigen", exact: true })
      .click();
    await expect(headings).toHaveCount(count);
  }

  await expect(
    dialog.getByRole("button", { name: "Mehr anzeigen", exact: true }),
  ).not.toBeVisible();

  await dialog.getByLabel("Modelle suchen").fill("0649");

  await expect(headings).toHaveCount(1);
  await expect(headings).toHaveText("Catalog model 0649");

  await dialog.getByLabel("Modelle suchen").fill("");

  await expect(headings).toHaveCount(50);

  await page.keyboard.press("Escape");
});

test("mobile navigation traps focus, closes on Escape and restores its trigger", async ({
  page,
}, testInfo) => {
  await authenticate(page);
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/#/projects");
  const trigger = page.getByRole("button", {
    name: "Menü öffnen",
    exact: true,
  });
  const drawer = page.getByRole("dialog", {
    name: "Hauptnavigation",
    exact: true,
  });
  await trigger.focus();
  await page.keyboard.press("Enter");

  await expect(drawer).toBeVisible();

  await page.keyboard.press("Shift+Tab");

  expect(
    await drawer.evaluate((element) =>
      element.contains(document.activeElement),
    ),
  ).toBe(true);
  await expect(
    page.locator('[data-slot="drawer-content"][data-entering="true"]'),
  ).toHaveCount(0);

  const bounds = await drawer.boundingBox();

  expect(bounds?.x).toBe(0);
  expect(bounds?.width).toBeGreaterThanOrEqual(240);
  expect(bounds?.width).toBeLessThan(390);

  await page.screenshot({
    path: testInfo.outputPath("navigation-mobile.png"),
    fullPage: true,
  });
  await page.keyboard.press("Escape");

  await expect(drawer).not.toBeVisible();
  await expect(trigger).toBeFocused();

  await navigateToPage(page, "Schlüssel");

  await expect(
    page.getByRole("heading", { name: "Schlüssel", exact: true }),
  ).toBeVisible();
  await expect(drawer).not.toBeVisible();

  await page.goBack();

  await expect(
    page.getByRole("heading", { name: "Projekte", exact: true }),
  ).toBeVisible();

  await page.setViewportSize({ width: 1280, height: 900 });

  await expect(trigger).not.toBeVisible();
  await expect(
    page.getByRole("navigation", { name: "Hauptnavigation" }),
  ).toHaveCount(1);
  await expect(
    page.getByRole("link", { name: "Alle Projekte", exact: true }),
  ).toHaveAttribute("aria-current", "page");
});
