import { expect, test } from "@playwright/test";
import { mkdir, writeFile } from "node:fs/promises";
import {
  agent,
  agents,
  config,
  project,
  transcript,
} from "./fixtures/visual-data";

for (const variant of [
  {
    name: "desktop-de-light",
    width: 1440,
    height: 900,
    locale: "de",
    theme: "light",
  },
  {
    name: "mobile-en-dark",
    width: 390,
    height: 844,
    locale: "en",
    theme: "dark",
  },
]) {
  test(`resolved automatic sandbox on machines and agents: ${variant.name}`, async ({
    page,
  }) => {
    await page.setViewportSize(variant);
    await page.addInitScript(({ locale, theme }) => {
      localStorage.setItem("zentrale-locale", locale);
      localStorage.setItem("zentrale-theme", theme);
    }, variant);
    await page.routeWebSocket("**/events?*", () => {});
    let selection: string | undefined;
    const observation = () => ({
      machineId: config.machines[0].id,
      mode: "auto",
      level: selection === "error" ? "" : (selection ?? ""),
      checkedAt: "2026-11-04T10:00:00Z",
      error:
        selection === "error"
          ? "Sandbox probe unavailable. Test the connection again."
          : "",
    });
    await page.route("**/graphql", async (route) => {
      const { query } = route.request().postDataJSON();
      if (query.includes("testMachine")) {
        selection = "bwrap";
        return route.fulfill({
          json: {
            data: {
              testMachine: {
                connected: true,
                error: "",
                hostKey: { host: "", fingerprint: "", publicKey: "" },
                sandbox: observation(),
              },
            },
          },
        });
      }
      const data = query.includes("status{")
        ? {
            status: {
              needsSetup: false,
              authenticated: true,
              githubConnected: false,
            },
          }
        : query.includes("recentMessages")
          ? { ...transcript, jobs: [], questions: [] }
          : query.includes("agents(projectId:")
            ? { project, agents }
            : { ...config, sandboxStatuses: selection ? [observation()] : [] };
      return route.fulfill({ json: { data } });
    });
    const errors: string[] = [];
    page.on("pageerror", (error) => errors.push(error.message));
    const english = variant.locale === "en";
    const automatic = english ? "Automatic" : "Automatisch";
    const badge = (label: string) =>
      page.getByText(`Sandbox: ${automatic}${label}`, { exact: true });
    await page.goto("/#/machines");
    await expect(
      badge(english ? " · not checked yet" : " · noch nicht ermittelt"),
    ).toBeVisible();
    await page
      .getByRole("button", {
        name: english ? "Test connection" : "Verbindung prüfen",
        exact: true,
      })
      .click();
    await expect(badge(" → Bubblewrap")).toBeVisible();
    await expect(
      page.getByText(english ? /Last checked:/ : /Zuletzt ermittelt:/),
    ).toBeVisible();
    await page.reload();
    await expect(badge(" → Bubblewrap")).toBeVisible();

    const captures: { name: string; file: string }[] = [];
    await mkdir("artifacts/ui", { recursive: true });
    for (const level of ["bwrap", "microvm", "none", "error"]) {
      selection = level;
      const label =
        level === "error"
          ? english
            ? " · detection failed"
            : " · Ermittlung fehlgeschlagen"
          : ` → ${level === "bwrap" ? "Bubblewrap" : level === "microvm" ? "MicroVM" : english ? "No sandbox" : "Ohne Sandbox"}`;
      for (const view of ["machines", "agent"]) {
        await page.goto(
          view === "machines"
            ? "/#/machines"
            : `/#/projects/${project.id}?agent=${agent.id}`,
        );
        await expect(badge(label)).toBeVisible();
        if (view === "machines" && level === "error")
          await expect(page.getByRole("main").getByRole("alert")).toContainText(
            "Sandbox probe unavailable",
          );
        expect(
          await page.evaluate(
            () => document.documentElement.scrollWidth - innerWidth,
          ),
        ).toBeLessThanOrEqual(1);
        await page.evaluate(() => document.fonts.ready);
        const file = `sandbox-${view}-${level}-${variant.name}.png`;
        await page.screenshot({ path: `artifacts/ui/${file}` });
        captures.push({ name: `sandbox-${view}-${level}`, file });
      }
    }
    // A successful retry replaces the previous detection error immediately,
    // including before any background configuration refresh has completed.
    await page.goto("/#/machines");
    await expect(page.getByRole("main").getByRole("alert")).toBeVisible();
    await page
      .getByRole("button", {
        name: english ? "Test connection" : "Verbindung prüfen",
        exact: true,
      })
      .click();
    await expect(badge(" → Bubblewrap")).toBeVisible();
    await expect(page.getByRole("main").getByRole("alert")).toHaveCount(0);
    await writeFile(
      `artifacts/ui/sandbox-${variant.name}.json`,
      JSON.stringify({ captures }),
    );
    expect(errors).toEqual([]);
  });
}
