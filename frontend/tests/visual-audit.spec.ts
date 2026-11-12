import { expect, test } from "@playwright/test";
import { installPushFixture } from "./fixtures/push-fixture";
import {
  bootstrapData,
  isTranscriptQuery,
  selectedAgentId,
} from "./fixtures/bootstrap-data";
import { mkdir, writeFile } from "node:fs/promises";
import path from "node:path";

import { measureThemeContrast } from "./fixtures/ui-contrast";
import {
  chooseContextAction,
  chooseProviderAccount,
  openProviderModels,
  setLogDiagnostics,
} from "./fixtures/context-actions";
import {
  agent,
  agents,
  config,
  gitConnections,
  models,
  project,
  sources,
  transcript,
} from "./fixtures/visual-data";

const directory = process.env.ZENTRALE_AUDIT_DIR ?? "artifacts/ui";

const baseline = process.env.ZENTRALE_AUDIT_BASELINE === "1";

const variants = [
  {
    name: "desktop-en-light",
    width: 1440,
    height: 900,
    locale: "en",
    theme: "light",
  },
  {
    name: "tablet-de-dark",
    width: 768,
    height: 1024,
    locale: "de",
    theme: "dark",
  },
  {
    name: "desktop-de-light",
    width: 1440,
    height: 1000,
    locale: "de",
    theme: "light",
  },
  {
    name: "mobile-de-light",
    width: 390,
    height: 844,
    locale: "de",
    theme: "light",
  },
  {
    name: "desktop-en-dark",
    width: 1280,
    height: 900,
    locale: "en",
    theme: "dark",
  },
  {
    name: "mobile-en-dark",
    width: 390,
    height: 844,
    locale: "en",
    theme: "dark",
  },
];

