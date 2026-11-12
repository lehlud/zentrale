import { expect, test, type WebSocketRoute } from "@playwright/test";
import { agent, config, project, transcript } from "./fixtures/visual-data";
import { bootstrapData } from "./fixtures/bootstrap-data";

test("live subscriptions accept unsolicited server pushes without HTTP or WS polling", async ({
  page,
}) => {
  let socket: WebSocketRoute | undefined;
  const frames: { type: string; requestId: string; query?: string }[] = [];
  let httpReads = 0;
  const snapshot = (content: string) =>
    bootstrapData("conversationBootstrap(", {
      ...transcript,
      messages: [
        {
          ...transcript.messages[0],
          id: "push-message",
          agentId: agent.id,
          role: "assistant",
          content,
          wire: "",
        },
      ],
    });
  await page.route("**/graphql", async (route) => {
    httpReads++;
    const { query } = route.request().postDataJSON();
    await route.fulfill({
      json: {
        data: query.includes("conversationBootstrap(")
          ? snapshot("Initial push fixture")
          : /\bstatus\s*\{/.test(query)
            ? {
                status: {
                  authenticated: true,
                  needsSetup: false,
                  githubConnected: false,
                },
              }
            : config,
      },
    });
  });
  await page.routeWebSocket("**/events?*", (connection) => {
    socket = connection;
    connection.onMessage((message) => frames.push(JSON.parse(String(message))));
  });
  await page.goto(`/#/projects/${project.id}?agent=${agent.id}`);
  await expect(
    page.getByText("Initial push fixture", { exact: true }),
  ).toBeVisible();
  await expect
    .poll(
      () =>
        frames.filter(
          (frame) =>
            frame.type === "subscribe" &&
            frame.query?.includes("conversationBootstrap("),
        ).length,
    )
    .toBe(1);
  const id = frames.find(
    (frame) =>
      frame.type === "subscribe" &&
      frame.query?.includes("conversationBootstrap("),
  )!.requestId;
  const before = httpReads;
  const subscriptions = frames.filter(
    (frame) => frame.type === "subscribe",
  ).length;
  socket!.send(
    JSON.stringify({
      type: "query_result",
      requestId: id,
      result: { data: snapshot("First server push") },
    }),
  );
  socket!.send(
    JSON.stringify({
      type: "query_result",
      requestId: id,
      result: { data: snapshot("Unsolicited server update") },
    }),
  );
  await expect(
    page.getByText("Unsolicited server update", { exact: true }),
  ).toBeVisible();
  socket!.send(JSON.stringify({ type: "sync", id: 123 }));
  // Advancing all timeout/interval clocks must never request live data.
  await page.clock.install();
  await page.clock.fastForward(120_000);
  expect(frames.filter((frame) => frame.type === "subscribe")).toHaveLength(
    subscriptions,
  );
  expect(frames.filter((frame) => frame.type === "query")).toHaveLength(0);
  expect(httpReads).toBe(before);
  await page.evaluate(() => {
    location.hash = "#/projects";
  });
  await expect
    .poll(() =>
      frames.some(
        (frame) => frame.type === "unsubscribe" && frame.requestId === id,
      ),
    )
    .toBe(true);
  socket!.send(
    JSON.stringify({
      type: "query_result",
      requestId: id,
      result: { data: snapshot("Stale subscription update") },
    }),
  );
  await expect(
    page.getByText("Stale subscription update", { exact: true }),
  ).toHaveCount(0);
});
