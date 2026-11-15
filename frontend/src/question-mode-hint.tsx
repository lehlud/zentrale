import { useI18n } from "./preferences";

export function QuestionModeHint({ mode }: { mode?: string }) {
  const { t } = useI18n();
  return (
    <p className="text-xs text-muted">
      {t(
        mode === "advisory"
          ? "Unabhängige Arbeit läuft weiter. Du kannst später antworten."
          : "Für die nächsten Schritte ist eine Antwort erforderlich.",
      )}
    </p>
  );
}
