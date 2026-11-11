import { expect, test } from "@playwright/test";

import { terminalConnectionError } from "../src/terminal-connection";

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
