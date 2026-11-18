import { Button } from "@heroui/react";
import {
  getDocument,
  GlobalWorkerOptions,
  type PDFDocumentProxy,
  type RenderTask,
} from "pdfjs-dist";
import worker from "pdfjs-dist/build/pdf.worker.min.mjs?url";
import { useEffect, useRef, useState } from "react";

import { useI18n } from "./preferences";

GlobalWorkerOptions.workerSrc = worker;

export default function WorkspacePDF({ data }: { data: Uint8Array }) {
  const { t } = useI18n();
  const [pdf, setPDF] = useState<PDFDocumentProxy>();
  const [page, setPage] = useState(1);
  const [width, setWidth] = useState(0);
  const [error, setError] = useState(false);
  const canvas = useRef<HTMLCanvasElement>(null);
  const container = useRef<HTMLDivElement>(null);

  useEffect(() => {
    let live = true;
    const task = getDocument({ data: data.slice(), useWasm: false });
    void task.promise.then(
      (document) => {
        if (live) setPDF(document);
      },
      () => {
        if (live) setError(true);
      },
    );
    return () => {
      live = false;
      void task.destroy().catch(() => {});
    };
  }, [data]);

  useEffect(() => {
    if (!container.current) return;
    const observer = new ResizeObserver(([entry]) =>
      setWidth(Math.floor(entry.contentRect.width)),
    );
    observer.observe(container.current);
    return () => observer.disconnect();
  }, []);

  useEffect(() => {
    const target = canvas.current;
    if (!target || !pdf || width < 1) return;
    let live = true;
    let render: RenderTask | undefined;
    void (async () => {
      try {
        const source = await pdf.getPage(page);
        if (!live) return;
        const base = source.getViewport({ scale: 1 });
        const displayScale = Math.min(width / base.width, 2);
        const scale = Math.min(
          displayScale * Math.min(devicePixelRatio, 2),
          4096 / Math.max(base.width, base.height),
        );
        const viewport = source.getViewport({ scale });
        target.width = Math.max(1, Math.round(viewport.width));
        target.height = Math.max(1, Math.round(viewport.height));
        target.style.width = `${base.width * displayScale}px`;
        render = source.render({ canvas: target, viewport });
        await render.promise;
      } catch {
        if (live) setError(true);
      }
    })();
    return () => {
      live = false;
      render?.cancel();
    };
  }, [pdf, page, width]);

  return (
    <div className="min-h-full bg-default">
      {pdf && !error && (
        <div className="sticky top-0 z-10 flex flex-wrap items-center justify-center gap-3 border-b border-border bg-surface p-2">
          <Button
            variant="ghost"
            isDisabled={page === 1}
            onPress={() => setPage(page - 1)}
          >
            {t("Vorherige Seite")}
          </Button>
          <span className="text-sm tabular-nums">
            {page} / {pdf.numPages}
          </span>
          <Button
            variant="ghost"
            isDisabled={page === pdf.numPages}
            onPress={() => setPage(page + 1)}
          >
            {t("Nächste Seite")}
          </Button>
        </div>
      )}
      <div ref={container} className="mx-auto max-w-5xl py-4">
        {error ? (
          <p role="alert" className="px-4 text-sm text-danger">
            {t("Das PDF konnte nicht angezeigt werden.")}
          </p>
        ) : pdf ? (
          <canvas
            key={page}
            ref={canvas}
            role="img"
            aria-label={`${t("Seite")} ${page}`}
            className="mx-auto block max-w-full bg-white shadow-sm"
          />
        ) : (
          <p role="status" className="px-4 text-sm text-muted">
            {t("PDF wird geladen …")}
          </p>
        )}
      </div>
    </div>
  );
}
