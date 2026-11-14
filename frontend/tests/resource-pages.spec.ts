import { expect, test } from "@playwright/test";

import { installPushFixture } from "./fixtures/push-fixture";
import { config, gitConnections, project } from "./fixtures/visual-data";

for (const collection of [
  {
    route: "machines",
    field: "machines",
    empty: "Keine Fernmaschinen",
    name: config.machines[0].name,
  },
  {
    route: "keys",
    field: "sshKeys",
    empty: "Keine SSH-Schlüssel",
    name: config.sshKeys[0].fingerprint,
  },
  {
    route: "providers",
    field: "providers",
    empty: "Keine Provider konfiguriert",
    name: config.providers[0].name,
  },
  {
    route: "projects",
    field: "projects",
    empty: "Noch keine Projekte",
    name: project.name,
  },
] as const) {
  test(`${collection.route}: initial failures, explicit retry and stale pushed collections`, async ({
    page,
  }) => {
    const state = structuredClone(config);
    let failing = true;
    const read = (query: string) => {
      if (query.includes("status{"))
        return { status: { needsSetup: false, authenticated: true } };
      if (query.includes("models(")) return { models: [] };
      if (query.includes("trustedKeys") && failing)
        throw new Error("Collection fixture unavailable");
      return state;
    };
    const push = await installPushFixture(page, read);
    await page.route("**/graphql", async (route) => {
      try {
        await route.fulfill({
          json: { data: read(route.request().postDataJSON().query) },
        });
      } catch (error) {
        await route.fulfill({
          json: { errors: [{ message: (error as Error).message }] },
        });
      }
    });
    await page.addInitScript(() =>
      localStorage.setItem("zentrale-locale", "de"),
    );
    await page.goto(`/#/${collection.route}`);
    const main = page.getByRole("main");
    const entry =
      collection.route === "providers"
        ? main.getByRole("heading", { name: collection.name, exact: true })
        : main.getByText(collection.name, { exact: true }).first();
    const failure = main
      .getByRole("alert")
      .filter({ hasText: "Collection fixture unavailable" });
    await expect(failure).toBeVisible();
    await expect(main.getByText(collection.empty, { exact: true })).toHaveCount(
      0,
    );

    failing = false;
    await failure
      .getByRole("button", { name: "Erneut versuchen", exact: true })
      .click();
    await expect(failure).toHaveCount(0);
    await expect(entry).toBeVisible();

    failing = true;
    await push.push();
    await expect(failure).toBeVisible();
    await expect(entry).toBeVisible();
    await expect(main.getByText(collection.empty, { exact: true })).toHaveCount(
      0,
    );

    failing = false;
    state[collection.field] = [];
    await push.push();
    await expect(failure).toHaveCount(0);
    await expect(
      main.getByText(collection.empty, { exact: true }),
    ).toBeVisible();
  });
}

test("project collection batches reset with the search and distinguish no matches from no projects", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1440, height: 600 });
  await page.addInitScript(() => localStorage.setItem("zentrale-locale", "de"));
  const state = {
    ...config,
    projects: Array.from({ length: 30 }, (_, index) => ({
      ...project,
      id: `collection-${index}`,
      name: `Collection project ${String(index).padStart(2, "0")}`,
    })),
  };
  const read = (query: string) =>
    query.includes("status{")
      ? { status: { needsSetup: false, authenticated: true } }
      : state;
  const push = await installPushFixture(page, read);
  await page.route("**/graphql", (route) =>
    route.fulfill({
      json: { data: read(route.request().postDataJSON().query) },
    }),
  );
  await page.goto("/#/projects");
  const main = page.getByRole("main");
  const links = main.getByRole("link", { name: /^Collection project / });
  await expect(links).toHaveCount(12);
  await links.last().scrollIntoViewIfNeeded();
  await expect(links).toHaveCount(24);

  const search = main.getByRole("searchbox", { name: "Projekte suchen" });
  await search.fill("Collection project 00");
  await expect(links).toHaveCount(1);
  await search.fill("");
  await expect(links).toHaveCount(12);
  await search.fill("no match");
  await expect(
    main.getByText("Keine passenden Projekte", { exact: true }),
  ).toBeVisible();
  await expect(
    main.getByText("Noch keine Projekte", { exact: true }),
  ).toHaveCount(0);
  await search.fill("");
  state.projects = [];
  await push.push();
  await expect(
    main.getByText("Noch keine Projekte", { exact: true }),
  ).toBeVisible();
});

for (const kind of ["keys", "providers"] as const) {
  test(`${kind}: expanded batches survive tab and responsive layout unmounts`, async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1440, height: 600 });
    await page.addInitScript(() =>
      localStorage.setItem("zentrale-locale", "de"),
    );
    const state = {
      ...config,
      sshKeys: Array.from({ length: 30 }, (_, index) => ({
        ...config.sshKeys[0],
        id: index + 100,
        fingerprint: `SHA256:collection-${index}`,
      })),
      providers: Array.from({ length: 30 }, (_, index) => ({
        ...config.providers[0],
        id: `collection-${index}`,
        name: `Collection provider ${index}`,
      })),
    };
    const read = (query: string) => {
      if (query.includes("status{"))
        return { status: { needsSetup: false, authenticated: true } };
      if (query.includes("models(")) return { models: [] };
      return state;
    };
    await installPushFixture(page, read);
    await page.route("**/graphql", (route) =>
      route.fulfill({
        json: { data: read(route.request().postDataJSON().query) },
      }),
    );
    await page.goto(`/#/${kind}`);
    const main = page.getByRole("main");
    const items =
      kind === "keys"
        ? main.getByRole("button", {
            name: "Öffentlichen Schlüssel kopieren",
            exact: true,
          })
        : main.locator("[data-provider-account]");
    await expect(items).toHaveCount(12);
    await items.last().scrollIntoViewIfNeeded();
    await expect(items).toHaveCount(24);
    if (kind === "keys") {
      await main
        .getByRole("tab", { name: "Vertrauenswürdige Hosts", exact: true })
        .click();
      await main
        .getByRole("tab", { name: "Eigene Schlüssel", exact: true })
        .click();
    } else {
      await page.setViewportSize({ width: 900, height: 600 });
      await expect(items).toHaveCount(1);
      await page.setViewportSize({ width: 1440, height: 600 });
    }
    await expect(items).toHaveCount(24);
  });
}

test("Git connections retain their unpaginated grid", async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem("zentrale-locale", "de"));
  const connections = Array.from({ length: 30 }, (_, index) => ({
    ...gitConnections[0],
    id: `github-${index}`,
  }));
  const read = (query: string) => {
    if (query.includes("status{"))
      return { status: { needsSetup: false, authenticated: true } };
    if (query.includes("gitConnections"))
      return { gitConnections: connections };
    if (query.includes("models(")) return { models: [] };
    return config;
  };
  await installPushFixture(page, read);
  await page.route("**/graphql", (route) =>
    route.fulfill({
      json: { data: read(route.request().postDataJSON().query) },
    }),
  );
  await page.goto("/#/settings");
  await expect(
    page
      .getByRole("region", { name: "Git-Verbindungen", exact: true })
      .getByRole("heading", { name: "GitHub", exact: true }),
  ).toHaveCount(30);
});
