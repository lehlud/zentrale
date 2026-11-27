import type { Goal, Message } from "./generated";
import type { PreferenceSnapshot } from "./preferences";

export type ExportedThread = {
  id: string;
  parentId: string;
  name: string;
  role: string;
  messages: Message[];
  goal?: Goal;
};

export type TranscriptArchive = {
  version: 1;
  exportedAt: string;
  projectId: string;
  projectName: string;
  projectDirectory: string;
  threads: ExportedThread[];
  attachments: Record<string, Record<string, string | null>>;
};

export type ExportDocument = TranscriptArchive & {
  preferences: PreferenceSnapshot;
};

const dataPlaceholder = ">__ZENTRALE_TRANSCRIPT_DATA__</script>";

export async function downloadTranscript(
  agentId: string,
  preferences: PreferenceSnapshot,
) {
  const [snapshotResponse, templateResponse] = await Promise.all([
    fetch(`/transcript-export/${encodeURIComponent(agentId)}`, {
      cache: "no-store",
    }),
    fetch("/assets/transcript-viewer.html", { cache: "no-store" }),
  ]);

  if (!snapshotResponse.ok || !templateResponse.ok) {
    throw new Error("HTML-Export fehlgeschlagen.");
  }

  const [snapshot, template]: [TranscriptArchive, string] = await Promise.all([
    snapshotResponse.json(),
    templateResponse.text(),
  ]);

  if (
    snapshot.version !== 1 ||
    !snapshot.threads.length ||
    !template.includes(dataPlaceholder)
  ) {
    throw new Error("HTML-Export fehlgeschlagen.");
  }

  // The snapshot is untrusted text. No literal '<' can terminate the inert JSON block.
  const data = JSON.stringify({ ...snapshot, preferences }).replaceAll(
    "<",
    "\\u003c",
  );
  const html = template.replace(dataPlaceholder, () => `>${data}</script>`);
  const file = new Blob([html], { type: "text/html;charset=utf-8" });
  const url = URL.createObjectURL(file);

  const link = document.createElement("a");
  const name = snapshot.threads[0].name.replaceAll(/[<>:"/\\|?*]/g, "_").trim();
  link.download = `${name.slice(0, 120) || "Zentrale"}.html`;
  link.href = url;
  link.click();

  setTimeout(() => URL.revokeObjectURL(url), 60_000);
}
