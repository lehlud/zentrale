import { expect, test } from "@playwright/test";
import { adminStatus, agent, config, project } from "./fixtures/visual-data";

for (const mobile of [false, true]) {
  test(`sidebar icon tabs show separate panels and expand projects in place (${mobile ? "mobile" : "desktop"})`, async ({
    page,
  }, testInfo) => {
    await page.setViewportSize({ width: mobile ? 390 : 1280, height: 900 });
    await page.addInitScript(() =>
      localStorage.setItem("zentrale-locale", "de"),
    );
    await page.routeWebSocket("**/events?*", () => {});
    await page.route("**/graphql", (route) => {
      const { query, variables } = route.request().postDataJSON();
      let data: unknown = config;

      if (query.includes("status{")) data = { status: adminStatus };
      if (query.includes("shortChats(")) {
        data = {
          shortChats: variables.pinned
            ? []
            : [{ id: "previous-chat", name: "Frühere Unterhaltung" }],
        };
      }
      if (query.includes("agents(projectId:"))
        data = { project, agents: [agent] };

      return route.fulfill({ json: { data } });
    });

    await page.goto("/#/chats");
    if (mobile) {
      await page
        .getByRole("button", { name: "Menü öffnen", exact: true })
        .click();
      await expect(
        page.locator('[data-slot="drawer-content"][data-entering="true"]'),
      ).toHaveCount(0);
    }

    const rail = page.getByRole("tablist", { name: "Bereiche", exact: true });
    const chats = rail.getByRole("tab", {
      name: "Unterhaltungen",
      exact: true,
    });
    const projects = rail.getByRole("tab", { name: "Projekte", exact: true });
    const history = page.getByRole("tabpanel", {
      name: "Unterhaltungen",
      exact: true,
    });
    const projectsPanel = page.getByRole("tabpanel", {
      name: "Projekte",
      exact: true,
    });
    await expect(rail.getByRole("tab")).toHaveCount(2);
    await expect(chats).toHaveText("");
    await expect(projects).toHaveText("");
    await expect(chats).toHaveAttribute("aria-selected", "true");
    await expect(
      history.getByRole("link", { name: "Frühere Unterhaltung" }),
    ).toBeVisible();
    await expect(projectsPanel).not.toBeVisible();
    expect((await rail.boundingBox())!.x).toBeLessThan(
      (await history.boundingBox())!.x,
    );

    // ARIA visibility alone also passes for icons clipped by the narrow rail.
    const railBounds = (await rail.boundingBox())!;
    expect(railBounds.height).toBeLessThan(120);
    const compose = (await page
      .getByRole("link", { name: "Neue Unterhaltung", exact: true })
      .boundingBox())!;

    for (const tab of [chats, projects]) {
      const icon = (await tab.locator("svg").boundingBox())!;
      const target = (await tab.boundingBox())!;

      expect(target.width).toBeGreaterThanOrEqual(44);
      expect(target.height).toBeGreaterThanOrEqual(44);
      expect(target.x).toBeGreaterThanOrEqual(railBounds.x);
      expect(target.x + target.width).toBeLessThanOrEqual(
        railBounds.x + railBounds.width,
      );
      expect(
        Math.abs(icon.x + icon.width / 2 - (target.x + target.width / 2)),
      ).toBeLessThan(1);
      expect(
        Math.abs(icon.x + icon.width / 2 - (compose.x + compose.width / 2)),
      ).toBeLessThan(1);
      expect(
        Math.abs(icon.y + icon.height / 2 - (target.y + target.height / 2)),
      ).toBeLessThan(1);
    }

    await chats.focus();
    await page.keyboard.press("ArrowDown");
    await expect(projects).toBeFocused();
    await expect(projectsPanel).toBeVisible();
    await expect(history).not.toBeVisible();
    await expect(page).toHaveURL(/#\/chats$/);

    const expand = projectsPanel.getByRole("button", {
      name: `Unterhaltungen in ${project.name}`,
    });
    const conversation = projectsPanel.getByRole("link", {
      name: agent.name,
      exact: true,
    });
    await expect(expand).toHaveAttribute("aria-expanded", "false");
    await expand.click();
    await expect(conversation).toBeVisible();
    await expect(page).toHaveURL(/#\/chats$/);
    await expand.click();
    await expect(conversation).not.toBeVisible();
    await expand.click();

    const settings = page.getByRole("button", {
      name: "Einstellungen öffnen",
      exact: true,
    });
    const admin = page.getByRole("button", { name: "Admin", exact: true });
    await settings.click();
    await expect(
      page.getByRole("menuitem", { name: "Persönliche Einstellungen" }),
    ).toBeVisible();
    await page.keyboard.press("Escape");
    await admin.click();
    await expect(
      page.getByRole("menuitem", { name: "Admin-Einstellungen" }),
    ).toBeVisible();
    await expect(
      page.getByRole("menuitem", { name: "Verbrauch", exact: true }),
    ).toBeVisible();
    await page.keyboard.press("Escape");
    await expect(projects).toHaveAttribute("aria-selected", "true");

    await page.screenshot({
      path: testInfo.outputPath("sidebar-layers.png"),
      animations: "disabled",
    });
    await conversation.click();
    await expect(page).toHaveURL(
      new RegExp(`#/projects/${project.id}\\?agent=${agent.id}`),
    );
    if (mobile)
      await expect(
        page.getByRole("dialog", { name: "Hauptnavigation" }),
      ).toHaveCount(0);

    // The rail shortcut needs to focus the composer even in an existing draft.
    for (const _ of [0, 1]) {
      if (mobile)
        await page
          .getByRole("button", { name: "Menü öffnen", exact: true })
          .click();
      await page
        .getByRole("link", { name: "Neue Unterhaltung", exact: true })
        .click();
      const input = page.getByRole("textbox", {
        name: "Nachricht",
        exact: true,
      });
      await expect(input).toBeFocused();
      await expect(input).toHaveCSS("outline-style", "none");
      await expect(input).toHaveCSS("border-top-width", "0px");
      await expect(input).toHaveCSS("box-shadow", "none");
    }
  });
}
