import { expect, test, type WebSocketRoute } from "@playwright/test";
import { Buffer } from "node:buffer";

import {
  agent,
  agents,
  config,
  project,
  transcript,
} from "./fixtures/visual-data";

for (const variant of [
  { locale: "de", theme: "light", width: 1440, height: 1000 },
  { locale: "en", theme: "dark", width: 390, height: 844 },
]) {
  test(`wterm VM terminal: fullscreen, Unicode, PTY input, resize and lifecycle (${variant.locale})`, async ({
    page,
  }, testInfo) => {
    const label = (de: string, en: string) =>
      variant.locale === "en" ? en : de;
    await page.setViewportSize(variant);
    await page.addInitScript(({ locale, theme }) => {
      localStorage.setItem("zentrale-locale", locale);
      localStorage.setItem("zentrale-theme", theme);
    }, variant);
    await page.routeWebSocket("**/events?*", () => {});
    const sockets: WebSocketRoute[] = [];
    const input: Buffer[] = [];
    const sizes: { cols: number; rows: number }[] = [];
    let closed = 0;
    await page.routeWebSocket("**/terminal/**", (socket) => {
      const url = new URL(socket.url());
      expect(url.pathname).toBe(`/terminal/${project.id}`);
      expect(url.searchParams.get("agent")).toBe(agent.id);
      expect(Number(url.searchParams.get("cols"))).toBeGreaterThan(1);
      expect(Number(url.searchParams.get("rows"))).toBeGreaterThan(1);
      sockets.push(socket);
      socket.onClose(() => {
        closed++;
      });
      socket.onMessage((data) => {
        if (typeof data !== "string") input.push(data);
        else {
          const message = JSON.parse(data);
          if (message.type === "resize") sizes.push(message);
        }
      });
      if (sockets.length === 3) {
        socket.send(
          JSON.stringify({
            type: "error",
            message: "terminal startup test failure",
          }),
        );
      } else {
        socket.send(JSON.stringify({ type: "status", status: "starting" }));
        socket.send(JSON.stringify({ type: "status", status: "ready" }));
        socket.send(Buffer.from(`READY_${sockets.length}\r\n$ `));
      }
    });
    await page.route("**/graphql", (route) => {
      const { query } = route.request().postDataJSON();
      const data = query.includes("status{")
        ? { status: { needsSetup: false, authenticated: true } }
        : query.includes("recentMessages")
          ? {
              ...transcript,
              agent,
              messages: [],
              questions: [],
              jobs: [],
              recentMessages: [],
            }
          : query.includes("agents(projectId:")
            ? { project, agents }
            : config;
      return route.fulfill({ json: { data } });
    });
    await page.goto(`/#/projects/${project.id}?agent=${agent.id}`);
    const launch = page.getByRole("button", {
      name: label("Terminal in der VM öffnen", "Open terminal in VM"),
      exact: true,
    });
    const vscode = page.getByRole("link", {
      name: label(
        "VS Code in neuem Tab öffnen ↗",
        "Open VS Code in a new tab ↗",
      ),
      exact: true,
    });
    await expect(launch).toBeVisible();
    expect(
      await vscode.evaluate((element) =>
        element.nextElementSibling?.getAttribute("aria-label"),
      ),
    ).toBe(label("Terminal in der VM öffnen", "Open terminal in VM"));
    expect(sockets).toHaveLength(0);
    await launch.click();
    const dialog = page.getByRole("dialog", {
      name: label("VM-Terminal", "VM terminal"),
      exact: true,
    });
    const screen = dialog.getByRole("group", {
      name: label("Terminal-Ein- und Ausgabe", "Terminal input and output"),
      exact: true,
    });
    await expect(screen).toContainText("READY_1");
    await expect
      .poll(async () => Math.round((await dialog.boundingBox())?.width ?? 0))
      .toBe(variant.width);
    await expect
      .poll(async () => Math.round((await dialog.boundingBox())?.height ?? 0))
      .toBe(variant.height);
    await expect(
      screen.getByRole("textbox", {
        name: label("Terminal-Eingabe", "Terminal input"),
        exact: true,
      }),
    ).toBeFocused();
    await page.keyboard.type("echo grüße");
    await page.keyboard.press("Enter");
    await page.keyboard.press("Control+c");
    await page.keyboard.press("Escape");
    await expect(dialog).toBeVisible();
    await expect
      .poll(() => Buffer.concat(input).toString("utf8"))
      .toContain("echo grüße\r\u0003\u001b");
    await expect.poll(() => sizes.length).toBeGreaterThan(0);
    const before = sizes.at(-1)!;
    const dimensions =
      variant.locale === "en"
        ? { width: 480, height: 900 }
        : { width: 1200, height: 820 };
    await page.setViewportSize(dimensions);
    await expect.poll(() => sizes.at(-1)?.cols).not.toBe(before.cols);
    const unicode = Buffer.from("\r\nhello 🐧 ä\r\n");
    const split = unicode.indexOf(0xf0) + 1;
    sockets[0].send(unicode.subarray(0, split));
    sockets[0].send(unicode.subarray(split));
    await expect(screen).toContainText("hello 🐧 ä");
    await page.screenshot({
      path: testInfo.outputPath(`terminal-${variant.locale}.png`),
    });
    await page.keyboard.press("Alt+Shift+T");
    await expect(dialog).not.toBeVisible();
    await expect.poll(() => closed).toBe(1);
    await expect(launch).toBeFocused();

    await launch.press("Enter");
    await expect(screen).toContainText("READY_2");
    sockets[1].send(JSON.stringify({ type: "exit", code: 7 }));
    await expect(dialog.getByRole("status")).toContainText("7");
    await dialog
      .getByRole("button", {
        name: label("Erneut verbinden", "Reconnect"),
        exact: true,
      })
      .click();
    await expect(dialog.getByRole("alert")).toContainText(
      "terminal startup test failure",
    );
    await dialog
      .getByRole("button", {
        name: label("Erneut verbinden", "Reconnect"),
        exact: true,
      })
      .click();
    await expect(screen).toContainText("READY_4");
    await dialog
      .getByRole("button", { name: label("Schließen", "Close"), exact: true })
      .click();
    await expect(dialog).not.toBeVisible();
    await expect.poll(() => closed).toBe(4);
    expect(sockets).toHaveLength(4);
  });
}
