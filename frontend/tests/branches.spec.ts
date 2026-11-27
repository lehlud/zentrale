import { expect, test } from "@playwright/test";
import { bootstrapData, isTranscriptQuery } from "./fixtures/bootstrap-data";
import { installPushFixture } from "./fixtures/push-fixture";
import {
  adminStatus,
  agent,
  config,
  project,
  transcript,
} from "./fixtures/visual-data";

for (const short of [false, true]) {
  test(`edits and branches without replacing the ${short ? "short chat" : "project"} original`, async ({
    page,
  }) => {
    await page.addInitScript(() =>
      localStorage.setItem("zentrale-locale", "de"),
    );
    const original = {
      ...agent,
      id: short ? "chat-original" : agent.id,
      projectId: short ? "" : project.id,
      status: "idle",
      branchParentId: "",
      branchMessageLineageId: "",
    };
    const variants = [original];
    const messages = new Map([[original.id, "Ursprüngliche Frage"]]);
    const received: unknown[] = [];

    const resolve = (query: string, variables: Record<string, string>) => {
      if (query.includes("status{")) return { status: adminStatus };
      if (query.includes("conversationBranches("))
        return { conversationBranches: variants };
      const current =
        variants.find(
          (variant) => variant.id === (variables.id ?? variables.agentId),
        ) ?? original;
      if (query.includes("shortChatModelOptions"))
        return { shortChatModelOptions: { presets: [], defaults: {} } };
      if (query.includes("shortChat(")) return { shortChat: current };
      if (isTranscriptQuery(query)) {
        const message = {
          ...transcript.messages[0],
          id: `user-${current.id}`,
          lineageId: "same-user-turn",
          agentId: current.id,
          role: "user",
          content: messages.get(current.id)!,
          attachments: [],
        };
        return bootstrapData(
          query,
          { ...transcript, agent: current, messages: [message] },
          { agents: variants },
        );
      }
      if (query.includes("agents(projectId:"))
        return { project, agents: variants };
      return config;
    };

    const pushes = await installPushFixture(page, resolve);
    await page.route("**/graphql", async (route) => {
      const { query, variables } = route.request().postDataJSON();
      if (query.includes("branchConversation(")) {
        received.push(variables);
        const branch = {
          ...original,
          id: `branch-${variants.length}`,
          branchParentId: variables.id,
          branchMessageLineageId: "same-user-turn",
        };
        variants.push(branch);
        messages.set(branch.id, variables.text ?? messages.get(variables.id));
        await route.fulfill({
          json: { data: { branchConversation: { id: branch.id } } },
        });
        await pushes.push();
        return;
      }
      await route.fulfill({ json: { data: resolve(query, variables ?? {}) } });
    });

    await page.goto(
      short
        ? `/#/chats/${original.id}`
        : `/#/projects/${project.id}?agent=${original.id}`,
    );
    await expect(
      page.getByText("Ursprüngliche Frage", { exact: true }),
    ).toBeVisible();
    await page.getByRole("button", { name: "Nachricht bearbeiten" }).click();
    const edit = page.getByRole("textbox", { name: "Nachricht bearbeiten" });
    await expect(edit).toBeFocused();
    await edit.fill("Bearbeitete Frage");
    await page.getByRole("button", { name: "Speichern und senden" }).click();
    await expect(
      page.getByText("Bearbeitete Frage", { exact: true }),
    ).toBeVisible();
    expect(received).toEqual([
      {
        id: original.id,
        message: `user-${original.id}`,
        text: "Bearbeitete Frage",
      },
    ]);

    await page.getByRole("button", { name: "Vorheriger Zweig" }).click();
    await expect(
      page.getByText("Ursprüngliche Frage", { exact: true }),
    ).toBeVisible();
    await page.getByRole("button", { name: "Nächster Zweig" }).click();
    await expect(
      page.getByText("Bearbeitete Frage", { exact: true }),
    ).toBeVisible();
    await page.getByRole("button", { name: "Hier abzweigen" }).click();
    await expect(page).toHaveURL(/branch-2$/);
    expect(received[1]).toEqual({
      id: "branch-1",
      message: "user-branch-1",
      text: null,
    });
    await expect(
      page.getByRole("group", { name: "Unterhaltungszweige" }),
    ).toContainText("3/3");
  });
}
