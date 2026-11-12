import { expect, test } from "@playwright/test";
import { bootstrapData, isTranscriptQuery } from "./fixtures/bootstrap-data";
import { mkdir } from "node:fs/promises";
import { agents, config, project, transcript } from "./fixtures/visual-data";

for (const english of [false, true]) {
  test(`private editor preview: ${english ? "mobile-en" : "desktop-de"}`, async ({
    page,
    context,
  }) => {
    const label = (de: string, en: string) => (english ? en : de);
    const errors: string[] = [];
    page.on("pageerror", (error) => errors.push(error.message));
    await page.setViewportSize({ width: english ? 390 : 1440, height: 1000 });
    await page.addInitScript((english) => {
      localStorage.setItem("zentrale-locale", english ? "en" : "de");
      localStorage.setItem("zentrale-theme", english ? "dark" : "light");
    }, english);
    await page.routeWebSocket("**/events?*", () => {});
    await page.route("**/graphql", async (route) => {
      const { query } = route.request().postDataJSON();
      const data = query.includes("status{")
        ? { status: { needsSetup: false, authenticated: true } }
        : query.includes("editorSession(")
          ? {
              editorSession: {
                id: "preview-session",
                projectId: project.id,
                agentId: "",
                machineId: "training",
                machineName: "GPU-Worker",
                sandbox: "microvm",
                url: "/ide/forecast/preview-session/",
              },
            }
          : isTranscriptQuery(query)
            ? transcript
            : query.includes("agents(projectId:")
              ? { project, agents }
              : config;
      return route.fulfill({ json: { data: bootstrapData(query, data) } });
    });
    await context.route("**/preview/**", (route) =>
      route.fulfill({
        contentType: "text/html",
        body: "<h1>Preview fixture</h1>",
      }),
    );
    await page.goto("/#/projects/forecast?view=editor");
    const port = page.getByRole("textbox", {
      name: label("Dev-Server-Port", "Dev server port"),
    });
    const preview = page.getByRole("link", {
      name: label("Vorschau öffnen ↗", "Open preview ↗"),
    });
    await expect(port).toHaveValue("1234");
    await expect(preview).toHaveAttribute(
      "href",
      "/preview/forecast/preview-session/1234/",
    );
    await port.fill("9842");
    await expect(preview).toHaveAttribute("aria-disabled", "true");
    await port.fill("65536");
    await expect(preview).toHaveAttribute("aria-disabled", "true");
    await port.fill("1235");
    await expect(preview).not.toHaveAttribute("aria-disabled", "true");
    const opened = context.waitForEvent("page");
    await preview.click();
    const tab = await opened;
    await expect(
      tab.getByRole("heading", { name: "Preview fixture" }),
    ).toBeVisible();
    expect(new URL(tab.url()).pathname).toBe(
      "/preview/forecast/preview-session/1235/",
    );
    expect(await tab.evaluate(() => window.opener === null)).toBe(true);
    await tab.close();
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth,
      ),
    ).toBe(true);
    expect(errors).toEqual([]);
    await mkdir("artifacts/ui", { recursive: true });
    await page.screenshot({
      path: `artifacts/ui/preview-${english ? "mobile-en" : "desktop-de"}.png`,
      animations: "disabled",
    });
  });
}
