import { expect, test, type Page, type WebSocketRoute } from "@playwright/test";
import { mkdir, writeFile } from "node:fs/promises";

import {
  agent,
  agents,
  config,
  project,
  transcript,
} from "./fixtures/visual-data";
import type { AgentInput } from "../src/generated";

async function connectionFixture(page: Page) {
  const state = { offline: false, failures: 0, mutations: 0, queryError: "" };
  const receiptIDs: string[] = [];
  const accepted: AgentInput[] = [];
  let socket: WebSocketRoute | undefined;
  let eventId = 0;
  await page.routeWebSocket("**/events?*", (connection) => {
    socket = connection;
  });
  await page.route("**/graphql", async (route) => {
    const { query, variables = {} } = route.request().postDataJSON();
    if (query.trimStart().startsWith("mutation")) state.mutations++;

    if (query.includes("submitInput(")) receiptIDs.push(variables.id);

    if (state.offline) {
      state.failures++;
      return route.abort("internetdisconnected");
    }

    if (state.queryError && query.includes("recentMessages")) {
      return route.fulfill({
        json: { errors: [{ message: state.queryError }] },
      });
    }

    if (query.includes("submitInput(")) {
      const receipt: AgentInput = {
        ...variables,
        projectId: project.id,
        status: "pending",
        createdAt: new Date().toISOString(),
        deliveredAt: "0001-01-01T00:00:00Z",
      };
      if (!accepted.some((input) => input.id === receipt.id))
        accepted.push(receipt);

      return route.fulfill({ json: { data: { submitInput: receipt } } });
    }

    const data = query.includes("status{")
      ? {
          status: {
            needsSetup: false,
            authenticated: true,
            githubConnected: false,
          },
        }
      : query.includes("recentMessages")
        ? { ...transcript, questions: [], jobs: [], agentInputs: accepted }
        : query.includes("agents(projectId:")
          ? { project, agents }
          : config;

    return route.fulfill({ json: { data } });
  });

  return {
    state,
    receiptIDs,
    refresh: async () => {
      await expect.poll(() => Boolean(socket)).toBe(true);
      socket!.send(JSON.stringify({ id: ++eventId }));
    },
  };
}

