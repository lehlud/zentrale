import { parseJSON, parseToolCalls } from "./agent-activity";
import {
  ActivityContent,
  ActivityRequest,
  toolLabel,
} from "./activity-details";
import { record } from "./activity-values";
import { useI18n } from "./preferences";

type ResponseText = { kind: "answer" | "reasoning"; text: string };

// Read only text intended for display. Opaque reasoning signatures and replay
// data are provider protocol, not user-facing explanations.
function responseText(value: unknown): ResponseText[] {
  if (Array.isArray(value)) return value.flatMap(responseText);
  if (value === null || typeof value !== "object") return [];
  const item = record(value);
  const texts: ResponseText[] = [];
  const add = (kind: ResponseText["kind"], text: unknown) => {
    if (typeof text === "string" && text.trim()) texts.push({ kind, text });
  };
  const reasoning =
    item.type === "reasoning" ||
    item.type === "thinking" ||
    item.thought === true;
  add(reasoning ? "reasoning" : "answer", item.text);
  add("reasoning", item.thinking);
  add("reasoning", item.reasoning);
  add("reasoning", item.reasoning_content);
  if (typeof item.content === "string") add("answer", item.content);
  else texts.push(...responseText(item.content));
  if (Array.isArray(item.summary))
    for (const entry of item.summary) add("reasoning", record(entry).text);
  texts.push(...responseText(item.parts), ...responseText(item.output));
  return texts;
}

export function ModelResponse({ content }: { content: string }) {
  const { t } = useI18n();
  const native = parseJSON(content);
  const calls = parseToolCalls(content);
  const texts = responseText(native);
  if (!calls.length && !texts.length)
    return (
      <p className="text-sm leading-7 text-muted">
        {t("Für diese Antwort sind keine lesbaren Details verfügbar.")}
      </p>
    );
  return (
    <div className="min-w-0 space-y-6">
      {texts.map((item, index) => (
        <section key={index} className="min-w-0 space-y-2">
          <h3 className="text-xs font-medium text-muted">
            {t(item.kind === "reasoning" ? "Überlegungen" : "Antwort")}
          </h3>
          <ActivityContent content={item.text} />
        </section>
      ))}
      {calls.map((call, index) => (
        <section
          key={call.id || index}
          className="min-w-0 space-y-3 border-t border-border pt-4 first:border-t-0 first:pt-0"
        >
          <h3 className="text-sm font-medium">{t(toolLabel(call.name))}</h3>
          <ActivityRequest
            name={call.name}
            arguments={call.arguments}
            network={call.network}
            justification={call.justification}
          />
        </section>
      ))}
    </div>
  );
}
