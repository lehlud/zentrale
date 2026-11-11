// Only run after a failed upgrade: normal sessions need no extra round trip.
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
