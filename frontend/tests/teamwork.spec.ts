import { expect, test, type Page } from "@playwright/test";

import { bootstrapData, isTranscriptQuery } from "./fixtures/bootstrap-data";
import { openComposerCommand } from "./fixtures/composer";
import { installPushFixture } from "./fixtures/push-fixture";
import { agent, config, project, transcript } from "./fixtures/visual-data";

async function teamworkFixture(page: Page, empty = false) {
  const submissions: Record<string, unknown>[] = [];
  let reads = 0;
  const board = {
    id: "board",
    agentId: agent.id,
    objective: "Implementieren und unabhängig prüfen",
    pattern: "solve_verify",
    status: "active",
    revision: 1,
    maxWorkers: 2,
    evidence: "",
    tasks: [
      {
        id: "build",
        title: "Implementierung",
        objective: "Vertrag umsetzen",
        acceptance: "Regression besteht",
        kind: "work",
        dependsOn: [],
        writePaths: ["backend"],
        ownerId: "worker",
        status: "claimed",
        result: "",
        evidence: "<img src=x onerror=alert(1)>",
      },
    ],
  };
  const resolve = (query: string, variables: Record<string, unknown>) => {
    if (query.includes("status{")) {
      return { status: { authenticated: true, needsSetup: false } };
    }

    if (query.includes("teamwork(")) {
      reads++;
      return { teamwork: empty ? null : board };
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

    if (isTranscriptQuery(query)) {
      return bootstrapData(query, {
        ...transcript,
        agent,
        questions: [],
        jobs: [],
      });
    }

    return config;
  };
  const push = await installPushFixture(page, resolve);
  await page.route("**/graphql", async (route) => {
    const { query, variables } = route.request().postDataJSON();
    await route.fulfill({ json: { data: resolve(query, variables ?? {}) } });
  });

  await page.goto(`/#/projects/${project.id}?agent=${agent.id}`);
  await expect(
    page.getByRole("textbox", {
      name: /Aufgabe oder neue Instruktion|Task or new instruction/,
    }),
  ).toBeVisible();

  return { board, push, submissions, reads: () => reads };
}

test("teamwork is a safe on-demand native board with live updates", async ({
  page,
}) => {
  const fixture = await teamworkFixture(page);
  expect(fixture.reads()).toBe(0);

  await openComposerCommand(page, "teamwork");
  const modal = page.getByRole("dialog", { name: "Teamwork", exact: true });
  await expect(modal).toContainText("Implementieren und unabhängig prüfen");
  await modal.locator("summary").click();
  await expect(modal).toContainText("Regression besteht");
  await expect(modal).toContainText("<img src=x onerror=alert(1)>");
  await expect(modal.locator("img")).toHaveCount(0);
  expect(fixture.submissions).toHaveLength(0);

  fixture.board.tasks[0].status = "done";
  fixture.board.tasks[0].result = "Geprüft";
  fixture.board.revision++;
  await fixture.push.push();
  await expect(modal.locator("summary")).toContainText("Abgeschlossen");

  await modal.getByRole("button", { name: "Schließen", exact: true }).click();
  await expect(modal).toHaveCount(0);
  await expect
    .poll(
      () =>
        fixture.push.frames.filter((frame) => frame.type === "unsubscribe")
          .length,
    )
    .toBeGreaterThan(0);
});

test("/teamwork submits an explicit request, never an automatic swarm", async ({
  page,
}) => {
  const fixture = await teamworkFixture(page, true);
  await openComposerCommand(page, "teamwork");
  const modal = page.getByRole("dialog", { name: "Teamwork", exact: true });
  await expect(modal).toContainText("Noch kein Teamwork-Board");
  expect(fixture.submissions).toHaveLength(0);
  await modal.getByRole("button", { name: "Schließen", exact: true }).click();

  const composer = page.getByRole("textbox", {
    name: "Aufgabe oder neue Instruktion",
  });
  await composer.fill("/teamwork Prüfe die zwei unabhängigen Lösungswege");
  await composer.press("Enter");
  await expect.poll(() => fixture.submissions.length).toBe(1);
  expect(fixture.submissions[0].text).toBe(
    "/teamwork Prüfe die zwei unabhängigen Lösungswege",
  );
  expect(fixture.submissions[0].mode).toBe("steer");
  await expect(composer).toHaveValue("");
});

test("teamwork board instructions follow the selected UI language", async ({
  page,
}) => {
  await page.addInitScript(() => localStorage.setItem("zentrale-locale", "en"));
  await teamworkFixture(page, true);
  await openComposerCommand(page, "teamwork");

  const modal = page.getByRole("dialog", { name: "Teamwork", exact: true });
  await expect(modal).toContainText("No teamwork board yet.");
  await expect(modal).toContainText("Agents are not started automatically.");
});
