import { createContext, useContext } from "react";

export const ProjectDirectory = createContext("");
export const ProjectIdentifier = createContext("");

export function relativeProjectPath(
  path: string,
  directory: string,
  projectId = "",
) {
  const root = directory.replace(/\/+$/, "");
  for (const candidate of [root, "/home/zentrale/project"]) {
    if (candidate && path.startsWith(candidate + "/"))
      return path.slice(candidate.length + 1);
  }
  // Resolve only this project's managed path, never an unrelated workspace.
  if (projectId) {
    const marker = `/projects/project-${projectId}/`;
    const index = path.indexOf(marker);
    if (path.startsWith("/") && index >= 0)
      return path.slice(index + marker.length);
  }
  return path;
}

export function useProjectPath() {
  const directory = useContext(ProjectDirectory);
  const projectId = useContext(ProjectIdentifier);
  return (path: string) => relativeProjectPath(path, directory, projectId);
}
