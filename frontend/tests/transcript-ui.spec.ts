import { expect, test, type Page, type WebSocketRoute } from "@playwright/test";
import { installPushFixture } from "./fixtures/push-fixture";
import { bootstrapData, isTranscriptQuery } from "./fixtures/bootstrap-data";
import {
  agent,
  agents,
  config,
  project,
  transcript,
} from "./fixtures/visual-data";
import type { Goal, Message } from "../src/generated";

function message(
  id: string,
  role: string,
  content: string,
  minute: number,
): Message {
  return {
    ...transcript.messages[0],
    id,
    role,
    content,
    wire: "",
    callId: "",
    toolName: "",
    change: null,
    changes: [],
    activities: [],
    attachments: [],
    createdAt: new Date(Date.UTC(2026, 0, 1, 0, minute)).toISOString(),
  };
}
const completed: Goal = {
  id: "old-goal",
  agentId: agent.id,
  objective: "Ein fest verankertes abgeschlossenes Ziel",
  origin: "agent",
  status: "complete",
  requestedStatus: "",
  requestReason: "",
  sourceInputIds: [],
  revision: 2,
  continuations: 0,
  blockedAttempts: 0,
  blocker: "",
  reason: "Fertig",
  evidence: "Geprüft",
  createdAt: "2026-01-01T00:00:00Z",
  updatedAt: "2026-01-01T00:02:00Z",
};

