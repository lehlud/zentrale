import { Accordion } from "@heroui/react";

import { ActivityFields } from "./activity-values";
import { MessageMarkdown } from "./message-markdown";
import { useI18n } from "./preferences";

export function AgentCoordination({
  value,
}: {
  value: Record<string, unknown>;
}) {
  const { t, showDiagnostics } = useI18n();
  const text = typeof value.text === "string" ? value.text : "";

  return (
    <Accordion hideSeparator className="min-w-0">
      <Accordion.Item id="agent-coordination">
        {({ isExpanded }) => (
          <>
            <Accordion.Heading>
              <Accordion.Trigger className="min-h-11 min-w-0 gap-3 text-left text-sm">
                <span className="min-w-0 flex-1 font-medium">
                  {t("Anweisung des Hauptagenten")}
                </span>
                <Accordion.Indicator className="shrink-0" />
              </Accordion.Trigger>
            </Accordion.Heading>
            <Accordion.Panel className="transition-none">
              {isExpanded && (
                <Accordion.Body className="space-y-3 pb-3 text-sm">
                  <MessageMarkdown content={text} />
                  {showDiagnostics && (
                    <details>
                      <summary>{t("Technische Details")}</summary>
                      <ActivityFields value={value} />
                    </details>
                  )}
                </Accordion.Body>
              )}
            </Accordion.Panel>
          </>
        )}
      </Accordion.Item>
    </Accordion>
  );
}
