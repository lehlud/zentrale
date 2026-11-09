import { useI18n } from "./preferences";

export function WorkingIndicator({ label }: { label?: string }) {
  const { t } = useI18n();
  return (
    <div
      role="status"
      className="flex min-h-8 items-center gap-2.5 py-2 text-xs text-muted"
    >
      <span className="working-indicator" aria-hidden="true">
        <span />
        <span />
        <span />
      </span>
      <span>{label ?? t("Arbeitet …")}</span>
    </div>
  );
}
