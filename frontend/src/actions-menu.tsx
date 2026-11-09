import { Button, Dropdown, Label } from "@heroui/react";
import { Icon } from "./icons";

export type MenuAction = {
  id: string;
  label: string;
  onAction: () => void;
  isDisabled?: boolean;
  destructive?: boolean;
};

export function ActionsMenu({
  label,
  actions,
  isDisabled = false,
}: {
  label: string;
  actions: MenuAction[];
  isDisabled?: boolean;
}) {
  return (
    <Dropdown>
      <Button
        isIconOnly
        size="sm"
        variant="ghost"
        className="min-h-11 min-w-11 shrink-0"
        aria-label={label}
        isDisabled={isDisabled}
      >
        <Icon name="more" className="size-5" />
      </Button>
      <Dropdown.Popover placement="bottom end">
        <Dropdown.Menu aria-label={label}>
          {actions.map((action) => (
            <Dropdown.Item
              key={action.id}
              id={action.id}
              textValue={action.label}
              className={`min-h-11 ${action.destructive ? "text-danger" : ""}`}
              isDisabled={action.isDisabled}
              onAction={action.onAction}
            >
              <Label className={action.destructive ? "text-danger" : undefined}>
                {action.label}
              </Label>
            </Dropdown.Item>
          ))}
        </Dropdown.Menu>
      </Dropdown.Popover>
    </Dropdown>
  );
}