for (const variant of variants) {
  test(`visual review: ${variant.name}`, async ({ page }) => {
    test.setTimeout(180000);
    page.setDefaultTimeout(10000);
    await mkdir(directory, { recursive: true });
    const captures: { name: string; file: string; overflow: number }[] = [];
    const errors: string[] = [];
    page.on("pageerror", (error) => errors.push(error.message));
    page
      .context()
      .on("page", (opened) =>
        opened.on("pageerror", (error) => errors.push(error.message)),
      );
    await page.setViewportSize(variant);
    await page.addInitScript(({ locale, theme }) => {
      localStorage.setItem("zentrale-locale", locale);
      localStorage.setItem("zentrale-theme", theme);
    }, variant);
    // UI fixtures never contact providers, SSH machines, or authentication services.
    let state: "populated" | "empty" | "error" | "loading" = "populated";
    let auth: "authenticated" | "login" | "setup" = "authenticated";
    let loginError = false;
    let emptyTranscript = false;
    let prompts = 0;
    let editorRunning = false;
    let connectionMode: "untrusted" | "connected" | "error" = "untrusted";
    let editorMode: "untrusted" | "connected" | "error" | "loading" =
      "untrusted";
    const hostKey = {
      host: "training-01.compute.example.org:22",
      fingerprint: config.trustedKeys[0].fingerprint,
      publicKey: config.trustedKeys[0].publicKey,
    };
    const noHostKey = { host: "", fingerprint: "", publicKey: "" };
    const resolve = async (
      query: string,
      variables: Record<string, any> = {},
    ) => {
      const send = (data: unknown) => bootstrapData(query, data);
      if (query.includes("status{")) {
        return send({
          status: {
            needsSetup: auth === "setup",
            authenticated: auth === "authenticated",
            githubConnected: false,
          },
        });
      }

      if (
        query.includes("beginProviderLogin") ||
        query.includes("providerLogin(")
      ) {
        const key = query.includes("beginProviderLogin")
          ? "beginProviderLogin"
          : "providerLogin";
        return send({
          [key]: {
            id: "login",
            providerId: variables.providerId,
            method:
              variables.providerId === "openrouter" ? "browser" : "device",
            status: loginError ? "error" : "pending",
            verificationUrl: "https://example.org/verify",
            userCode: variables.providerId === "openrouter" ? "" : "ABCD-EFGH",
            interval: 60,
            error: loginError
              ? english
                ? "Sign-in expired. Please try again."
                : "Die Anmeldung ist abgelaufen. Bitte erneut versuchen."
              : "",
          },
        });
      }

      if (query.includes("cancelProviderLogin")) {
        return send({ cancelProviderLogin: true });
      }

      if (query.includes("testMachine") || query.includes("prepareAgent")) {
        const key = query.includes("testMachine")
          ? "testMachine"
          : "prepareAgent";
        return send({
          [key]: {
            connected: connectionMode === "connected",
            error:
              connectionMode === "error"
                ? "SSH authentication failed. Check the user and SSH key."
                : "",
            hostKey: connectionMode === "untrusted" ? hostKey : noHostKey,
            sandbox: config.sandboxStatuses[0],
          },
        });
      }

      if (query.includes("trustHostKey")) {
        expect(variables.publicKey).toBe(hostKey.publicKey);

        connectionMode = "connected";
        editorMode = "connected";

        return send({ trustHostKey: true });
      }

      if (query.includes("editorSession(")) {
        return send({
          editorSession: editorRunning
            ? {
                id: "session",
                projectId: project.id,
                machineId: "training",
                machineName: "GPU-Worker Frankfurt",
                url: "/ide/forecast/session/",
              }
            : {
                id: "",
                projectId: "",
                machineId: "",
                machineName: "",
                url: "",
              },
        });
      }

      if (
        query.includes("project(id:") &&
        !query.includes("agents(projectId:")
      ) {
        return send({ project });
      }

      if (query.includes("startEditor")) {
        editorRunning = editorMode === "connected";
        if (editorMode === "loading") {
          return;
        }

        return send({
          startEditor: {
            session: {
              id: "session",
              projectId: project.id,
              machineId: "training",
              machineName: "GPU-Worker Frankfurt",
              url: "/ide/forecast/session/?folder=%2Fsrv%2Fprojects",
            },
            error:
              editorMode === "error"
                ? "remote editor disconnected; reconnect the editor"
                : "",
            hostKey: editorMode === "untrusted" ? hostKey : noHostKey,
          },
        });
      }

      if (query.includes("stopEditor")) {
        editorRunning = false;
        return send({ stopEditor: true });
      }

      if (query.includes("submitInput(agentId:")) {
        prompts++;
        return send({ submitInput: { ...variables, status: "active" } });
      }

      if (query.includes("resumeInputs(")) {
        return send({ resumeInputs: true });
      }

      if (query.startsWith("mutation")) {
        throw new Error(
          english
            ? "Saving failed. Please try again."
            : "Speichern fehlgeschlagen. Bitte erneut versuchen.",
        );
      }

      if (state === "loading") {
        return new Promise<never>(() => {});
      }
      // Released by navigation; deliberately pending read.
      if (state === "error") {
        throw new Error(
          english
            ? "The connection was interrupted. Please try again."
            : "Die Verbindung wurde unterbrochen. Bitte erneut versuchen.",
        );
      }

      if (query.includes("machines(limit:500)")) {
        const data = structuredClone(config);
        if (state === "empty") {
          data.machines = [];
          data.projects = [];
          data.pools = [];
          data.providers = [];
          data.sshKeys = [];
          data.trustedKeys = [];
          data.modelDefaults.main = { providerId: "", modelId: "" };
          data.modelDefaults.approver = { providerId: "", modelId: "" };
        }

        return send(data);
      }

      if (isTranscriptQuery(query)) {
        const data = structuredClone(transcript);
        data.agent = structuredClone(
          agents.find((item) => item.id === selectedAgentId(variables)) ??
            agent,
        );
        if (data.agent.role === "approver") {
          data.messages = [
            {
              ...data.messages[1],
              id: "review",
              agentId: data.agent.id,
              content: JSON.stringify({
                allowed: false,
                reason: "Trainingsparameter zuerst bestätigen.",
              }),
            },
          ];
          data.questions = [];
          data.jobs = [];
          data.notes = [];
        }

        if (emptyTranscript) {
          data.messages = [];
          data.jobs = [];
          data.approvals = [];
          data.notes = [];
          data.questions = [];
          data.agent.summary = "";
        }

        return send({
          ...data,
          agentRunState: {
            agentId: variables.id,
            phase: connectionMode === "untrusted" && prompts ? "error" : "idle",
            error: "",
            hostKey:
              connectionMode === "untrusted" && prompts ? hostKey : noHostKey,
          },
        });
      }

      if (query.includes("agents(projectId:")) {
        return send({
          project:
            state === "empty"
              ? { ...project, machineIds: [], poolIds: [] }
              : project,
          agents: state === "empty" ? [] : agents,
        });
      }

      if (query.includes("projects(limit:500)"))
        return send({ projects: state === "empty" ? [] : config.projects });

      if (query.includes("sources(projectId:")) {
        return send({
          sources: state === "empty" ? [] : sources,
          sshKeys: config.sshKeys,
        });
      }

      if (query.includes("approvals(agentId:")) {
        return send({ approvals: transcript.approvals });
      }

      if (query.includes("gitConnections")) {
        return send({ gitConnections });
      }

      if (query.includes("models(providerId:")) {
        return send({
          models: models.filter((item) =>
            `${item.name} ${item.id}`
              .toLowerCase()
              .includes((variables.search ?? "").toLowerCase()),
          ),
        });
      }

      if (query.includes("model(providerId:")) {
        return send({
          model: models.find((item) => item.id === variables.modelId) ?? null,
        });
      }

      throw new Error(`Missing visual fixture: ${query}`);
    };
    const push = await installPushFixture(page, resolve);
    await page.context().route("**/graphql", async (route) => {
      const { query, variables = {} } = route.request().postDataJSON();
      try {
        const data = await resolve(query, variables);
        if (query.trimStart().startsWith("mutation")) await push.push();
        await route.fulfill({ json: { data } });
      } catch (error) {
        await route.fulfill({
          json: { errors: [{ message: String((error as Error).message) }] },
        });
      }
    });
    await page.context().route("**/ide/**", (route) =>
      route.fulfill({
        contentType: "text/html",
        body: '<html lang="de"><body style="margin:0;background:#202024;color:#eee;font:16px system-ui;padding:32px"><h1>Editor-Fixture</h1><p>HTTP und WebSocket werden im Backend separat über SSH geprüft.</p></body></html>',
      }),
    );
    const english = variant.locale === "en";
    const label = (de: string, en: string) => (english ? en : de);
    const dialog = () =>
      page.locator('[data-slot="modal-dialog"]:not([data-exiting])').last();
    let navigation = 0;

    async function go(route: string) {
      await page.goto(`/?visual=${navigation++}#/${route}`);
      await expect(page.locator("main")).toBeVisible();
      if (state !== "loading")
        await expect(page.locator('[data-slot="spinner"]')).toHaveCount(0);
    }

    async function capture(name: string, target = page) {
      await target.evaluate(() => document.fonts.ready);
      if (!(await dialog().isVisible()))
        await target.evaluate(() => window.scrollTo(0, 0));

      const file = `${variant.name}-${String(captures.length + 1).padStart(2, "0")}-${name}.png`;
      await target.screenshot({
        path: path.join(directory, file),
        fullPage: !(await dialog().isVisible()),
        animations: "disabled",
      });
      const overflow = await target.evaluate(
        () => document.documentElement.scrollWidth - innerWidth,
      );
      captures.push({ name, file, overflow });
    }

    async function close() {
      const id = await dialog().getAttribute("id");
      const closing = page.locator(`[data-slot="modal-dialog"][id="${id}"]`);
      await page.keyboard.press("Escape");

      await expect(closing).toHaveCount(0);

      if (await dialog().isVisible()) {
        await expect
          .poll(() =>
            dialog().evaluate((element) =>
              element.contains(document.activeElement),
            ),
          )
          .toBe(true);
      }
    }

    await go("providers");
    const contrasts = await measureThemeContrast(page);
    await capture("providers");
    const providerActions = page
      .getByRole("button", { name: /^(Aktionen für|Actions for) / })
      .first();
    await providerActions.press("Enter");

    await expect(
      page.getByRole("menuitem", {
        name: label("Bearbeiten", "Edit"),
        exact: true,
      }),
    ).toBeVisible();

    await capture("provider-actions");
    await page.keyboard.press("Escape");

    await expect(providerActions).toBeFocused();

    await page
      .getByRole("button", {
        name: label("Provider hinzufügen", "Add provider"),
        exact: true,
      })
      .click();
    await capture("provider-create");
    await dialog()
      .getByRole("button", {
        name: label("Erweiterte Einstellungen", "Advanced settings"),
        exact: true,
      })
      .click();
    await capture("provider-advanced");
    const measurements = await dialog()
      .locator("[data-field]")
      .evaluateAll((fields) =>
        fields.map((field) => {
          const control = field.querySelector(
            'input:not([type="hidden"]), [data-slot="select-trigger"], textarea',
          );
          return {
            key: field.getAttribute("data-field"),
            top: field.getBoundingClientRect().top,
            bottom: field.getBoundingClientRect().bottom,
            height: control?.getBoundingClientRect().height ?? 0,
          };
        }),
      );
    if (!baseline) {
      for (const field of measurements)
        expect(
          field.height,
          `${field.key}: touch target`,
        ).toBeGreaterThanOrEqual(44);
      for (let index = 1; index < measurements.length; index++)
        expect(
          measurements[index].top - measurements[index - 1].bottom,
          "space between fields",
        ).toBeGreaterThanOrEqual(19);
    }

    await dialog()
      .getByRole("button", { name: /Provider/ })
      .click();
    await capture("provider-options");
    await page.keyboard.press("Escape");
    await dialog()
      .getByRole("button", { name: label("Speichern", "Save"), exact: true })
      .click();
    await capture("provider-invalid");

    await expect(
      dialog().getByRole("textbox", { name: /^Name/ }),
    ).toBeFocused();

    await dialog()
      .getByRole("textbox", { name: /^Name/ })
      .fill("Forschungsteam");
    await dialog()
      .getByRole("button", { name: label("Speichern", "Save"), exact: true })
      .click();

    await expect(dialog().getByRole("alert")).toBeVisible();
    await expect(dialog().getByRole("textbox", { name: /^Name/ })).toHaveValue(
      "Forschungsteam",
    );

    await capture("provider-save-error");
    await close();
    await openProviderModels(page, "OpenAI · Forschung");
    const catalog = page.getByRole("region", {
      name: label("Sprachmodelle", "Language models"),
      exact: true,
    });

    await expect(
      catalog.getByRole("heading", { name: "Hauptmodell", exact: true }),
    ).toBeVisible();

    await capture("models");
    const modelActions = await chooseContextAction(
      page,
      catalog.locator('[data-slot="card"]').filter({
        has: page.getByRole("heading", { name: "Hauptmodell", exact: true }),
      }),
      label("Metadaten ergänzen", "Add metadata"),
    );
    await capture("model-metadata");
    await page.keyboard.press("Escape");

    await expect(
      page.locator('[data-slot="modal-dialog"]:not([data-exiting])'),
    ).toHaveCount(0);
    await expect(modelActions).toBeFocused();

    await catalog.getByRole("searchbox").fill("kein-treffer");

    await expect(
      catalog.getByText(
        label(
          "Keine passenden Modelle. Modellliste beim Provider aktualisieren.",
          "No matching models. Refresh the provider’s model list.",
        ),
      ),
    ).toBeVisible();

    await capture("models-empty-search");
    await catalog.getByRole("searchbox").fill("");
    for (const [name, title] of [
      ["device", "ChatGPT · Team"],
      ["browser", "OpenRouter"],
    ]) {
      await (
        await chooseProviderAccount(page, title)
      )
        .getByRole("button", {
          name: label("Anmelden", "Sign in"),
          exact: true,
        })
        .click();

      await expect(dialog().getByRole("link")).toBeVisible();

      await capture(`provider-login-${name}`);
      await close();
    }

    loginError = true;
    await (
      await chooseProviderAccount(page, "ChatGPT · Team")
    )
      .getByRole("button", { name: label("Anmelden", "Sign in"), exact: true })
      .click();

    await expect(dialog().getByRole("alert")).toBeVisible();

    await capture("provider-login-error");
    await close();

    for (const route of ["projects", "machines", "keys", "settings"]) {
      await go(route);
      await capture(route);
      if (route !== "settings") {
        const actions = page
          .getByRole("button", { name: /^(Aktionen für|Actions for) / })
          .first();
        await actions.press("Enter");

        await expect(page.getByRole("menu")).toBeVisible();

        await capture(`${route}-actions`);
        await page.keyboard.press("Escape");

        await expect(actions).toBeFocused();
      }
    }

    await go("projects");
    await page
      .getByRole("button", {
        name: label("Projekt anlegen", "Create project"),
        exact: true,
      })
      .click();
    await capture("project-create");
    await dialog().getByLabel("Name", { exact: true }).fill("Quicksort Demo");

    await expect(
      dialog().getByLabel(
        label(
          "Projektverzeichnis auf der Fernmaschine",
          "Project directory on the remote machine",
        ),
      ),
    ).toHaveCount(0);

    await dialog()
      .getByRole("button", { name: label("Weiter", "Continue"), exact: true })
      .click();
    await capture("project-machines-step");
    await dialog()
      .getByRole("button", { name: label("Fernmaschinen", "Remote machines") })
      .click();
    await page
      .getByRole("option", { name: "GPU-Worker Frankfurt", exact: true })
      .click();
    await page.keyboard.press("Escape");
    await capture("project-multi-select");
    await dialog()
      .getByRole("button", { name: label("Weiter", "Continue"), exact: true })
      .click();
    await capture("project-repository-step");
    await dialog()
      .getByRole("button", { name: label("Weiter", "Continue"), exact: true })
      .click();
    await capture("project-identity-step");
    await close();
    await go("machines");
    await page
      .getByRole("button", {
        name: label("Verbindung prüfen", "Test connection"),
      })
      .click();

    await expect(
      page.getByText(label("Host-Schlüssel prüfen", "Review host key"), {
        exact: true,
      }),
    ).toBeVisible();

    await capture("machine-host-key");
    await page
      .getByRole("button", {
        name: label(
          "Schlüssel vertrauen und erneut verbinden",
          "Trust key and reconnect",
        ),
      })
      .click();

    await expect(
      page.getByText(label("Verbindung erfolgreich", "Connection successful"), {
        exact: true,
      }),
    ).toBeVisible();

    await capture("machine-connection-success");
    connectionMode = "error";
    await page
      .getByRole("button", {
        name: label("Verbindung prüfen", "Test connection"),
      })
      .click();

    await expect(page.getByRole("main").getByRole("alert")).toBeVisible();

    await capture("machine-connection-error");
    connectionMode = "connected";
    await page
      .getByRole("button", {
        name: label("Maschine hinzufügen", "Add machine"),
        exact: true,
      })
      .click();
    await capture("machine-create-top");
    await dialog().getByLabel("Name", { exact: true }).fill("Worker 2");
    await dialog()
      .getByLabel("Host", { exact: true })
      .fill("worker-2.example.org");
    await dialog()
      .getByLabel(label("Benutzer", "User"), { exact: true })
      .fill("developer");
    await dialog()
      .getByRole("button", { name: label("Weiter", "Continue"), exact: true })
      .click();
    await capture("machine-access-step");
    await dialog()
      .getByRole("button", {
        name: label("SSH-Schlüssel", "SSH key"),
      })
      .and(dialog().locator('[data-slot="select-trigger"]'))
      .click();
    await page.getByRole("option").first().click();
    await dialog()
      .getByRole("button", { name: label("Weiter", "Continue"), exact: true })
      .click();
    await capture("machine-environment-step");
    await dialog()
      .getByRole("button", {
        name: label("Erweiterte Einstellungen", "Advanced settings"),
        exact: true,
      })
      .click();

    await expect(dialog().getByLabel(/code-server/)).toHaveCount(0);

    await dialog()
      .locator('[data-slot="modal-body"]')
      .evaluate((body) => {
        body.scrollTop = body.scrollHeight;
      });
    if (!baseline) {
      const footer = await dialog()
        .locator('[data-slot="modal-footer"]')
        .boundingBox();
      expect(footer).not.toBeNull();
      expect(footer!.y + footer!.height).toBeLessThanOrEqual(variant.height);
    }

    await capture("machine-create-bottom");
    await close();
    await page.getByRole("tab", { name: "Pools", exact: true }).click();
    await capture("pools");
    await page
      .getByRole("button", {
        name: label("YAML importieren / aktualisieren", "Import / update YAML"),
      })
      .click();

    await expect(dialog().locator(".monaco-editor")).toBeVisible();
    await expect
      .poll(() =>
        dialog()
          .locator(".monaco-editor .view-line")
          .first()
          .locator("span[class^=mtk]")
          .evaluateAll(
            (tokens) => new Set(tokens.map((token) => token.className)).size,
          ),
      )
      .toBeGreaterThan(1);

    await capture("pool-yaml");
    await close();
    await go("keys");
    await chooseContextAction(
      page,
      page.locator("section").filter({
        has: page.getByRole("heading", {
          name: label("Schlüssel 2", "Key 2"),
          exact: true,
        }),
      }),
      label("Entfernen", "Remove"),
    );
    await capture("destructive-confirmation");
    await close();
    await page
      .getByRole("tab", {
        name: label("Vertrauenswürdige Hosts", "Trusted hosts"),
      })
      .click();
    await capture("trusted-hosts");
    await page
      .getByRole("button", {
        name: label("Host-Schlüssel hinzufügen", "Add host key"),
        exact: true,
      })
      .first()
      .click();
    await capture("trusted-host-create");
    await close();

    await go("projects/forecast");
    await capture("agent-conversation");
    connectionMode = "untrusted";
    const prompt = page.getByRole("textbox", {
      name: label("Aufgabe oder neue Instruktion", "Task or new instruction"),
    });
    await prompt.fill("Erstelle ein Python-Quicksort-Beispiel.");
    await page
      .getByRole("button", {
        name: new RegExp("^" + label("Starten", "Start")),
      })
      .click();

    await expect(
      page.getByText(label("Host-Schlüssel prüfen", "Review host key"), {
        exact: true,
      }),
    ).toBeVisible();
    await expect(prompt).toHaveValue("");
    expect(prompts).toBe(1);

    await capture("agent-connection-required");
    await page
      .getByRole("button", {
        name: label(
          "Schlüssel vertrauen und erneut verbinden",
          "Trust key and reconnect",
        ),
      })
      .click();

    await expect(prompt).toHaveValue("");
    expect(prompts).toBe(1);

    await capture("agent-connection-ready");

    await setLogDiagnostics(page, true);
    await page
      .getByRole("button", {
        name: label("Schrittdetails", "Step details"),
      })
      .click();
    await capture("agent-wire");
    await close();
    await setLogDiagnostics(page, false);
    await page
      .getByRole("button", {
        name: new RegExp("^" + label("Agentendetails", "Agent details")),
      })
      .click();
    await capture("agent-details");
    await dialog()
      .getByRole("button", {
        name: new RegExp("^" + label("Ausgabe öffnen:", "Open output:")),
      })
      .first()
      .click();
    await capture("job-output");
    await close();
    await page
      .getByRole("button", {
        name: label("Projektaktionen", "Project actions"),
        exact: true,
      })
      .click();
    await page
      .getByRole("menuitem", { name: "Recherche ↗", exact: true })
      .click();
    await capture("subagent");
    await go("projects/forecast");
    emptyTranscript = true;
    await go("projects/forecast");
    await capture("agent-empty-conversation");
    await page
      .getByRole("button", {
        name: new RegExp("^" + label("Agentendetails", "Agent details")),
      })
      .click();
    await capture("agent-empty-details");
    await close();
    emptyTranscript = false;
    await go("projects/forecast");
    await page
      .getByRole("button", {
        name: label("Projektaktionen", "Project actions"),
        exact: true,
      })
      .click();
    await page
      .getByRole("menuitem", { name: "Auto-Approver ↗", exact: true })
      .click();

    await expect(
      page.getByRole("textbox", {
        name: label("Aufgabe oder neue Instruktion", "Task or new instruction"),
      }),
    ).toHaveCount(0);

    await capture("approver-readonly");
    await go("projects/forecast");
    await page
      .getByRole("button", {
        name: label("Projektaktionen", "Project actions"),
        exact: true,
      })
      .click();
    await page
      .getByRole("menuitem", {
        name: label("Quellen verwalten", "Manage sources"),
        exact: true,
      })
      .click();
    await capture("sources");
    await page
      .getByRole("button", {
        name: label("Quelle manuell hinzufügen", "Add source manually"),
      })
      .click();
    await capture("source-create");
    await close();
    await go("projects/forecast?view=editor");
    await capture("optional-editor");

    await expect(page.locator("iframe")).toHaveCount(0);

    editorMode = "untrusted";
    const openEditor = async () => {
      const opened = page.context().waitForEvent("page");
      await dialog()
        .getByRole("link", {
          name: label(
            "VS Code in neuem Tab öffnen ↗",
            "Open VS Code in a new tab ↗",
          ),
          exact: true,
        })
        .click();
      const editor = await opened;
      await editor.setViewportSize(variant);

      return editor;
    };
    let editorPage = await openEditor();

    await expect(
      editorPage.getByText(label("Host-Schlüssel prüfen", "Review host key"), {
        exact: true,
      }),
    ).toBeVisible();
    await expect(editorPage.getByRole("banner")).toHaveCount(0);

    await capture("editor-host-key", editorPage);
    await editorPage
      .getByRole("button", {
        name: label(
          "Schlüssel vertrauen und erneut verbinden",
          "Trust key and reconnect",
        ),
      })
      .click();

    await expect(
      editorPage.getByRole("heading", { name: "Editor-Fixture" }),
    ).toBeVisible();
    expect(await editorPage.evaluate(() => window.opener === null)).toBe(true);

    await capture("editor-connected-new-tab", editorPage);
    await editorPage.close();
    await go("projects/forecast?view=editor");
    await page
      .getByRole("button", {
        name: label("Editor beenden", "Stop editor"),
        exact: true,
      })
      .click();
    await capture("editor-stop-confirmation");
    await dialog()
      .getByRole("button", {
        name: label("Editor beenden", "Stop editor"),
        exact: true,
      })
      .click();

    await expect(
      page.getByRole("button", {
        name: label("Editor beenden", "Stop editor"),
        exact: true,
      }),
    ).toHaveCount(0);

    editorMode = "error";
    editorPage = await openEditor();

    await expect(editorPage.getByRole("alert")).toBeVisible();

    await capture("editor-error", editorPage);
    await editorPage.close();
    editorMode = "loading";
    editorPage = await openEditor();

    await expect(
      editorPage.getByRole("heading", {
        name: label("VS Code öffnen", "Open VS Code"),
        exact: true,
      }),
    ).toBeVisible();

    await capture("editor-starting", editorPage);
    await editorPage.close();
    editorMode = "connected";

    state = "empty";
    for (const route of [
      "projects",
      "machines",
      "keys",
      "providers",
      "settings",
      "projects/forecast?view=sources",
      "projects/forecast?view=editor",
      "projects/forecast",
    ]) {
      await go(route);
      await capture(`empty-${route.replaceAll(/[/?=]/g, "-")}`);
    }

    await go("machines");
    await page.getByRole("tab", { name: "Pools", exact: true }).click();
    await capture("empty-pools");
    await go("keys");
    await page
      .getByRole("tab", {
        name: label("Vertrauenswürdige Hosts", "Trusted hosts"),
      })
      .click();
    await capture("empty-trusted-hosts");
    state = "error";
    for (const route of [
      "projects",
      "machines",
      "keys",
      "providers",
      "settings",
      "projects/forecast",
    ]) {
      await go(route);
      await expect(
        page.getByRole("main").getByRole("alert").first(),
      ).toBeVisible();
      await capture(`error-${route.replaceAll("/", "-")}`);
    }

    state = "loading";
    for (const route of [
      "projects",
      "machines",
      "keys",
      "providers",
      "settings",
      "projects/forecast",
    ]) {
      await go(route);
      await capture(`loading-${route.replaceAll("/", "-")}`);
    }

    state = "populated";
    if (variant.width < 1024) {
      await go("projects");
      await page
        .getByRole("button", { name: label("Menü öffnen", "Open menu") })
        .click();
      await capture("navigation-drawer");
    }

    auth = "login";
    await go("projects");
    await capture("login");
    auth = "setup";
    await go("projects");
    await capture("setup");
    await page
      .getByLabel(label("Passwort", "Password"), { exact: true })
      .fill("visual setup password");
    await page
      .getByLabel(label("Passwort wiederholen", "Confirm password"), {
        exact: true,
      })
      .fill("visual setup password");
    await page
      .getByRole("button", { name: label("Weiter", "Continue"), exact: true })
      .click();
    await capture("setup-identity-step");
    await writeFile(
      path.join(directory, `${variant.name}.json`),
      JSON.stringify({ captures, measurements, contrasts, errors }, null, 2),
    );

    expect(errors).toEqual([]);

    if (!baseline)
      expect(
        contrasts.filter((item) => item.ratio < item.minimum),
        "theme contrast",
      ).toEqual([]);

    if (!baseline)
      expect(
        captures.filter((item) => item.overflow > 1),
        "pages must fit the viewport",
      ).toEqual([]);
  });
}
