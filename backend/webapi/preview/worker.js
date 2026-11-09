/* This scope is one editor session. Zentrale and other previews stay untouched. */
const root = new URL("../", self.location.href).pathname;
const loopback = new Set(["localhost", "127.0.0.1", "[::1]"]);
self.addEventListener("install", () => self.skipWaiting());
self.addEventListener("activate", (event) =>
  event.waitUntil(self.clients.claim()),
);
self.addEventListener("fetch", (event) => {
  event.respondWith(
    (async () => {
      const address = new URL(event.request.url);
      const client = event.clientId && (await self.clients.get(event.clientId));
      const clientURL = client && new URL(client.url);
      const primaryPort = clientURL?.pathname.startsWith(root)
        ? clientURL.pathname.slice(root.length).split("/")[0]
        : "";
      if (
        address.origin === self.location.origin &&
        address.pathname.startsWith(`${root}_zentrale/`)
      ) {
        return fetch(event.request);
      }
      if (
        !address.pathname.startsWith(root) ||
        address.origin !== self.location.origin
      ) {
        if (!client) return fetch(event.request);
        if (!/^\d+$/.test(primaryPort)) return fetch(event.request);
        let port = primaryPort;
        if (address.origin !== self.location.origin) {
          if (!loopback.has(address.hostname)) return fetch(event.request);
          port = address.port || (address.protocol === "https:" ? "443" : "80");
        }
        address.protocol = self.location.protocol;
        address.host = self.location.host;
        address.pathname = `${root}${port}${address.pathname}`;
      }
      if (address.href !== event.request.url)
        return Response.redirect(address.href, 307);
      const headers = new Headers(event.request.headers);
      headers.set("X-Zentrale-Preview", "1");
      headers.set("X-Zentrale-Preview-Credentials", event.request.credentials);
      if (/^\d+$/.test(primaryPort))
        headers.set("X-Zentrale-Preview-Origin-Port", primaryPort);
      // Copy the Request rather than rebuilding its body as a ReadableStream:
      // normal POSTs must also work behind HTTP/1.1 TLS-terminating proxies.
      return fetch(
        new Request(event.request, {
          headers,
          mode: "same-origin",
          credentials: "same-origin",
          cache: "no-store",
        }),
      );
    })(),
  );
});
