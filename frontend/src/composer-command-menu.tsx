import { Autocomplete, Label, ListBox, ListBoxItem } from "@heroui/react";
import { useState, type ReactNode } from "react";

import { commandSuggestions } from "./composer-commands";
import { useI18n } from "./preferences";

export function ComposerCommandMenu({
  value,
  onChange,
  onCommand,
  children,
}: {
  value: string;
  onChange: (value: string) => void;
  onCommand: (name: string) => void;
  children: (open: boolean) => ReactNode;
}) {
  const { t } = useI18n();
  const [focused, setFocused] = useState(false);
  const [dismissed, setDismissed] = useState<string | null>(null);
  const suggestions = commandSuggestions(value);
  const open = focused && dismissed !== value && suggestions.length > 0;

  return (
    <div
      className="relative min-w-0 flex-1"
      onFocusCapture={() => setFocused(true)}
      onBlurCapture={(event) => {
        if (!event.currentTarget.contains(event.relatedTarget))
          setFocused(false);
      }}
      onKeyDownCapture={(event) => {
        if (event.nativeEvent.isComposing) return;

        if (open && event.key === "Enter" && event.shiftKey) {
          // Keep the textarea's newline behavior; React Aria otherwise treats
          // modified Enter as an action on the highlighted command as well.
          event.stopPropagation();
          return;
        }

        if (open && event.key === "Escape") {
          event.preventDefault();
          setDismissed(value);
        } else if (event.key === "ArrowDown" && dismissed === value) {
          setDismissed(null);
        }
      }}
    >
      <Autocomplete.Filter
        inputValue={value}
        onInputChange={onChange}
        filter={() => true}
      >
        {children(open)}
        {open && (
          <ListBox
            aria-label={t("Befehle")}
            items={suggestions}
            className="absolute inset-x-0 bottom-full z-30 mb-2 max-h-72 overflow-auto rounded-xl border border-border bg-surface p-1 shadow-lg"
            onAction={(key) => onCommand(String(key))}
          >
            {(command) => (
              <ListBoxItem
                id={command.name}
                textValue={`/${command.name}`}
                className="min-h-12"
              >
                <div className="flex min-w-0 flex-col gap-0.5">
                  <Label className="font-mono">/{command.name}</Label>
                  <span className="text-xs text-muted">
                    {t(command.description)}
                  </span>
                </div>
              </ListBoxItem>
            )}
          </ListBox>
        )}
      </Autocomplete.Filter>
    </div>
  );
}
