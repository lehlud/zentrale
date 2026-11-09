(() => {
  "use strict";
  const script = document.currentScript;
  const root = script.dataset.root;
  const port = Number(script.dataset.port);
  const prefix = `${root}${port}/`;

  if (script.dataset.boot === "true") {
    const start = async () => {
      if (!navigator.serviceWorker)
        throw new Error("The preview requires HTTPS.");
      const controlled = new Promise((resolve) => {
        if (navigator.serviceWorker.controller) resolve();
        else
          navigator.serviceWorker.addEventListener(
            "controllerchange",
            resolve,
            { once: true },
          );
      });
      await navigator.serviceWorker.register(`${root}_zentrale/worker.js`, {
        scope: root,
      });
      await navigator.serviceWorker.ready;
      await controlled;
      location.reload();
    };
    start().catch((error) => {
      document.getElementById("preview-status").textContent = error.message;
    });
    return;
  }

  globalThis.__ZENTRALE_PREVIEW_BASE__ = prefix;
  const cookiePrefix = script.dataset.cookies;
  const nativeCookie = Object.getOwnPropertyDescriptor(
    Document.prototype,
    "cookie",
  );
  const logicalPath = () => "/" + location.pathname.slice(prefix.length);
  const cookieMatches = (path) =>
    logicalPath() === path ||
    (logicalPath().startsWith(path) &&
      (path.endsWith("/") || logicalPath()[path.length] === "/"));
  if (nativeCookie && cookiePrefix)
    Object.defineProperty(document, "cookie", {
      configurable: true,
      get() {
        const visible = [];
        for (const entry of nativeCookie.get.call(document).split(/;\s*/)) {
          const separator = entry.indexOf("=");
          if (!entry.startsWith(cookiePrefix) || separator < 0) continue;
          try {
            const encoded = entry
              .slice(cookiePrefix.length, separator)
              .replaceAll("-", "+")
              .replaceAll("_", "/");
            const bytes = Uint8Array.from(atob(encoded), (character) =>
              character.charCodeAt(0),
            );
            const [name, path] = JSON.parse(new TextDecoder().decode(bytes));
            if (cookieMatches(path))
              visible.push({ name, path, value: entry.slice(separator + 1) });
          } catch {
            /* Ignore unrelated or malformed browser cookies. */
          }
        }
        return visible
          .sort((a, b) => b.path.length - a.path.length)
          .map(({ name, value }) => `${name}=${value}`)
          .join("; ");
      },
      set(value) {
        const [pair, ...attributes] = String(value).split(";");
        const separator = pair.indexOf("=");
        if (separator < 1) return;
        const name = pair.slice(0, separator).trim();
        const pathAttribute = attributes.find((attribute) =>
          /^\s*path\s*=/i.test(attribute),
        );
        let path = pathAttribute?.slice(pathAttribute.indexOf("=") + 1).trim();
        if (!path?.startsWith("/"))
          path = logicalPath().slice(0, logicalPath().lastIndexOf("/")) || "/";
        const bytes = new TextEncoder().encode(JSON.stringify([name, path]));
        const encoded = btoa(String.fromCharCode(...bytes))
          .replaceAll("+", "-")
          .replaceAll("/", "_")
          .replace(/=+$/, "");
        const retained = attributes.filter(
          (attribute) => !/^\s*(path|domain)\s*=/i.test(attribute),
        );
        nativeCookie.set.call(
          document,
          `${cookiePrefix}${encoded}=${pair.slice(separator + 1)}; Path=${root}; ${retained.join(";")}`,
        );
      },
    });
  const loopback = new Set(["localhost", "127.0.0.1", "[::1]"]);
  function rewrite(value, websocket = false) {
    const original = String(value);
    const address = new URL(original, location.href);
    if (!["http:", "https:", "ws:", "wss:"].includes(address.protocol))
      return original;
    let targetPort = port;
    if (address.host === location.host) {
      if (address.pathname.startsWith(root)) return original;
    } else if (loopback.has(address.hostname)) {
      targetPort = Number(
        address.port ||
          (address.protocol === "https:" || address.protocol === "wss:"
            ? 443
            : 80),
      );
    } else return original;
    address.protocol = websocket
      ? location.protocol === "https:"
        ? "wss:"
        : "ws:"
      : location.protocol;
    address.host = location.host;
    address.pathname = `${root}${targetPort}${address.pathname}`;
    if (websocket)
      address.searchParams.set("__zentrale_preview_origin", String(port));
    return address.href;
  }

  const nativeFetch = globalThis.fetch;
  globalThis.fetch = async function (input, options) {
    if (input instanceof Request) {
      const address = rewrite(input.url);
      if (address === input.url)
        return nativeFetch.call(globalThis, input, options);
      const source = new Request(input, options);
      const body = ["GET", "HEAD"].includes(source.method)
        ? undefined
        : await source.blob();
      return nativeFetch.call(
        globalThis,
        new Request(address, {
          method: source.method,
          headers: source.headers,
          body,
          mode: source.mode,
          credentials: source.credentials,
          redirect: source.redirect,
          cache: source.cache,
          referrer:
            source.referrer === "about:client"
              ? location.href
              : source.referrer,
          referrerPolicy: source.referrerPolicy,
          integrity: source.integrity,
          keepalive: source.keepalive,
          signal: source.signal,
        }),
      );
    }
    return nativeFetch.call(globalThis, rewrite(input), options);
  };
  const nativeOpen = XMLHttpRequest.prototype.open;
  XMLHttpRequest.prototype.open = function (method, address, ...options) {
    return nativeOpen.call(this, method, rewrite(address), ...options);
  };
  const NativeWebSocket = globalThis.WebSocket;
  globalThis.WebSocket = class extends NativeWebSocket {
    constructor(address, ...options) {
      super(rewrite(address, true), ...options);
    }
  };
  const NativeEventSource = globalThis.EventSource;
  globalThis.EventSource = class extends NativeEventSource {
    constructor(address, ...options) {
      super(rewrite(address), ...options);
    }
  };
  for (const method of ["pushState", "replaceState"]) {
    const native = history[method];
    history[method] = function (state, title, address) {
      return native.call(
        this,
        state,
        title,
        address == null ? address : rewrite(address),
      );
    };
  }
  document.addEventListener(
    "click",
    (event) => {
      const anchor = event.target.closest?.("a[href]");
      if (anchor) anchor.href = rewrite(anchor.href);
    },
    true,
  );
  document.addEventListener(
    "submit",
    (event) => {
      if (event.target instanceof HTMLFormElement)
        event.target.action = rewrite(event.target.action);
    },
    true,
  );
  const nativeWindowOpen = window.open;
  window.open = function (address, ...options) {
    return nativeWindowOpen.call(
      this,
      address == null ? address : rewrite(address),
      ...options,
    );
  };
})();