async function fixture(
  page: Page,
  messages: Message[],
  goals: Goal[] = [],
  olderDelay = 0,
) {
  let socket: WebSocketRoute | undefined;
  const cursors: string[] = [];
  const resolve = async (
    query: string,
    variables: Record<string, any> = {},
  ) => {
    let data: unknown = config;
    if (query.includes("status{"))
      data = {
        status: {
          authenticated: true,
          needsSetup: false,
          githubConnected: false,
        },
      };
    else if (isTranscriptQuery(query)) {
      const history = [
        ...messages,
        ...goals.map((goal) => ({
          ...message(`goal:${goal.id}`, "goal", JSON.stringify(goal), 0),
          createdAt: goal.updatedAt,
        })),
      ];
      if (goals.length)
        history.sort(
          (left, right) =>
            left.createdAt.localeCompare(right.createdAt) ||
            left.id.localeCompare(right.id),
        );
      if (variables.beforeId) {
        if (olderDelay)
          await new Promise((resolve) => setTimeout(resolve, olderDelay));
        const before = history.findIndex(
          (item) => item.id === variables.beforeId,
        );
        data = {
          messages: history.slice(Math.max(0, before - 100), before).reverse(),
        };
      } else
        data = {
          ...transcript,
          agent: { ...agent, status: "idle" },
          messages: history.slice(-100).reverse(),
          recentMessages: [],
          goal: goals.at(-1) ?? null,
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
    if (isTranscriptQuery(query) && variables.beforeId)
      cursors.push(variables.beforeId);
    await route.fulfill({ json: { data: await resolve(query, variables) } });
  });
  await page.goto(`/#/projects/${project.id}?agent=${agent.id}`);
  return {
    cursors,
    refresh: async () => {
      await expect.poll(() => Boolean(socket)).toBe(true);
      await push.push();
    },
  };
}

test("completed goals stay between their historical messages after new output and reload", async ({
  page,
}) => {
  const messages = [
    message("before", "assistant", "Arbeit vor dem Abschluss", 1),
    message("after", "user", "Neue Arbeit nach dem Abschluss", 3),
  ];
  const state = await fixture(page, messages, [completed]);
  const timeline = page.getByRole("list", {
    name: "Nachrichten und Werkzeugaktivität",
  });
  await expect(
    timeline.getByRole("region", { name: "Zielstatus" }),
  ).toHaveCount(1);
  await expect(timeline.getByRole("region", { name: "Zielstatus" })).toHaveCSS(
    "padding",
    "4px",
  );
  const order = () =>
    timeline
      .locator("[data-message-id], [data-goal-id]")
      .evaluateAll((items) =>
        items.map(
          (item) =>
            item.getAttribute("data-message-id") ??
            item.getAttribute("data-goal-id"),
        ),
      );
  await expect.poll(order).toEqual(["before", "old-goal", "after"]);
  messages.push(message("later", "assistant", "Noch eine spätere Antwort", 4));
  await state.refresh();
  await expect.poll(order).toEqual(["before", "old-goal", "after", "later"]);
  await page.reload();
  await expect.poll(order).toEqual(["before", "old-goal", "after", "later"]);
  await timeline.getByRole("button", { name: /Arbeitsziel/ }).click();
  await expect(timeline.getByRole("region", { name: "Zielstatus" })).toHaveCSS(
    "padding",
    "4px",
  );
  expect(
    (await timeline.getByRole("button", { name: /Arbeitsziel/ }).boundingBox())!
      .height,
  ).toBeGreaterThanOrEqual(44);
  await expect(
    page.getByText(
      "Vom Agenten formuliert. Die ursprünglichen Nutzeranweisungen gelten weiterhin.",
    ),
  ).toHaveCount(0);
});

test("old completed goals are absent until their ordinary scrollback page arrives", async ({
  page,
}) => {
  const messages = Array.from({ length: 105 }, (_, index) =>
    message(
      `history-${index}`,
      "assistant",
      `History row ${index}\n\n` + "Content. ".repeat(30),
      index + 10,
    ),
  );
  const goals = Array.from({ length: 8 }, (_, index) => ({
    ...completed,
    id: `archived-${index}`,
    objective: `Archived objective ${index}`,
    updatedAt: new Date(Date.UTC(2026, 0, 1, 0, index + 1)).toISOString(),
  }));
  const state = await fixture(page, messages, goals, 1500);
  await expect(page.getByText(/History row 104/)).toBeVisible();
  await expect(page.locator("[data-goal-id]")).toHaveCount(0);
  await page.evaluate(() => scrollTo(0, 0));
  await expect.poll(() => state.cursors.length).toBe(1);
  // In-flight scrollback must not eagerly inject the full goal catalog.
  await expect(page.locator("[data-goal-id]")).toHaveCount(0);
  await expect
    .poll(() => page.locator("[data-transcript-item]").count())
    .toBe(113);
  await page.evaluate(() => scrollTo(0, 0));
  await expect(
    page.getByRole("button", { name: /Arbeitsziel Archived objective 0/ }),
  ).toBeVisible();
});

test("failed command output stays inside its disclosure and is not duplicated", async ({
  page,
}) => {
  const call = message("command-call", "assistant", "", 1);
  call.wire = JSON.stringify([
    {
      type: "function_call",
      call_id: "cmd-1",
      name: "exec_command",
      arguments: JSON.stringify({ cmd: "make test" }),
    },
  ]);
  const result = message(
    "command-result",
    "tool",
    JSON.stringify({
      error: "Process exited with status 1\nunique command stderr",
      result: {
        job: {
          status: "failed",
          output: "unique command stderr",
          error: "Process exited with status 1\nunique command stderr",
        },
      },
    }),
    2,
  );
  result.callId = "cmd-1";
  result.toolName = "exec_command";
  await fixture(page, [call, result]);
  const disclosure = page.getByRole("button", {
    name: /Befehl ausführen.*make test/,
  });
  await expect(disclosure).toBeVisible();
  await expect(
    page.getByText("unique command stderr", { exact: true }),
  ).toBeHidden();
  await disclosure.click();
  await expect(
    page.getByText("unique command stderr", { exact: true }),
  ).toHaveCount(1);
  await expect(
    page.getByText("unique command stderr", { exact: true }),
  ).toBeVisible();
});

test("goal creation describes its objective once and edit headers use relative paths", async ({
  page,
}) => {
  const call = message("goal-call", "assistant", "", 1);
  const objective = "Dieses Ziel nicht zweimal anzeigen";
  call.wire = JSON.stringify([
    {
      type: "function_call",
      call_id: "g",
      name: "create_goal",
      arguments: JSON.stringify({ objective }),
    },
  ]);
  const result = message(
    "goal-result",
    "tool",
    JSON.stringify({ result: { ...completed, objective, status: "active" } }),
    2,
  );
  result.callId = "g";
  result.toolName = "create_goal";
  const editCall = message("edit-call", "assistant", "", 3);
  editCall.wire = JSON.stringify([
    {
      type: "function_call",
      call_id: "p",
      name: "apply_patch",
      arguments: JSON.stringify({ input: "patch" }),
    },
  ]);
  const edit = message(
    "edit-result",
    "tool",
    JSON.stringify({ result: { files: [] } }),
    4,
  );
  edit.callId = "p";
  edit.toolName = "apply_patch";
  edit.changes = [
    {
      id: "edit",
      agentId: agent.id,
      messageId: edit.id,
      createdAt: edit.createdAt,
      path: `${project.directory}/frontend/tests/connection.spec.ts`,
      kind: "modified",
      patch: "@@ -1 +1 @@\n-old\n+new\n",
      added: 1,
      removed: 1,
      truncated: false,
      notice: "",
    },
  ];
  await fixture(page, [call, result, editCall, edit]);
  await page.getByRole("button", { name: /Ziel setzen/ }).click();
  await expect(page.getByText(objective, { exact: true })).toHaveCount(1);
  await page.getByRole("button", { name: /Dateien ändern/ }).click();
  await expect(
    page.getByText("frontend/tests/connection.spec.ts", { exact: true }),
  ).toBeVisible();
  await expect(
    page.getByText(`${project.directory}/frontend/tests/connection.spec.ts`, {
      exact: true,
    }),
  ).toHaveCount(0);
});

test("scrollback uses stable cursors and long transcripts keep expensive rendered rows bounded", async ({
  page,
}) => {
  const messages = Array.from({ length: 350 }, (_, index) =>
    message(
      `m-${index}`,
      index % 2 ? "assistant" : "user",
      `Nachricht ${index}\n\n` + "Längerer Markdown-Inhalt. ".repeat(20),
      index,
    ),
  );
  // The cursor follows server insertion order even if a delayed write carries
  // an earlier display timestamp. Sorting the visible messages must not skip pages.
  messages[300].createdAt = messages[0].createdAt;
  const state = await fixture(page, messages);
  await expect(page.getByText(/Nachricht 349/)).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Mehr anzeigen", exact: true }),
  ).toHaveCount(0);
  await expect
    .poll(() => page.locator("[data-transcript-item]").count())
    .toBe(100);
  expect(await page.locator('[data-rendered="true"]').count()).toBeLessThan(50);
  for (let pageIndex = 0; pageIndex < 3; pageIndex++) {
    await page.evaluate(() => scrollTo(0, 0));
    await expect.poll(() => state.cursors.length).toBe(pageIndex + 1);
    await expect
      .poll(() => page.locator("[data-transcript-item]").count())
      .toBe(Math.min(350, 200 + pageIndex * 100));
  }
  expect(state.cursors).toEqual(["m-250", "m-150", "m-50"]);
  await page.evaluate(() => scrollTo(0, 0));
  await expect(page.getByText(/^Nachricht 0\n/)).toBeVisible();
  expect(await page.locator('[data-rendered="true"]').count()).toBeLessThan(50);
  await page.evaluate(() => scrollTo(0, document.documentElement.scrollHeight));
  await expect(page.getByText(/Nachricht 349/)).toBeVisible();
  expect(await page.locator('[data-rendered="true"]').count()).toBeLessThan(50);
});
