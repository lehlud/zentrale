import { expect, test } from "@playwright/test";
import {
  agent,
  agents,
  config,
  project,
  transcript,
} from "./fixtures/visual-data";

test("users can pause/resume subagents but cannot prompt them or answer their questions", async ({
  page,
}) => {
  const child = { ...agents[2], status: "stopped" };
  const actions: string[] = [];
  await page.routeWebSocket("**/events?*", () => {});
  await page.route("**/graphql", async (route) => {
    const { query, variables = {} } = route.request().postDataJSON();
    let data: unknown = config;
    if (query.includes("status{"))
      data = { status: { authenticated: true, needsSetup: false } };
    else if (query.includes("resumeInputs(")) {
      actions.push("resume");
      child.status = "running";
      data = { resumeInputs: true };
    } else if (query.includes("stopAgent(")) {
      actions.push("pause");
      child.status = "stopped";
      data = { stopAgent: true };
    } else if (query.includes("recentMessages")) {
      const isChild = variables.id === child.id;
      data = {
        ...transcript,
        agent: isChild ? child : agent,
        messages: [
          {
            ...transcript.messages[0],
            id: "note",
            role: "assistant",
            content: "Status report",
            wire: "",
          },
        ],
        recentMessages: [],
        goal: null,
        agentInputs: [],
        questions: isChild
          ? [
              {
                id: "child-question",
                agentId: child.id,
                title: "Which implementation?",
                options: ["A", "B"],
                status: "open",
                answer: "",
              },
            ]
          : [],
        jobs: [],
      };
    } else if (query.includes("agents(projectId:"))
      data = { project, agents: [agent, agents[1], child] };
    await route.fulfill({ json: { data } });
  });
  await page.goto(`/#/projects/${project.id}?agent=${child.id}`);
  await expect(
    page.getByText("Dieser Verlauf ist schreibgeschützt."),
  ).toBeVisible();
  await expect(page.locator("textarea")).toHaveCount(0);
  await expect(page.getByText("Which implementation?")).toHaveCount(0);
  await page
    .getByRole("button", { name: "Agent fortsetzen", exact: true })
    .click();
  await expect.poll(() => actions).toEqual(["resume"]);
  await page
    .getByRole("button", { name: "Agent stoppen", exact: true })
    .click();
  await expect.poll(() => actions).toEqual(["resume", "pause"]);
  await expect(
    page.getByRole("button", { name: "Agent fortsetzen", exact: true }),
  ).toBeVisible();
  await expect(page.locator("textarea")).toHaveCount(0);
  await page.goto(`/#/projects/${project.id}?agent=${agent.id}`);
  await expect(page.locator("textarea")).toHaveCount(1);
});
