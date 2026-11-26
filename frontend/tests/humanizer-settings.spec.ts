import { expect, test } from "@playwright/test";
import type { HumanizerSettingsInput } from "../src/generated";
import { installPushFixture } from "./fixtures/push-fixture";
import { adminStatus, config, models } from "./fixtures/visual-data";

test("switches between catalog, hosted GPU, and ZeroGPU Humanizers", async ({
  page,
}) => {
  page.setDefaultTimeout(10000);
  await page.addInitScript(() => localStorage.setItem("zentrale-locale", "de"));
  const featherless = {
    ...config.providers[0],
    id: "featherless",
    kind: "featherless",
    name: "Featherless",
    connected: true,
    modelCount: 1,
  };
  const smaug = {
    ...models[0],
    id: "abacusai/Smaug-Humanizer",
    name: "Smaug Humanizer",
    providerId: featherless.id,
  };
  const fal = {
    ...featherless,
    id: "writer-fal",
    kind: "fal",
    name: "fal.ai",
    modelCount: 0,
  };
  const empty = {
    model: { providerId: "", modelId: "", reasoningLevel: "" },
    spaceUrl: "",
    spaceEndpoint: "",
    useHuggingFaceAccount: false,
  };
  let saved: HumanizerSettingsInput = {};
  const resolve = (query: string, variables: Record<string, unknown> = {}) => {
    if (query.includes("setHumanizer")) {
      saved = variables.input as HumanizerSettingsInput;
      return { setHumanizer: true };
    }
    if (query.includes("humanizerSettings")) {
      return {
        humanizerSettings: { ...empty, ...saved },
        providers: [featherless, fal, ...config.providers],
      };
    }
    if (query.includes("models(providerId:")) {
      return {
        models: variables.providerId === featherless.id ? [smaug] : models,
      };
    }
    return {
      ...config,
      status: adminStatus,
    };
  };
  const push = await installPushFixture(page, resolve);
  await page.route("**/graphql", async (route) => {
    const { query, variables } = route.request().postDataJSON();
    const data = resolve(query, variables);
    if (query.includes("setHumanizer")) await push.push();
    await route.fulfill({ json: { data } });
  });
  await page.goto("/#/admin-settings");
  const section = page.getByRole("region", { name: "Humanizer" });
  await expect(section.getByText("Ausgeschaltet")).toBeVisible();
  await section
    .getByRole("button", { name: "Katalogmodell auswählen" })
    .click();
  let dialog = page.getByRole("dialog");
  await dialog.getByRole("option", { name: /Smaug Humanizer/ }).click();
  await dialog.getByRole("button", { name: "Auswahl übernehmen" }).click();
  await expect(
    section.getByText("Featherless · abacusai/Smaug-Humanizer"),
  ).toBeVisible();
  expect(saved.model?.providerId).toBe("featherless");

  await section
    .getByRole("button", { name: "GPU-Humanizer einrichten" })
    .click();
  dialog = page.getByRole("dialog");
  await dialog.getByLabel("Modell- oder Deployment-ID").fill("team/writer");
  await dialog.getByRole("button", { name: "Speichern", exact: true }).click();
  await expect(
    section.getByText("fal.ai · jialinyyzz/humanizer"),
  ).toBeVisible();
  expect(saved).toEqual({
    hosted: {
      providerId: "writer-fal",
      modelId: "jialinyyzz/humanizer",
      target: "team/writer",
    },
  });

  await section
    .getByRole("button", { name: "ZeroGPU-Space einrichten" })
    .click();
  dialog = page.getByRole("dialog");
  await dialog.getByLabel("Space-Adresse").fill("https://team-writer.hf.space");
  await dialog.getByLabel("Gradio-Endpunkt").fill("rewrite");
  const connectedAccount = dialog.getByRole("checkbox", {
    name: /ZeroGPU-Kontingent/,
  });
  await connectedAccount.press("Space");
  await expect(connectedAccount).toBeChecked();
  await dialog.getByRole("checkbox", { name: /Textliste/ }).press("Space");
  await dialog.getByRole("button", { name: "Speichern", exact: true }).click();
  await expect(
    section.getByText("https://team-writer.hf.space/rewrite"),
  ).toBeVisible();
  expect(saved).toEqual({
    spaceUrl: "https://team-writer.hf.space",
    spaceEndpoint: "rewrite",
    useHuggingFaceAccount: true,
    spaceBatch: true,
  });

  await section
    .getByRole("button", { name: "Ausschalten", exact: true })
    .click();
  await expect(section.getByText("Ausgeschaltet")).toBeVisible();
  expect(saved).toEqual({});
});
