import { expect, test, type Page, type WebSocketRoute } from "@playwright/test";
import {
  conversationBootstrapQuery,
  bootstrapTranscript,
  type ConversationBootstrap,
} from "../src/conversation-bootstrap";
import {
  agent,
  agents,
  config,
  project,
  transcript,
} from "./fixtures/visual-data";

test("bootstrap selection contains core attachments but excludes detail blobs", () => {
  expect(conversationBootstrapQuery).toContain(
    "conversationBootstrap(projectId:$projectId, agentId:$agentId)",
  );
  expect(conversationBootstrapQuery).toContain(
    "attachments { id name mimeType size path machineId }",
  );
  expect(conversationBootstrapQuery).toContain(
    "questions { id title options status answer agentId }",
  );
  expect(conversationBootstrapQuery).not.toMatch(
    /\b(approvals|notes|usageSummary|providerKinds)\b/,
  );
  expect(conversationBootstrapQuery).not.toMatch(/jobs\s*\{[^}]*\boutput\b/);
  const core = {
    ...transcript,
    jobs: [],
    project,
    agents,
    modelDefaults: config.modelDefaults,
    machineName: "",
    sandboxMode: "",
  } as unknown as ConversationBootstrap;
  const first = bootstrapTranscript(core)!;
  expect(first.messages).toBe(core.messages);
  expect(first.jobs).toEqual([]);
  expect(first.approvals).toEqual([]);
  first.usageSummary.inputTokens = 99;
  expect(bootstrapTranscript(core)!.usageSummary.inputTokens).toBe(0);
  expect(bootstrapTranscript({ ...core, agent: null })).toBeUndefined();
});

