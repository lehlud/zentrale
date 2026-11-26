import { expect, test } from "@playwright/test";
import {
  adminStatus,
  agent,
  agents,
  config,
  models as catalogModels,
  project,
  transcript,
} from "./fixtures/visual-data";
import { installPushFixture } from "./fixtures/push-fixture";
import { bootstrapData, isTranscriptQuery } from "./fixtures/bootstrap-data";

test("the conversation menu changes both models and their reasoning while the agent runs", async ({
  page,
}) => {
  const choices = {
    main: {
      providerId: "openai",
      modelId: "original",
      reasoningLevel: "xhigh",
    },
    approver: {
      providerId: "openai",
      modelId: "original",
      reasoningLevel: "medium",
    },
  };
  const providers = [
    { id: "openai", name: "OpenAI", connected: true, modelCount: 2 },
    { id: "other", name: "Weiterer Provider", connected: true, modelCount: 1 },
  ];
  const models = ["original", "new-reviewer", "new-main"].map((id) => ({
    ...catalogModels[0],
    id,
    name: id,
    providerId: id === "new-main" ? "other" : "openai",
    reasoningLevels: ["low", "medium", "high", "xhigh"],
  }));
  const submitted: unknown[] = [];
  const resolve = (query: string, variables: Record<string, any> = {}) => {
    if (query.includes("status{")) return { status: adminStatus };
    if (query.includes("conversationModels("))
      return { conversationModels: choices, modelProviders: providers };
    if (query.includes("setConversationModel(")) {
      expect(variables.agentId).toBe(agent.id);
      const role = variables.role as "main" | "approver";
      choices[role] = variables.choice;
      submitted.push({ role, ...variables.choice });
      return { setConversationModel: true };
    }
    if (query.includes("models(providerId:"))
      return {
        models: models.filter(
          (model) => model.providerId === variables.providerId,
        ),
      };
    if (query.includes("model(providerId:"))
      return {
        model: models.find(
          (model) =>
            model.providerId === variables.providerId &&
            model.id === variables.modelId,
        ),
      };
    if (isTranscriptQuery(query))
      return bootstrapData(query, {
        ...transcript,
        agent: { ...agent, status: "running" },
      });
    if (query.includes("agents(projectId:")) return { project, agents };
    return config;
  };
  const push = await installPushFixture(page, resolve);
  await page.route("**/graphql", async (route) => {
    const { query, variables } = route.request().postDataJSON();
    await route.fulfill({ json: { data: resolve(query, variables) } });
    if (query.includes("setConversationModel(")) await push.push();
  });
  await page.goto(`/#/projects/${project.id}?agent=${agent.id}`);
  const open = async () => {
    await page
      .getByRole("button", { name: "Projektaktionen", exact: true })
      .click();
    await page
      .getByRole("menuitem", { name: "Modelle ändern", exact: true })
      .click();
  };
  await open();
  const dialog = page.getByRole("dialog", { name: "Modelle der Unterhaltung" });
  await dialog.getByRole("button", { name: /Provider/ }).click();
  await page
    .getByRole("option", { name: "Weiterer Provider", exact: true })
    .click();
  await dialog.getByRole("option", { name: /new-main/ }).click();
  await dialog.getByRole("button", { name: /Denkintensität/ }).click();
  await page.getByRole("option", { name: "high", exact: true }).click();
  await dialog
    .getByRole("button", { name: "Auswahl übernehmen", exact: true })
    .click();
  await expect.poll(() => submitted.length).toBe(1);
  await dialog.getByRole("tab", { name: "Auto-Approver", exact: true }).click();
  await dialog.getByRole("option", { name: /new-reviewer/ }).click();
  await dialog.getByRole("button", { name: /Denkintensität/ }).click();
  await page.getByRole("option", { name: "low", exact: true }).click();
  await dialog
    .getByRole("button", { name: "Auswahl übernehmen", exact: true })
    .click();
  await expect.poll(() => submitted.length).toBe(2);
  expect(submitted).toEqual([
    {
      role: "main",
      providerId: "other",
      modelId: "new-main",
      reasoningLevel: "high",
    },
    {
      role: "approver",
      providerId: "openai",
      modelId: "new-reviewer",
      reasoningLevel: "low",
    },
  ]);
  await dialog.getByRole("button", { name: "Schließen", exact: true }).click();
  await expect(page.getByText("Arbeitet …", { exact: true })).toBeVisible();
  await open();
  await expect(
    dialog.getByRole("button", { name: /Denkintensität/ }),
  ).toContainText("high");
  await dialog.getByRole("tab", { name: "Auto-Approver", exact: true }).click();
  await expect(
    dialog.getByRole("button", { name: /Denkintensität/ }),
  ).toContainText("low");
});

