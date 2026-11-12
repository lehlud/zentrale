import { expect, test } from "@playwright/test";
import { installPushFixture } from "./fixtures/push-fixture";
import { mkdir, writeFile } from "node:fs/promises";

import { config, gitConnections } from "./fixtures/visual-data";

for (const english of [false, true]) {
  test(`guided creation preserves inputs and routes errors: ${english ? "mobile-en-dark" : "desktop-de-light"}`, async ({
    page,
  }) => {
    const label = (de: string, en: string) => (english ? en : de);
    const variant = english ? "mobile-en-dark" : "desktop-de-light";
    await page.setViewportSize({ width: english ? 390 : 1440, height: 950 });
    await page.addInitScript(
      ({ english }) => {
        localStorage.setItem("zentrale-locale", english ? "en" : "de");
        localStorage.setItem("zentrale-theme", english ? "dark" : "light");
      },
      { english },
    );
    const data = structuredClone(config);
    let setup = false;
    let projectsSaved = 0;
    let machinesSaved = 0;
    let keysGenerated = 0;
    let setupAttempts = 0;
    const projectIDs: string[] = [];
    const resolve = async (
      query: string,
      variables: Record<string, any> = {},
    ) => {
      const send = (value: unknown) => value;
      const fail = (message: string): never => {
        throw new Error(message);
      };
      if (query.includes("status{")) {
        return send({ status: { needsSetup: setup, authenticated: !setup } });
      }

      if (query.includes("saveProject")) {
        projectsSaved++;
        projectIDs.push(variables.input.id);
        if (projectsSaved === 1) {
          return fail("select a remote machine or pool");
        }

        expect(variables.input.directory).toBeUndefined();
        expect(variables.input.machineIds).toEqual([config.machines[0].id]);
        expect(variables.input.commitName).toBe("Project Author");
        expect(variables.input.commitEmail).toBe("project@example.test");

        data.projects.push(variables.input);

        return send({ saveProject: { id: variables.input.id } });
      }

      if (query.includes("generateSSHKey")) {
        keysGenerated++;
        const key = {
          ...config.sshKeys[0],
          id: 7,
          canDelete: true,
          purposeId: "",
          usages: [],
        };
        data.sshKeys.push(key);

        return send({ generateSSHKey: key });
      }

      if (query.includes("saveMachine")) {
        machinesSaved++;
        if (machinesSaved === 1) {
          return fail("microvm_command must be an absolute path when supplied");
        }

        expect(variables.input.keyId).toBe(7);
        expect(variables.input.host).toBe("worker.example.org");
        expect(variables.input.workspaceRoot).toBe("/srv/persistent-projects");
        expect(variables.input.microVMCommand).toBe("/opt/runner");
        expect(variables.input.properties).toEqual(["GPU", "Training"]);

        data.machines.push({ ...variables.input, id: "new-worker" });

        return send({ saveMachine: { id: "new-worker" } });
      }

      if (query.includes("setup(password:")) {
        setupAttempts++;

        expect(variables.password).toBe("flow setup password");

        if (setupAttempts === 1) {
          return fail(
            "commit name must contain 1–200 characters without line breaks or angle brackets",
          );
        }

        expect(variables.identity).toEqual({
          name: "Flow Team",
          email: "flow@example.test",
        });

        setup = false;

        return send({ setup: true });
      }

      if (query.includes("gitConnections")) {
        return send({ gitConnections });
      }

      return send(data);
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
    const dialog = page.getByRole("dialog");
    const next = () =>
      dialog
        .getByRole("button", { name: label("Weiter", "Continue"), exact: true })
        .click();
    const back = () =>
      dialog
        .getByRole("button", { name: label("Zurück", "Back"), exact: true })
        .click();
    const captures: { name: string; file: string; overflow: number }[] = [];

    async function capture(name: string) {
      await mkdir("artifacts/ui", { recursive: true });
      const file = `flow-${variant}-${name}.png`;
      const overflow = await page.evaluate(
        () => document.documentElement.scrollWidth - innerWidth,
      );

      expect(overflow).toBeLessThanOrEqual(1);

      await page.screenshot({
        path: `artifacts/ui/${file}`,
        animations: "disabled",
      });
      captures.push({ name, file, overflow });
    }

    await page.goto("/#/projects");
    await page
      .getByRole("button", {
        name: label("Projekt anlegen", "Create project"),
        exact: true,
      })
      .click();

    await expect(dialog.getByRole("progressbar")).toHaveAttribute(
      "aria-valuetext",
      label("Schritt 1 von 4", "Step 1 of 4"),
    );

    await next();

    await expect(dialog.getByLabel("Name", { exact: true })).toBeFocused();

    await dialog.getByLabel("Name", { exact: true }).fill("Flow Project");
    const directoryLabel = label(
      "Projektverzeichnis auf der Fernmaschine",
      "Project directory on the remote machine",
    );

    await expect(dialog.getByLabel(directoryLabel)).toHaveCount(0);

    await capture("project-name");
    await next();

    await expect(
      dialog.getByRole("heading", {
        name: label(
          "Wo sollen die Agenten arbeiten?",
          "Where should agents work?",
        ),
        exact: true,
      }),
    ).toBeFocused();

    await back();

    await expect(dialog.getByLabel("Name", { exact: true })).toHaveValue(
      "Flow Project",
    );
    await expect(dialog.getByLabel(directoryLabel)).toHaveCount(0);

    await next();
    await next();
    const machineChoice = dialog
      .getByRole("button", {
        name: label("Fernmaschinen", "Remote machines"),
      })
      .and(dialog.locator('[data-slot="select-trigger"]'));

    await expect(machineChoice).toBeFocused();

    await machineChoice.click();
    await page
      .getByRole("option", { name: config.machines[0].name, exact: true })
      .click();
    await page.keyboard.press("Escape");
    await capture("project-machines");
    await next();
    await capture("project-repository");
    await next();
    await capture("project-identity");

    await expect(
      dialog.getByLabel(
        label("Commit-Name (optional)", "Commit name (optional)"),
      ),
    ).not.toBeVisible();
    expect(projectsSaved).toBe(0);

    await dialog
      .getByRole("button", {
        name: label("Projekt anlegen", "Create project"),
        exact: true,
      })
      .click();

    await expect(machineChoice).toBeFocused();

    await capture("project-error-return");

    await expect(machineChoice).toContainText(config.machines[0].name);

    await next();
    await next();
    await dialog
      .getByRole("button", {
        name: label("Commit-Identität anpassen", "Customize commit identity"),
        exact: true,
      })
      .click();
    await dialog
      .getByLabel(label("Commit-Name (optional)", "Commit name (optional)"))
      .fill("Project Author");
    await dialog
      .getByLabel(label("Commit-E-Mail (optional)", "Commit email (optional)"))
      .fill("project@example.test");
    await dialog
      .getByRole("button", {
        name: label("Projekt anlegen", "Create project"),
        exact: true,
      })
      .click();

    await expect(dialog).toHaveCount(0);
    expect(projectIDs).toHaveLength(2);
    expect(projectIDs[0]).toBe(projectIDs[1]);

    data.sshKeys = [];
    await page.goto("/#/machines");
    await page
      .getByRole("button", {
        name: label("Maschine hinzufügen", "Add machine"),
        exact: true,
      })
      .click();
    await dialog.getByLabel("Name", { exact: true }).fill("Flow Worker");
    await dialog.getByLabel("Host", { exact: true }).fill("worker.example.org");
    await dialog
      .getByLabel(label("Benutzer", "User"), { exact: true })
      .fill("developer");
    await next();
    await next();

    await expect(
      dialog
        .getByRole("button", {
          name: label("SSH-Schlüssel", "SSH key"),
        })
        .and(dialog.locator('[data-slot="select-trigger"]')),
    ).toBeFocused();
    expect(machinesSaved).toBe(0);

    await dialog
      .getByRole("button", {
        name: label("Neuen SSH-Schlüssel erzeugen", "Generate a new SSH key"),
        exact: true,
      })
      .click();

    await expect(
      dialog
        .getByRole("button", {
          name: label("SSH-Schlüssel", "SSH key"),
        })
        .and(dialog.locator('[data-slot="select-trigger"]')),
    ).toContainText(label("Schlüssel 7", "Key 7"));
    await expect(
      dialog.getByRole("button", {
        name: label("Öffentlichen Schlüssel kopieren", "Copy public key"),
        exact: true,
      }),
    ).toBeVisible();
    expect(keysGenerated).toBe(1);

    await capture("machine-access");
    await next();
    await capture("machine-environment");
    const workspaceRoot = dialog.getByLabel(
      label("Persistentes Projektverzeichnis", "Persistent project directory"),
      { exact: true },
    );
    await workspaceRoot.fill("");
    await dialog
      .getByRole("button", { name: label("Speichern", "Save"), exact: true })
      .click();

    await expect(workspaceRoot).toBeFocused();
    expect(machinesSaved).toBe(0);

    await workspaceRoot.fill("/srv/persistent-projects");
    await dialog
      .getByRole("button", {
        name: label("Erweiterte Einstellungen", "Advanced settings"),
        exact: true,
      })
      .click();
    const runner = dialog.getByLabel(
      label(
        "Eigener MicroVM-Runner (optional)",
        "Custom MicroVM runner (optional)",
      ),
    );
    await runner.fill("invalid-runner");
    await dialog
      .getByLabel(
        label("Eigenschaften (kommagetrennt)", "Properties (comma separated)"),
      )
      .fill("GPU, Training");
    await back();
    await next();
    await dialog
      .getByRole("button", { name: label("Speichern", "Save"), exact: true })
      .click();

    await expect(runner).toBeVisible();
    await expect(runner).toBeFocused();
    await expect(runner).toHaveValue("invalid-runner");

    await capture("machine-advanced-error");
    await runner.fill("/opt/runner");
    await dialog
      .getByRole("button", { name: label("Speichern", "Save"), exact: true })
      .click();

    await expect(dialog).toHaveCount(0);

    setup = true;
    await page.goto("/?flow-setup=1#/projects");
    const password = page.getByLabel(label("Passwort", "Password"), {
      exact: true,
    });
    const confirmation = page.getByLabel(
      label("Passwort wiederholen", "Confirm password"),
      { exact: true },
    );
    const continueSetup = () =>
      page
        .getByRole("button", { name: label("Weiter", "Continue"), exact: true })
        .click();
    await password.fill("flow setup password");
    await confirmation.fill("different password");
    await continueSetup();

    await expect(confirmation).toBeFocused();
    await expect(confirmation).toHaveAttribute("aria-invalid", "true");

    await confirmation.fill("flow setup password");
    await capture("setup-password");
    await continueSetup();
    const commitName = page.getByLabel(
      label("Name für Commits", "Commit name"),
      { exact: true },
    );
    const commitEmail = page.getByLabel(
      label("E-Mail für Commits", "Commit email"),
      { exact: true },
    );
    await commitName.fill("Invalid <Name>");
    await commitEmail.fill("flow@example.test");
    await page
      .getByRole("button", { name: label("Zurück", "Back"), exact: true })
      .click();

    await expect(password).toHaveValue("flow setup password");
    await expect(confirmation).toHaveValue("flow setup password");

    await continueSetup();

    await expect(commitName).toHaveValue("Invalid <Name>");
    expect(setupAttempts).toBe(0);

    await page
      .getByRole("button", {
        name: label("Einrichtung abschließen", "Complete setup"),
        exact: true,
      })
      .click();

    await expect(commitName).toBeFocused();
    await expect(commitEmail).toHaveValue("flow@example.test");

    await capture("setup-identity-error");
    await commitName.fill("Flow Team");
    await page
      .getByRole("button", {
        name: label("Einrichtung abschließen", "Complete setup"),
        exact: true,
      })
      .click();

    await expect(
      page.getByRole("heading", {
        name: label("Projekte", "Projects"),
        exact: true,
      }),
    ).toBeVisible();
    expect(setupAttempts).toBe(2);

    await writeFile(
      `artifacts/ui/form-flows-${variant}.json`,
      JSON.stringify({ captures }, null, 2),
    );
  });
}
