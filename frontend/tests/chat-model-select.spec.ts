import { expect, test } from "@playwright/test";
import { bootstrapData, isTranscriptQuery } from "./fixtures/bootstrap-data";
import { installPushFixture } from "./fixtures/push-fixture";
import { adminStatus, agent, config } from "./fixtures/visual-data";

const alpha = {
  providerId: "a",
  modelId: "alpha-code",
  displayName: "Alpha",
  providerName: "Provider A",
  reasoningLevel: "low",
  contextWindow: 80000,
};
const betaA = {
  ...alpha,
  modelId: "beta-a-code",
  displayName: "Beta",
  reasoningLevel: "high",
  contextWindow: 160000,
};
const betaB = {
  ...betaA,
  modelId: "beta-b-code",
  providerId: "b",
  providerName: "Provider B",
};
const presets = [alpha, betaA, betaB];

test("chat model popover uses catalog names and disambiguates by provider", async ({
  page,
}) => {
  await page.addInitScript(() => localStorage.setItem("zentrale-locale", "de"));
  let active = alpha;
  const chat = {
    ...agent,
    id: "chat-model-fixture",
    projectId: "",
    name: "Chat",
  };
  const mutationChoices: unknown[] = [];

  function resolve(query: string) {
    if (query.includes("status{")) return { status: adminStatus };
    if (query.includes("shortChatModelOptions"))
      return { shortChatModelOptions: { presets, defaults: { main: alpha } } };
    if (query.includes("conversationModels("))
      return {
        conversationModels: { main: active },
        conversationModelPresets: presets,
      };
    if (query.includes("shortChat(")) return { shortChat: chat };
    if (isTranscriptQuery(query))
      return bootstrapData(
        query,
        {
          agent: {
            ...chat,
            contextWindow: active.contextWindow,
            contextTokens: 20000,
          },
          messages: [],
          jobs: [],
          questions: [],
        },
        { agents: [] },
      );
    return config;
  }

  const fixture = await installPushFixture(page, resolve);
  await page.route("**/graphql", async (route) => {
    const { query, variables } = route.request().postDataJSON();

    if (query.includes("setConversationModel(")) {
      mutationChoices.push(variables.choice);
      active = betaA;
      await route.fulfill({ json: { data: { setConversationModel: true } } });
      await fixture.push();
    } else await route.fulfill({ json: { data: resolve(query) } });
  });

  await page.goto("/#/chats");
  const model = page
    .getByRole("button", { name: "Modell", exact: false })
    .filter({ has: page.locator('[data-slot="select-value"]') });
  await expect(model).toHaveText("Alpha · low");
  await model.focus();
  await page.keyboard.press("Space");
  const options = page.getByRole("listbox");
  await expect(options.getByRole("option")).toHaveText([
    "Alpha · low",
    "Beta · Provider A · high",
    "Beta · Provider B · high",
  ]);
  await options
    .getByRole("option", { name: "Beta · Provider B · high" })
    .click();
  await expect(model).toHaveText("Beta · Provider B · high");
  await expect(options).not.toBeVisible();
  expect(mutationChoices).toEqual([]);
  const context = page.getByRole("button", { name: "Kontextfenster" });
  await context.click();
  const contextPanel = page.getByRole("dialog", { name: "Kontextfenster" });
  await expect(contextPanel).toContainText("160.000");
  await expect(
    contextPanel.getByRole("button", { name: /compact|verdichten/i }),
  ).toHaveCount(0);
  await page.keyboard.press("Escape");
  await expect(context).toBeFocused();

  await page.goto(`/#/chats/${chat.id}`);
  await expect(model).toHaveText("Alpha · low");
  await expect(context).toHaveAccessibleDescription(/25\s*% belegt/);
  await expect(context.locator("circle[stroke-dasharray]")).toHaveAttribute(
    "stroke-dasharray",
    "25 100",
  );
  await model.click();
  await options
    .getByRole("option", { name: "Beta · Provider A · high" })
    .click();
  await expect(model).toHaveText("Beta · Provider A · high");
  expect(mutationChoices).toEqual([
    { providerId: "a", modelId: "beta-a-code", reasoningLevel: "high" },
  ]);
});

