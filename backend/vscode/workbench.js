// The workbench and browser extension hosts are served by Zentrale. Only the
// workspace protocol, terminals and workspace extension hosts use the SSH agent.
const settings = JSON.parse(
  document.querySelector("#zentrale-workbench").dataset.settings,
);
const {
  create,
  URI,
  Emitter,
  RemoteAuthorityResolverError,
  RemoteAuthorityResolverErrorCode,
} = await import(
  `${settings.assets}/out/vs/workbench/workbench.web.main.internal.js`
);

const parameters = new URL(location.href).searchParams;
const workspaceFile = parameters.get("workspace");
const remoteURI = (path) =>
  URI.from({
    scheme: "vscode-remote",
    authority: settings.remoteAuthority,
    path,
  });
const workspace = workspaceFile
  ? { workspaceUri: remoteURI(workspaceFile) }
  : { folderUri: remoteURI(parameters.get("folder") || settings.directory) };

// Compatible with VS Code's bundled callback page; random request IDs also
// separate simultaneous workbenches opened in different tabs.
const callbacks = new Emitter();
const pendingCallbacks = new Set();
window.addEventListener("storage", () => {
  for (const key of pendingCallbacks) {
    const result = localStorage.getItem(key);
    if (!result) continue;
    pendingCallbacks.delete(key);
    localStorage.removeItem(key);
    callbacks.fire(URI.revive(JSON.parse(result)));
  }
});

function createSSHSocket(address) {
  const url = new URL(address);
  url.host = location.host;
  const socket = new WebSocket(url);
  socket.binaryType = "arraybuffer";
  const open = new Emitter();
  const data = new Emitter();
  const error = new Emitter();
  const close = new Emitter();
  socket.addEventListener("open", () => open.fire());
  socket.addEventListener("message", (event) => data.fire(event.data));
  socket.addEventListener("error", () =>
    error.fire(
      new RemoteAuthorityResolverError(
        "The SSH remote agent is unavailable",
        RemoteAuthorityResolverErrorCode.TemporarilyNotAvailable,
      ),
    ),
  );
  socket.addEventListener("close", (event) => {
    close.fire({
      code: event.code,
      reason: event.reason,
      wasClean: event.wasClean,
      event,
    });
    for (const emitter of [open, data, error, close]) emitter.dispose();
  });
  return {
    onOpen: open.event,
    onData: data.event,
    onError: error.event,
    onClose: close.event,
    send: (message) => socket.send(message),
    close: () => socket.close(),
  };
}

create(document.body, {
  remoteAuthority: settings.remoteAuthority,
  serverBasePath: settings.prefix,
  connectionToken: "",
  webSocketFactory: { create: createSSHSocket },
  resourceUriProvider: (uri) =>
    URI.from({
      scheme: location.protocol.slice(0, -1),
      authority: location.host,
      path: `${settings.prefix}/stable-${settings.commit}/vscode-remote-resource`,
      query: new URLSearchParams({ path: uri.path }).toString(),
      fragment: uri.fragment,
    }),
  enableWorkspaceTrust: true,
  urlCallbackProvider: {
    onCallback: callbacks.event,
    create(options = {}) {
      const random = crypto.getRandomValues(new Uint32Array(2));
      const id = random[0] * 2097152 + (random[1] >>> 11);
      const key = `vscode-web.url-callbacks[${id}]`;
      const query = new URLSearchParams({ "vscode-reqid": String(id) });
      for (const name of ["scheme", "authority", "path", "query", "fragment"]) {
        if (options[name]) query.set(`vscode-${name}`, options[name]);
      }
      localStorage.removeItem(key);
      pendingCallbacks.add(key);
      return URI.parse(location.href).with({
        path: `${settings.prefix}/callback`,
        query: query.toString(),
        fragment: "",
      });
    },
  },
  workspaceProvider: {
    workspace,
    trusted: true,
    async open(target, options) {
      const uri = target?.folderUri || target?.workspaceUri;
      if (
        !uri ||
        uri.scheme !== "vscode-remote" ||
        uri.authority !== settings.remoteAuthority
      )
        return false;
      const url = new URL(location.href);
      url.searchParams.delete("folder");
      url.searchParams.delete("workspace");
      url.searchParams.set(target.folderUri ? "folder" : "workspace", uri.path);
      if (options?.reuse) {
        location.assign(url);
        return true;
      }
      // Browsers deliberately return null with noopener, even on success.
      window.open(url, "_blank", "noopener,noreferrer");
      return true;
    },
  },
  productConfiguration: {
    embedderIdentifier: "zentrale",
    // Keep the trusted browser extension-host bootstrap on Zentrale as well.
    // Untrusted extension webviews retain VS Code's isolated webview endpoint.
    webEndpointUrlTemplate: "",
    // Keep workspace language backends on the SSH host, including the first
    // transition from restricted mode to a trusted workspace. User overrides
    // in VS Code's remote.extensionKind setting still take precedence.
    extensionKind: {
      "vscode.typescript-language-features": ["workspace", "-web"],
      "vscode.json-language-features": ["workspace", "-web"],
      "vscode.css-language-features": ["workspace", "-web"],
      "vscode.html-language-features": ["workspace", "-web"],
    },
  },
  configurationDefaults: {
    "workbench.startupEditor": "none",
    "workbench.secondarySideBar.defaultVisibility": "hidden",
    "workbench.colorTheme":
      settings.theme === "light"
        ? "Default Light Modern"
        : "Default Dark Modern",
  },
  windowIndicator: {
    label: `$(remote) ${settings.machineName}`,
    tooltip: `Remote SSH — ${settings.machineName}`,
  },
});
document.querySelector("#startup")?.remove();
