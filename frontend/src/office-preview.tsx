import { Button } from "@heroui/react";
import {
  createContext,
  useCallback,
  useEffect,
  useId,
  useRef,
  useState,
  type ReactNode,
} from "react";

import { MAX_ATTACHMENT_BYTES } from "./attachments";
import { useI18n } from "./preferences";

const editors: Record<string, string> = {
  xls: "spreadsheeteditor",
  xlsx: "spreadsheeteditor",
  doc: "documenteditor",
  docx: "documenteditor",
  ppt: "presentationeditor",
  pptx: "presentationeditor",
};

function extensionOf(name: string) {
  return /\.([a-z0-9]+)$/i.exec(name)?.[1].toLowerCase() ?? "";
}

export function isOfficeFile(name: string) {
  return Object.hasOwn(editors, extensionOf(name));
}

type ConvertedDocument = {
  binary: ArrayBuffer;
  images: [name: string, type: string, data: ArrayBuffer][];
};

async function documentBytes(url: string, signal: AbortSignal) {
  const response = await fetch(url, { signal, redirect: "error" });
  if (!response.ok || !response.body) throw new Error("Office download failed");

  const chunks = [];
  let size = 0;
  const reader = response.body.getReader();
  while (true) {
    const { done, value } = await reader.read();
    if (done) break;

    size += value.byteLength;
    if (size > MAX_ATTACHMENT_BYTES) {
      await reader.cancel();
      throw new Error("Office document exceeds preview limit");
    }
    chunks.push(value);
  }

  return new Blob(chunks).arrayBuffer();
}

export function OfficeDocument({ name, url }: { name: string; url: string }) {
  const { t, locale } = useI18n();
  const frame = useRef<HTMLIFrameElement>(null);
  const [status, setStatus] = useState<"loading" | "ready" | "error">(
    "loading",
  );

  useEffect(() => {
    const request = new AbortController();
    const extension = extensionOf(name);
    const child = frame.current;
    if (!child || !isOfficeFile(name)) return;

    const root = new URL("/office/", location.href);
    const entry = new URL(
      `web-apps/apps/${editors[extension]}/main/index.html`,
      root,
    );
    entry.search = new URLSearchParams({
      lang: locale,
      parentOrigin: location.origin,
      frameEditorId: "zentrale-office-preview",
      mode: "view",
    }).toString();

    let worker: Worker | undefined;
    let converted: ConvertedDocument | undefined;
    let appReady = false;
    const timer = window.setTimeout(fail, 60_000);

    function fail() {
      request.abort();
      worker?.terminate();
      window.clearTimeout(timer);
      setStatus("error");
    }

    function send(command: string, data: unknown) {
      // The trusted editor is in a sandboxed iframe with an opaque origin.
      child?.contentWindow?.postMessage(JSON.stringify({ command, data }), "*");
    }

    function open() {
      if (!appReady || !converted || !child?.contentWindow) return;

      const { binary, images } = converted;
      converted = undefined;
      child.contentWindow.postMessage(
        { type: "zentrale:office-images", images },
        "*",
        images.map((image) => image[2]),
      );
      send("init", {
        config: {
          lang: locale,
          mode: "view",
          user: { id: "viewer", name: "Zentrale" },
          customization: {
            compactToolbar: false,
            hideRightMenu: false,
            plugins: false,
            macros: false,
            chat: false,
          },
        },
      });
      send("openDocument", {
        doc: {
          title: name,
          fileType: extension,
          key: "local-preview",
          permissions: {
            edit: false,
            print: false,
            download: false,
            comment: false,
          },
        },
      });
      child.contentWindow.postMessage(
        { command: "openDocumentFromBinary", data: binary },
        "*",
        [binary],
      );
    }

    function onMessage(event: MessageEvent) {
      if (
        event.source !== child?.contentWindow ||
        event.origin !== "null" ||
        request.signal.aborted
      )
        return;
      if (typeof event.data !== "string") return;

      let message;
      try {
        message = JSON.parse(event.data);
      } catch {
        return;
      }

      switch (message?.event) {
        case "onAppReady":
          appReady = true;
          open();
          break;
        case "cryptPadSendMessageFromOO":
          if (message.data?.msg?.type === "auth") {
            // The fork's local collaboration protocol needs an initial viewer
            // session; no socket, server callback or saving is involved.
            send("cryptPadMessageToOO", {
              type: "auth",
              result: 1,
              sessionId: "local-preview",
              participants: [],
              locks: [],
              changes: [],
              changesIndex: 0,
              indexUser: 0,
              licenseType: 3,
            });
          }
          break;
        case "onDocumentReady":
          window.clearTimeout(timer);
          setStatus("ready");
          break;
        case "onError":
          fail();
          break;
      }
    }

    setStatus("loading");
    window.addEventListener("message", onMessage);
    child.src = entry.href;

    async function convert() {
      try {
        const converter = new Worker(new URL("x2t/preview-worker.js", root));
        worker = converter;
        converter.onerror = fail;
        converter.onmessage = ({ data }) => {
          converter.terminate();
          if (request.signal.aborted) return;
          if (!(data.binary instanceof ArrayBuffer)) {
            fail();
            return;
          }
          converted = data;
          open();
        };
        const bytes = await documentBytes(url, request.signal);
        if (request.signal.aborted) return;
        converter.postMessage({ extension, bytes }, [bytes]);
      } catch {
        if (!request.signal.aborted) fail();
      }
    }
    void convert();

    return () => {
      request.abort();
      window.clearTimeout(timer);
      worker?.terminate();
      window.removeEventListener("message", onMessage);
    };
  }, [locale, name, url]);

  return (
    <div className="relative h-full min-h-0">
      {status !== "error" && (
        <iframe
          ref={frame}
          title={`Euro-Office · ${name}`}
          sandbox="allow-scripts"
          referrerPolicy="no-referrer"
          className={`absolute inset-0 size-full border-0 ${status === "ready" ? "" : "invisible"}`}
        />
      )}
      {status === "loading" && (
        <p className="p-4 text-sm text-muted" role="status">
          {t("Datei wird geladen …")}
        </p>
      )}
      {status === "error" && (
        <p className="p-4 text-sm text-danger" role="alert">
          {t(
            "Die Office-Vorschau konnte nicht geöffnet werden. Du kannst die Datei herunterladen.",
          )}
        </p>
      )}
    </div>
  );
}

