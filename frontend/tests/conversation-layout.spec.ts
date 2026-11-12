import { expect, test } from "@playwright/test";
import {
  agent,
  agents,
  config,
  project,
  transcript,
} from "./fixtures/visual-data";
import { bootstrapData, isTranscriptQuery } from "./fixtures/bootstrap-data";

for (const childView of [false, true]) {
  test(`conversation layout: ${childView ? "short child footer stays at viewport bottom" : "wide main composer and active goal outside history"}`, async ({
    page,
  }) => {
    await page.setViewportSize({ width: 2200, height: 1000 });
    await page.addInitScript(() =>
      localStorage.setItem("zentrale-locale", "de"),
    );
    const selected = childView
      ? { ...agent, id: "layout-child", role: "subagent", parentId: agent.id }
      : agent;
    const goal = {
      id: "layout-goal",
      agentId: selected.id,
      objective: "Aktives Layoutziel außerhalb des Arbeitslogs",
      origin: "agent",
      status: "active",
      requestedStatus: "",
      requestReason: "",
      sourceInputIds: [],
      revision: 1,
      continuations: 0,
      blockedAttempts: 0,
      blocker: "",
      reason: "",
      evidence: "",
      createdAt: "2026-11-11T12:00:00Z",
      updatedAt: "2026-11-11T12:00:00Z",
    };
    await page.routeWebSocket("**/events?*", () => {});
    await page.route("**/graphql", async (route) => {
      const { query } = route.request().postDataJSON();
      const data = /\bstatus\s*\{/.test(query)
        ? {
            status: {
              authenticated: true,
              needsSetup: false,
              githubConnected: false,
            },
          }
        : isTranscriptQuery(query)
          ? bootstrapData(
              query,
              {
                ...transcript,
                agent: selected,
                goal: childView ? null : goal,
                messages: [
                  {
                    ...transcript.messages[0],
                    id: "layout-message",
                    agentId: selected.id,
                    role: "assistant",
                    content: "Kurzer Verlauf",
                    wire: "",
                  },
                ],
                jobs: [],
                questions: [],
              },
              { agents: [...agents, selected] },
            )
          : { ...config, project, agents: [...agents, selected] };
      await route.fulfill({ json: { data } });
    });
    await page.goto(`/#/projects/${project.id}?agent=${selected.id}`);
    await expect(
      page.getByText("Kurzer Verlauf", { exact: true }),
    ).toBeVisible();
    if (childView) {
      const footer = page
        .locator("footer")
        .filter({ hasText: "Dieser Verlauf ist schreibgeschützt." });
      await expect(footer).toBeVisible();
      const box = (await footer.boundingBox())!;
      expect(Math.abs(box.y + box.height - 1000)).toBeLessThanOrEqual(2);
      await page.setViewportSize({ width: 390, height: 844 });
      await expect
        .poll(async () => {
          const current = (await footer.boundingBox())!;
          return Math.abs(current.y + current.height - 844);
        })
        .toBeLessThanOrEqual(2);
      expect(
        await page.evaluate(() => document.documentElement.scrollWidth),
      ).toBeLessThanOrEqual(390);
    } else {
      const composer = page.getByRole("textbox", {
        name: "Aufgabe oder neue Instruktion",
      });
      await expect(composer).toBeVisible();
      expect((await composer.boundingBox())!.width).toBeGreaterThan(1200);
      const controls = page.getByRole("group", { name: "Agentensteuerung" });
      await expect(
        controls.getByRole("region", { name: "Zielstatus" }),
      ).toHaveCSS("padding", "4px");
      await expect(
        controls.getByRole("button", { name: /Arbeitsziel/ }),
      ).toContainText(goal.objective);
      await expect(
        page
          .locator("[data-transcript-item]")
          .filter({ hasText: goal.objective }),
      ).toHaveCount(0);
    }
  });
}