test("connection toast uses the top right and aligns icon and close button at the top", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1280, height: 900 });
  await page.emulateMedia({ reducedMotion: "reduce" });
  await page.addInitScript(() => localStorage.setItem("zentrale-locale", "de"));
  await installPushFixture(page, (query) =>
    query.includes("status{") ? { status: adminStatus } : config,
  );
  await page.route("**/graphql", async (route) => {
    const { query } = route.request().postDataJSON();
    await route.fulfill({
      json: {
        data: query.includes("status{") ? { status: adminStatus } : config,
      },
    });
  });
  await page.goto("/#/chats");
  await expect(
    page.getByRole("heading", { name: "Neue Unterhaltung" }),
  ).toBeVisible();
  await page.evaluate(() => window.dispatchEvent(new Event("offline")));

  const toast = page
    .locator('[data-slot="toast"]')
    .filter({ hasText: "Verbindung unterbrochen" });
  await expect(toast).toBeVisible();
  const box = (await toast.boundingBox())!;
  expect(box.y).toBeLessThan(50);
  expect(box.x + box.width).toBeGreaterThan(1230);
  expect(box.width).toBeCloseTo(540, 0);
  const positions = await toast.evaluate((card) => {
    const bounds = (slot: string) =>
      card.querySelector(`[data-slot="${slot}"]`)!.getBoundingClientRect();
    const icon = bounds("toast-indicator");
    const close = bounds("toast-close");
    const content = bounds("toast-content");

    return {
      icon: { x: icon.x, y: icon.y },
      close: { x: close.x, y: close.y },
      content: { x: content.x, y: content.y },
    };
  });

  expect(Math.abs(positions.icon.y - positions.content.y)).toBeLessThan(4);
  expect(Math.abs(positions.close.y - positions.content.y)).toBeLessThan(4);
  expect(positions.icon.x).toBeLessThan(positions.content.x);
  expect(positions.close.x).toBeGreaterThan(positions.content.x);
});

test("title-only notices fit their text and center the title between the controls", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1280, height: 900 });
  await page.emulateMedia({ reducedMotion: "reduce" });
  await page.addInitScript(() => localStorage.setItem("zentrale-locale", "de"));
  await installPushFixture(page, (query) =>
    query.includes("status{") ? { status: adminStatus } : config,
  );
  await page.route("**/graphql", async (route) => {
    const { query } = route.request().postDataJSON();
    await route.fulfill({
      json: {
        data: query.includes("status{") ? { status: adminStatus } : config,
      },
    });
  });
  await page.goto("/#/chats");
  await expect(
    page.getByRole("heading", { name: "Neue Unterhaltung" }),
  ).toBeVisible();

  const showNotice = (title: string) =>
    page.evaluate((detail) => {
      window.dispatchEvent(new CustomEvent("zentrale:notice", { detail }));
    }, title);
  const toast = page.locator('[data-slot="toast"]');
  const shortNotice = toast.filter({ hasText: "Erfolgreich" });
  await showNotice("Erfolgreich");
  await expect(shortNotice).toBeVisible();

  const geometry = await shortNotice.evaluate((card) => {
    const bounds = (slot: string) =>
      card.querySelector(`[data-slot="${slot}"]`)!.getBoundingClientRect();
    const middle = ({ y, height }: DOMRect) => y + height / 2;
    const icon = bounds("toast-indicator");
    const title = bounds("toast-title");
    const close = bounds("toast-close");
    const box = card.getBoundingClientRect();
    return {
      width: box.width,
      right: box.right,
      top: box.top,
      height: box.height,
      closeWidth: close.width,
      centerOffsets: [
        middle(icon) - middle(title),
        middle(close) - middle(title),
      ],
    };
  });

  expect(geometry.width).toBeLessThan(250);
  expect(geometry.height).toBeLessThan(60);
  expect(geometry.top).toBeLessThan(50);
  expect(geometry.right).toBeCloseTo(1264, 0);
  expect(geometry.closeWidth).toBeGreaterThanOrEqual(44);
  expect(geometry.centerOffsets.every((offset) => Math.abs(offset) < 2)).toBe(
    true,
  );
  await shortNotice
    .getByRole("button", { name: "Benachrichtigung schließen" })
    .click();
  await expect(shortNotice).toHaveCount(0);

  await page.setViewportSize({ width: 320, height: 700 });
  const longTitle = "Eine längere Bestätigung ohne weiteren Beschreibungstext";
  await showNotice(longTitle);
  const longNotice = toast.filter({ hasText: longTitle });
  await expect(longNotice).toBeVisible();
  const box = (await longNotice.boundingBox())!;
  expect(box.width).toBeGreaterThan(geometry.width);
  expect(box.x).toBeGreaterThanOrEqual(15);
  expect(box.x + box.width).toBeLessThanOrEqual(305);
});
