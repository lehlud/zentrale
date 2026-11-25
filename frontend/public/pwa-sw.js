const offlinePage = `<!doctype html>
<html lang="de">
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="theme-color" content="#101014">
<title>Zentrale – offline</title>
<style>
  body { background: #101014; color: #f4f4f8; font: 1rem/1.6 system-ui, sans-serif; margin: 0; }
  main { margin: 12vh auto; max-width: 32rem; padding: 0 1.5rem; }
  h1 { line-height: 1.2; }
  a { color: #8ceada; }
</style>
<main>
  <p>Zentrale</p>
  <h1>Keine Verbindung zum Server</h1>
  <p>Prüfe deine Verbindung und versuche es erneut.</p>
  <a href="/">Erneut versuchen</a>
</main>
</html>`;

self.addEventListener("activate", (event) => {
  event.waitUntil(self.clients.claim());
});

self.addEventListener("fetch", (event) => {
  const { request } = event;

  if (
    request.method !== "GET" ||
    request.mode !== "navigate" ||
    request.destination !== "document"
  ) {
    return;
  }

  const url = new URL(request.url);
  if (
    url.origin !== self.location.origin ||
    (url.pathname !== "/" && url.pathname !== "/index.html")
  ) {
    return;
  }

  // Handle only the app entry point and do not write anything to Cache Storage.
  // GraphQL, attachments, workspace files, the editor and previews go untouched.
  event.respondWith(
    fetch(request, { cache: "no-store" }).catch(
      () =>
        new Response(offlinePage, {
          status: 503,
          headers: {
            "Content-Type": "text/html; charset=utf-8",
            "Cache-Control": "no-store",
            "Content-Security-Policy":
              "default-src 'none'; style-src 'unsafe-inline'; frame-ancestors 'none'; base-uri 'none'",
          },
        }),
    ),
  );
});
