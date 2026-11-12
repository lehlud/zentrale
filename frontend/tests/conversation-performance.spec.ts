import { expect, test, type WebSocketRoute } from "@playwright/test";
import { installPushFixture } from "./fixtures/push-fixture";
import { bootstrapData, isTranscriptQuery } from "./fixtures/bootstrap-data";
import {
  agent,
  agents,
  config,
  project,
  transcript,
} from "./fixtures/visual-data";

test.afterEach(async ({ page }) => {
  if (!page.isClosed()) {
    const session = await page.context().newCDPSession(page);
    await session.send("Emulation.setCPUThrottlingRate", { rate: 1 });
    await session.detach();
  }
});

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
  let queries = 0;
  const submitted: {
    agentId: string;
    id: string;
    text: string;
    mode: string;
  }[] = [];
  const resolve = async (
    query: string,
    variables: Record<string, any> = {},
  ) => {
    let data: unknown = config;
    if (query.includes("status{"))
      data = { status: { authenticated: true, needsSetup: false } };
    else if (query.includes("submitInput(")) {
      submitted.push({
        agentId: variables.agentId,
        id: variables.id,
        text: variables.text,
        mode: variables.mode,
      });
      data = {
        submitInput: {
          ...variables,
          status: "queued",
          createdAt: new Date().toISOString(),
          deliveredAt: "",
          projectId: project.id,
        },
      };
    } else if (isTranscriptQuery(query)) {
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
    return bootstrapData(query, data);
  };
  const push = await installPushFixture(page, resolve, (connection) => {
    socket = connection;
  });
  await page.route("**/graphql", async (route) => {
    const { query, variables = {} } = route.request().postDataJSON();
    // Explicit scrollback reads are expected when the user scrolls to the top;
    // they are not live snapshot refetches.
    if (isTranscriptQuery(query) && !variables.beforeId) queries++;
    const data = await resolve(query, variables);
    if (query.trimStart().startsWith("mutation")) await push.push();
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
  let draft = "A responsive draft while reading a very long conversation.";
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

  await page.evaluate(() => {
    (window as any).conversationPerf.latencies = [];
  });
  let liveRevision = 0;
  const liveTimer = setInterval(() => {
    socket!.send(
      JSON.stringify({
        type: "response",
        data: JSON.stringify({
          id: "perf-stream",
          agentId: agent.id,
          text: `Streaming output ${++liveRevision}`,
          revision: liveRevision,
          snapshot: true,
          status: "running",
          createdAt: new Date().toISOString(),
        }),
      }),
    );
  }, 40);
  const suffix = " With live output.";
  try {
    await input.pressSequentially(suffix, { delay: 15 });
    await expect.poll(() => liveRevision).toBeGreaterThan(2);
    await expect(
      page.getByRole("article", { name: "Live-Antwort" }),
    ).toContainText("Streaming output");
  } finally {
    clearInterval(liveTimer);
  }
  draft += suffix;
  await expect(input).toHaveValue(draft);
  await expect(input).toBeFocused();
  await page.evaluate(
    () =>
      new Promise<void>((resolve) =>
        requestAnimationFrame(() => requestAnimationFrame(() => resolve())),
      ),
  );
  const liveLatencies = await page.evaluate(
    () => (window as any).conversationPerf.latencies as number[],
  );
  liveLatencies.sort((a, b) => a - b);
  await testInfo.attach("typing-with-live-output.json", {
    body: JSON.stringify(liveLatencies),
    contentType: "application/json",
  });
  expect(liveLatencies.length).toBeGreaterThan(5);
  expect(liveLatencies[Math.floor(liveLatencies.length * 0.95)]).toBeLessThan(
    250,
  );
  // Even the first SMALL upward gesture must suspend following. Previously
  // the <100px "near bottom" threshold immediately re-enabled it, and concurrent
  // virtual-row measurements made the height-based scroll heuristic miss it.
  const latest = page.getByRole("button", {
    name: "Zu neuesten Einträgen ↓",
    exact: true,
  });
  if (await latest.isVisible()) await latest.click();
  await page.mouse.move(1100, 400);
  await page.mouse.wheel(0, -40);
  await expect(latest).toBeVisible();
  socket!.send(
    JSON.stringify({
      type: "response",
      data: JSON.stringify({
        id: "perf-stream",
        agentId: agent.id,
        text:
          "Streaming output with changing height\n\n" +
          "A growing paragraph.\n\n".repeat(40),
        revision: ++liveRevision,
        snapshot: true,
        status: "running",
        createdAt: new Date().toISOString(),
      }),
    }),
  );
  await page.waitForTimeout(400);
  await expect(latest).toBeVisible();
  expect(
    await page.evaluate(
      () => document.documentElement.scrollHeight - innerHeight - scrollY,
    ),
  ).toBeGreaterThan(20);
  // Keyboard scrolling must work too, without depending on a wheel event.
  await latest.click();
  await page.evaluate(() => (document.activeElement as HTMLElement)?.blur());
  await page.keyboard.press("PageUp");
  await expect(latest).toBeVisible();
  await page.waitForTimeout(250);
  expect(
    await page.evaluate(
      () => document.documentElement.scrollHeight - innerHeight - scrollY,
    ),
  ).toBeGreaterThan(100);
  await latest.click();
  await expect(latest).not.toBeVisible();
  await input.focus();
  socket!.send(JSON.stringify({ type: "response_reset" }));
  await expect(page.getByRole("article", { name: "Live-Antwort" })).toHaveCount(
    0,
  );

  const refresh = async () => {
    const before = queries;
    await push.push();
    await page.waitForTimeout(200);
    expect(queries, "server snapshots must not cause HTTP reads").toBe(before);
  };
  // Unchanged server snapshots must retain message identity and cached rendering.
  // Removing the live draft changes viewport geometry; wait for the virtual
  // rows affected by that earlier action to settle before measuring snapshots.
  let previousTimes = -1;
  let stableSamples = 0;
  await expect
    .poll(
      async () => {
        const times = await page.evaluate(
          () => (window as any).conversationPerf.times,
        );
        stableSamples = times === previousTimes ? stableSamples + 1 : 0;
        previousTimes = times;
        return stableSamples;
      },
      { intervals: [100], timeout: 5000 },
    )
    .toBeGreaterThanOrEqual(3);
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
