import { Button } from "@heroui/react";
import {
  createContext,
  useCallback,
  useEffect,
  useEffectEvent,
  useId,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";

import { createPortal } from "react-dom";

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

type OfficeFile = {
  name: string;
  url: string;
  workspace?: { path: string; download: string; nested: boolean };
};

type OfficeSlot = {
  id: number;
  editor: string;
  file?: OfficeFile & { scope: string };
  used: number;
  failed?: boolean;
};

let officeRoot: Promise<URL> | undefined;

function officeAssets() {
  const root = new URL("/office/", location.href);
  return (officeRoot ??= fetch(new URL("VERSION", root), { cache: "no-cache" })
    .then(async (response) => {
      if (!response.ok) return root;
      const version = (await response.text()).trim();
      return /^[a-f0-9]{20}$/.test(version)
        ? new URL(`${version}/`, root)
        : root;
    })
    .catch(() => {
      officeRoot = undefined;
      return root;
    }));
}

function OfficeDocument({
  editor,
  file,
  onReady,
  onError,
}: {
  editor: string;
  file?: OfficeFile;
  onReady: () => void;
  onError: () => void;
}) {
  const { t, locale } = useI18n();
  const frame = useRef<HTMLIFrameElement>(null);
  const runtime = useRef<Promise<void>>(null);
  const active = useRef<AbortController>(null);
  const [status, setStatus] = useState<
    "starting" | "loading" | "ready" | "error"
  >("starting");
  const url = file?.url;
  const name = file?.name;
  const finish = useEffectEvent((result: "ready" | "error") => {
    if (active.current?.signal.aborted) return;
    active.current?.abort();
    setStatus(result);
    if (result === "ready") onReady();
    else onError();
  });

  useEffect(() => {
    const child = frame.current;
    if (!child) return;
    let disposed = false;
    let started: () => void;
    runtime.current = new Promise<void>((resolve) => {
      started = resolve;
    });

    function onMessage(event: MessageEvent) {
      if (event.source !== child?.contentWindow || event.origin !== "null")
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
          started();
          setStatus((current) =>
            current === "starting" ? "loading" : current,
          );
          break;
        case "cryptPadSendMessageFromOO":
          if (message.data?.msg?.type === "auth") {
            // Supply the viewer's local collaboration session inside the opaque frame.
            sendToOffice(child, "cryptPadMessageToOO", {
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
          finish("ready");
          break;
        case "onError":
          finish("error");
          break;
      }
    }

    window.addEventListener("message", onMessage);
    void officeAssets().then((root) => {
      if (disposed) return;
      const entry = new URL(`web-apps/apps/${editor}/main/index.html`, root);
      entry.search = new URLSearchParams({
        lang: locale,
        parentOrigin: location.origin,
        frameEditorId: "zentrale-office-preview",
        mode: "view",
      }).toString();
      child.src = entry.href;
    });
    return () => {
      disposed = true;
      window.removeEventListener("message", onMessage);
    };
  }, [editor, locale]);

  useEffect(() => {
    const child = frame.current;
    const started = runtime.current;
    if (!url || !name || !child || !started) return;
    const request = new AbortController();
    active.current = request;
    const timer = window.setTimeout(() => finish("error"), 60_000);
    request.signal.addEventListener("abort", () => window.clearTimeout(timer), {
      once: true,
    });

    void Promise.all([
      convertOfficeFile(url, extensionOf(name), request),
      started,
    ])
      .then(([document]) => {
        if (!request.signal.aborted)
          showOfficeFile(child, name, locale, document);
      })
      .catch(() => {
        if (!request.signal.aborted) finish("error");
      });

    return () => request.abort();
  }, [name, url, locale]);

  return (
    <div className="relative h-full min-h-0">
      <iframe
        ref={frame}
        title={name ? `Euro-Office · ${name}` : "Euro-Office"}
        sandbox="allow-scripts"
        referrerPolicy="no-referrer"
        className={`absolute inset-0 size-full border-0 ${status === "ready" ? "" : "invisible"}`}
      />
      {file && (status === "loading" || status === "starting") && (
        <p className="p-4 text-sm text-muted" role="status">
          {t(
            status === "starting"
              ? "Office-Vorschau wird gestartet …"
              : "Datei wird geladen …",
          )}
        </p>
      )}
      {file && status === "error" && (
        <p className="p-4 text-sm text-danger" role="alert">
          {t(
            "Die Office-Vorschau konnte nicht geöffnet werden. Du kannst die Datei herunterladen.",
          )}
        </p>
      )}
    </div>
  );
}

async function convertOfficeFile(
  url: string,
  extension: string,
  request: AbortController,
): Promise<ConvertedDocument> {
  let worker: Worker | undefined;
  try {
    return await new Promise((resolve, reject) => {
      request.signal.addEventListener(
        "abort",
        () => reject(request.signal.reason),
        { once: true },
      );
      const runtime = officeAssets().then((root) => {
        request.signal.throwIfAborted();
        const converter = new Worker(new URL("x2t/preview-worker.js", root));
        worker = converter;
        converter.onerror = reject;
        converter.onmessage = ({ data }) => {
          if (data.binary instanceof ArrayBuffer) resolve(data);
          else reject(new Error("Office conversion failed"));
        };
        return converter;
      });
      void Promise.all([runtime, documentBytes(url, request.signal)])
        .then(([converter, bytes]) => {
          request.signal.throwIfAborted();
          converter.postMessage({ extension, bytes }, [bytes]);
        })
        .catch(reject);
    });
  } finally {
    worker?.terminate();
  }
}

function showOfficeFile(
  child: HTMLIFrameElement,
  name: string,
  locale: string,
  { binary, images }: ConvertedDocument,
) {
  child.contentWindow?.postMessage(
    { type: "zentrale:office-images", images },
    "*",
    images.map((image) => image[2]),
  );
  sendToOffice(child, "init", {
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
  sendToOffice(child, "openDocument", {
    doc: {
      title: name,
      fileType: extensionOf(name),
      key: "local-preview",
      permissions: {
        edit: false,
        print: false,
        download: false,
        comment: false,
      },
    },
  });
  child.contentWindow?.postMessage(
    { command: "openDocumentFromBinary", data: binary },
    "*",
    [binary],
  );
}

function sendToOffice(
  child: HTMLIFrameElement | null,
  command: string,
  data: unknown,
) {
  // The trusted editor is in a sandboxed iframe with an opaque origin.
  child?.contentWindow?.postMessage(JSON.stringify({ command, data }), "*");
}

export const OpenOfficePreview = createContext<{
  open: (file: OfficeFile) => void;
  warm: (name: string) => void;
} | null>(null);

export function OfficePreviews({
  scope,
  children,
}: {
  scope: string;
  children: ReactNode;
}) {
  const [{ slots, selected }, setSessions] = useState<{
    slots: OfficeSlot[];
    selected?: number;
  }>({ slots: [] });
  const clock = useRef(0);
  const { locale } = useI18n();

  const warmEditor = useCallback((editor: string) => {
    const id = ++clock.current;
    setSessions((current) => {
      if (current.slots.some((slot) => !slot.file)) return current;
      return {
        ...current,
        slots: [...current.slots, { id, editor, used: id }],
      };
    });
  }, []);
  const warm = useCallback(
    (name: string) => {
      const editor = editors[extensionOf(name)];
      if (editor) warmEditor(editor);
    },
    [warmEditor],
  );

  const open = useCallback(
    (file: OfficeFile) => {
      const editor = editors[extensionOf(file.name)];
      if (!editor) return;
      const used = ++clock.current;
      setSessions(({ slots }) => {
        const existing =
          !file.workspace &&
          slots.find(
            (slot) =>
              !slot.failed &&
              slot.file?.url === file.url &&
              slot.file.scope === scope,
          );
        if (existing) {
          return {
            slots: slots.map((slot) =>
              slot.id === existing.id ? { ...slot, used } : slot,
            ),
            selected: existing.id,
          };
        }

        const spare = slots.find(
          (slot) => !slot.file && slot.editor === editor,
        );
        const next = {
          id: spare?.id ?? used,
          editor,
          used,
          file: { ...file, scope },
        };
        const previous = slots
          .filter(
            (slot) =>
              slot.file?.scope === scope &&
              slot.file.url !== file.url &&
              !slot.failed &&
              !slot.file.workspace,
          )
          .sort((a, b) => b.used - a.used)[0];
        // Keep the previous attachment. Preserve DOM order so moving an
        // iframe never causes the browser to destroy its browsing context.
        const nextSlots = slots.flatMap((slot) =>
          slot === spare ? [next] : slot === previous ? [slot] : [],
        );
        if (!spare) nextSlots.push(next);
        return { slots: nextSlots, selected: next.id };
      });
    },
    [scope],
  );

  useEffect(() => {
    setSessions(({ slots }) => ({
      slots: slots.filter((slot) => !slot.file || slot.file.scope === scope),
    }));
  }, [scope]);

  const close = useCallback(() => {
    setSessions(({ slots }) => ({
      slots: slots.filter((slot) => !slot.file?.workspace),
    }));
  }, []);
  const fail = useCallback((id: number) => {
    officeRoot = undefined;
    setSessions((current) => ({
      ...current,
      slots: current.slots.flatMap((slot) =>
        slot.id !== id ? [slot] : slot.file ? [{ ...slot, failed: true }] : [],
      ),
    }));
  }, []);
  const controls = useMemo(() => ({ open, warm }), [open, warm]);
  return (
    <OpenOfficePreview.Provider value={controls}>
      {children}
      {createPortal(
        slots.map(({ id, editor, file }) => (
          <OfficePreview
            key={`${locale}/${id}`}
            editor={editor}
            file={file}
            open={id === selected && file?.scope === scope}
            onClose={close}
            onReady={() => warmEditor(editor)}
            onError={() => fail(id)}
          />
        )),
        document.body,
      )}
    </OpenOfficePreview.Provider>
  );
}

function OfficePreview({
  editor,
  file,
  open,
  onClose,
  onReady,
  onError,
}: {
  editor: string;
  file?: OfficeFile;
  open: boolean;
  onClose: () => void;
  onReady: () => void;
  onError: () => void;
}) {
  const { t } = useI18n();
  const dialog = useRef<HTMLDialogElement>(null);
  const heading = useId();
  useEffect(() => {
    const element = dialog.current;
    if (!element) return;
    if (open && !element.open) element.showModal();
    if (!open && element.open) element.close();
    if (!open) return;
    function dismissFromFrame(event: MessageEvent) {
      if (
        event.origin === "null" &&
        event.source === element?.querySelector("iframe")?.contentWindow &&
        event.data?.type === "zentrale:office-dismiss"
      ) {
        element?.close();
        onClose();
      }
    }
    window.addEventListener("message", dismissFromFrame);
    return () => window.removeEventListener("message", dismissFromFrame);
  }, [onClose, open]);
  const dismiss = () => {
    dialog.current?.close();
    onClose();
  };
  return (
    <dialog
      ref={dialog}
      data-react-aria-top-layer
      aria-labelledby={heading}
      className="m-0 h-dvh w-full max-h-none max-w-none overflow-hidden bg-background p-0 text-foreground open:flex open:flex-col"
      onCancel={(event) => {
        event.preventDefault();
        dismiss();
      }}
      onKeyDown={(event) => {
        if (event.key !== "Escape" || event.defaultPrevented) return;
        event.preventDefault();
        event.stopPropagation();
        dismiss();
      }}
    >
      <header className="flex shrink-0 items-center gap-3 border-b border-border p-3">
        <div className="min-w-0 flex-1">
          <h2 id={heading} className="truncate font-medium">
            {file?.name}
          </h2>
          {file?.workspace && file.workspace.path !== file.name && (
            <p className="truncate text-xs text-muted">{file.workspace.path}</p>
          )}
        </div>
        {file && (
          <a
            href={file.workspace?.download ?? file.url}
            download={file.name}
            className="shrink-0 text-sm text-accent underline-offset-4 hover:underline"
          >
            {t("Herunterladen")}
          </a>
        )}
        <Button variant="ghost" size="sm" onPress={dismiss}>
          {t(file?.workspace?.nested ? "Zurück" : "Schließen")}
        </Button>
      </header>
      <div className="min-h-0 flex-1">
        <OfficeDocument
          editor={editor}
          file={file}
          onReady={onReady}
          onError={onError}
        />
      </div>
    </dialog>
  );
}
