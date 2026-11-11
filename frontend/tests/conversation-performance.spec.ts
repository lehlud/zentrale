import { expect, test, type WebSocketRoute } from "@playwright/test";
import {
  agent,
  agents,
  config,
  project,
  transcript,
} from "./fixtures/visual-data";

test("long conversations keep typing isolated, observers bounded and updates responsive", async ({
  page,
}, testInfo) => {
  test.setTimeout(90000);
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.addInitScript(() => {
    const stats = {
      scrolls: 0,
      times: 0,
      latencies: [] as number[],
      observers: new Set<IntersectionObserver>(),
    };
    Object.assign(window, { conversationPerf: stats });
    const scroll = window.scrollTo;
    window.scrollTo = ((...args: unknown[]) => {
      stats.scrolls++;
      Reflect.apply(scroll, window, args);
    }) as typeof window.scrollTo;
    const time = Date.prototype.toLocaleTimeString;
    Date.prototype.toLocaleTimeString = function (...args) {
      stats.times++;
      return Reflect.apply(time, this, args);
    };
    const Observer = window.IntersectionObserver;
    window.IntersectionObserver = class extends Observer {
      constructor(
        callback: IntersectionObserverCallback,
        options?: IntersectionObserverInit,
      ) {
        super(callback, options);
        stats.observers.add(this);
      }
      disconnect() {
        stats.observers.delete(this);
        super.disconnect();
      }
    };
    document.addEventListener(
      "input",
      (event) => {
        if (!(event.target instanceof HTMLTextAreaElement)) return;
        const start = performance.now();
        requestAnimationFrame(() =>
          stats.latencies.push(performance.now() - start),
        );
      },
      true,
    );
  });
  const messages = Array.from({ length: 1500 }, (_, index) => ({
    ...transcript.messages[0],
    id: `perf-${index}`,
    role: index % 2 ? "assistant" : "user",
    content:
      `Performance message ${index}\n\n` +
      "A substantial paragraph with **Markdown** and `code`. ".repeat(24),
    wire: "",
    callId: "",
    toolName: "",
    change: null,
    changes: [],
    activities: [],
    createdAt: new Date(Date.UTC(2026, 0, 1, 0, index)).toISOString(),
  }));
  let socket: WebSocketRoute | undefined;
  let eventId = 0;
  let queries = 0;
  const submitted: { text: string; mode: string }[] = [];
  await page.routeWebSocket("**/events?*", (connection) => {
    socket = connection;
  });
  await page.route("**/graphql", async (route) => {
    const { query, variables = {} } = route.request().postDataJSON();
    let data: unknown = config;
    if (query.includes("status{"))
      data = { status: { authenticated: true, needsSetup: false } };
    else if (query.includes("submitInput(")) {
      submitted.push(variables);
      data = {
        submitInput: {
          ...variables,
          status: "queued",
          createdAt: new Date().toISOString(),
          deliveredAt: "",
          projectId: project.id,
        },
      };
    } else if (query.includes("recentMessages")) {
      queries++;
      // Simulate an already-loaded scrollback; pagination itself has separate tests.
      data = variables.beforeId
        ? { messages: [] }
        : {
            ...transcript,
            agent: { ...agent, status: "idle" },
            messages: [...messages].reverse(),
            recentMessages: [],
            goal: null,
            goalHistory: [],
            agentInputs: [],
            questions: [],
            jobs: [],
          };
    } else if (query.includes("agents(projectId:")) data = { project, agents };
    await route.fulfill({ json: { data } });
  });
  await page.goto(`/#/projects/${project.id}?agent=${agent.id}`);
  const input = page.getByRole("textbox", {
    name: "Aufgabe oder neue Instruktion",
  });
  await expect(page.getByText(/^Performance message 1499\n/)).toBeVisible();
  await expect(page.locator("[data-transcript-item]")).toHaveCount(1500);
  // Slow CPU makes main-thread regressions easier to spot without a brittle FPS target.
  const cdp = await page.context().newCDPSession(page);
  await cdp.send("Emulation.setCPUThrottlingRate", { rate: 4 });
  await input.click();
  await page.waitForTimeout(250);
  await page.evaluate(() => {
    const stats = (window as any).conversationPerf;
    stats.scrolls = stats.times = 0;
    stats.latencies = [];
  });
  const draft = "A responsive draft while reading a very long conversation.";
  await input.pressSequentially(draft);
  await expect(input).toHaveValue(draft);
  await page.evaluate(
    () =>
      new Promise<void>((resolve) =>
        requestAnimationFrame(() => requestAnimationFrame(() => resolve())),
      ),
  );
  const metrics = await page.evaluate(() => {
    const stats = (window as any).conversationPerf;
    return {
      scrolls: stats.scrolls,
      historyRenders: stats.times,
      observers: stats.observers.size,
      latencies: stats.latencies as number[],
    };
  });
  await testInfo.attach("typing-performance.json", {
    body: JSON.stringify(metrics, null, 2),
    contentType: "application/json",
  });
  expect(
    metrics.historyRenders,
    "typing must not rerender historical messages",
  ).toBe(0);
  expect(
    metrics.scrolls,
    "typing must not force history layout/scroll on each key",
  ).toBeLessThan(3);
  expect(
    metrics.observers,
    "observers must not grow with message count",
  ).toBeLessThan(20);
  const sorted = [...metrics.latencies].sort((a, b) => a - b);
  expect(sorted.length).toBeGreaterThan(20);
  expect(
    sorted[Math.floor(sorted.length * 0.95)],
    "p95 input-to-next-frame at 4x CPU slowdown",
  ).toBeLessThan(250);

  const refresh = async () => {
    const before = queries;
    socket!.send(JSON.stringify({ id: ++eventId }));
    await expect.poll(() => queries).toBeGreaterThan(before);
    await page.waitForTimeout(200);
  };
  // Unchanged server snapshots must retain message identity and cached rendering.
  await page.evaluate(() => {
    (window as any).conversationPerf.times = 0;
  });
  await refresh();
  await refresh();
  expect(
    await page.evaluate(() => (window as any).conversationPerf.times),
  ).toBe(0);
  await expect(input).toHaveValue(draft);
  messages.at(-1)!.content = "Revised existing message after a server update";
  await refresh();
  await expect(
    page.getByText("Revised existing message after a server update", {
      exact: true,
    }),
  ).toBeVisible();
  await page.evaluate(() => scrollTo(0, 0));
  await expect(page.getByText(/^Performance message 0\n/)).toBeVisible();
  messages.push({
    ...messages.at(-1)!,
    id: "perf-new",
    content: "New output while reading old history",
    createdAt: "2026-02-01T00:00:00Z",
  });
  await refresh();
  await expect.poll(() => page.evaluate(() => scrollY)).toBeLessThan(100);
  await expect(input).toHaveValue(draft);
  expect(await page.locator('[data-rendered="true"]').count()).toBeLessThan(60);
  await page.evaluate(() => scrollTo(0, document.documentElement.scrollHeight));
  await expect(
    page.getByText("New output while reading old history", { exact: true }),
  ).toBeVisible();
  await input.press("Enter");
  await expect
    .poll(() => submitted)
    .toEqual([
      { agentId: agent.id, id: expect.any(String), text: draft, mode: "steer" },
    ]);
  await expect(input).toHaveValue("");
});
