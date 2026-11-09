import { useSyncExternalStore } from "react";

const pages = new Set([
  "projects",
  "machines",
  "keys",
  "providers",
  "settings",
  "editor",
]);
function subscribe(listener: () => void) {
  window.addEventListener("hashchange", listener);
  return () => window.removeEventListener("hashchange", listener);
}
function snapshot() {
  return location.hash;
}
export function useRoute() {
  const hash = useSyncExternalStore(subscribe, snapshot);
  const [pathname, query = ""] = hash.replace(/^#\/?/, "").split("?");
  const [requestedPage, projectId = ""] = pathname.split("/");
  return {
    page: pages.has(requestedPage) ? requestedPage : "projects",
    projectId: decodeURIComponent(projectId),
    params: new URLSearchParams(query),
  };
}
export function navigate(page: string, projectId = "") {
  location.hash = `/${page}${projectId ? `/${encodeURIComponent(projectId)}` : ""}`;
}
export function setRouteParameter(key: string, value: string) {
  const [pathname, query = ""] = location.hash.split("?");
  const params = new URLSearchParams(query);
  params.set(key, value);
  location.hash = pathname + "?" + params.toString();
}

export function editorLaunchHref(projectId: string, agentId = "") {
  const path = `#/editor/${encodeURIComponent(projectId)}`;
  return agentId ? `${path}?agent=${encodeURIComponent(agentId)}` : path;
}

export function conversationHref(projectId: string, agentId: string) {
  return `#/projects/${encodeURIComponent(projectId)}?agent=${encodeURIComponent(agentId)}`;
}
