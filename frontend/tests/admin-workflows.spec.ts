import { expect, test } from "@playwright/test";
import { bootstrapData, isTranscriptQuery } from "./fixtures/bootstrap-data";
import { installPushFixture } from "./fixtures/push-fixture";
import { mkdir } from "node:fs/promises";

import {
  chooseProviderAccount,
  openProviderModels,
} from "./fixtures/context-actions";
import {
  agents,
  config,
  models,
  project,
  transcript,
} from "./fixtures/visual-data";

for (const variant of [
  {
    name: "desktop-de-light",
    width: 1440,
    height: 1000,
    locale: "de",
    theme: "light",
  },
  {
    name: "mobile-en-dark",
    width: 390,
    height: 844,
    locale: "en",
    theme: "dark",
  },
]) {
  test(`administration task flows: ${variant.name}`, async ({
    page,
    context,
  }) => {
    const label = (de: string, en: string) =>
      variant.locale === "de" ? de : en;
    await page.setViewportSize(variant);
    await page.addInitScript(({ locale, theme }) => {
      localStorage.setItem("zentrale-locale", locale);
      localStorage.setItem("zentrale-theme", theme);
    }, variant);
    const state = structuredClone(config);
    let rejectDefaults = true;
    let signInFailed = true;
    const events: string[] = [];
    const resolve = async (
      query: string,
      variables: Record<string, any> = {},
    ) => {
      const send = (data: unknown) => bootstrapData(query, data);
      if (query.includes("status{")) {
        return send({
          status: {
            needsSetup: false,
            authenticated: true,
            githubConnected: false,
          },
        });
      }

      if (isTranscriptQuery(query)) {
        return send({ ...transcript, questions: [], jobs: [] });
      }

      if (query.includes("agents(projectId:")) {
        return send({ project, agents });
      }

      if (query.includes("setModelDefaults")) {
        if (rejectDefaults) {
          throw new Error(
            label(
              "Speichern fehlgeschlagen. Bitte erneut versuchen.",
              "Saving failed. Please try again.",
            ),
          );
        }

        state.modelDefaults = variables.input;

        return send({ setModelDefaults: true });
      }

      if (query.includes("beginProviderLogin")) {
        events.push("begin");
        return send({
          beginProviderLogin: {
            id: "admin-login",
            providerId: variables.providerId,
            method:
              variables.providerId === "openrouter" ? "browser" : "device",
            status: signInFailed ? "error" : "pending",
            verificationUrl: "https://example.org/verify",
            userCode: variables.providerId === "openrouter" ? "" : "LOGIN-42",
            interval: 60,
            expiresAt: "2026-11-05T23:00:00Z",
            error: signInFailed
              ? label("Die Anmeldung ist abgelaufen.", "Sign-in expired.")
              : "",
          },
        });
      }

      if (query.includes("cancelProviderLogin")) {
        events.push("cancel");
        return send({ cancelProviderLogin: true });
      }

      if (query.includes("pollProviderLogin")) {
        return send({
          pollProviderLogin: {
            id: variables.id,
            providerId: "openrouter",
            method: "browser",
            status: "pending",
            verificationUrl: "https://example.org/verify",
            userCode: "",
            interval: 60,
            error: label("Dieser Code ist ungültig.", "This code is invalid."),
          },
        });
      }

      if (query.includes("models(providerId:")) {
        return send({
          models: models.filter(
            (model) =>
              !variables.search ||
              `${model.name} ${model.id}`
                .toLowerCase()
                .includes(variables.search.toLowerCase()),
          ),
        });
      }

      if (query.includes("model(providerId:")) {
        return send({
          model: models.find((model) => model.id === variables.modelId) ?? null,
        });
      }

      if (query.includes("trustHostKey")) {
        throw new Error(label("Ungültiger SSH-Schlüssel.", "Invalid SSH key."));
      }

      return send(state);
    };
    const push = await installPushFixture(page, resolve);
    await page.route("**/graphql", async (route) => {
      const { query, variables = {} } = route.request().postDataJSON();
      try {
        const data = await resolve(query, variables);
        if (query.trimStart().startsWith("mutation")) await push.push();
        await route.fulfill({ json: { data } });
      } catch (error) {
        await route.fulfill({
          json: { errors: [{ message: (error as Error).message }] },
        });
      }
    });
    const shot = async (name: string) => {
      await mkdir("artifacts/ui", { recursive: true });
      await page.screenshot({
        path: `artifacts/ui/admin-${variant.name}-${name}.png`,
        animations: "disabled",
      });
    };
    await page.goto("/#/projects");
    await page
      .getByRole("searchbox", {
        name: label("Projekte suchen", "Search projects"),
      })
      .fill("does-not-exist");

    await expect(
      page.getByText(label("Keine passenden Projekte", "No matching projects")),
    ).toBeVisible();

    await page.getByRole("searchbox").fill(project.name);

    await expect(
      page
        .getByRole("main")
        .getByRole("link", { name: project.name, exact: true }),
    ).toHaveAttribute("href", `#/projects/${project.id}`);

    await shot("projects");
    await page
      .getByRole("main")
      .getByRole("link", { name: project.name, exact: true })
      .click();

    await expect(
      page.getByRole("main").getByRole("heading", { level: 1 }),
    ).toHaveCount(1);

    const controls = page.getByRole("group", {
      name: label("Agentensteuerung", "Agent controls"),
      exact: true,
    });

    await expect(
      controls.getByRole("heading", { name: project.name, exact: true }),
    ).toBeVisible();
    await expect(
      controls.getByRole("link", {
        name: label(
          "VS Code in neuem Tab öffnen ↗",
          "Open VS Code in a new tab ↗",
        ),
      }),
    ).toHaveAttribute("target", "_blank");

    await shot("project-header");
    await page.goto("/#/keys");

    await expect(
      page.getByRole("textbox", {
        name: label("Öffentlicher Schlüssel", "Public key"),
        exact: true,
      }),
    ).toHaveCount(0);
    await expect(
      page.getByText(
        label(
          "Verwendet von GPU-Worker Frankfurt",
          "Used by GPU-Worker Frankfurt",
        ),
      ),
    ).toBeVisible();

    await context.grantPermissions(["clipboard-read", "clipboard-write"]);
    await page
      .getByRole("button", {
        name: label("Öffentlichen Schlüssel kopieren", "Copy public key"),
        exact: true,
      })
      .first()
      .click();

    expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(
      state.sshKeys[0].publicKey,
    );

    await shot("keys");
    await page
      .getByRole("tab", {
        name: label("Vertrauenswürdige Hosts", "Trusted hosts"),
        exact: true,
      })
      .click();

    await expect(page).toHaveURL(/type=hosts/);

    await page
      .getByRole("button", {
        name: label("Host-Schlüssel hinzufügen", "Add host key"),
        exact: true,
      })
      .click();
    let dialog = page.getByRole("dialog");
    const hostInput = dialog.getByRole("textbox");
    await hostInput.fill("ssh-invalid keep-my-value");
    await dialog
      .getByRole("button", { name: label("Vertrauen", "Trust"), exact: true })
      .click();

    await expect(dialog.getByRole("alert")).toContainText(
      label("Ungültiger SSH-Schlüssel.", "Invalid SSH key."),
    );
    await expect(hostInput).toHaveValue("ssh-invalid keep-my-value");

    await shot("host-form-error");
    await page.keyboard.press("Escape");

    await expect(
      page.getByRole("button", {
        name: label("Host-Schlüssel hinzufügen", "Add host key"),
        exact: true,
      }),
    ).toBeFocused();

    await page.goto("/#/providers");
    const catalog = page.getByRole("region", {
      name: label("Sprachmodelle", "Language models"),
      exact: true,
    });

    await expect(
      catalog.getByRole("heading", { name: models[0].name, exact: true }),
    ).toBeVisible();
    await expect(page.getByRole("dialog")).toHaveCount(0);

    await shot("providers");
    await openProviderModels(page, "OpenAI · Forschung");
    const catalogHeading = page.getByRole("heading", {
      name:
        variant.width < 1280
          ? label("Sprachmodelle", "Language models")
          : `${label("Sprachmodelle", "Language models")} · OpenAI · Forschung`,
      exact: true,
    });
    if (variant.width >= 1280) await expect(catalogHeading).toBeFocused();

    await expect(catalogHeading).toBeInViewport();

    await shot("provider-models");
    await page
      .getByRole("button", {
        name: label("Provider hinzufügen", "Add provider"),
        exact: true,
      })
      .click();
    dialog = page.getByRole("dialog");

    await expect(
      dialog.getByRole("radio", {
        name: label("ChatGPT-Konto", "ChatGPT account"),
        exact: true,
      }),
    ).toBeVisible();

    await dialog
      .getByRole("radio", {
        name: label("ChatGPT-Konto", "ChatGPT account"),
        exact: true,
      })
      .press("Space");

    await expect(dialog.locator('input[type="password"]')).toHaveCount(0);

    await shot("provider-create");
    await page.keyboard.press("Escape");
    const deviceAccount = await chooseProviderAccount(page, "ChatGPT · Team");
    await deviceAccount
      .getByRole("button", { name: label("Anmelden", "Sign in"), exact: true })
      .click();
    dialog = page.getByRole("dialog");

    await expect(dialog.getByRole("alert")).toContainText(
      label("Die Anmeldung ist abgelaufen.", "Sign-in expired."),
    );

    signInFailed = false;
    await dialog
      .getByRole("button", {
        name: label("Anmeldung erneut starten", "Restart sign-in"),
        exact: true,
      })
      .click();

    await expect(dialog.getByText("LOGIN-42", { exact: true })).toBeVisible();

    await dialog
      .getByRole("button", {
        name: label("Code kopieren", "Copy code"),
        exact: true,
      })
      .click();

    expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(
      "LOGIN-42",
    );
    await expect(
      dialog.getByRole("link", {
        name: label("Anmeldeseite öffnen ↗", "Open sign-in page ↗"),
        exact: true,
      }),
    ).toHaveAttribute("target", "_blank");

    await shot("device-login");
    await page.keyboard.press("Escape");

    await expect.poll(() => events.includes("cancel")).toBe(true);

    const browserAccount = await chooseProviderAccount(page, "OpenRouter");
    await browserAccount
      .getByRole("button", { name: label("Anmelden", "Sign in"), exact: true })
      .click();
    dialog = page.getByRole("dialog");
    const code = dialog.getByRole("textbox", {
      name: label("Anmeldecode", "Authorization code"),
    });
    await code.fill("keep-invalid-code");
    await dialog
      .getByRole("button", { name: label("Verbinden", "Connect"), exact: true })
      .click();

    await expect(code).toHaveValue("keep-invalid-code");
    await expect(code).toBeFocused();
    await expect(
      dialog.getByText(
        label("Dieser Code ist ungültig.", "This code is invalid."),
      ),
    ).toBeVisible();

    await shot("browser-login-error");
    await page.keyboard.press("Escape");
    await page.goto("/#/settings");

    await expect(
      page.getByRole("heading", {
        name: label("Standardmodelle", "Default models"),
        exact: true,
      }),
    ).toBeVisible();

    const role = page.getByRole("group", {
      name: label("Hauptmodell", "Main model"),
      exact: true,
    });
    await role
      .getByRole("button", { name: label("Ändern", "Change"), exact: true })
      .click();
    dialog = page.getByRole("dialog");

    await expect(dialog).toHaveCount(1);

    await dialog.getByRole("searchbox").fill(models[1].id);

    await expect(dialog.getByRole("option")).toHaveCount(1);

    await shot("model-picker");
    await dialog.getByRole("option").click();

    await dialog
      .getByRole("button", {
        name: label("Auswahl übernehmen", "Apply selection"),
      })
      .click();

    await expect(dialog).toHaveCount(0);
    await expect(role.getByText(models[1].name, { exact: true })).toBeVisible();
    await expect(
      page.getByRole("status").filter({
        hasText: label("Nicht gespeicherte Änderungen", "Unsaved changes"),
      }),
    ).toBeVisible();

    await page
      .getByRole("button", {
        name: label("Modell-Defaults speichern", "Save model defaults"),
        exact: true,
      })
      .click();

    await expect(page.getByRole("main").getByRole("alert")).toContainText(
      label("Speichern fehlgeschlagen.", "Saving failed."),
    );
    await expect(role.getByText(models[1].name, { exact: true })).toBeVisible();

    rejectDefaults = false;
    await page
      .getByRole("button", { name: label("Verwerfen", "Discard"), exact: true })
      .click();

    await expect(page.getByRole("main").getByRole("alert")).toHaveCount(0);
    await expect(
      role
        .getByText(models[0].name, { exact: true })
        .and(role.getByRole("paragraph")),
    ).toBeVisible();

    await role
      .getByRole("button", { name: label("Ändern", "Change"), exact: true })
      .click();
    dialog = page.getByRole("dialog");
    await dialog.getByRole("searchbox").fill(models[1].id);
    await dialog.getByRole("option").click();
    await dialog
      .getByRole("button", {
        name: label("Auswahl übernehmen", "Apply selection"),
      })
      .click();
    await page
      .getByRole("button", {
        name: label("Modell-Defaults speichern", "Save model defaults"),
        exact: true,
      })
      .click();

    await expect(
      page.getByRole("button", {
        name: label("Modell-Defaults speichern", "Save model defaults"),
        exact: true,
      }),
    ).toHaveCount(0);

    await shot("defaults");
    if (variant.width > 1000)
      await page.setViewportSize({ width: 720, height: 500 });

    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth - innerWidth,
      ),
    ).toBeLessThanOrEqual(1);
  });
}
