import { expect, test, type Page } from "@playwright/test";

import {
  composerCommand,
  composerCommands,
  commandSuggestions,
} from "../src/composer-commands";
import { bootstrapData, isTranscriptQuery } from "./fixtures/bootstrap-data";
import { installPushFixture } from "./fixtures/push-fixture";
import { agent, config, project, transcript } from "./fixtures/visual-data";
import { openComposerCommand } from "./fixtures/composer";

test("the command registry drives matching and dispatch without changing requests", async () => {
  expect(new Set(composerCommands.map((command) => command.name)).size).toBe(
    composerCommands.length,
  );
  expect(commandSuggestions("/").map((command) => command.name)).toEqual([
    "side",
    "teamwork",
    "continue",
    "stop",
  ]);
  expect(commandSuggestions("/co").map((command) => command.name)).toEqual([
    "continue",
  ]);
  for (const input of [
    "hello",
    "/unknown",
    "/side question",
    "/tmp/file",
    "a\n/stop",
  ]) {
    expect(commandSuggestions(input)).toEqual([]);
  }

  const effects: string[] = [];
  const context = {
    side: (question: string) => {
      effects.push(`side:${question}`);
    },
    teamwork: () => {
      effects.push("board");
    },
    resume: async () => {
      effects.push("resume");
    },
    stop: async () => {
      effects.push("stop");
    },
  };
  for (const input of ["/side How?", "/teamwork", "/continue", "/stop"]) {
    const parsed = composerCommand(input)!;
    expect(await parsed.command.execute(context, parsed.args)).toBe(true);
  }

  expect(effects).toEqual(["side:How?", "board", "resume", "stop"]);
  const request = composerCommand("/teamwork Prüfe zwei unabhängige Ansätze")!;
  expect(await request.command.execute(context, request.args)).toBe(false);
  expect(composerCommand("/stopper")).toBeUndefined();
});

async function commandFixture(page: Page) {
  const current = { ...agent, status: "running" };
  const actions: string[] = [];
  const submissions: Record<string, unknown>[] = [];
  const resolve = (query: string, variables: Record<string, unknown>) => {
    if (query.includes("status{"))
      return { status: { authenticated: true, needsSetup: false } };

    if (query.includes("stopAgent(")) {
      actions.push("stop");
      current.status = "stopped";
      return { stopAgent: true };
    }

    if (query.includes("resumeInputs(")) {
      actions.push("continue");
      current.status = "running";
      return { resumeInputs: true };
    }

    if (query.includes("submitInput(")) {
      submissions.push(variables);
      return {
        submitInput: {
          ...variables,
          status: "queued",
          projectId: project.id,
          createdAt: new Date().toISOString(),
          deliveredAt: "",
          attachments: [],
        },
      };
    }

    if (query.includes("teamwork(")) return { teamwork: null };

    if (isTranscriptQuery(query))
      return bootstrapData(query, {
        ...transcript,
        agent: current,
        messages: [],
        recentMessages: [],
        questions: [],
        jobs: [],
        goal: null,
        agentInputs: [],
      });

    return config;
  };
  await installPushFixture(page, resolve);
  await page.route("**/graphql", async (route) => {
    const { query, variables } = route.request().postDataJSON();
    await route.fulfill({ json: { data: resolve(query, variables ?? {}) } });
  });
  await page.goto(`/#/projects/${project.id}?agent=${agent.id}`);

  return {
    actions,
    submissions,
    composer: page.getByRole("textbox", {
      name: "Aufgabe oder neue Instruktion",
    }),
  };
}

test("the unchanged textarea has no side tools or send/stop button", async ({
  page,
}) => {
  const { composer, submissions } = await commandFixture(page);
  await expect(composer).toHaveAttribute("rows", "2");
  await expect(composer).toHaveAttribute(
    "placeholder",
    "Was soll der Agent erledigen?",
  );
  const form = composer.locator("xpath=ancestor::form");
  await expect(
    form.getByRole("button", {
      name: /Nebenfrage|Teamwork|Agent stoppen|Instruktion senden|Starten/,
    }),
  ).toHaveCount(0);

  await composer.fill("Eine normale Aufgabe");
  await composer.press("Shift+Enter");
  await expect(composer).toHaveValue("Eine normale Aufgabe\n");
  expect(submissions).toHaveLength(0);
  await composer.press("Enter");
  await expect.poll(() => submissions.length).toBe(1);
  expect(submissions[0].text).toBe("Eine normale Aufgabe\n");
});

test("native HeroUI suggestions support filtering, arrows and lifecycle commands", async ({
  page,
}) => {
  const { composer, actions, submissions } = await commandFixture(page);
  await composer.fill("/");
  const menu = page.getByRole("listbox", { name: "Befehle" });
  await expect(menu.getByRole("option")).toHaveCount(4);
  await composer.press("ArrowDown");
  await composer.press("ArrowDown");
  await composer.press("Enter");
  await expect.poll(() => actions).toEqual(["continue"]);
  await expect(composer).toHaveValue("");
  await expect(menu).toHaveCount(0);
  expect(submissions).toHaveLength(0);

  await composer.fill("/sto");
  await expect(menu.getByRole("option")).toHaveCount(1);
  await menu.getByRole("option", { name: /\/stop/ }).click();
  await expect.poll(() => actions).toEqual(["continue", "stop"]);
  expect(submissions).toHaveLength(0);
});

