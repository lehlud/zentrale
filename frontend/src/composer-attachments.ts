import {
  useEffect,
  useRef,
  useState,
  type ClipboardEvent,
  type DragEvent,
} from "react";
import {
  MAX_ATTACHMENTS,
  MAX_ATTACHMENT_BYTES,
  MAX_ATTACHMENT_TOTAL,
  attachmentPath,
  isPreviewImage,
  largeTextPaste,
  uploadAttachment,
  type Attachment,
} from "./attachments";

type DraftAttachment = {
  id: string;
  file: File;
  preview: string;
  uploaded?: Attachment;
};

function releasePreview(item: DraftAttachment) {
  if (item.preview.startsWith("blob:")) URL.revokeObjectURL(item.preview);
}

export function useComposerAttachments(agentId: string) {
  const [items, setItems] = useState<DraftAttachment[]>([]);
  const itemsRef = useRef(items);
  itemsRef.current = items;
  const [error, setError] = useState("");
  const [uploading, setUploading] = useState(false);
  const [progress, setProgress] = useState(0);
  const [dragging, setDragging] = useState(false);
  const dragDepth = useRef(0);
  const controller = useRef<AbortController | null>(null);
  const alive = useRef(true);
  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
      controller.current?.abort();
      itemsRef.current.forEach(releasePreview);
    };
  }, []);
  const add = (files: File[]) => {
    if (controller.current || !files.length) return;
    if (itemsRef.current.length + files.length > MAX_ATTACHMENTS) {
      setError("Höchstens 10 Anhänge pro Nachricht.");
      return;
    }
    if (files.some((file) => file.size > MAX_ATTACHMENT_BYTES)) {
      setError("Eine Datei darf höchstens 25 MiB groß sein.");
      return;
    }
    if (
      itemsRef.current.reduce(
        (size, item) => size + (item.uploaded?.size ?? item.file.size),
        0,
      ) +
        files.reduce((size, file) => size + file.size, 0) >
      MAX_ATTACHMENT_TOTAL
    ) {
      setError("Anhänge dürfen zusammen höchstens 100 MiB groß sein.");
      return;
    }
    const next = files.map((file) => ({
      id: crypto.randomUUID(),
      file,
      preview: isPreviewImage(file.type) ? URL.createObjectURL(file) : "",
    }));
    itemsRef.current = [...itemsRef.current, ...next];
    setItems(itemsRef.current);
    setError("");
  };
  const remove = (id: string) => {
    if (controller.current) return;
    const item = itemsRef.current.find((item) => item.id === id);
    if (item) releasePreview(item);
    itemsRef.current = itemsRef.current.filter((item) => item.id !== id);
    setItems(itemsRef.current);
    setError("");
  };
  const clear = () => {
    itemsRef.current.forEach(releasePreview);
    itemsRef.current = [];
    setItems([]);
    setError("");
  };
  const upload = async (): Promise<Attachment[] | null> => {
    if (controller.current) return null;
    if (!itemsRef.current.length) return [];
    if (itemsRef.current.length > MAX_ATTACHMENTS) {
      setError("Höchstens 10 Anhänge pro Nachricht.");
      return null;
    }
    if (
      itemsRef.current.reduce(
        (size, item) => size + (item.uploaded?.size ?? item.file.size),
        0,
      ) > MAX_ATTACHMENT_TOTAL
    ) {
      setError("Anhänge dürfen zusammen höchstens 100 MiB groß sein.");
      return null;
    }
    const abort = new AbortController();
    controller.current = abort;
    setUploading(true);
    setProgress(0);
    setError("");
    // A cold VM can take longer than ordinary GraphQL requests. Cancellation
    // still reaches the upload and provisioner, and retries keep the same IDs.
    const timeout = setTimeout(() => abort.abort(), 180000);
    try {
      const results: Attachment[] = [];
      for (const item of itemsRef.current) {
        const uploaded =
          item.uploaded ??
          (await uploadAttachment(agentId, item.id, item.file, abort.signal));
        if (abort.signal.aborted || !alive.current) return null;
        results.push(uploaded);
        itemsRef.current = itemsRef.current.map((entry) =>
          entry.id === item.id ? { ...entry, uploaded } : entry,
        );
        setItems(itemsRef.current);
        setProgress(results.length);
      }
      return results;
    } catch (cause) {
      if (alive.current)
        setError(
          abort.signal.aborted
            ? "Upload abgebrochen. Entwurf und Anhänge bleiben erhalten."
            : (cause as Error).message,
        );
      return null;
    } finally {
      clearTimeout(timeout);
      controller.current = null;
      if (alive.current) setUploading(false);
    }
  };
  const paste = (event: ClipboardEvent<HTMLTextAreaElement>) => {
    const files = Array.from(event.clipboardData.files);
    if (files.length) {
      event.preventDefault();
      add(files);
      return;
    }
    const text = event.clipboardData.getData("text/plain");
    if (largeTextPaste(text)) {
      event.preventDefault();
      add([
        new File([text], "eingefuegter-text.txt", {
          type: "text/plain;charset=utf-8",
        }),
      ]);
    }
  };
  const restore = (attachments: Attachment[]) => {
    const existing = new Set(itemsRef.current.map((item) => item.id));
    const restored = attachments
      .filter((attachment) => !existing.has(attachment.id))
      .map((uploaded) => ({
        id: uploaded.id,
        file: new File([], uploaded.name, { type: uploaded.mimeType }),
        preview: isPreviewImage(uploaded.mimeType)
          ? attachmentPath(agentId, uploaded.id)
          : "",
        uploaded,
      }));
    // Retain all queued attachments on edit; let the user resolve an oversized
    // merged draft rather than silently dropping files from the canceled input.
    itemsRef.current = [...itemsRef.current, ...restored];
    setItems(itemsRef.current);
    if (itemsRef.current.length > MAX_ATTACHMENTS)
      setError("Höchstens 10 Anhänge pro Nachricht.");
  };
  const drop = (event: DragEvent<HTMLFormElement>) => {
    if (!event.dataTransfer.types.includes("Files")) return;
    event.preventDefault();
    dragDepth.current = 0;
    setDragging(false);
    if (
      Array.from(event.dataTransfer.items).some(
        (item) => item.webkitGetAsEntry?.()?.isDirectory,
      )
    ) {
      setError("Ordner bitte als Archiv anhängen.");
      return;
    }
    add(Array.from(event.dataTransfer.files));
  };
  const dragEnter = (event: DragEvent<HTMLFormElement>) => {
    if (!event.dataTransfer.types.includes("Files")) return;
    event.preventDefault();
    dragDepth.current++;
    setDragging(true);
  };
  const dragLeave = () => {
    if (--dragDepth.current <= 0) {
      dragDepth.current = 0;
      setDragging(false);
    }
  };
  const dragOver = (event: DragEvent<HTMLFormElement>) => {
    if (event.dataTransfer.types.includes("Files")) {
      event.preventDefault();
      event.dataTransfer.dropEffect = "copy";
    }
  };
  return {
    items,
    error,
    uploading,
    progress,
    dragging,
    add,
    remove,
    clear,
    upload,
    restore,
    paste,
    drop,
    dragEnter,
    dragLeave,
    dragOver,
    cancel: () => controller.current?.abort(),
  };
}
