import { expect, test } from "@playwright/test";
import {
  agent,
  agents,
  config,
  gitConnections,
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
    if (query.includes("status{"))
      return { status: { authenticated: true, needsSetup: false } };
    if (query.includes("conversationModels("))
      return { conversationModels: choices, providers };
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

test("account-discovered auto-review can be selected and replaced by a generic approver", async ({
  page,
}) => {
  const state = {
    ...structuredClone(config),
    modelDefaults: {
      main: {
        providerId: "openai",
        modelId: "gpt-5.4",
        reasoningLevel: "xhigh",
      },
      approver: {
        providerId: "openai",
        modelId: "gpt-5.4",
        reasoningLevel: "medium",
      },
    },
  };
  const models = ["gpt-5.4", "codex-auto-review"].map((id) => ({
    ...catalogModels[0],
    id,
    name: id,
    providerId: "openai",
    reasoningLevels: ["low", "medium", "high"],
  }));
  const saved: unknown[] = [];
  const resolve = async (
    query: string,
    variables: Record<string, any> = {},
  ) => {
    if (query.includes("status{"))
      return { status: { authenticated: true, needsSetup: false } };
    if (query.includes("setModelDefaults")) {
      state.modelDefaults = variables.input;
      saved.push(structuredClone(variables.input));
      return { setModelDefaults: true };
    }
    if (query.includes("models(providerId:")) return { models };
    if (query.includes("model(providerId:"))
      return { model: models.find((model) => model.id === variables.modelId) };
    if (query.includes("gitConnections")) return { gitConnections };
    return state;
  };
  const push = await installPushFixture(page, resolve);
  await page.route("**/graphql", async (route) => {
    const { query, variables } = route.request().postDataJSON();
    const data = await resolve(query, variables);
    await route.fulfill({ json: { data } });
    if (query.trimStart().startsWith("mutation")) await push.push();
  });
  await page.goto("/#/settings");
  const approver = page.getByRole("group", {
    name: "Auto-Approver",
    exact: true,
  });
  for (const modelId of ["codex-auto-review", "gpt-5.4"]) {
    await approver.getByRole("button", { name: "Ändern", exact: true }).click();
    const dialog = page.getByRole("dialog", {
      name: "Auto-Approver auswählen",
    });
    await expect(dialog.getByRole("option")).toHaveCount(2);
    await dialog.getByRole("option").filter({ hasText: modelId }).click();
    await expect(
      dialog.getByRole("button", { name: /Denkintensität/ }),
    ).toContainText("medium");
    await dialog
      .getByRole("button", { name: "Auswahl übernehmen", exact: true })
      .click();
    await page
      .getByRole("button", { name: "Modell-Defaults speichern" })
      .click();
    await expect.poll(() => state.modelDefaults.approver.modelId).toBe(modelId);
    await page.reload();
    await expect(approver.getByText(modelId, { exact: true })).toBeVisible();
    await expect(
      approver.getByRole("button", { name: /Denkintensität/ }),
    ).toContainText("medium");
  }
  expect(saved).toHaveLength(2);
});

for (const { known, explicit } of [true, false].flatMap((known) =>
  [true, false].map((explicit) => ({ known, explicit })),
)) {
  test(`model reasoning defaults can be changed, persisted and reloaded (${known ? "known" : "unknown"} capabilities, ${explicit ? "explicit" : "automatic"})`, async ({
    page,
  }) => {
    const state = {
      ...structuredClone(config),
      modelDefaults: {
        main: {
          providerId: "openai",
          modelId: "gpt-5.4",
          reasoningLevel: explicit ? "xhigh" : "",
        },
        approver: {
          providerId: "openai",
          modelId: "gpt-5.4",
          reasoningLevel: explicit ? "medium" : "",
        },
      },
    };
    let saved = 0;
    await page.addInitScript(() =>
      localStorage.setItem("zentrale-locale", "de"),
    );
    await page.routeWebSocket("**/events?*", () => {});
    await page.route("**/graphql", async (route) => {
      const { query, variables = {} } = route.request().postDataJSON();
      const send = (data: unknown) => route.fulfill({ json: { data } });
      if (query.includes("status{"))
        return send({ status: { needsSetup: false, authenticated: true } });
      if (query.includes("setModelDefaults")) {
        saved++;
        expect(variables.input.main.reasoningLevel).toBe("high");
        expect(variables.input.approver.reasoningLevel).toBe("low");
        state.modelDefaults = variables.input;
        return send({ setModelDefaults: true });
      }
      if (query.includes("model(providerId:"))
        return send({
          model: {
            id: "gpt-5.4",
            name: "GPT-5.4",
            reasoningLevels: known ? ["low", "medium", "high", "xhigh"] : [],
          },
        });
      if (query.includes("gitConnections")) return send({ gitConnections });
      return send(state);
    });
    await page.goto("/#/settings");
    const main = page.getByRole("group", { name: "Hauptmodell", exact: true });
    const approver = page.getByRole("group", {
      name: "Auto-Approver",
      exact: true,
    });
    await expect(
      main.getByRole("button", { name: /Denkintensität/ }),
    ).toContainText(known || explicit ? "xhigh" : "Provider-Standard");
    await expect(
      approver.getByRole("button", { name: /Denkintensität/ }),
    ).toContainText(known || explicit ? "medium" : "Provider-Standard");
    await main.getByRole("button", { name: /Denkintensität/ }).click();
    await page.getByRole("option", { name: "high", exact: true }).click();
    // The closing popover remains mounted during its exit animation.
    await expect(page.getByRole("listbox")).toHaveCount(0);
    await approver.getByRole("button", { name: /Denkintensität/ }).click();
    await page.getByRole("option", { name: "low", exact: true }).click();
    await page
      .getByRole("button", { name: "Modell-Defaults speichern" })
      .click();
    await expect.poll(() => saved).toBe(1);
    await page.reload();
    await expect(
      main.getByRole("button", { name: /Denkintensität/ }),
    ).toContainText("high");
    await expect(
      approver.getByRole("button", { name: /Denkintensität/ }),
    ).toContainText("low");
  });
}
