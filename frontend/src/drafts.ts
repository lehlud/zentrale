import { useState } from "react";

// Drafts survive route/agent changes and page reloads within the current tab.
export function useDraft(key: string): [string, (value: string) => void] {
  const storageKey = `zentrale:draft:${key}`;
  const [value, setValue] = useState(
    () => sessionStorage.getItem(storageKey) ?? "",
  );
  return [
    value,
    (next) => {
      setValue(next);
      if (next) sessionStorage.setItem(storageKey, next);
      else sessionStorage.removeItem(storageKey);
    },
  ];
}