test("Escape preserves the draft and ordinary Tab still queues instructions", async ({
  page,
}) => {
  const { composer, submissions, actions } = await commandFixture(page);
  await composer.fill("/s");
  await expect(page.getByRole("listbox", { name: "Befehle" })).toBeVisible();
  await composer.press("Escape");
  await expect(page.getByRole("listbox", { name: "Befehle" })).toHaveCount(0);
  await expect(composer).toHaveValue("/s");
  expect(actions).toHaveLength(0);

  await composer.fill("Später prüfen");
  await composer.press("Tab");
  await expect.poll(() => submissions.length).toBe(1);
  expect(submissions[0].mode).toBe("queue");
});

test("the project menu keeps Stop but not redundant actions or agent links", async ({
  page,
}) => {
  const { actions } = await commandFixture(page);
  const header = page.getByRole("group", { name: "Agentensteuerung" });
  await expect(
    header.getByRole("heading", { name: project.name, exact: true }),
  ).toHaveCount(0);
  await header
    .getByRole("button", { name: "Projektaktionen", exact: true })
    .click();
  const menu = page.getByRole("menu", { name: "Projektaktionen" });
  await expect(menu.getByRole("menuitem")).toHaveCount(2);
  for (const name of [
    "Neue Unterhaltung",
    "Projekteinstellungen",
    "Editorverbindung",
    "Agentendetails",
    "Umbenennen",
    "Auto-Approver ↗",
  ]) {
    await expect(menu.getByRole("menuitem", { name, exact: true })).toHaveCount(
      0,
    );
  }

  await menu
    .getByRole("menuitem", { name: "Agent stoppen", exact: true })
    .click();
  await expect.poll(() => actions).toEqual(["stop"]);
});

test("the teamwork board remains available as a command, not a side button", async ({
  page,
}) => {
  await commandFixture(page);
  await openComposerCommand(page, "teamwork");
  await expect(
    page.getByRole("dialog", { name: "Teamwork", exact: true }),
  ).toBeVisible();
});

test("ArrowUp and Escape reopening use native focus without losing the draft", async ({
  page,
}) => {
  const { composer, actions } = await commandFixture(page);
  const menu = page.getByRole("listbox", { name: "Befehle" });
  await composer.fill("/s");
  await expect(menu.getByRole("option")).toHaveCount(2);
  await composer.press("Escape");
  await expect(menu).toHaveCount(0);
  await composer.press("ArrowDown");
  await expect(menu).toBeVisible();
  await expect(composer).toHaveValue("/s");

  await composer.fill("/st");
  await composer.press("ArrowUp");
  await composer.press("Enter");
  await expect.poll(() => actions).toEqual(["stop"]);
  await expect(composer).toBeFocused();
});

test("Shift+Enter and IME Enter never execute a highlighted command", async ({
  page,
}) => {
  const { composer, actions, submissions } = await commandFixture(page);
  await composer.fill("/stop");
  await expect(page.getByRole("listbox", { name: "Befehle" })).toBeVisible();
  await composer.dispatchEvent("keydown", {
    key: "Enter",
    code: "Enter",
    isComposing: true,
  });
  await expect(composer).toHaveValue("/stop");
  expect(actions).toEqual([]);

  await composer.press("Shift+Enter");
  await expect(composer).toHaveValue("/stop\n");
  await expect(page.getByRole("listbox", { name: "Befehle" })).toHaveCount(0);
  expect(actions).toEqual([]);
  expect(submissions).toEqual([]);
});

test("a failed lifecycle command retains its draft and can be retried", async ({
  page,
}) => {
  const { composer, actions, submissions } = await commandFixture(page);
  let fail = true;
  await page.route("**/graphql", async (route) => {
    const { query } = route.request().postDataJSON();
    if (query.includes("stopAgent(") && fail) {
      await route.fulfill({
        json: { errors: [{ message: "Stop vorübergehend fehlgeschlagen" }] },
      });
      return;
    }

    await route.fallback();
  });

  await openComposerCommand(page, "stop");
  await expect(
    page.getByText("Stop vorübergehend fehlgeschlagen"),
  ).toBeVisible();
  await expect(composer).toHaveValue("/stop");
  expect(actions).toEqual([]);
  expect(submissions).toEqual([]);

  fail = false;
  await composer.press("Enter");
  await expect.poll(() => actions).toEqual(["stop"]);
  await expect(composer).toHaveValue("");
});

test("the English command menu fits above the unchanged mobile composer", async ({
  page,
}, testInfo) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.addInitScript(() => localStorage.setItem("zentrale-locale", "en"));
  const { actions } = await commandFixture(page);
  const composer = page.getByRole("textbox", {
    name: "Task or new instruction",
  });
  await composer.fill("/s");
  const menu = page.getByRole("listbox", { name: "Commands" });
  await expect(menu.getByRole("option")).toHaveCount(2);
  await expect(menu).toContainText("Stop the current agent");
  const menuBounds = (await menu.boundingBox())!;
  const inputBounds = (await composer.boundingBox())!;
  expect(menuBounds.x).toBeGreaterThanOrEqual(0);
  expect(menuBounds.x + menuBounds.width).toBeLessThanOrEqual(390);
  expect(menuBounds.y + menuBounds.height).toBeLessThanOrEqual(inputBounds.y);
  await page.screenshot({ path: testInfo.outputPath("commands-mobile.png") });

  await composer.fill("/stop");
  await composer.press("Enter");
  await expect.poll(() => actions).toEqual(["stop"]);
});
