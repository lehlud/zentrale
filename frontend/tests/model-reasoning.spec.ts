import { expect, test } from "@playwright/test";
import { config, gitConnections } from "./fixtures/visual-data";

test("model reasoning defaults can be changed, persisted and reloaded", async ({
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
  let saved = 0;
  await page.addInitScript(() => localStorage.setItem("zentrale-locale", "de"));
  await page.routeWebSocket("**/events?*", () => {});
  await page.route("**/graphql", async (route) => {
    const { query, variables = {} } = route.request().postDataJSON();
    const send = (data: unknown) => route.fulfill({ json: { data } });
    if (query.includes("status{"))
      return send({ status: { needsSetup: false, authenticated: true } });
    if (query.includes("setModelDefaults")) {
      saved++;
      expect(variables.input.main.reasoningLevel).toBe("high");
      expect(variables.input.approver.reasoningLevel).toBe("medium");
      state.modelDefaults = variables.input;
      return send({ setModelDefaults: true });
    }
    if (query.includes("model(providerId:"))
      return send({
        model: {
          id: "gpt-5.4",
          name: "GPT-5.4",
          reasoningLevels: ["low", "medium", "high", "xhigh"],
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
  ).toContainText("xhigh");
  await expect(
    approver.getByRole("button", { name: /Denkintensität/ }),
  ).toContainText("medium");
  await main.getByRole("button", { name: /Denkintensität/ }).click();
  await page.getByRole("option", { name: "high", exact: true }).click();
  await page.getByRole("button", { name: "Modell-Defaults speichern" }).click();
  await expect.poll(() => saved).toBe(1);
  await page.reload();
  await expect(
    main.getByRole("button", { name: /Denkintensität/ }),
  ).toContainText("high");
  await expect(
    approver.getByRole("button", { name: /Denkintensität/ }),
  ).toContainText("medium");
});