for (const variant of [
  {
    name: "desktop-de-light",
    width: 1440,
    height: 900,
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
  test(`one network notification, retained work and automatic recovery: ${variant.name}`, async ({
    page,
  }) => {
    await page.setViewportSize(variant);
    await page.addInitScript(({ locale, theme }) => {
      localStorage.setItem("zentrale-locale", locale);
      localStorage.setItem("zentrale-theme", theme);
    }, variant);
    const fixture = await connectionFixture(page);
    const english = variant.locale === "en";
    const pageErrors: string[] = [];
    page.on("pageerror", (error) => pageErrors.push(error.message));
    await page.goto(`/#/projects/${project.id}?agent=${agent.id}`);
    const input = page.getByRole("textbox", {
      name: english
        ? "Task or new instruction"
        : "Aufgabe oder neue Instruktion",
    });
    const message = page.getByText(transcript.messages[0].content, {
      exact: true,
    });

    await expect(message).toBeVisible();

    await input.fill(
      "Diese noch nicht gesendete Aufgabe muss erhalten bleiben.",
    );

    fixture.state.offline = true;
    await fixture.refresh();
    const notification = page.locator(
      '[data-slot="toast"]:not([data-exiting])',
    );

    await expect(notification).toHaveCount(1);
    await expect(notification).toContainText(
      english ? "Connection interrupted" : "Verbindung unterbrochen",
    );
    await expect.poll(() => fixture.state.failures).toBeGreaterThanOrEqual(3);
    await expect(
      page.locator('[role="alert"]:not([data-slot="toast-content"])'),
    ).toHaveCount(0);
    await expect(
      page.getByText("Failed to fetch", { exact: true }),
    ).toHaveCount(0);
    await expect(message).toBeVisible();
    await expect(input).toHaveValue(/noch nicht gesendete Aufgabe/);
    await expect(input).toBeFocused();
    await expect(page.getByRole("heading", { level: 1 })).toHaveText(
      project.name,
    );

    // Durable messages are retried with their original receipt ID; other
    // mutations are never replayed by the reconnection monitor.
    await page
      .getByRole("button", {
        name: english ? "Start ↑" : "Starten ↑",
        exact: true,
      })
      .click();

    await expect.poll(() => fixture.state.mutations).toBe(1);

    const retry = notification.getByRole("button", {
      name: english ? "Reconnect now" : "Jetzt verbinden",
    });
    await retry.click();

    await expect(notification).toHaveCount(1);

    await page.keyboard.press("Alt+t");

    await expect(
      page.getByRole("region", {
        name: english ? "Notifications" : "Benachrichtigungen",
      }),
    ).toBeFocused();

    for (const button of await notification.getByRole("button").all()) {
      const bounds = await button.boundingBox();
      expect(bounds?.height).toBeGreaterThanOrEqual(44);
      expect(bounds?.width).toBeGreaterThanOrEqual(44);
    }

    await page.mouse.move(0, 0);
    await page.evaluate(() => scrollTo(0, 0));
    await page.evaluate(() => document.fonts.ready);
    await mkdir("artifacts/ui", { recursive: true });
    await page.screenshot({ path: `artifacts/ui/network-${variant.name}.png` });

    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth - innerWidth,
      ),
    ).toBeLessThanOrEqual(1);

    // No click or reload is needed after the server becomes reachable again.
    fixture.state.offline = false;

    await expect(notification).toContainText(
      english ? "Connection restored" : "Wieder verbunden",
      { timeout: 12000 },
    );
    await expect(notification).toHaveCount(1);
    await expect(input).toHaveValue("");
    await expect(
      page.getByRole("region", {
        name: english ? "Message queue" : "Nachrichtenwarteschlange",
      }),
    ).toContainText("noch nicht gesendete Aufgabe");
    await expect(message).toBeVisible();
    await expect.poll(() => fixture.state.mutations).toBe(2);
    expect(new Set(fixture.receiptIDs).size).toBe(1);
    expect(pageErrors).toEqual([]);

    await page.screenshot({
      path: `artifacts/ui/network-restored-${variant.name}.png`,
    });
    await writeFile(
      `artifacts/ui/notifications-${variant.name}.json`,
      JSON.stringify({
        captures: [
          { name: "network-interrupted", file: `network-${variant.name}.png` },
          {
            name: "network-restored",
            file: `network-restored-${variant.name}.png`,
          },
        ],
      }),
    );
    await notification
      .getByRole("button", {
        name: english ? "Dismiss notification" : "Benachrichtigung schließen",
      })
      .click();

    await expect(notification).toHaveCount(0);
  });
}

test("initially offline: one notification and a recoverable loading state", async ({
  page,
}) => {
  const { state } = await connectionFixture(page);
  state.offline = true;
  await page.goto("/");
  const notification = page.locator('[data-slot="toast"]:not([data-exiting])');

  await expect(notification).toHaveCount(1);
  await expect(
    page.getByText("Warten auf Verbindung …", { exact: true }),
  ).toBeVisible();
  await expect(
    page.locator('[role="alert"]:not([data-slot="toast-content"])'),
  ).toHaveCount(0);
  await expect(page.getByRole("progressbar")).toHaveCount(0);

  state.offline = false;
  await notification.getByRole("button", { name: "Jetzt verbinden" }).click();

  await expect(
    page.getByRole("heading", { name: "Projekte", exact: true }),
  ).toBeVisible();
  await expect(notification).toContainText("Wieder verbunden");
});

test("application errors stay contextual and do not report a network interruption", async ({
  page,
}) => {
  const { state, refresh } = await connectionFixture(page);
  await page.goto(`/#/projects/${project.id}?agent=${agent.id}`);

  await expect(
    page.getByText(transcript.messages[0].content, { exact: true }),
  ).toBeVisible();

  state.queryError =
    "Agentendaten konnten nicht gelesen werden. Erneut versuchen.";
  await refresh();

  await expect(
    page.locator('[role="alert"]:not([data-slot="toast-content"])'),
  ).toHaveCount(1);
  await expect(
    page.locator('[role="alert"]:not([data-slot="toast-content"])'),
  ).toContainText(state.queryError);
  await expect(page.locator('[data-slot="toast"]')).toHaveCount(0);
  await expect(
    page.getByText(transcript.messages[0].content, { exact: true }),
  ).toBeVisible();

  state.queryError = "";
  await page
    .getByRole("button", { name: "Erneut versuchen", exact: true })
    .click();

  await expect(
    page.locator('[role="alert"]:not([data-slot="toast-content"])'),
  ).toHaveCount(0);
});
