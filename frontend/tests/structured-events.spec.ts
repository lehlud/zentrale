import { expect, test } from "@playwright/test";
import {
  bootstrapData,
  isTranscriptQuery,
  selectedAgentId,
} from "./fixtures/bootstrap-data";
import {
  agent,
  agents,
  config,
  project,
  transcript,
} from "./fixtures/visual-data";

for (const mobile of [false, true]) {
  test(`question exchanges and GitHub responses stay compact (${mobile ? "mobile" : "desktop"})`, async ({
    page,
  }, testInfo) => {
    await page.setViewportSize({ width: mobile ? 390 : 1440, height: 950 });
    await page.addInitScript(() =>
      localStorage.setItem("zentrale-locale", "de"),
    );
    const child = agents[2];
    const question = {
      id: "q",
      title: "Welche Tests sollen laufen?",
      options: ["Unit-Tests", "Integrationstests"],
      status: "open",
      answer: "",
    };
    const message = (
      id: string,
      role: string,
      content: string,
      extra = {},
    ) => ({
      ...transcript.messages[0],
      id,
      role,
      content,
      wire: "",
      callId: "",
      toolName: "",
      changes: [],
      activities: [],
      change: null,
      ...extra,
    });
    const questionEvent = message(
      "question-event",
      "event",
      JSON.stringify({
        kind: "subagent_question",
        agentId: child.id,
        question,
        notice: "MODEL_ROUTING_NOTICE_NOT_FOR_NORMAL_UI",
      }),
    );
    const answerEvent = message(
      "answer-event",
      "event",
      JSON.stringify({
        kind: "parent_question_answer",
        parentId: agent.id,
        question: {
          ...question,
          status: "answered",
          answer: "Bitte **beide** Tests ausführen.",
        },
        notice: "MODEL_ROUTING_NOTICE_NOT_FOR_NORMAL_UI",
      }),
    );
    const call = message("gh-call", "assistant", "", {
      wire: JSON.stringify([
        {
          type: "function_call",
          call_id: "gh",
          name: "harness",
          arguments: JSON.stringify({
            action: "github",
            arguments: JSON.stringify({
              method: "GET",
              path: "/repos/test/project/actions/runs",
            }),
            network: true,
          }),
        },
      ]),
    });
    const result = message(
      "gh-result",
      "tool",
      JSON.stringify({
        result: {
          status: 200,
          body: JSON.stringify({
            total_count: 1,
            workflow_runs: [
              {
                id: 123,
                name: "Tests",
                display_title: "Terminal integration tests",
                head_branch: "fix/terminal-origin-configuration",
                head_sha: "a76b01ea21e8fc11",
                status: "completed",
                conclusion: "success",
                html_url: "https://github.com/test/project/actions/runs/123",
                repository: { owner: { node_id: "REPEATED_GITHUB_METADATA" } },
                head_repository: {
                  owner: { node_id: "REPEATED_GITHUB_METADATA" },
                },
              },
            ],
          }),
        },
      }),
      { callId: "gh", toolName: "harness" },
    );
    await page.routeWebSocket("**/events?*", () => {});
    await page.route("**/graphql", (route) => {
      const { query, variables = {} } = route.request().postDataJSON();
      const isChild = selectedAgentId(variables) === child.id;
      const data = query.includes("status{")
        ? { status: { needsSetup: false, authenticated: true } }
        : isTranscriptQuery(query)
          ? {
              ...transcript,
              agent: isChild ? child : agent,
              messages: isChild ? [answerEvent] : [questionEvent, call, result],
              recentMessages: [],
              questions: [],
              agentInputs: [],
              jobs: [],
              goal: null,
            }
          : query.includes("agents(projectId:")
            ? { project, agents }
            : config;
      return route.fulfill({ json: { data: bootstrapData(query, data) } });
    });
    await page.goto(`/#/projects/${project.id}?agent=${agent.id}`);
    const questionTrigger = page.getByRole("button", {
      name: /Rückfrage des Subagenten/,
    });
    await expect(questionTrigger).toHaveAttribute("aria-expanded", "false");
    await questionTrigger.click();
    await expect(
      page.getByText("Integrationstests", { exact: true }),
    ).toBeVisible();
    await expect(
      page.getByText("MODEL_ROUTING_NOTICE_NOT_FOR_NORMAL_UI"),
    ).toHaveCount(0);
    await questionTrigger.click();
    await page.getByRole("button", { name: /GitHub-Anfrage/ }).click();
    const github = page.getByLabel("GitHub-Ergebnis");
    await expect(
      github.getByRole("link", { name: "Terminal integration tests" }),
    ).toHaveAttribute(
      "href",
      "https://github.com/test/project/actions/runs/123",
    );
    await expect(github).toContainText("HTTP 200");
    await expect(github).toContainText("a76b01ea");
    await expect(github.getByText(/REPEATED_GITHUB_METADATA/)).toHaveCount(0);
    await github.getByText("Vollständige API-Antwort", { exact: true }).click();
    await expect(github.locator("pre")).toContainText(
      "REPEATED_GITHUB_METADATA",
    );
    await github.getByText("Vollständige API-Antwort", { exact: true }).click();
    await expect(github.locator("pre")).toHaveCount(0);
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth,
      ),
    ).toBe(true);
    await page.screenshot({ path: testInfo.outputPath("compact-github.png") });
    await page.goto(`/#/projects/${project.id}?agent=${child.id}`);
    const answerTrigger = page.getByRole("button", {
      name: /Antwort des Hauptagenten/,
    });
    await expect(answerTrigger).toHaveAttribute("aria-expanded", "false");
    await answerTrigger.click();
    await expect(page.getByText("Bitte beide Tests ausführen.")).toBeVisible();
    await expect(
      page.getByText("MODEL_ROUTING_NOTICE_NOT_FOR_NORMAL_UI"),
    ).toHaveCount(0);
    await expect(page.locator("textarea")).toHaveCount(0);
  });
}
