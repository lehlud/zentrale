import { Label, TextArea, TextField } from "@heroui/react";
import type { ComponentProps } from "react";

export function ChatInput({
  label,
  value,
  onChange,
  autoFocus,
  isDisabled,
  compact = false,
  ...input
}: Pick<
  ComponentProps<typeof TextField>,
  "value" | "onChange" | "autoFocus" | "isDisabled"
> &
  Pick<
    ComponentProps<typeof TextArea>,
    "placeholder" | "onPaste" | "onKeyDown"
  > & {
    label: string;
    compact?: boolean;
  }) {
  return (
    <TextField
      fullWidth
      className="min-w-0 flex-1"
      value={value}
      onChange={onChange}
      autoFocus={autoFocus}
      isDisabled={isDisabled}
    >
      <Label className="sr-only">{label}</Label>
      <TextArea
        className={
          compact
            ? "min-h-11 max-h-40 resize-none border-0 bg-transparent px-1 py-3 shadow-none focus:shadow-none [field-sizing:content]"
            : "min-h-16 max-h-40 resize-none [field-sizing:content]"
        }
        rows={compact ? 1 : 2}
        {...input}
      />
    </TextField>
  );
}
