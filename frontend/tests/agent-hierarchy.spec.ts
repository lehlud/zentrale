import { expect, test } from "@playwright/test";
import {
  bootstrapData,
  isTranscriptQuery,
  selectedAgentId,
} from "./fixtures/bootstrap-data";

import { agent, config, project, transcript } from "./fixtures/visual-data";

for (const mobile of [false, true]) {
  test(`drawer keeps subagents under their conversation: ${mobile ? "mobile" : "desktop"}`, async ({
    page,
  }) => {
    await page.setViewportSize({ width: mobile ? 390 : 1440, height: 950 });
    const main = {
      ...agent,
      id: "main",
      name: "Main conversation",
      role: "main",
      parentId: "",
      status: "idle",
    };
    const child = {
      ...main,
      id: "child",
      name: "Research worker",
      role: "subagent",
      parentId: main.id,
    };
    const other = { ...main, id: "other", name: "Other conversation" };
    let records = [
      child,
      other,
      { ...child, id: "reviewer", name: "Auto-Approver", role: "approver" },
      main,
    ];
    await page.routeWebSocket("**/events?*", () => {});
    await page.route("**/graphql", async (route) => {
      const { query, variables = {} } = route.request().postDataJSON();
      const send = (data: unknown) =>
        route.fulfill({
          json: { data: bootstrapData(query, data, { agents: records }) },
        });
      if (query.includes("status{"))
        return send({ status: { authenticated: true, needsSetup: false } });
      if (query.includes("deleteResource")) {
        records = records.filter(
          (record) => record.id !== main.id && record.parentId !== main.id,
        );
        return send({ deleteResource: true });
      }
      if (isTranscriptQuery(query))
        return send({
          ...transcript,
          agent:
            records.find(
              (record) => record.id === selectedAgentId(variables),
            ) ?? child,
          messages: [],
          jobs: [],
          questions: [],
        });
      if (query.includes("agents(projectId:"))
        return send({ project, agents: records });
      return send(config);
    });
    await page.goto(`/#/projects/${project.id}?agent=${child.id}`);
    const menu = page.getByRole("button", { name: "Menü öffnen", exact: true });
    if (mobile) await menu.click();
    const navigation = page.getByRole("navigation", {
      name: "Projekte und Unterhaltungen",
    });
    const children = navigation.getByRole("list", {
      name: "Subagenten von Main conversation",
      exact: true,
    });
    await expect(
      children.getByRole("link", { name: child.name, exact: true }),
    ).toHaveAttribute("aria-current", "page");
    await expect(children.getByRole("link")).toHaveCount(1);
    await expect(
      navigation
        .getByRole("list", { name: "Unterhaltungen", exact: true })
        .locator(":scope > li"),
    ).toHaveCount(2);
    await expect(
      navigation.getByText("Auto-Approver", { exact: true }),
    ).toHaveCount(0);
    await expect(
      navigation.getByRole("link", { name: other.name, exact: true }),
    ).toBeVisible();
    await expect(
      page.locator('[data-slot="drawer-content"][data-entering="true"]'),
    ).toHaveCount(0);
    await page.screenshot({
      path: test.info().outputPath("agent-hierarchy.png"),
    });
    await navigation
      .getByRole("button", {
        name: "Aktionen für Main conversation",
        exact: true,
      })
      .click();
    await page
      .getByRole("menuitem", { name: "Entfernen", exact: true })
      .click();
    await page
      .getByRole("dialog", { name: "Entfernen bestätigen", exact: true })
      .getByRole("button", { name: "Entfernen", exact: true })
      .click();
    await expect(page).toHaveURL(new RegExp(`#/projects/${project.id}$`));
    if (mobile) await menu.click();
    await expect(
      navigation.getByRole("link", { name: child.name, exact: true }),
    ).toHaveCount(0);
    await expect(
      navigation.getByRole("link", { name: other.name, exact: true }),
    ).toBeVisible();
  });
}
