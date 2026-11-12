import { expect, test } from "@playwright/test";
import { bootstrapData, isTranscriptQuery } from "./fixtures/bootstrap-data";
import { installPushFixture } from "./fixtures/push-fixture";

import {
  agent,
  agents,
  config,
  project,
  transcript,
} from "./fixtures/visual-data";
import { chooseContextAction } from "./fixtures/context-actions";

for (const english of [false, true]) {
  test(`MicroVM GPU configuration preserves assignments: ${english ? "mobile-en" : "desktop-de"}`, async ({
    page,
  }) => {
    const label = (de: string, en: string) => (english ? en : de);
    await page.setViewportSize({ width: english ? 390 : 1440, height: 950 });
    await page.addInitScript((english) => {
      localStorage.setItem("zentrale-locale", english ? "en" : "de");
    }, english);
    const state = structuredClone(config);
    let saved: Record<string, unknown> | undefined;
    const push = await installPushFixture(page, (query) =>
      query.includes("status{")
        ? { status: { authenticated: true, needsSetup: false } }
        : state,
    );
    await page.route("**/graphql", async (route) => {
      const { query, variables = {} } = route.request().postDataJSON();
      if (query.includes("status{")) {
        return route.fulfill({
          json: {
            data: { status: { authenticated: true, needsSetup: false } },
          },
        });
      }

      if (query.includes("saveMachine")) {
        saved = variables.input;
        Object.assign(state.machines[0], variables.input);
        await push.push();

        return route.fulfill({
          json: { data: { saveMachine: { id: state.machines[0].id } } },
        });
      }

      return route.fulfill({ json: { data: state } });
    });
    await page.goto("/#/machines");
    const owner = page.locator('[data-slot="card"]').filter({
      has: page.getByRole("heading", {
        name: state.machines[0].name,
        exact: true,
      }),
    });
    await chooseContextAction(page, owner, label("Bearbeiten", "Edit"));
    const dialog = page.getByRole("dialog", {
      name: label("Maschine bearbeiten", "Edit machine"),
      exact: true,
    });
    const devices = dialog.getByLabel(
      label("GPU-PCI-Geräte", "GPU PCI devices"),
    );
    await devices.fill("0000:01:00.0, 0000:01:00.1");
    await dialog
      .getByRole("button", { name: label("Speichern", "Save"), exact: true })
      .click();

    await expect(dialog).not.toBeVisible();
    expect(saved).not.toHaveProperty("vmType");
    await expect(dialog.getByLabel(label("VM-Typ", "VM type"))).toHaveCount(0);
    expect(saved?.gpuDevices).toEqual(["0000:01:00.0", "0000:01:00.1"]);
    expect(saved).not.toHaveProperty("gpuDevicesText");

    await chooseContextAction(page, owner, label("Bearbeiten", "Edit"));

    await expect(devices).toHaveValue("0000:01:00.0, 0000:01:00.1");
  });

  test(`VM lifecycle does not require manual controls: ${english ? "en" : "de"}`, async ({
    page,
  }) => {
    const label = (de: string, en: string) => (english ? en : de);
    await page.addInitScript((english) => {
      localStorage.setItem("zentrale-locale", english ? "en" : "de");
    }, english);
    let status = "idle";
    await page.routeWebSocket("**/events?*", () => {});
    await page.route("**/graphql", async (route) => {
      const { query } = route.request().postDataJSON();
      const send = (data: unknown) =>
        route.fulfill({ json: { data: bootstrapData(query, data) } });
      if (query.includes("status{")) {
        return send({ status: { authenticated: true, needsSetup: false } });
      }

      if (isTranscriptQuery(query)) {
        return send({
          ...transcript,
          agent: { ...agent, status },
          messages: [],
          jobs: [],
          questions: [],
        });
      }

      if (query.includes("agents(projectId:")) {
        return send({
          project,
          agents: agents.map((entry) => ({ ...entry, status })),
        });
      }

      return send(config);
    });
    await page.goto(`/#/projects/${project.id}?agent=${agent.id}`);
    const actions = page
      .getByRole("group", { name: label("Agentensteuerung", "Agent controls") })
      .getByRole("button", {
        name: label("Projektaktionen", "Project actions"),
        exact: true,
      });
    await actions.click();
    await expect(
      page.getByRole("menuitem", {
        name: label("VM stoppen", "Stop VM"),
        exact: true,
      }),
    ).toHaveCount(0);

    status = "running";
    await page.reload();
    await actions.click();

    await expect(
      page.getByRole("menuitem", {
        name: label("VM stoppen", "Stop VM"),
        exact: true,
      }),
    ).toHaveCount(0);
  });
}