async function bootstrapAPI(page: Page, messageCount = 1) {
  const calls: { query: string; variables: Record<string, string> }[] = [];
  let socket: WebSocketRoute | undefined;
  const subscriptions = new Map<
    string,
    { query: string; variables: Record<string, string> }
  >();
  let revision = 1;
  let authenticated = true;
  let gate: Promise<void> | undefined;
  let heldAgent: string | undefined;
  let release = () => {};
  const child = {
    ...agent,
    id: "bootstrap-child",
    name: "Bootstrap child",
    parentId: agent.id,
    role: "child",
  };
  await page.routeWebSocket("**/events?*", (connection) => {
    socket = connection;
    connection.onMessage((frame) => {
      const request = JSON.parse(String(frame));
      if (request.type === "unsubscribe")
        subscriptions.delete(request.requestId);
      if (request.type === "subscribe") {
        subscriptions.set(request.requestId, request);
        void push(request.requestId, request);
      }
    });
  });
  const resolveData = async (
    query: string,
    variables: Record<string, string> = {},
  ) => {
    let data: unknown = config;
    if (/\bstatus\s*\{/.test(query)) {
      data = {
        status: { authenticated, needsSetup: false, githubConnected: false },
      };
    } else if (query.includes("login(")) {
      authenticated = true;
      data = { login: true };
    } else if (query.includes("conversationBootstrap(")) {
      const selected = variables.agentId === child.id ? child : agent;
      const content = `Bootstrap ${selected.id} revision ${revision}`;
      if (heldAgent === (variables.agentId || agent.id)) await gate;
      data = {
        conversationBootstrap: {
          project,
          agents: [...agents, child],
          agent: selected,
          messages: Array.from({ length: messageCount }, (_, index) => ({
            ...transcript.messages[0],
            id: `${selected.id}-message-${messageCount - index}`,
            agentId: selected.id,
            content: messageCount === 1 ? content : `${content} item ${index}`,
          })),
          goal: null,
          questions: [],
          agentInputs: [],
          jobs: [],
          agentRunState: {
            agentId: selected.id,
            phase: "idle",
            error: "",
            hostKey: { host: "", fingerprint: "", publicKey: "" },
          },
          modelDefaults: config.modelDefaults,
          machineName: "Worker",
          sandboxMode: "auto",
        },
      };
    } else if (query.includes("recentMessages(")) {
      data = {
        ...transcript,
        messages: variables.beforeId
          ? [
              {
                ...transcript.messages[0],
                id: "older-bootstrap-message",
                agentId: variables.id,
                createdAt: "2000-01-01T00:00:00Z",
                content: "Older bootstrap page retained after refresh",
              },
            ]
          : [],
      };
    } else if (query.includes("agents(projectId:")) {
      data = { project, agents: [...agents, child] };
    }
    return data;
  };
  const push = async (
    requestId: string,
    request: { query: string; variables: Record<string, string> },
  ) => {
    const data = await resolveData(request.query, request.variables);
    if (subscriptions.get(requestId) === request)
      socket?.send(
        JSON.stringify({ type: "query_result", requestId, result: { data } }),
      );
  };
  await page.route("**/graphql", async (route) => {
    const { query, variables = {} } = route.request().postDataJSON();
    calls.push({ query, variables });
    const data = await resolveData(query, variables);
    try {
      await route.fulfill({ json: { data } });
    } catch (error) {
      // A deliberately held old-route request is cancelled by useQuery cleanup.
      if (!route.request().failure() && !page.isClosed()) throw error;
    }
  });
  return {
    calls,
    child,
    hold(id: string) {
      heldAgent = id;
      gate = new Promise<void>((resolve) => {
        release = resolve;
      });
    },
    release() {
      heldAgent = undefined;
      release();
    },
    refresh() {
      revision++;
      for (const [id, request] of subscriptions) void push(id, request);
    },
    logout() {
      authenticated = false;
      revision++;
      for (const [id, request] of subscriptions) void push(id, request);
    },
  };
}

for (const explicit of [false, true]) {
  test(`authenticated ${explicit ? "explicit" : "default"} navigation paints after exactly one conversation RTT`, async ({
    page,
  }) => {
    const api = await bootstrapAPI(page);
    await page.goto("/#/projects");
    await expect(
      page.getByRole("heading", { name: "Projekte", exact: true }),
    ).toBeVisible();
    api.calls.length = 0;
    api.hold(agent.id);
    await page.evaluate(
      (hash) => {
        location.hash = hash;
      },
      `#/projects/${project.id}${explicit ? `?agent=${agent.id}` : ""}`,
    );
    await expect
      .poll(
        () =>
          api.calls.filter((call) =>
            call.query.includes("conversationBootstrap("),
          ).length,
      )
      .toBe(1);
    expect(
      api.calls.filter((call) =>
        /\b(agent\(id:|recentMessages\(|goal\(agentId:)/.test(call.query),
      ).length,
    ).toBe(0);
    expect(
      api.calls.find((call) => call.query.includes("conversationBootstrap("))!
        .variables,
    ).toEqual({ projectId: project.id, agentId: explicit ? agent.id : "" });
    api.release();
    await expect(
      page.getByText(`Bootstrap ${agent.id} revision 1`, { exact: true }),
    ).toBeVisible();
    expect(
      api.calls.filter((call) => call.query.includes("conversationBootstrap("))
        .length,
    ).toBe(1);
    expect(
      api.calls.filter((call) =>
        /\b(agent\(id:|recentMessages\(|goal\(agentId:)/.test(call.query),
      ).length,
    ).toBe(0);
    expect(
      api.calls.filter((call) =>
        /\b(jobs|approvals|usageSummary)\(/.test(call.query),
      ),
    ).toEqual([]);
  });
}

test("deep link and websocket refresh keep the selected child without a second core query", async ({
  page,
}) => {
  const api = await bootstrapAPI(page);
  await page.goto(`/#/projects/${project.id}?agent=${api.child.id}`);
  await expect(
    page.getByText(`Bootstrap ${api.child.id} revision 1`, { exact: true }),
  ).toBeVisible();
  expect(
    api.calls.filter((call) => call.query.includes("conversationBootstrap("))
      .length,
  ).toBe(1);
  api.refresh();
  await expect(
    page.getByText(`Bootstrap ${api.child.id} revision 2`, { exact: true }),
  ).toBeVisible();
  expect(
    api.calls
      .filter((call) => call.query.includes("conversationBootstrap("))
      .map((call) => call.variables.agentId),
  ).toEqual([api.child.id]);
  expect(
    api.calls.filter((call) =>
      /\b(agent\(id:|recentMessages\(|goal\(agentId:)/.test(call.query),
    ).length,
  ).toBe(0);
});

test("a delayed old route cannot overwrite the new conversation", async ({
  page,
}) => {
  const api = await bootstrapAPI(page);
  api.hold(agent.id);
  await page.goto(`/#/projects/${project.id}?agent=${agent.id}`);
  await expect
    .poll(
      () =>
        api.calls.filter((call) =>
          call.query.includes("conversationBootstrap("),
        ).length,
    )
    .toBe(1);
  await page.evaluate((hash) => {
    location.hash = hash;
  }, `#/projects/${project.id}?agent=${api.child.id}`);
  await expect(
    page.getByText(`Bootstrap ${api.child.id} revision 1`, { exact: true }),
  ).toBeVisible();
  api.release();
  await expect(
    page.getByText(`Bootstrap ${agent.id} revision 1`, { exact: true }),
  ).toHaveCount(0);
  api.refresh();
  await expect(
    page.getByText(`Bootstrap ${api.child.id} revision 2`, { exact: true }),
  ).toBeVisible();
});

test("logout removes the conversation instead of exposing a retained bootstrap", async ({
  page,
}) => {
  const api = await bootstrapAPI(page);
  await page.goto(`/#/projects/${project.id}?agent=${agent.id}`);
  await expect(
    page.getByText(`Bootstrap ${agent.id} revision 1`, { exact: true }),
  ).toBeVisible();
  api.logout();
  await expect(page.getByLabel("Passwort", { exact: true })).toBeVisible();
  await expect(
    page.getByText(`Bootstrap ${agent.id} revision 1`, { exact: true }),
  ).toHaveCount(0);
  await page
    .getByLabel("Passwort", { exact: true })
    .fill("test login password");
  await page.getByRole("button", { name: "Anmelden", exact: true }).click();
  await expect(
    page.getByText(`Bootstrap ${agent.id} revision 2`, { exact: true }),
  ).toBeVisible();
  await expect(
    page.getByText(`Bootstrap ${agent.id} revision 1`, { exact: true }),
  ).toHaveCount(0);
});

test("bootstrap history paginates with the existing cursor and retains older pages on refresh", async ({
  page,
}) => {
  const api = await bootstrapAPI(page, 100);
  await page.goto(`/#/projects/${project.id}?agent=${agent.id}`);
  await expect(page.locator("[data-transcript-item]")).toHaveCount(100);
  await page.evaluate(() => window.scrollTo(0, 0));
  await expect
    .poll(() => api.calls.filter((call) => call.variables.beforeId).length)
    .toBe(1);
  const request = api.calls.find((call) => call.variables.beforeId)!;
  expect(request.variables).toEqual({
    id: agent.id,
    beforeId: `${agent.id}-message-1`,
  });
  await expect(page.locator("[data-transcript-item]")).toHaveCount(101);
  api.refresh();
  // Virtualized rows outside the viewport are intentionally not in the DOM.
  await page.evaluate(() => window.scrollTo(0, 0));
  await expect(
    page.getByText("Older bootstrap page retained after refresh", {
      exact: true,
    }),
  ).toHaveCount(1);
  await expect
    .poll(
      () =>
        api.calls.filter((call) =>
          call.query.includes("conversationBootstrap("),
        ).length,
    )
    .toBe(1);
  expect(api.calls.filter((call) => call.variables.beforeId)).toHaveLength(1);
});
