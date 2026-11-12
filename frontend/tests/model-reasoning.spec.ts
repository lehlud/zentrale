import { expect, test } from "@playwright/test";
import {
  config,
  gitConnections,
  models as catalogModels,
} from "./fixtures/visual-data";
import { installPushFixture } from "./fixtures/push-fixture";

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
