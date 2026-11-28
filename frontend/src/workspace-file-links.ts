import { createContext } from "react";
import { relativeProjectPath } from "./project-path";

export type WorkspaceFileLinks = {
  projectId: string;
  agentId: string;
  directory: string;
  open: (path: string) => void;
  warm?: (path: string) => void;
};

export const WorkspaceFileContext = createContext<WorkspaceFileLinks | null>(
  null,
);

export function workspaceLink(
  href: string | undefined,
  links: WorkspaceFileLinks,
  documentPath?: string,
) {
  if (!href || /^(?:[a-z][a-z\d+.-]*:|\/\/|#|\?)/i.test(href)) return;
  let pathname: string;
  try {
    pathname = decodeURIComponent(href.split(/[?#]/, 1)[0]);
  } catch {
    return;
  }
  if (/[\x00-\x1f\x7f\\]/.test(pathname)) return;
  const relative = relativeProjectPath(
    pathname,
    links.directory,
    links.projectId,
  );
  if (relative.startsWith("/")) return;
  const parts =
    documentPath && !pathname.startsWith("/")
      ? documentPath.split("/").slice(0, -1)
      : [];
  for (const part of relative.split("/")) {
    if (part === "." || !part) continue;
    if (part === "..") {
      if (!parts.length) return;
      parts.pop();
    } else {
      parts.push(part);
    }
  }
  return parts.length ? parts.join("/") : undefined;
}

export function workspaceFileURL(
  projectId: string,
  agentId: string,
  path: string,
) {
  const query = new URLSearchParams({ agent: agentId, path });
  return `/workspace-file/${encodeURIComponent(projectId)}?${query}`;
}
