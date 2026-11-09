import { expect, test } from "@playwright/test";
import { mkdir } from "node:fs/promises";

import {
  agent,
  agents,
  config,
  project,
  transcript,
} from "./fixtures/visual-data";

for (const mobile of [false, true]) {
  test(`chat navigation preserves drafts and creates conversations without a naming form: ${mobile ? "mobile" : "desktop"}`, async ({
    page,
  }) => {
    page.setDefaultTimeout(10000);
    await page.setViewportSize({ width: mobile ? 390 : 1440, height: 900 });
    await page.addInitScript(() => {
      localStorage.setItem("zentrale-locale", "de");
      localStorage.setItem("zentrale-theme", "light");
    });
    const sessions = agents.map((item) => ({ ...item }));
    const created = {
      ...agent,
      id: "conversation-new",
      name: "Neue Unterhaltung",
      machineId: "",
      status: "idle",
    };
    let creations = 0;
    let renames = 0;
    await page.routeWebSocket("**/events?*", () => {});
    await page.route("**/graphql", async (route) => {
      const { query, variables = {} } = route.request().postDataJSON();
      const send = (data: unknown) => route.fulfill({ json: { data } });
      if (query.includes("createAgent(")) {
        creations++;

        expect(variables.projectId).toBe(project.id);
        expect(variables.name).toBe("Neue Unterhaltung");

        sessions.push(created);

        return send({ createAgent: created });
      }

      if (query.includes("renameAgent(")) {
        renames++;
        const session = sessions.find((item) => item.id === variables.agentId)!;
        session.name = variables.name.trim();

        return send({ renameAgent: true });
      }

      if (query.includes("status{")) {
        return send({
          status: {
            needsSetup: false,
            authenticated: true,
            githubConnected: false,
          },
        });
      }

      if (query.includes("recentMessages")) {
        const current =
          sessions.find((item) => item.id === variables.id) ?? agent;
        return send({
          ...transcript,
          agent: current,
          messages: current.id === created.id ? [] : transcript.messages,
          questions: [],
          jobs: [],
        });
      }

      if (query.includes("agents(projectId:")) {
        return send({ project, agents: sessions });
      }

      return send(config);
    });
    await page.goto(`/#/projects/${project.id}?agent=${agent.id}`);
    const input = page.getByRole("textbox", {
      name: "Aufgabe oder neue Instruktion",
    });
    await input.fill("Entwurf der ersten Unterhaltung");
    if (!mobile) {
      await page
        .getByRole("button", { name: "Seitenleiste schließen", exact: true })
        .click();
      const open = page.getByRole("button", {
        name: "Seitenleiste öffnen",
        exact: true,
      });

      await expect(open).toBeFocused();
      await expect(
        page.getByRole("navigation", { name: "Hauptnavigation", exact: true }),
      ).toHaveCount(0);

      await page.reload();

      await expect(open).toBeVisible();
      await expect(input).toHaveValue("Entwurf der ersten Unterhaltung");

      await open.click();

      await expect(
        page.getByRole("button", {
          name: "Seitenleiste schließen",
          exact: true,
        }),
      ).toBeFocused();
    }

    const openNavigation = async () => {
      if (mobile)
        await page
          .getByRole("button", { name: "Menü öffnen", exact: true })
          .click();
    };
    await openNavigation();
    if (mobile) {
      await page
        .getByRole("button", { name: "Menü schließen", exact: true })
        .click();

      await expect(page.locator('[data-slot="drawer-dialog"]')).toHaveCount(0);
      await expect(
        page.getByRole("button", { name: "Menü öffnen", exact: true }),
      ).toBeFocused();

      await openNavigation();
    }

    await page
      .getByRole("navigation", { name: "Hauptnavigation", exact: true })
      .getByRole("button", { name: "Neue Unterhaltung", exact: true })
      .click();

    await expect(page).toHaveURL(/agent=conversation-new/);
    await expect(input).toHaveValue("");
    await expect(input).toBeFocused();
    expect(creations).toBe(1);
    await expect(
      page.getByRole("heading", { name: "Woran möchtest du arbeiten?" }),
    ).toBeVisible();

    await input.fill("Zweiter Entwurf");
    await page
      .getByRole("button", { name: "Projektaktionen", exact: true })
      .click();
    await page
      .getByRole("menuitem", { name: "Umbenennen", exact: true })
      .click();
    const rename = page.getByRole("dialog");
    await rename
      .getByLabel("Name", { exact: true })
      .fill("Mein nächster Schritt");
    await rename
      .getByRole("button", { name: "Speichern", exact: true })
      .click();

    await expect(rename).toHaveCount(0);
    expect(renames).toBe(1);
    await expect(
      page.getByRole("button", {
        name: "Agent wechseln: Mein nächster Schritt",
      }),
    ).toBeVisible();
    await expect(input).toHaveValue("Zweiter Entwurf");

    await openNavigation();
    await page
      .getByRole("button", { name: "Einstellungen öffnen", exact: true })
      .click();
    await page
      .getByRole("menuitem", { name: "Darstellung und Sprache", exact: true })
      .click();
    const appearance = page.getByRole("dialog", {
      name: "Darstellung und Sprache",
    });

    await expect(appearance).toBeVisible();

    await page.keyboard.press("Escape");

    await expect(appearance).toHaveCount(0);
    await expect(
      page.getByRole("button", {
        name: mobile ? "Menü öffnen" : "Einstellungen öffnen",
        exact: true,
      }),
    ).toBeFocused();
    await expect(input).toHaveValue("Zweiter Entwurf");

    await openNavigation();
    await page
      .getByRole("navigation", {
        name: "Projekte und Unterhaltungen",
        exact: true,
      })
      .getByRole("link", { name: agent.name, exact: true })
      .click();

    await expect(input).toHaveValue("Entwurf der ersten Unterhaltung");

    await page.goBack();

    await expect(input).toHaveValue("Zweiter Entwurf");
    await expect(page.locator('[data-slot="drawer-dialog"]')).toHaveCount(0);

    await mkdir("artifacts/ui", { recursive: true });
    await page.screenshot({
      path: `artifacts/ui/chat-shell-${mobile ? "mobile" : "desktop"}.png`,
      animations: "disabled",
    });

    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth > innerWidth,
      ),
    ).toBe(false);
  });
}