export const OpenOfficePreview = createContext<
  ((name: string, url: string) => void) | null
>(null);

export function OfficePreviews({
  scope,
  children,
}: {
  scope: string;
  children: ReactNode;
}) {
  const [file, setFile] = useState<{
    scope: string;
    name: string;
    url: string;
    open: boolean;
  }>();
  const open = useCallback(
    (name: string, url: string) => {
      setFile({ scope, name, url, open: true });
    },
    [scope],
  );
  const close = useCallback(() => {
    setFile((current) => current && { ...current, open: false });
  }, []);

  useEffect(() => {
    if (!file) return;
    if (file.scope !== scope) {
      setFile(undefined);
      return;
    }
    if (file.open) return;
    // Retain just the most recent viewer in memory for a short reopen.
    const timeout = window.setTimeout(() => setFile(undefined), 45_000);
    return () => window.clearTimeout(timeout);
  }, [file, scope]);

  return (
    <OpenOfficePreview.Provider value={open}>
      {children}
      {file?.scope === scope && (
        <OfficePreview key={file.url} {...file} onClose={close} />
      )}
    </OpenOfficePreview.Provider>
  );
}

function OfficePreview({
  name,
  url,
  open,
  onClose,
}: {
  name: string;
  url: string;
  open: boolean;
  onClose: () => void;
}) {
  const { t } = useI18n();
  const dialog = useRef<HTMLDialogElement>(null);
  const heading = useId();
  useEffect(() => {
    const element = dialog.current;
    if (!element) return;
    if (open && !element.open) element.showModal();
    if (!open && element.open) element.close();
  }, [open]);
  return (
    <dialog
      ref={dialog}
      aria-labelledby={heading}
      className="m-0 h-dvh w-full max-h-none max-w-none overflow-hidden bg-background p-0 text-foreground open:flex open:flex-col"
      onCancel={(event) => {
        event.preventDefault();
        onClose();
      }}
    >
      <header className="flex shrink-0 items-center gap-3 border-b border-border p-3">
        <h2 id={heading} className="min-w-0 flex-1 truncate font-medium">
          {name}
        </h2>
        <a
          href={url}
          download={name}
          className="shrink-0 text-sm text-accent underline-offset-4 hover:underline"
        >
          {t("Herunterladen")}
        </a>
        <Button variant="ghost" size="sm" onPress={onClose}>
          {t("Schließen")}
        </Button>
      </header>
      <div className="min-h-0 flex-1">
        <OfficeDocument name={name} url={url} />
      </div>
    </dialog>
  );
}
