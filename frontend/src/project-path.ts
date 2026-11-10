import { createContext, useContext } from "react";

export const ProjectDirectory = createContext("");

export function relativeProjectPath(path: string, directory: string) {
  const root = directory.replace(/\/+$/, "");
  return root && path.startsWith(root + "/")
    ? path.slice(root.length + 1)
    : path;
}

export function useProjectPath() {
  const directory = useContext(ProjectDirectory);
  return (path: string) => relativeProjectPath(path, directory);
}