for (const known of [true, false]) {
  test(`project model presets persist reasoning defaults and allow switching reviewers (${known ? "known" : "unknown"} capabilities)`, async ({
    page,
  }) => {
    const choice = (modelId: string, reasoningLevel: string) => ({
      providerId: "openai",
      modelId,
      reasoningLevel,
    });
    const mainChoice = choice("gpt-5.4", known ? "high" : "");
    const reviewChoice = choice("codex-auto-review", known ? "low" : "");
    const empty = { providerId: "", modelId: "", reasoningLevel: "" };
    const unconfigured = {
      configured: false,
      presets: [],
      defaults: { main: empty, approver: empty },
    };
    let projectPolicy = unconfigured;
    const models = [mainChoice, reviewChoice].map(({ modelId }) => ({
      ...catalogModels[0],
      id: modelId,
      name: modelId,
      providerId: "openai",
      reasoningLevels: known ? ["low", "medium", "high", "xhigh"] : [],
    }));
    let saved = 0;
    await page.addInitScript(() =>
      localStorage.setItem("zentrale-locale", "de"),
    );
    await page.routeWebSocket("**/events?*", () => {});
    await page.route("**/graphql", async (route) => {
      const { query, variables = {} } = route.request().postDataJSON();
      const send = (data: unknown) => route.fulfill({ json: { data } });
      if (query.includes("status{")) return send({ status: adminStatus });
      if (query.includes("setModelPresetPolicy")) {
        expect(variables.scope).toBe("project");
        expect(variables.defaults).toEqual({
          main: mainChoice,
          approver: saved === 0 ? reviewChoice : mainChoice,
        });
        expect(variables.presets).toEqual([mainChoice, reviewChoice]);
        projectPolicy = {
          configured: true,
          presets: variables.presets,
          defaults: variables.defaults,
        };
        saved++;
        return send({ setModelPresetPolicy: true });
      }
      if (query.includes("modelPresetPolicy("))
        return send({
          modelPresetPolicy:
            variables.scope === "project" ? projectPolicy : unconfigured,
          modelProviders: config.providers,
        });
      if (query.includes("models(providerId:")) return send({ models });
      if (query.includes("model(providerId:"))
        return send({
          model: models.find((model) => model.id === variables.modelId),
        });
      return send(config);
    });
    await page.goto("/#/admin-settings");
    const projectSettings = page.locator("div.rounded-xl").filter({
      has: page.getByRole("heading", { name: "Projekte", exact: true }),
    });
    const release = projectSettings.getByRole("button", {
      name: "Modell freigeben",
    });
    const dialog = page.getByRole("dialog", { name: "Modell freigeben" });
    const thinking = dialog.getByRole("button", { name: /Denkintensität/ });

    for (const { modelId, reasoningLevel } of [mainChoice, reviewChoice]) {
      await release.click();
      await dialog.getByRole("option").filter({ hasText: modelId }).click();
      await expect(thinking).toContainText(
        known ? "xhigh" : "Provider-Standard",
      );
      if (known) {
        await thinking.click();
        await page
          .getByRole("option", { name: reasoningLevel, exact: true })
          .click();
      }
      await dialog.getByRole("button", { name: "Auswahl übernehmen" }).click();
      await expect(dialog).toHaveCount(0);
    }

    const main = projectSettings.getByRole("combobox", {
      name: "Standardmodell",
    });
    const approver = projectSettings.getByRole("combobox", {
      name: "Auto-Approver",
    });
    const value = ({
      providerId,
      modelId,
      reasoningLevel,
    }: typeof mainChoice) =>
      JSON.stringify([providerId, modelId, reasoningLevel]);
    const save = projectSettings.getByRole("button", { name: "Speichern" });
    await main.selectOption(value(mainChoice));

    for (const [index, choice] of [reviewChoice, mainChoice].entries()) {
      await approver.selectOption(value(choice));
      await save.click();
      await expect.poll(() => saved).toBe(index + 1);
      await page.reload();
      await expect(main).toHaveValue(value(mainChoice));
      await expect(approver).toHaveValue(value(choice));
    }
  });
}
