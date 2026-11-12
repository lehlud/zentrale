import { expect, test, type WebSocketRoute } from "@playwright/test";
import { bootstrapData, isTranscriptQuery } from "./fixtures/bootstrap-data";
import { Buffer } from "node:buffer";
import { installPushFixture } from "./fixtures/push-fixture";

import {
  agent,
  agents,
  config,
  project,
  transcript,
} from "./fixtures/visual-data";

test("wterm stays responsive while hidden conversation pushes are suspended and resumes on close", async ({
  page,
}, testInfo) => {
  test.setTimeout(60000);
  const diagnostics: string[] = [];
  page.on("console", (message) => {
    if (message.text().startsWith("TERMINAL_TRACE ")) {
      diagnostics.push(message.text());
      if (diagnostics.length > 200) diagnostics.shift();
    }
  });
  await page.addInitScript(() => localStorage.setItem("zentrale-locale", "de"));
  await page.addInitScript(() => {
    const stats = { sent: 0, received: 0, lastReceived: "" };
    Object.assign(window, { terminalTransportStats: stats });
    const trace = (kind: string) =>
      console.debug(
        "TERMINAL_TRACE " +
          JSON.stringify({
            kind,
            at: performance.now(),
            ...stats,
            active: document.activeElement?.tagName,
          }),
      );
    window.addEventListener(
      "keydown",
      (event) => {
        if (event.key === "x") trace("keydown");
      },
      true,
    );
    const NativeSocket = window.WebSocket;
    window.WebSocket = class extends NativeSocket {
      constructor(url: string | URL, protocols?: string | string[]) {
        super(url, protocols);
        if (String(url).includes("/terminal/"))
          this.addEventListener("message", (event) => {
            stats.received++;
            stats.lastReceived =
              event.data instanceof ArrayBuffer
                ? new TextDecoder().decode(event.data).slice(-200)
                : String(event.data).slice(-200);
            trace("received");
          });
      }
      send(data: Parameters<WebSocket["send"]>[0]) {
        if (this.url.includes("/terminal/")) {
          stats.sent++;
          trace("sent");
        }
        super.send(data);
      }
    };
  });
  const messages = Array.from({ length: 1000 }, (_, index) => ({
    ...transcript.messages[0],
    id: `terminal-perf-${index}`,
    role: "assistant",
    content:
      `Historical message ${index} ` + "Background conversation. ".repeat(20),
    wire:
      index >= 900
        ? JSON.stringify({
            tool_calls: [
              {
                id: `call-${index}`,
                type: "function",
                function: {
                  name: "exec",
                  arguments: JSON.stringify({
                    cmd: "printf '" + "x".repeat(20000) + "'",
                    timeout_ms: 30000,
                  }),
                },
              },
            ],
          })
        : "",
    callId: "",
    toolName: "",
    change: null,
    changes: [],
    activities: [],
  }));
  const resolve = (query: string) => {
    const data = query.includes("status{")
      ? { status: { needsSetup: false, authenticated: true } }
      : isTranscriptQuery(query)
        ? {
            ...transcript,
            agent,
            messages,
            recentMessages: [],
            questions: [],
            jobs: [],
          }
        : query.includes("agents(projectId:")
          ? { project, agents }
          : config;
    return bootstrapData(query, data);
  };
  const push = await installPushFixture(page, resolve);
  let bootstrapReads = 0;
  await page.route("**/graphql", (route) => {
    const query = route.request().postDataJSON().query;
    if (query.includes("conversationBootstrap")) bootstrapReads++;
    return route.fulfill({
      json: { data: resolve(route.request().postDataJSON().query) },
    });
  });
  let echoes = 0;
  await page.routeWebSocket("**/terminal/**", (socket) => {
    socket.onMessage((data) => {
      if (typeof data !== "string" && data.toString() === "x") {
        socket.send(Buffer.from(`\r\nECHO_${++echoes}\r\n`));
      }
    });
    socket.send(JSON.stringify({ type: "status", status: "ready" }));
    socket.send(
      Buffer.from(
        Array.from({ length: 2100 }, (_, n) => `Scrollback line ${n}\r\n`).join(
          "",
        ) + "SCROLLBACK_READY\r\n",
      ),
    );
  });
  await page.goto(`/#/projects/${project.id}?agent=${agent.id}`);
  await page
    .getByRole("button", { name: "Terminal in der VM öffnen", exact: true })
    .click();
  const dialog = page.getByRole("dialog", { name: "VM-Terminal", exact: true });
  const screen = dialog.getByRole("group", {
    name: "Terminal-Ein- und Ausgabe",
    exact: true,
  });
  await expect(screen).toContainText("SCROLLBACK_READY");
  const bootstrapSubscription = push.frames
    .filter(
      (frame) =>
        frame.type === "subscribe" &&
        frame.query.includes("conversationBootstrap"),
    )
    .at(-1)!;
  const initialSubscriptions = push.frames.filter(
    (frame) =>
      frame.type === "subscribe" &&
      frame.requestId === bootstrapSubscription.requestId,
  ).length;
  await expect
    .poll(() =>
      push.frames.some(
        (frame) =>
          frame.type === "unsubscribe" &&
          frame.requestId === bootstrapSubscription.requestId,
      ),
    )
    .toBe(true);
  const initialReads = bootstrapReads;
  const cdp = await page.context().newCDPSession(page);
  await cdp.send("Emulation.setCPUThrottlingRate", { rate: 4 });
  let revision = 0;
  const updates = setInterval(() => {
    messages[messages.length - 1].content =
      `Live background revision ${++revision}`;
    void push.push();
  }, 200);
  const latencies: number[] = [];
  try {
    await screen.locator("textarea").focus();
    // Sustain traffic beyond the reported five-second period. A short eager
    // burst can finish before periodic lifecycle/live-update work begins.
    for (let n = 1; n <= 80; n++) {
      const started = Date.now();
      await page.keyboard.type("x");
      await expect(screen).toContainText(`ECHO_${n}`, { timeout: 1000 });
      latencies.push(Date.now() - started);
      await page.waitForTimeout(125); // Deliberate typing cadence, not a retry.
    }
    expect(revision).toBeGreaterThan(0);
    expect(echoes).toBe(80);
    expect(
      Math.max(...latencies),
      "No multi-second terminal stalls at 4x CPU slowdown",
    ).toBeLessThan(1000);
    clearInterval(updates);
    messages[messages.length - 1].wire = "";
    messages[messages.length - 1].content =
      "LATEST_CONVERSATION_AFTER_TERMINAL";
    await push.push();
    await dialog
      .getByRole("button", { name: "Schließen", exact: true })
      .click();
    await expect(dialog).not.toBeVisible();
    await expect(
      page.getByText("LATEST_CONVERSATION_AFTER_TERMINAL", { exact: true }),
    ).toBeVisible();
    expect(
      bootstrapReads,
      "Closing uses the pushed snapshot, not another HTTP read",
    ).toBe(initialReads);
    expect(
      push.frames.filter(
        (frame) =>
          frame.type === "subscribe" &&
          frame.requestId === bootstrapSubscription.requestId,
      ),
    ).toHaveLength(initialSubscriptions + 1);
  } catch (error) {
    // Never issue another browser RPC on the failing/closing page: it can
    // consume the remaining test deadline and obscure the original failure.
    throw new Error(
      `Terminal latency diagnostics: ${JSON.stringify({ echoes, latencies, revision, diagnostics: diagnostics.slice(-6) })}\n${String(error)}`,
    );
  } finally {
    clearInterval(updates);
    await testInfo.attach("terminal-keystroke-to-paint.json", {
      body: JSON.stringify({
        latencies,
        backgroundUpdates: revision,
        echoes,
        diagnostics,
      }),
      contentType: "application/json",
    });
    // A closed target must not replace the useful latency assertion.
    await Promise.race([
      cdp
        .send("Emulation.setCPUThrottlingRate", { rate: 1 })
        .then(() => cdp.detach())
        .catch(() => {}),
      new Promise((resolve) => setTimeout(resolve, 500)),
    ]);
  }
});

