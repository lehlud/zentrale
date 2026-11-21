import { Label, TextArea, TextField } from "@heroui/react";
import type { ComponentProps } from "react";

export function ChatInput({
  label,
  value,
  onChange,
  autoFocus,
  isDisabled,
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
        className="min-h-16 max-h-40 resize-none [field-sizing:content]"
        rows={2}
        {...input}
      />
    </TextField>
  );
}
