import { Icon } from "./icons";
import { Button, Dropdown, Label } from "@heroui/react";
import { useEffect, useRef } from "react";

import type { Agent } from "./generated";
import { useI18n } from "./preferences";

export function AgentSwitcher({
  current,
  agents,
  onSelect,
  autoFocus,
  onFocus,
}: {
  current?: Agent;
  agents: Agent[];
  onSelect: (id: string) => void;
  autoFocus: boolean;
  onFocus: () => void;
}) {
  const { t } = useI18n();
  const name = current?.name || t("Agent");
  const conversations = agents.filter((agent) => agent.role !== "approver");
  const selected =
    current?.role === "approver" ? current.parentId : current?.id;
  const trigger = useRef<HTMLButtonElement>(null);
  useEffect(() => {
    if (!autoFocus) return;
    // The previous menu restores focus as it closes. Wait until that cleanup
    // has completed before focusing the newly mounted conversation's trigger.
    const frame = requestAnimationFrame(() => {
      trigger.current?.focus({ preventScroll: true });
      onFocus();
    });
    return () => cancelAnimationFrame(frame);
  }, [autoFocus, current?.id, onFocus]);

  return (
    <Dropdown>
      <Button
        ref={trigger}
        variant="ghost"
        className="min-h-11 min-w-0 max-w-full shrink justify-start px-2 text-base font-medium"
        aria-label={t("Agent wechseln: {name}", { name })}
      >
        <span className="min-w-0 truncate">{name}</span>
        <Icon name="chevron" className="size-4 text-muted" />
      </Button>
      <Dropdown.Popover placement="bottom start">
        <Dropdown.Menu
          aria-label={t("Agent wechseln")}
          selectionMode="single"
          selectedKeys={
            new Set(
              conversations.some((agent) => agent.id === selected)
                ? [selected!]
                : [],
            )
          }
          onAction={(key) => onSelect(String(key))}
        >
          {conversations.map((agent) => (
            <Dropdown.Item
              id={agent.id}
              key={agent.id}
              className="min-h-11"
              textValue={agent.name}
            >
              <Label>
                {agent.name}
                {agent.role === "subagent" && (
                  <span className="text-muted"> · {t("Subagent")}</span>
                )}
              </Label>
              <Dropdown.ItemIndicator />
            </Dropdown.Item>
          ))}
        </Dropdown.Menu>
      </Dropdown.Popover>
    </Dropdown>
  );
}
