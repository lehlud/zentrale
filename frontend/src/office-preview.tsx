import { Button, Modal } from "@heroui/react";
import { useEffect, useRef, useState } from "react";

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
          customization: { plugins: false, macros: false, chat: false },
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

    void documentBytes(url, request.signal)
      .then((bytes) => {
        if (request.signal.aborted) return;

        worker = new Worker(new URL("x2t/preview-worker.js", root).href);
        worker.onerror = fail;
        worker.onmessage = ({ data }) => {
          worker?.terminate();
          if (request.signal.aborted) return;
          if (!(data.binary instanceof ArrayBuffer)) {
            fail();
            return;
          }

          converted = data;
          open();
        };
        worker.postMessage({ extension, bytes }, [bytes]);
      })
      .catch(() => {
        if (!request.signal.aborted) fail();
      });

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

export function OfficePreview({
  name,
  url,
  onClose,
}: {
  name: string;
  url: string;
  onClose: () => void;
}) {
  const { t } = useI18n();
  return (
    <Modal isOpen onOpenChange={(open) => !open && onClose()}>
      <Modal.Backdrop isDismissable={false}>
        <Modal.Container size="full">
          <Modal.Dialog className="flex h-dvh max-h-dvh flex-col gap-0 overflow-hidden p-0">
            <Modal.Header className="flex shrink-0 flex-row items-center gap-3 border-b border-border p-3">
              <Modal.Heading className="min-w-0 flex-1 truncate">
                {name}
              </Modal.Heading>
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
            </Modal.Header>
            <div className="min-h-0 flex-1">
              <OfficeDocument name={name} url={url} />
            </div>
          </Modal.Dialog>
        </Modal.Container>
      </Modal.Backdrop>
    </Modal>
  );
}