test("real rejected WebSocket handshake shows actionable origin diagnostics", async ({
  page,
}) => {
  await page.addInitScript(() => localStorage.setItem("zentrale-locale", "de"));
  await page.routeWebSocket("**/events?*", () => {});
  // Do not mock the terminal WebSocket: the real backend rejects the fixture's
  // nonexistent login. A failed upgrade must reach onerror and then the probe.
  let probes = 0;
  await page.route("**/terminal/**", (route) => {
    if (route.request().method() !== "POST") return route.fallback();
    probes++;
    expect(route.request().headers()["origin"]).toBe(
      new URL(page.url()).origin,
    );
    return route.fulfill({ status: 403, json: { message: "Forbidden" } });
  });
  await page.route("**/graphql", (route) => {
    const { query } = route.request().postDataJSON();
    const data = query.includes("status{")
      ? { status: { needsSetup: false, authenticated: true } }
      : isTranscriptQuery(query)
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
    return route.fulfill({ json: { data: bootstrapData(query, data) } });
  });
  await page.goto(`/#/projects/${project.id}?agent=${agent.id}`);
  await page
    .getByRole("button", { name: "Terminal in der VM öffnen", exact: true })
    .click();
  const dialog = page.getByRole("dialog", { name: "VM-Terminal", exact: true });
  await expect(dialog.getByRole("alert")).toContainText(
    "Terminal-Origin abgelehnt",
  );
  await expect(
    dialog.getByRole("button", { name: "Erneut verbinden", exact: true }),
  ).toBeVisible();
  expect(probes).toBe(1);
});

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
    // A mocked socket can report close again when effect cleanup calls close()
    // after an exit/error has already closed it. Count distinct sessions.
    const closed = new Set<WebSocketRoute>();
    await page.routeWebSocket("**/terminal/**", (socket) => {
      const url = new URL(socket.url());
      expect(url.pathname).toBe(`/terminal/${project.id}`);
      expect(url.searchParams.get("agent")).toBe(agent.id);
      expect(Number(url.searchParams.get("cols"))).toBeGreaterThan(1);
      expect(Number(url.searchParams.get("rows"))).toBeGreaterThan(1);
      sockets.push(socket);
      socket.onClose(() => {
        closed.add(socket);
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
        : isTranscriptQuery(query)
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
      return route.fulfill({ json: { data: bootstrapData(query, data) } });
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
    await expect.poll(() => closed.size).toBe(1);
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
    await expect.poll(() => closed.size).toBe(4);
    expect(sockets).toHaveLength(4);
  });
}
