export const terminalHeartbeatInterval = 25_000;
const terminalHeartbeatTimeout = 75_000;

// Native WebSocket pongs are invisible to JavaScript. Track application-level
// replies so a half-open socket cannot leave the UI saying "connected" forever.
export class TerminalHeartbeat {
  private lastReply: number;
  private lastCheck: number;

  constructor(now: number) {
    this.lastReply = this.lastCheck = now;
  }

  acknowledge(now: number) {
    this.lastReply = now;
  }

  check(now: number, hidden: boolean): boolean {
    // Background throttling, device sleep and a frozen event loop are not
    // evidence of a lost connection. Give a resumed page a full probe window.
    if (hidden || now - this.lastCheck > 2 * terminalHeartbeatInterval) {
      this.lastReply = now;
    }
    this.lastCheck = now;
    return now - this.lastReply < terminalHeartbeatTimeout;
  }
}

// Only run after a failed upgrade, not after an established session is lost.
// Unlike WebSocket.onerror, fetch exposes authentication and routing failures.
// The POST checks the same guards, without provisioning a VM or opening a shell.
export async function terminalConnectionError(
  socketURL: URL,
  signal: AbortSignal,
  request: typeof fetch = fetch,
): Promise<string> {
  const url = new URL(socketURL);
  url.protocol = url.protocol === "wss:" ? "https:" : "http:";
  try {
    const response = await request(url, {
      method: "POST",
      credentials: "same-origin",
      cache: "no-store",
      redirect: "error",
      signal,
    });
    if (response.status === 401) {
      return "Terminal-Anmeldung abgelaufen. Bitte neu anmelden.";
    }
    if (response.status === 403) {
      return "Terminal-Origin abgelehnt. Hinter einem HTTPS-Reverse-Proxy muss die öffentliche Origin mit -origin konfiguriert sein. Prüfe auch -secure-cookie.";
    }
    if (response.status === 404 || response.status === 405) {
      return "Terminal-Endpunkt nicht gefunden. Prüfe die Weiterleitung von /terminal/ im Reverse-Proxy und die Backend-Version.";
    }
    if (!response.ok) {
      return "Terminal-Server nicht verfügbar. Bitte Server-Logs und Reverse-Proxy prüfen.";
    }
    const body: unknown = await response.json().catch(() => null);
    if (
      !body ||
      typeof body !== "object" ||
      !("status" in body) ||
      body.status !== "ok"
    ) {
      return "Terminal-Endpunkt nicht gefunden. Prüfe die Weiterleitung von /terminal/ im Reverse-Proxy und die Backend-Version.";
    }
    return "Terminal-Endpunkt erreichbar, aber WebSocket-Verbindung fehlgeschlagen. Prüfe WebSocket-Upgrades für /terminal/ im Reverse-Proxy.";
  } catch {
    return "Terminal-Endpunkt nicht erreichbar. Prüfe Netzwerk und Reverse-Proxy.";
  }
}
