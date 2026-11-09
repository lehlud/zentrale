import { ActivityFields, ActivityValue, record } from "./activity-values";
import { CodeSnippet } from "./code-snippet";
import { MessageMarkdown } from "./message-markdown";
import { useI18n } from "./preferences";

export function MCPRequest({
  name,
  args,
  network,
}: {
  name: string;
  args: Record<string, unknown>;
  network?: boolean;
}) {
  const { t, showDiagnostics } = useI18n();
  return (
    <div className="min-w-0 space-y-3">
      {typeof args.command === "string" && (
        <CodeSnippet text={args.command} label={t("Befehl")} wrap />
      )}
      {typeof args.url === "string" && (
        <p className="break-all font-mono text-sm">{args.url}</p>
      )}
      {name === "mcp_call" && <ActivityFields value={record(args.arguments)} />}
      {showDiagnostics &&
        name === "mcp_tools" &&
        typeof args.query === "string" && (
          <p className="text-sm">{args.query}</p>
        )}
      {network && (name === "mcp_connect" || showDiagnostics) && (
        <ActivityFields value={{ network }} />
      )}
      {showDiagnostics && (
        <ActivityFields
          value={args}
          omit={["command", "url", "arguments", "query"]}
        />
      )}
    </div>
  );
}

export function MCPResult({ name, value }: { name: string; value: unknown }) {
  const { t, showDiagnostics } = useI18n();
  const result = record(value);
  const tools = Array.isArray(result.tools) ? result.tools : undefined;
  const schema = record(result.inputSchema);
  if (name === "mcp_connect" || name === "mcp_disconnect")
    return (
      <p className="text-sm">
        {t(
          name === "mcp_connect"
            ? "MCP-Server verbunden."
            : "MCP-Verbindung beendet.",
        )}
      </p>
    );
  if (tools || name === "mcp_list") {
    const items = tools ?? (Array.isArray(value) ? value : []);
    return (
      <div className="space-y-3">
        {!items.length && (
          <p className="text-sm text-muted">
            {t(
              name === "mcp_list"
                ? "Keine MCP-Server verbunden."
                : "Keine passenden Werkzeuge.",
            )}
          </p>
        )}
        <ul className="space-y-3">
          {items.map((item, index) => {
            const tool = record(item);
            return (
              <li key={index} className="min-w-0 space-y-1">
                <p className="break-words font-mono text-sm">
                  {String(tool.name ?? "")}
                </p>
                {typeof tool.description === "string" && (
                  <p className="whitespace-pre-wrap break-words text-sm text-muted">
                    {tool.description}
                  </p>
                )}
              </li>
            );
          })}
        </ul>
        {result.hasMore === true && (
          <p className="text-xs text-muted">
            {t("Weitere Werkzeuge verfügbar.")}
          </p>
        )}
      </div>
    );
  }
  if (result.inputSchema) {
    const required = Array.isArray(schema.required) ? schema.required : [];
    return (
      <div className="space-y-3">
        {typeof result.description === "string" && (
          <p className="text-sm">{result.description}</p>
        )}
        <dl className="space-y-3">
          {Object.entries(record(schema.properties)).map(
            ([key, definition]) => {
              const field = record(definition);
              return (
                <div key={key} className="min-w-0 space-y-1">
                  <dt className="break-all font-mono text-sm">
                    {key}
                    {required.includes(key) ? " *" : ""}
                  </dt>
                  <dd className="break-words text-sm text-muted">
                    {String(field.description ?? field.type ?? "")}
                  </dd>
                </div>
              );
            },
          )}
        </dl>
        {showDiagnostics && <ActivityValue value={result.inputSchema} />}
      </div>
    );
  }
  return (
    <div className="min-w-0 space-y-3">
      {Array.isArray(result.content) &&
        result.content.map((item, index) => {
          const content = record(item);
          if (content.omitted === true)
            return (
              <p key={index} className="text-sm text-muted">
                {t("Binärinhalt wird nicht als Text angezeigt.")}
              </p>
            );
          return typeof content.text === "string" ? (
            <MessageMarkdown key={index} content={content.text} />
          ) : (
            <ActivityValue key={index} value={content} />
          );
        })}
      {result.structuredContent != null && (
        <ActivityValue value={result.structuredContent} />
      )}
    </div>
  );
}
