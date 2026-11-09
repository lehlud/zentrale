# HTTPS web app previews

[← Documentation](README.md)

Start an HTTP development server in the browser VS Code terminal. In the
project's editor panel, enter its port under **Web app preview** and choose
**Open preview**. VS Code's forwarded ports and localhost links use the same
gateway.

For example, a frontend on port 1234 and an API on port 1235 are available at:

```text
https://zentrale.example/preview/PROJECT/EDITOR_SESSION/1234/
https://zentrale.example/preview/PROJECT/EDITOR_SESSION/1235/
```

Both use the same public hostname and HTTPS origin. Only the reverse proxy's
HTTPS port needs to be exposed. Connections travel through the editor's existing
SSH session. On managed VMs, the target is guest loopback, including IPv6-only
listeners; a failed guest connection never falls back to the SSH host.

## Frontend and API connections

An injected browser adapter maps `fetch`, `Request` inputs, XMLHttpRequest,
EventSource and WebSocket targets such as `http://localhost:1235` and
`http://127.0.0.1:1235` to that session's HTTPS gateway. Computed URLs work too;
the adapter changes the request destination before the browser sends it.
WebSockets use WSS externally, allowing development hot reload through TLS
termination.

A service worker scoped to the editor session routes root-relative assets and
module imports for each preview tab. HTML links, forms and HTTP redirects are
adjusted to the preview path. Zentrale's main page and other editor sessions are
outside this worker's scope. No project source files are edited.

Application cookies are namespaced separately from Zentrale, while retaining
their original names and logical paths inside the app. Cookies can be shared
between the frontend and API ports, as on localhost. Zentrale's login cookie is
never forwarded upstream; an app can also use a cookie named `zentrale_session`.
Requests with `credentials: "omit"` still authenticate to the private gateway
without forwarding or accepting application cookies.

For new applications, relative API URLs and the development server's own API
proxy remain the most portable arrangement.

## Routers and browser features

The preview has a path prefix. A client router should use
`globalThis.__ZENTRALE_PREVIEW_BASE__ ?? "/"` as its basename, removing a trailing
slash if its router requires it. This value is provided before application code
runs. History navigation and ordinary links stay within the preview.

This is a development compatibility layer, not a browser origin emulator.
Root-bound SSR frameworks, OAuth callback URLs, application-owned service
workers and direct assignments to `location` can require application-specific
configuration. Browser storage still belongs to the shared public origin.
Use trusted development apps; a path prefix does not isolate malicious code
from other pages on that origin. Dedicated preview subdomains are the appropriate
alternative when independent origins or completely unchanged root URLs are
required.

## Reverse proxy and lifecycle

Terminate TLS in the external reverse proxy and forward all paths to Zentrale,
preserving `Host`. Enable WebSocket upgrades and disable response buffering for
streaming endpoints. Configure Zentrale with the public origin and secure cookies:

```sh
zentrale -origin https://zentrale.example -secure-cookie
```

The guest development services themselves use HTTP. HTTPS and WSS terminate at
the public proxy; no certificates are installed in the guest. A valid HTTPS
certificate is required for the browser's service worker.

Previews require the Zentrale login and an active editor session. Stopping the
editor, disconnecting SSH, stopping its VM or restarting Zentrale closes the
route. Reopen VS Code to obtain a fresh preview URL. Keep long-lived servers in
the VS Code terminal; agent commands need `network:true` to share the VM's
network namespace with the editor.
