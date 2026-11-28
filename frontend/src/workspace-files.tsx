import { Button, Modal } from "@heroui/react";
import {
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";

import { MessageMarkdown } from "./message-markdown";
import { isOfficeFile, OpenOfficePreview } from "./office-preview";
import { useI18n } from "./preferences";
import { WorkspaceFileContext, workspaceFileURL } from "./workspace-file-links";

export function WorkspaceFiles({
  projectId,
  agentId,
  directory,
  children,
}: {
  projectId: string;
  agentId: string;
  directory: string;
  children: ReactNode;
}) {
  const [stack, setStack] = useState<{ id: number; path: string }[]>([]);
  const next = useRef(0);
  const office = useContext(OpenOfficePreview);
  const openOffice = office?.open;
  const warm = office?.warm;
  const open = useCallback(
    (path: string) => {
      if (openOffice && isOfficeFile(path)) {
        const url = workspaceFileURL(projectId, agentId, path);
        openOffice({
          name: path.split("/").at(-1) ?? path,
          url: `${url}&preview=office`,
          workspace: { path, download: url, nested: stack.length > 0 },
        });
        return;
      }
      const id = next.current++;
      setStack((files) => [...files, { id, path }]);
    },
    [agentId, openOffice, projectId, stack.length],
  );
  const close = (id: number) =>
    setStack((files) => (files.at(-1)?.id === id ? files.slice(0, -1) : files));
  const links = useMemo(
    () => ({ projectId, agentId, directory, open, warm }),
    [projectId, agentId, directory, open, warm],
  );
  return (
    <WorkspaceFileContext.Provider value={links}>
      {children}
      {stack.map(({ id, path }, index) => (
        <WorkspaceFileDialog
          key={id}
          path={path}
          url={workspaceFileURL(projectId, agentId, path)}
          nested={index > 0}
          onClose={() => close(id)}
        />
      ))}
    </WorkspaceFileContext.Provider>
  );
}

type Preview = "pdf" | "markdown" | "text" | "image";

function previewType(path: string): Preview | undefined {
  const name = path.split("/").at(-1)?.toLowerCase() ?? "";
  if (/\.(?:png|jpe?g|gif|webp)$/.test(name)) return "image";
  if (name.endsWith(".pdf")) return "pdf";
  if (/\.(?:md|markdown|mdown)$/.test(name)) return "markdown";
  if (
    /\.(?:txt|log|csv|tsv|json|jsonl|ya?ml|toml|ini|xml|tex|rst|py|sh|go|rs|js|tsx?|css|html?|sql)$/.test(
      name,
    ) ||
    /^(?:readme|license|makefile|dockerfile)$/.test(name)
  )
    return "text";
}

function WorkspaceFileDialog({
  path,
  url,
  nested,
  onClose,
}: {
  path: string;
  url: string;
  nested: boolean;
  onClose: () => void;
}) {
  const { t } = useI18n();
  const format = previewType(path);
  const name = path.split("/").at(-1) ?? path;
  const [preview, setPreview] = useState<{
    text?: string;
    image?: string;
    error?: string;
  }>({});
  const activeDownload = useRef<AbortController | null>(null);
  const [downloading, setDownloading] = useState(false);
  const [downloadError, setDownloadError] = useState("");
  useEffect(() => () => activeDownload.current?.abort(), []);

  const download = async () => {
    if (activeDownload.current) return;
    const request = new AbortController();
    activeDownload.current = request;
    setDownloading(true);
    setDownloadError("");
    try {
      const response = await fetch(url, { signal: request.signal });
      if (!response.ok) {
        setDownloadError(fileError(response.status, false));
        return;
      }
      const blob = await response.blob();
      if (request.signal.aborted) return;
      const href = URL.createObjectURL(blob);
      const link = document.createElement("a");
      link.href = href;
      link.download = name;
      link.click();
      setTimeout(() => URL.revokeObjectURL(href), 1000);
    } catch {
      if (!request.signal.aborted) setDownloadError(fileError(0, false));
    } finally {
      activeDownload.current = null;
      if (!request.signal.aborted) setDownloading(false);
    }
  };

  useEffect(() => {
    if (!format || format === "pdf") return;
    const request = new AbortController();
    let image: string | undefined;
    void (async () => {
      try {
        const response = await fetch(
          `${url}&preview=${format === "image" ? "image" : "text"}`,
          {
            signal: request.signal,
          },
        );
        if (!response.ok) {
          if (!request.signal.aborted)
            setPreview({ error: fileError(response.status, true) });
          return;
        }
        if (format === "image") {
          const blob = await response.blob();
          if (!request.signal.aborted) {
            image = URL.createObjectURL(blob);
            setPreview({ image });
          }
          return;
        }
        const text = await response.text();
        if (!request.signal.aborted) setPreview({ text });
      } catch {
        if (!request.signal.aborted)
          setPreview({ error: "Die Datei konnte nicht geladen werden." });
      }
    })();
    return () => {
      request.abort();
      if (image) URL.revokeObjectURL(image);
    };
  }, [format, url]);

  const loading = (
    <p role="status" className="p-4 text-sm text-muted">
      {t("Datei wird geladen …")}
    </p>
  );
  return (
    <Modal
      isOpen
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
    >
      <Modal.Backdrop isDismissable={false}>
        <Modal.Container size="full" scroll="inside">
          <Modal.Dialog className="flex h-dvh max-h-dvh flex-col overflow-hidden p-0">
            <Modal.Header className="flex shrink-0 flex-row flex-wrap items-center gap-3 border-b border-border p-3">
              <div className="min-w-0 flex-1">
                <Modal.Heading className="truncate" title={path}>
                  {name}
                </Modal.Heading>
                {path !== name && (
                  <p className="truncate text-xs text-muted">{path}</p>
                )}
              </div>
              {format !== "pdf" && (
                <Button
                  variant="secondary"
                  className="min-h-11"
                  isPending={downloading}
                  onPress={download}
                >
                  {t("Herunterladen")}
                </Button>
              )}
              <Button variant="ghost" className="min-h-11" onPress={onClose}>
                {t(nested ? "Zurück" : "Schließen")}
              </Button>
            </Modal.Header>
            {downloadError && (
              <p
                role="alert"
                className="shrink-0 px-4 py-2 text-sm text-danger"
              >
                {t(downloadError)}
              </p>
            )}
            <div className="min-h-0 flex-1 overflow-auto">
              {format === "pdf" ? (
                <PDFViewer name={name} url={url} onClose={onClose} />
              ) : !format ? (
                <p className="p-4 text-sm text-muted">
                  {t("Für diesen Dateityp gibt es keine Vorschau.")}
                </p>
              ) : preview.error ? (
                <p role="alert" className="p-4 text-sm text-danger">
                  {t(preview.error)}
                </p>
              ) : preview.image ? (
                <div className="flex h-full items-center justify-center p-4">
                  <img
                    src={preview.image}
                    alt={name}
                    className="max-h-full max-w-full object-contain"
                  />
                </div>
              ) : preview.text !== undefined ? (
                <div className="mx-auto max-w-5xl p-4 sm:p-8">
                  {format === "markdown" ? (
                    <MessageMarkdown
                      content={preview.text}
                      documentPath={path}
                    />
                  ) : (
                    <pre className="whitespace-pre-wrap break-words font-mono text-sm">
                      {preview.text}
                    </pre>
                  )}
                </div>
              ) : (
                loading
              )}
            </div>
          </Modal.Dialog>
        </Modal.Container>
      </Modal.Backdrop>
    </Modal>
  );
}

function PDFViewer({
  name,
  url,
  onClose,
}: {
  name: string;
  url: string;
  onClose: () => void;
}) {
  const frame = useRef<HTMLIFrameElement>(null);
  useEffect(() => {
    const element = frame.current;
    if (!element) return;
    let current: Document | null = null;
    const keydown = (event: KeyboardEvent) => {
      // Let PDF.js handle Escape in its own dialogs and toolbars first.
      if (
        event.key === "Escape" &&
        !event.repeat &&
        !current?.querySelector("dialog[open]")
      ) {
        setTimeout(() => {
          if (!event.defaultPrevented) onClose();
        }, 0);
      }
    };
    const listen = () => {
      current?.defaultView?.removeEventListener("keydown", keydown, true);
      current = element.contentDocument;
      current?.defaultView?.addEventListener("keydown", keydown, true);
    };
    listen();
    element.addEventListener("load", listen);
    return () => {
      element.removeEventListener("load", listen);
      current?.defaultView?.removeEventListener("keydown", keydown, true);
    };
  }, [onClose]);
  return (
    <iframe
      ref={frame}
      title={name}
      src={`/pdfjs/web/viewer.html?${new URLSearchParams({ file: url })}`}
      className="block h-full w-full border-0"
    />
  );
}

function fileError(status: number, preview: boolean) {
  if (status === 413)
    return preview
      ? "Die Datei ist zu groß für die Vorschau."
      : "Die Datei ist zu groß für den Download.";
  if (status === 404) return "Die Datei wurde im Projekt nicht gefunden.";
  return "Die Datei konnte nicht geladen werden.";
}
