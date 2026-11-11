import { Accordion } from "@heroui/react";
import { ActivityFields, record } from "./activity-values";
import { MessageMarkdown } from "./message-markdown";
import { useI18n } from "./preferences";

// Keep the model-facing routing/security notice in the stored event, not in the
// reader's normal transcript. Both sides of a question use the same disclosure.
export function QuestionExchange({
  value,
}: {
  value: Record<string, unknown>;
}) {
  const { t, showDiagnostics } = useI18n();
  const question = record(value.question);
  const answered = value.kind === "parent_question_answer";
  const title = typeof question.title === "string" ? question.title : "";
  const answer = typeof question.answer === "string" ? question.answer : "";
  const options = Array.isArray(question.options)
    ? question.options.filter(
        (option): option is string => typeof option === "string",
      )
    : [];
  return (
    <Accordion hideSeparator className="min-w-0">
      <Accordion.Item id="question-exchange">
        <Accordion.Heading>
          <Accordion.Trigger className="min-h-11 min-w-0 gap-3 text-left text-sm">
            <span className="shrink-0 font-medium">
              {t(
                answered
                  ? "Antwort des Hauptagenten"
                  : "Rückfrage des Subagenten",
              )}
            </span>
            <span className="min-w-0 flex-1 truncate text-muted">{title}</span>
            <Accordion.Indicator className="shrink-0" />
          </Accordion.Trigger>
        </Accordion.Heading>
        <Accordion.Panel>
          <Accordion.Body className="space-y-3 pb-3 text-sm">
            <p className="whitespace-pre-wrap break-words font-medium [overflow-wrap:anywhere]">
              {title}
            </p>
            {options.length > 0 && (
              <ul className="list-disc space-y-1 pl-5">
                {options.map((option, index) => (
                  <li key={index} className="whitespace-pre-wrap break-words">
                    {option}
                  </li>
                ))}
              </ul>
            )}
            {answered && answer && (
              <div className="border-l-2 border-accent pl-3">
                <MessageMarkdown content={answer} />
              </div>
            )}
            {showDiagnostics && (
              <details>
                <summary>{t("Technische Details")}</summary>
                <ActivityFields value={value} />
              </details>
            )}
          </Accordion.Body>
        </Accordion.Panel>
      </Accordion.Item>
    </Accordion>
  );
}