test("new conversation asks only for a project and preserves the choice after an error", async ({
  page,
}) => {
  page.setDefaultTimeout(10000);
  await page.setViewportSize({ width: 1280, height: 900 });
  await page.addInitScript(() => {
    localStorage.setItem("zentrale-locale", "de");
    localStorage.setItem("zentrale-theme", "light");
  });
  const secondProject = { ...project, id: "second", name: "Zweites Projekt" };
  const created = {
    ...agent,
    id: "created",
    projectId: secondProject.id,
    name: "Neue Unterhaltung",
  };
  let attempts = 0;
  await page.routeWebSocket("**/events?*", () => {});
  await page.route("**/graphql", async (route) => {
    const { query, variables = {} } = route.request().postDataJSON();
    const send = (data: unknown) => route.fulfill({ json: { data } });
    if (query.includes("createAgent(")) {
      attempts++;

      expect(variables.projectId).toBe(secondProject.id);

      if (attempts === 1) {
        return route.fulfill({
          json: {
            errors: [
              {
                message:
                  "Die Unterhaltung konnte nicht gespeichert werden. Erneut versuchen.",
              },
            ],
          },
        });
      }

      return send({ createAgent: created });
    }

    if (query.includes("status{")) {
      return send({ status: { needsSetup: false, authenticated: true } });
    }

    if (query.includes("recentMessages")) {
      return send({
        ...transcript,
        agent: created,
        messages: [],
        recentMessages: [],
        questions: [],
        jobs: [],
      });
    }

    if (query.includes("agents(projectId:")) {
      return send({ project: secondProject, agents: [created] });
    }

    return send({ ...config, projects: [project, secondProject] });
  });
  await page.goto("/#/projects");
  await page
    .getByRole("navigation", {
      name: "Projekte und Unterhaltungen",
      exact: true,
    })
    .getByRole("link", { name: "Projekt anlegen", exact: true })
    .click();

  await expect(
    page.getByRole("heading", { name: "Neues Projekt", exact: true }),
  ).toBeVisible();

  await page.goBack();

  await expect(page.getByRole("dialog")).toHaveCount(0);

  await page
    .getByRole("button", { name: "Neue Unterhaltung", exact: true })
    .click();
  const dialog = page.getByRole("dialog");

  await expect(dialog.getByRole("textbox")).toHaveCount(0);

  await dialog.getByRole("button", { name: /Projekt/ }).click();
  await page
    .getByRole("option", { name: secondProject.name, exact: true })
    .click();
  const submit = dialog.getByRole("button", {
    name: "Unterhaltung starten",
    exact: true,
  });
  await submit.click();

  await expect(dialog.getByRole("alert")).toContainText(
    "Die Unterhaltung konnte nicht gespeichert werden.",
  );
  await expect(dialog.getByRole("button", { name: /Projekt/ })).toContainText(
    secondProject.name,
  );

  await mkdir("artifacts/ui", { recursive: true });
  await page.screenshot({ path: "artifacts/ui/chat-project-choice-error.png" });
  await submit.click();

  await expect(page).toHaveURL(/projects\/second\?agent=created/);
  await expect(dialog).toHaveCount(0);
  await expect(
    page.getByRole("textbox", { name: "Aufgabe oder neue Instruktion" }),
  ).toBeFocused();
  expect(attempts).toBe(2);
});
