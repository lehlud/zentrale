export type Attachment = {
  id: string;
  name: string;
  mimeType: string;
  size: number;
  path: string;
  machineId: string;
};

export const MAX_ATTACHMENT_BYTES = 25 * 1024 * 1024;
export const MAX_ATTACHMENT_TOTAL = 100 * 1024 * 1024;
export const MAX_ATTACHMENTS = 10;

export function isPreviewImage(mimeType: string) {
  return /^image\/(png|jpeg|gif|webp)$/.test(mimeType);
}

export function attachedFiles(value: {
  id: string;
  attachments?: Attachment[];
}): Attachment[] {
  return Array.isArray(value.attachments) ? value.attachments : [];
}

export function attachmentPath(agentId: string, id: string) {
  const preview =
    (globalThis as typeof globalThis & { __ZENTRALE_PREVIEW_BASE__?: string })
      .__ZENTRALE_PREVIEW_BASE__ ?? "/";
  return `${preview.replace(/\/?$/, "/")}attachments/${encodeURIComponent(agentId)}/${encodeURIComponent(id)}`;
}

export function largeTextPaste(text: string) {
  return text.length > 12000 || text.split("\n").length > 200;
}

export async function uploadAttachment(
  agentId: string,
  id: string,
  file: File,
  signal: AbortSignal,
): Promise<Attachment> {
  const response = await fetch(
    `${attachmentPath(agentId, id)}?name=${encodeURIComponent(file.name)}`,
    {
      method: "POST",
      credentials: "same-origin",
      redirect: "error",
      headers: { "Content-Type": file.type || "application/octet-stream" },
      body: file,
      signal,
    },
  );
  if (!response.ok) {
    const body: unknown = await response.json().catch(() => null);
    const detail =
      body &&
      typeof body === "object" &&
      "message" in body &&
      typeof body.message === "string"
        ? body.message
        : "";
    throw new Error(
      `Upload HTTP ${response.status}${detail ? `: ${detail}` : ""}`,
    );
  }
  const result: unknown = await response.json();
  if (
    !result ||
    typeof result !== "object" ||
    !("id" in result) ||
    result.id !== id ||
    !("name" in result) ||
    typeof result.name !== "string" ||
    !("mimeType" in result) ||
    typeof result.mimeType !== "string" ||
    !("size" in result) ||
    typeof result.size !== "number" ||
    !("path" in result) ||
    typeof result.path !== "string" ||
    !("machineId" in result) ||
    typeof result.machineId !== "string"
  ) {
    throw new Error("Ungültige Upload-Bestätigung.");
  }
  return result as Attachment;
}
