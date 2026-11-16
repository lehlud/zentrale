import { expect, test, type Page, type WebSocketRoute } from "@playwright/test";

import { bootstrapData, isTranscriptQuery } from "./fixtures/bootstrap-data";
import { openComposerCommand } from "./fixtures/composer";
import { installPushFixture } from "./fixtures/push-fixture";
import { agent, config, project, transcript } from "./fixtures/visual-data";

async function sideFixture(page: Page, answer = true) {
  const questions: { id: string; text: string }[] = [];
  const mainFrames: string[] = [];
  const connections: WebSocketRoute[] = [];
  let closed = 0;

  page.on("websocket", (socket) => {
    if (!socket.url().includes("/side/")) {
      socket.on("framesent", (frame) => mainFrames.push(String(frame.payload)));
    }
  });
  await installPushFixture(page, (query) => {
    if (query.includes("status{")) {
      return {
        status: {
          authenticated: true,
          needsSetup: false,
          githubConnected: false,
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
  });
  await page.route("**/graphql", async (route) => {
    const { query } = route.request().postDataJSON();
    const data = query.includes("status{")
      ? {
          status: {
            authenticated: true,
            needsSetup: false,
            githubConnected: false,
          },
        }
      : isTranscriptQuery(query)
        ? bootstrapData(query, {
            ...transcript,
            agent,
            questions: [],
            jobs: [],
          })
        : config;
    await route.fulfill({ json: { data } });
  });
  await page.routeWebSocket("**/side/**", (socket) => {
    connections.push(socket);
    socket.onClose(() => {
      closed++;
    });
    socket.onMessage((raw) => {
      const message = JSON.parse(String(raw));
      questions.push(message);
      if (answer) {
        socket.send(
          JSON.stringify({ type: "delta", id: message.id, text: "Geprüfte " }),
        );
        socket.send(
          JSON.stringify({
            type: "done",
            id: message.id,
            text: "Geprüfte Messung aus dem Unteragenten. <img src=x onerror=alert(1)>",
          }),
        );
      }
    });
    socket.send(JSON.stringify({ type: "ready", readOnly: true }));
  });
  await page.goto(`/#/projects/${project.id}?agent=${agent.id}`);
  return { questions, mainFrames, connections, closed: () => closed };
}

test("/side answers in an ephemeral modal without steering the main agent", async ({
  page,
}) => {
  const fixture = await sideFixture(page);
  const composer = page.getByRole("textbox", {
    name: "Aufgabe oder neue Instruktion",
  });
  await composer.fill("/side Was meldet der Prüfagent?");
  await composer.press("Enter");

  const modal = page.getByRole("dialog", { name: "Nebenfrage (/side)" });
  await expect(modal).toBeVisible();
  await expect(modal).toContainText("Geprüfte Messung aus dem Unteragenten.");
  await expect(modal.locator("img")).toHaveCount(0);
  await expect(composer).toHaveValue("");
  expect(fixture.questions.map((question) => question.text)).toEqual([
    "Was meldet der Prüfagent?",
  ]);
  expect(fixture.mainFrames.join("\n")).not.toContain(
    "Was meldet der Prüfagent?",
  );

  await modal
    .getByRole("textbox", { name: "Kurze Frage zum Hauptthread" })
    .fill("Und welcher Test?");
  await modal.getByRole("button", { name: "Nebenfrage senden" }).click();
  await expect.poll(() => fixture.questions.length).toBe(2);
  expect(fixture.connections).toHaveLength(1);

  await modal.getByRole("button", { name: "Schließen", exact: true }).click();
  await expect(modal).toHaveCount(0);
  await expect.poll(fixture.closed).toBe(1);

  await openComposerCommand(page, "side");
  await expect(modal).toBeVisible();
  await expect(modal).not.toContainText("Was meldet der Prüfagent?");
  expect(fixture.questions).toHaveLength(2);
});

test("side conversation controls follow the selected UI language", async ({
  page,
}) => {
  await page.addInitScript(() => localStorage.setItem("zentrale-locale", "en"));
  await sideFixture(page);
  await openComposerCommand(page, "side");

  const modal = page.getByRole("dialog", { name: "Side question (/side)" });
  await expect(modal).toContainText("Usage is attributed to the main agent.");
  await expect(
    modal.getByRole("textbox", {
      name: "Short question about the main thread",
    }),
  ).toBeVisible();
  await expect(
    modal.getByRole("button", { name: "Send side question" }),
  ).toBeVisible();
});

test("closing /side cancels its socket even while an answer is running", async ({
  page,
}) => {
  const fixture = await sideFixture(page, false);
  await openComposerCommand(page, "side");
  const modal = page.getByRole("dialog", { name: "Nebenfrage (/side)" });
  await modal
    .getByRole("textbox", { name: "Kurze Frage zum Hauptthread" })
    .fill("Laufende Frage");
  await modal.getByRole("button", { name: "Nebenfrage senden" }).click();
  await expect(modal).toContainText("Antwort wird erarbeitet");
  await modal.getByRole("button", { name: "Schließen", exact: true }).click();
  await expect.poll(fixture.closed).toBe(1);
  expect(fixture.connections).toHaveLength(1);
});

test("page hide closes /side and does not replay on a restored page", async ({
  page,
}) => {
  const fixture = await sideFixture(page);
  await openComposerCommand(page, "side");
  await expect(
    page.getByRole("dialog", { name: "Nebenfrage (/side)" }),
  ).toBeVisible();
  await page.evaluate(() =>
    dispatchEvent(new PageTransitionEvent("pagehide", { persisted: true })),
  );
  await expect.poll(fixture.closed).toBe(1);
  await expect(page.getByRole("dialog")).toContainText(
    "Nebenunterhaltung beendet",
  );
  expect(fixture.questions).toHaveLength(0);
});

test("/side honors the configured preview base", async ({ page }) => {
  const fixture = await sideFixture(page);
  await page.evaluate(() => {
    (
      globalThis as typeof globalThis & { __ZENTRALE_PREVIEW_BASE__?: string }
    ).__ZENTRALE_PREVIEW_BASE__ = "/preview/";
  });
  await openComposerCommand(page, "side");
  await expect.poll(() => fixture.connections.length).toBe(1);
  expect(fixture.connections[0].url()).toContain(`/preview/side/${agent.id}`);
});
