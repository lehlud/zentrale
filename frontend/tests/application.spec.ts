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
    await page.getByRole("tab", { name: /^(Projekte|Projects)$/ }).click();
    await page
      .getByRole("link", { name: /^(Alle Projekte|All projects)$/ })
      .click();
  } else {
    const labels: Record<string, string> = {
      Schlüssel: "Schlüssel",
      Keys: "Keys",
      Provider: "Provider und Modelle",
      Einstellungen: "Persönliche Einstellungen",
      Machines: "Machines",
      Maschinen: "Maschinen",
    };
    const english = (await page.locator("html").getAttribute("lang")) === "en";
    const englishLabels: Record<string, string> = {
      Keys: "Keys",
      Provider: "Providers and models",
      Settings: "Personal settings",
      Machines: "Machines",
    };
    const menu = /^(Einstellungen|Settings)$/.test(name)
      ? /^(Einstellungen öffnen|Open settings)$/
      : /^Admin$/;
    await page.getByRole("button", { name: menu }).click();
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
            arguments: JSON.stringify({
              command: "echo test",
              timeout_ms: 5000,
            }),
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
  await dialog.getByRole("button", { name: /Sandbox/ }).click();
  await page
    .getByRole("option", { name: "Nur Auto-Approval", exact: true })
    .click();
  await dialog.getByRole("button", { name: "Speichern", exact: true }).click();

  await expect(dialog).not.toBeVisible();
  await expect(page.getByRole("heading", { name: "Testserver" })).toBeVisible();

  await page
    .getByRole("button", { name: "Verbindung prüfen", exact: true })
    .click();

  await expect(
    page.getByText("Sandbox: Ohne Sandbox", { exact: true }),
  ).toBeVisible();

  await page.reload();

  await expect(
    page.getByText("Sandbox: Ohne Sandbox", { exact: true }),
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

  await navigateToPage(page, "Admin-Einstellungen");
  const projectModels = page.locator("div.rounded-xl").filter({
    has: page.getByRole("heading", { name: "Projekte", exact: true }),
  });
  for (const model of ["Fixture Main", "Fixture Approver"]) {
    await projectModels
      .getByRole("button", { name: "Modell freigeben" })
      .click();
    const chooser = page.getByRole("dialog", { name: "Modell freigeben" });
    await chooser.getByRole("option", { name: new RegExp(model) }).click();
    await chooser.getByRole("button", { name: "Auswahl übernehmen" }).click();
    await expect(chooser).toHaveCount(0);
  }

  for (const [role, model] of [
    ["Standardmodell", "fixture-main"],
    ["Auto-Approver", "fixture-approver"],
  ]) {
    const select = projectModels.getByRole("combobox", {
      name: role,
      exact: true,
    });
    const option = select.getByRole("option", { name: new RegExp(model) });
    await select.selectOption({ label: (await option.textContent())! });
  }

  const saveModels = projectModels.getByRole("button", {
    name: "Speichern",
    exact: true,
  });
  await saveModels.click();
  await expect(saveModels).toBeDisabled();

  await navigateToPage(page, "Projekte");
  await page.getByRole("button", { name: "Projekt anlegen" }).click();
  dialog = page.locator('[data-slot="modal-dialog"]');
  await dialog.getByLabel("Name", { exact: true }).fill("Mein Projekt");

  await expect(
    dialog.getByLabel("Projektverzeichnis auf der Fernmaschine"),
  ).toHaveCount(0);

  await dialog.getByRole("button", { name: "Weiter", exact: true }).click();
  await dialog.getByRole("button", { name: /Fernmaschine oder Pool/ }).click();
  await page
    .locator('[data-slot="select-popover"]:not([data-exiting])')
    .getByRole("option", { name: "Testserver" })
    .click();

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
    page.getByRole("heading", { name: "Mein Projekt", exact: true }),
  ).toHaveCount(0);

  await page
    .getByRole("main")
    .getByRole("button", { name: "Neue Unterhaltung im Projekt", exact: true })
    .click();

  await expect(page.getByLabel("Aufgabe oder neue Instruktion")).toBeVisible();

  await page
    .getByLabel("Aufgabe oder neue Instruktion")
    .fill("Prüfe den Ablauf und speichere eine Notiz.");
  await page.getByLabel("Aufgabe oder neue Instruktion").press("Enter");

  await expect(
    page.getByText("Die Aufgabe ist abgeschlossen.", { exact: true }),
  ).toBeVisible();
  await expect(
    page.getByText(/^Nicht durch den Auftrag gedeckt \(approvalId:/),
  ).toBeHidden();
  await page
    .getByRole("button", { name: /Befehl ausführen.*echo test.*Abgelehnt/ })
    .click();
  await expect(
    page.getByText(/^Nicht durch den Auftrag gedeckt \(approvalId:/),
  ).toBeVisible();

  await page.getByRole("button", { name: "Kontextfenster" }).click();
  const detailsDialog = page.getByRole("dialog", { name: "Kontextfenster" });

  await expect(
    detailsDialog.getByText("Nicht durch den Auftrag gedeckt", { exact: true }),
  ).toHaveCount(0);
  await expect(
    detailsDialog.getByText(
      "Der Kontext wird bei Bedarf automatisch verdichtet.",
    ),
  ).toBeVisible();

  await page.keyboard.press("Escape");
  await page
    .getByRole("button", { name: "Projektaktionen", exact: true })
    .click();
  await expect(
    page.getByRole("menuitem", { name: "Auto-Approver ↗", exact: true }),
  ).toHaveCount(0);
  await page.keyboard.press("Escape");

  // Approver conversations remain addressable and read-only; the redundant
  // navigation links are deliberately no longer part of the actions menu.
  const location = new URL(page.url());
  const projectRoute = location.hash.split("?")[0];
  const projectId = projectRoute.split("/").at(-1)!;
  const response = await page.request.post("/graphql", {
    data: {
      query: "query($id:String!){agents(projectId:$id){id role name}}",
      variables: { id: projectId },
    },
  });
  const result = await response.json();
  expect(result.errors).toBeUndefined();
  const sessions: { id: string; role: string; name: string }[] =
    result.data.agents;
  const approver = sessions.find((item) => item.role === "approver");
  const main = sessions.find((item) => item.role === "main");
  expect(approver).toBeDefined();
  expect(main).toBeDefined();
  await page.goto(
    `${location.origin}${location.pathname}${projectRoute}?agent=${approver!.id}`,
  );
  await expect(
    page.getByRole("button", { name: /^Agent wechseln:/ }),
  ).toBeVisible();

  await expect(
    page.getByLabel("Aufgabe oder neue Instruktion"),
  ).not.toBeVisible();

  await page.getByRole("button", { name: /^Agent wechseln:/ }).click();
  await page
    .getByRole("menuitemradio", {
      name: main!.name,
      exact: true,
    })
    .click();
  await expect(page.getByLabel("Aufgabe oder neue Instruktion")).toBeVisible();
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
  // Deliver the authoritative snapshot before the HTTP acknowledgement, as
  // parallel live queries can do. The empty view alone does not mean the
  // mutation's selection/navigation callback has finished.
  await page.route("**/graphql", async (route) => {
    const { query, variables } = route.request().postDataJSON();
    if (query.includes("deleteResource") && variables?.kind === "agent") {
      const response = await route.fetch();
      await expect(
        page.getByText("No agents yet", { exact: true }),
      ).toBeVisible();
      await route.fulfill({ response });
    } else {
      await route.continue();
    }
  });
  const removedAgent = page.waitForResponse(
    (response) =>
      response.url().endsWith("/graphql") &&
      response.request().method() === "POST" &&
      response.request().postDataJSON().query.includes("deleteResource"),
  );
  await page
    .locator('[data-slot="modal-dialog"]')
    .getByRole("button", { name: "Remove", exact: true })
    .click();

  expect((await (await removedAgent).json()).errors).toBeUndefined();
  await expect(page).toHaveURL(/[?&]agent=(?:&|$)/);
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

test("Modal automatic setup only requires an API token pair", async ({
  page,
}) => {
  await authenticate(page);
  await page.goto("/#/providers");
  await page.route("**/graphql", async (route) => {
    const request = route.request().postDataJSON();
    if (!request?.query?.includes("setupModalHumanizer")) {
      await route.continue();
      return;
    }
    await route.fulfill({
      json: { data: { setupModalHumanizer: { id: "modal-managed-fixture" } } },
    });
  });
  await page.getByRole("button", { name: "Provider hinzufügen" }).click();
  const dialog = page.getByRole("dialog");
  await dialog.locator('[data-field="kind"]').getByRole("button").click();
  await dialog.getByRole("option", { name: "Modal", exact: true }).click();
  for (const field of ["Name", "App-Name", "Web-Function-URL"]) {
    await expect(dialog.getByLabel(field, { exact: true })).toHaveCount(0);
  }
  await dialog.getByLabel("Token-ID", { exact: true }).fill("ak-fixture");
  await dialog.getByLabel("Token-Secret", { exact: true }).fill("as-fixture");
  const request = page.waitForRequest(
    (request) => request.postData()?.includes("setupModalHumanizer") ?? false,
  );
  await dialog
    .getByRole("button", { name: "Bereitstellen", exact: true })
    .click();
  expect((await request).postDataJSON().variables).toEqual({
    tokenId: "ak-fixture",
    tokenSecret: "as-fixture",
    modelId: "jialinyyzz/humanizer",
  });
  await expect(dialog).toHaveCount(0);
});

test("Modal supports separate proxy and API token pairs", async ({ page }) => {
  await authenticate(page);
  await page.goto("/#/providers");
  const dialog = page.getByRole("dialog");
  const input = (label: string) => dialog.getByLabel(label, { exact: true });
  const save = dialog.getByRole("button", { name: "Speichern", exact: true });
  for (const native of [false, true]) {
    await page
      .getByRole("button", { name: "Provider hinzufügen", exact: true })
      .click();
    await dialog.locator('[data-field="kind"]').getByRole("button").click();
    await dialog.getByRole("option", { name: "Modal", exact: true }).click();
    await dialog.getByRole("button", { name: /Modal-Zugang/ }).click();
    await dialog
      .getByRole("option", {
        name: native
          ? "API-Token (Modal-Funktion)"
          : "Proxy-Token (Web Function)",
      })
      .click();
    await input("Name").fill(
      native ? "Modal native fixture" : "Modal proxy fixture",
    );
    if (native) {
      await expect(input("Web-Function-URL")).toHaveCount(0);
      await input("App-Name").fill("zentrale-humanizer");
      await input("Funktionsname").fill("run_batch");
    } else {
      await input("Web-Function-URL").fill(
        "https://fixture--rewrite.modal.run",
      );
      await expect(input("App-Name")).toHaveCount(0);
    }
    await input("Token-ID").fill(native ? "ak-fixture" : "wk-fixture");
    await save.click();
    await expect(input("Token-Secret")).toBeFocused();
    await expect(input("Token-Secret")).toHaveAttribute("type", "password");
    await input("Token-Secret").fill(native ? "as-fixture" : "ws-fixture");
    await save.click();
    await expect(dialog).not.toBeVisible();
  }
  const response = await page.request.post("/graphql", {
    data: { query: "{providers(limit:50){name kind baseUrl connected}}" },
  });
  const { data } = await response.json();
  expect(data.providers).toEqual(
    expect.arrayContaining([
      {
        name: "Modal proxy fixture",
        kind: "modal",
        baseUrl: "https://fixture--rewrite.modal.run",
        connected: true,
      },
      {
        name: "Modal native fixture",
        kind: "modal",
        baseUrl: "modal://main/zentrale-humanizer/run_batch",
        connected: true,
      },
    ]),
  );
});

test("provider forms preserve errors, guided login and inline model catalog", async ({
  page,
}) => {
  const fixtureLogin = (providerId: string) => ({
    id: "fixture-login",
    providerId,
    method: "device",
    status: "pending",
    verificationUrl: "https://auth.openai.com/codex/device",
    userCode: "ABCD-1234",
    interval: 5,
    expiresAt: new Date(Date.now() + 60000).toISOString(),
    error: "",
  });
  let loginProvider = "";
  // Keep this an actual backend integration. Only the explicitly mocked OAuth
  // transaction is synthetic, including its read-only push subscription.
  await page.routeWebSocket("**/events?*", (socket) => {
    const server = socket.connectToServer();
    socket.onMessage((message) => {
      const frame = JSON.parse(String(message));
      if (
        frame.type === "subscribe" &&
        frame.query.includes("providerLogin(")
      ) {
        socket.send(
          JSON.stringify({
            type: "query_result",
            requestId: frame.requestId,
            result: { data: { providerLogin: fixtureLogin(loginProvider) } },
          }),
        );
      } else server.send(message);
    });
  });
  await authenticate(page);
  await navigateToPage(page, "Provider");
  await page
    .getByRole("button", { name: "Provider hinzufügen", exact: true })
    .click();
  const dialog = page.getByRole("dialog");
  await dialog
    .getByLabel("Name", { exact: true })
    .fill("Invalid endpoint test");
  const advanced = dialog.getByRole("button", {
    name: "Erweiterte Einstellungen",
  });
  const endpoint = dialog.getByLabel("API-Basis-URL");
  await advanced.click();
  await endpoint.fill("http://remote.example/v1");
  await advanced.click();
  await expect(advanced).toHaveAttribute("aria-expanded", "false");
  await dialog.getByRole("button", { name: "Speichern", exact: true }).click();

  await expect(advanced).toHaveAttribute("aria-expanded", "true");
  await expect(endpoint).toBeFocused();
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
    if (request.query.includes("beginProviderLogin")) {
      loginProvider = request.variables.providerId;
      await route.fulfill({
        json: {
          data: {
            beginProviderLogin: fixtureLogin(loginProvider),
          },
        },
      });
    } else if (request.query.includes("providerLogin(")) {
      await route.fulfill({
        json: { data: { providerLogin: fixtureLogin(loginProvider) } },
      });
    } else await route.continue();
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
  // Keep this a real-backend E2E: forward traffic and only inject the explicit
  // recovery error into matching server-generated configuration snapshots.
  let failConfiguration = false;
  let recoverConfigurationOnRetry = false;
  const configurationFaults = new Set<() => void>();
  await page.routeWebSocket("**/events?*", (socket) => {
    const server = socket.connectToServer();
    const configurationSubscriptions = new Set<string>();
    const injectFault = () => {
      for (const requestId of configurationSubscriptions)
        socket.send(
          JSON.stringify({
            type: "query_result",
            requestId,
            result: { errors: [{ message: "Temporary connection failure" }] },
          }),
        );
    };
    configurationFaults.add(injectFault);
    socket.onClose(() => configurationFaults.delete(injectFault));
    socket.onMessage((message) => {
      const frame = JSON.parse(message.toString());
      if (
        frame.type === "subscribe" &&
        frame.query.includes("machines(limit:500)")
      ) {
        // Keep failures active until the user's retry actually restarts the
        // subscription; an in-flight snapshot must not clear the alert first.
        if (recoverConfigurationOnRetry) {
          failConfiguration = false;
          recoverConfigurationOnRetry = false;
        }
        configurationSubscriptions.add(frame.requestId);
      } else if (frame.type === "unsubscribe") {
        configurationSubscriptions.delete(frame.requestId);
      }
      server.send(message);
    });
    server.onMessage((message) => {
      const frame = JSON.parse(message.toString());
      if (
        failConfiguration &&
        frame.type === "query_result" &&
        configurationSubscriptions.has(frame.requestId)
      ) {
        socket.send(
          JSON.stringify({
            ...frame,
            result: { errors: [{ message: "Temporary connection failure" }] },
          }),
        );
      } else {
        socket.send(message);
      }
    });
  });
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

  failConfiguration = true;
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

  // Unchanged model catalogs are correctly deduplicated by the server. Inject
  // a transport-level subscription failure explicitly, rather than requiring
  // a redundant snapshot or HTTP refetch just to exercise error recovery.
  for (const injectFault of configurationFaults) injectFault();

  await expect(
    page
      .getByRole("main")
      .getByRole("alert")
      .filter({ hasText: "Temporary connection failure" }),
  ).toBeVisible();
  await expect(
    page.getByRole("heading", { name: "Testmodell", exact: true }),
  ).toBeVisible();

  recoverConfigurationOnRetry = true;
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

test("project move selects a different target and displays a recoverable failure", async ({
  page,
}) => {
  await authenticate(page);
  const query = async (
    query: string,
    variables: Record<string, unknown> = {},
  ) => {
    const response = await page.request.post("/graphql", {
      data: { query, variables },
    });
    const body = await response.json();
    expect(body.errors).toBeUndefined();
    return body.data;
  };
  const machine = async (name: string) => {
    const { generateSSHKey } = await query("mutation{generateSSHKey{id}}");
    const { saveMachine } = await query(
      "mutation($input:MachineInput!){saveMachine(input:$input){id}}",
      {
        input: {
          name,
          host: "127.0.0.1",
          port: 1,
          user: "developer",
          keyId: generateSSHKey.id,
        },
      },
    );
    return saveMachine.id as string;
  };
  const origin = await machine("Umzugsquelle");
  const destination = await machine("Umzugsziel");
  const { saveProject } = await query(
    "mutation($input:ProjectInput!){saveProject(input:$input){id}}",
    { input: { name: "Umzugstest", machineIds: [origin] } },
  );

  await page.goto("/#/projects");
  await page.getByRole("button", { name: "Aktionen für Umzugstest" }).click();
  await page.getByRole("menuitem", { name: "Projekt umziehen" }).click();
  const dialog = page.getByRole("dialog");
  await dialog.getByRole("button", { name: /Fernmaschine oder Pool/ }).click();
  await expect(page.getByRole("option", { name: /Umzugsquelle/ })).toHaveCount(
    0,
  );
  await page.getByRole("option", { name: /Umzugsziel/ }).click();
  await dialog.getByRole("button", { name: "Umzug starten" }).click();
  await expect(
    dialog.getByRole("button", { name: "Umzug fortsetzen" }),
  ).toBeVisible();
  expect(
    await query(
      "query($id:String!){project(id:$id){machineIds} projectMove(projectId:$id){machineId phase}}",
      { id: saveProject.id },
    ),
  ).toEqual({
    project: { machineIds: [origin] },
    projectMove: { machineId: destination, phase: "preflight_failed" },
  });
});
