import { Chip } from "@heroui/react";
import type { SandboxStatus } from "./generated";
import { useI18n } from "./preferences";

const sandboxNames: Record<string, string> = {
  auto: "Automatisch",
  microvm: "MicroVM",
  bwrap: "Bubblewrap",
  none: "Ohne Sandbox",
  unknown: "noch nicht ermittelt",
};

export function SandboxBadge({
  mode = "auto",
  status,
  showCheckedAt = false,
  compact = false,
}: {
  mode?: string;
  status?: SandboxStatus;
  showCheckedAt?: boolean;
  compact?: boolean;
}) {
  const { t, numberLocale } = useI18n();
  const observed = status?.mode === mode ? status : undefined;
  const selected = observed?.level;
  const name = (level: string) => t(sandboxNames[level] ?? level);
  const selection =
    mode !== "auto"
      ? name(mode)
      : selected
        ? `${name(mode)} → ${name(selected)}`
        : `${name(mode)} · ${t(observed?.error ? "Ermittlung fehlgeschlagen" : "noch nicht ermittelt")}`;

  if (compact)
    return (
      <span className="text-[11px] text-muted">
        {t("Sandbox: {selection}", { selection })}
      </span>
    );
  return (
    <span className="inline-flex max-w-full flex-wrap items-center gap-2">
      <Chip
        size="sm"
        variant="soft"
        color={selected === "none" || mode === "none" ? "warning" : "default"}
      >
        {t("Sandbox: {selection}", { selection })}
      </Chip>
      {showCheckedAt && observed?.checkedAt && (
        <time dateTime={observed.checkedAt} className="text-xs text-muted">
          {t("Zuletzt ermittelt: {time}", {
            time: new Date(observed.checkedAt).toLocaleString(numberLocale, {
              dateStyle: "short",
              timeStyle: "short",
            }),
          })}
        </time>
      )}
    </span>
  );
}
