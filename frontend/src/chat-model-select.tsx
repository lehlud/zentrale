import { ListBox, Select } from "@heroui/react";

import type { ModelChoice } from "./generated";
import { modelSelectionKey, type ModelSelection } from "./model-selection";
import { useI18n } from "./preferences";

export const chatModelFields =
  "providerId modelId reasoningLevel displayName providerName";

function displayName(choice: ModelChoice) {
  return choice.displayName?.trim() || choice.modelId;
}

export function ChatModelSelect({
  value,
  presets,
  disabled,
  onChange,
}: {
  value: ModelChoice;
  presets: ModelChoice[];
  disabled?: boolean;
  onChange: (choice: ModelSelection) => void;
}) {
  const { t } = useI18n();
  const placeholder = t(
    presets.length ? "Modell auswählen" : "Keine Modelle freigegeben.",
  );

  function label(choice: ModelChoice) {
    const name = displayName(choice);
    const ambiguous = presets.some(
      (other) =>
        other.providerId !== choice.providerId &&
        (other.modelId === choice.modelId ||
          displayName(other).toLowerCase() === name.toLowerCase()),
    );

    return [
      name,
      ambiguous ? choice.providerName || choice.providerId : null,
      choice.reasoningLevel || t("Standard"),
    ]
      .filter(Boolean)
      .join(" · ");
  }

  return (
    <Select
      aria-label={t("Modell")}
      className="min-w-0"
      isDisabled={disabled || !presets.length}
      value={modelSelectionKey(value) || null}
      onChange={(key) => {
        const choice = presets.find((item) => modelSelectionKey(item) === key);
        if (choice) {
          const { providerId, modelId, reasoningLevel } = choice;
          onChange({ providerId, modelId, reasoningLevel });
        }
      }}
    >
      <Select.Trigger className="min-w-0">
        <Select.Value className="truncate">
          {value.modelId ? label(value) : placeholder}
        </Select.Value>
        <Select.Indicator className="shrink-0" />
      </Select.Trigger>
      <Select.Popover className="min-w-56 max-w-[90vw]">
        <ListBox>
          {presets.map((choice) => (
            <ListBox.Item
              key={modelSelectionKey(choice)}
              id={modelSelectionKey(choice)}
              textValue={label(choice)}
            >
              <span className="min-w-0 flex-1 break-words">
                {label(choice)}
              </span>
              <ListBox.ItemIndicator />
            </ListBox.Item>
          ))}
        </ListBox>
      </Select.Popover>
    </Select>
  );
}
