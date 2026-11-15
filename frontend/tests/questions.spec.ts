import { expect, test } from "@playwright/test";

import { bootstrapData, isTranscriptQuery } from "./fixtures/bootstrap-data";
import { installPushFixture } from "./fixtures/push-fixture";
import { agent, config, project, transcript } from "./fixtures/visual-data";

for (const mode of ["advisory", "blocking", undefined]) {
  test(`${mode ?? "legacy"} questions explain whether an answer is required`, async ({
    page,
  }) => {
    let open = true;
    let answerMessage: (typeof transcript.messages)[number] | undefined;
    const answers: unknown[] = [];
    const resolve = (query: string) => {
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
          messages: answerMessage
            ? [...transcript.messages, answerMessage]
            : transcript.messages,
          questions: open ? [{ ...transcript.questions[0], mode }] : [],
          jobs: [],
        });
      }
      return config;
    };
    const push = await installPushFixture(page, resolve);
    await page.route("**/graphql", async (route) => {
      const { query, variables } = route.request().postDataJSON();
      if (query.includes("answerQuestion(")) {
        answers.push(variables);
        open = false;
        answerMessage = {
          ...transcript.messages[0],
          id: "question_answer:q1",
          role: "user",
          content: JSON.stringify({
            kind: "user_question_answer",
            question: {
              ...transcript.questions[0],
              mode,
              answer: variables.answer,
              status: variables.status,
            },
          }),
        };
        await route.fulfill({ json: { data: { answerQuestion: true } } });
        await push.push();
        return;
      }
      await route.fulfill({ json: { data: resolve(query) } });
    });
    await page.goto(`/#/projects/${project.id}?agent=${agent.id}`);
    const question = page.getByRole("region", {
      name: transcript.questions[0].title,
      exact: true,
    });
    await expect(question).toContainText(
      mode === "advisory"
        ? "Unabhängige Arbeit läuft weiter. Du kannst später antworten."
        : "Für die nächsten Schritte ist eine Antwort erforderlich.",
    );
    await question.getByLabel("Eigene Antwort").fill("Schneller Testlauf");
    await question
      .getByRole("button", { name: "Antworten", exact: true })
      .click();
    await expect(question).toHaveCount(0);
    await page.getByRole("button", { name: /Deine Antwort/ }).click();
    await expect(
      page.getByRole("paragraph").filter({ hasText: /^Schneller Testlauf$/ }),
    ).toBeVisible();
    await expect(page.getByText(/"kind":"user_question_answer"/)).toHaveCount(
      0,
    );
    expect(answers).toEqual([
      {
        id: transcript.questions[0].id,
        answer: "Schneller Testlauf",
        status: "answered",
      },
    ]);
  });
}
