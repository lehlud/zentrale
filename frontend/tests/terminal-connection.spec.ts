import { expect, test } from "@playwright/test";

import {
  TerminalHeartbeat,
  terminalConnectionError,
  terminalHeartbeatInterval,
} from "../src/terminal-connection";

test("terminal heartbeat detects missing replies but tolerates hidden pages and browser suspension", () => {
  const interval = terminalHeartbeatInterval;
  const stalled = new TerminalHeartbeat(0);
  expect(stalled.check(interval, false)).toBe(true);
  expect(stalled.check(2 * interval, false)).toBe(true);
  expect(stalled.check(3 * interval, false)).toBe(false);

  const healthy = new TerminalHeartbeat(0);
  for (let n = 1; n <= 12; n++) {
    expect(healthy.check(n * interval, false)).toBe(true);
    healthy.acknowledge(n * interval);
  }

  const suspended = new TerminalHeartbeat(0);
  expect(suspended.check(30 * interval, false)).toBe(true);
  expect(suspended.check(31 * interval, false)).toBe(true);
  expect(suspended.check(32 * interval, false)).toBe(true);
  expect(suspended.check(33 * interval, false)).toBe(false);

  const background = new TerminalHeartbeat(0);
  for (let n = 1; n <= 12; n++) {
    expect(background.check(n * interval, true)).toBe(true);
  }
  // A visible-page event grants a new probe window after background throttling.
  background.acknowledge(100 * interval);
  expect(background.check(100 * interval, false)).toBe(true);
});

test("failed WebSocket diagnostics distinguish auth, origin, routing and upgrade failures", async () => {
  const url = new URL(
    "wss://zentrale.example/preview/terminal/project?agent=a",
  );
  const signal = new AbortController().signal;
  for (const [status, body, expected] of [
    [401, "{}", "Anmeldung abgelaufen"],
    [403, "{}", "Origin abgelehnt"],
    [404, "{}", "Endpunkt nicht gefunden"],
    [405, "{}", "Endpunkt nicht gefunden"],
    [502, "{}", "Server nicht verfügbar"],
    [200, "<html>SPA fallback</html>", "Endpunkt nicht gefunden"],
    [200, '{"status":"ok"}', "WebSocket-Upgrades"],
  ] as const) {
    const request: typeof fetch = async (input, init) => {
      expect(String(input)).toBe(
        "https://zentrale.example/preview/terminal/project?agent=a",
      );
      expect(init).toMatchObject({
        method: "POST",
        credentials: "same-origin",
        cache: "no-store",
        redirect: "error",
        signal,
      });
      return new Response(body, { status });
    };
    expect(await terminalConnectionError(url, signal, request)).toContain(
      expected,
    );
    expect(url.protocol).toBe("wss:");
  }
  const offline: typeof fetch = async () => {
    throw new TypeError("offline");
  };
  expect(await terminalConnectionError(url, signal, offline)).toContain(
    "Netzwerk",
  );
});
