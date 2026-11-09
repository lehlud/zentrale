import type { Message } from "./generated";

export type ToolCall = {
  id: string;
  name: string;
  providerTool: string;
  arguments: unknown;
  wire: string;
  network?: boolean;
  justification?: string;
};

function object(value: unknown): Record<string, unknown> {
  return value && typeof value === "object" && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : {};
}

export function parseJSON(value: string): unknown {
  try {
    return JSON.parse(value);
  } catch {
    return undefined;
  }
}

// Providers store their native wire formats. Normalize only the tool metadata
// needed by the transcript; the complete response remains available to inspect.
export function toolCalls(message: Message): ToolCall[] {
  return parseToolCalls(message.wire);
}

export function parseToolCalls(wire: string): ToolCall[] {
  const native = parseJSON(wire);
  const envelope = object(native);
  const entries = Array.isArray(native)
    ? native
    : Array.isArray(envelope.tool_calls)
      ? envelope.tool_calls
      : Array.isArray(envelope.parts)
        ? envelope.parts
        : [envelope];
  return entries.flatMap((value, index) => {
    const entry = object(value);
    const call = object(entry.function ?? entry.functionCall ?? entry);
    const name = call.name ?? call.action;
    if (typeof name !== "string") return [];
    const raw = call.arguments ?? call.args ?? call.input ?? {};
    const args = typeof raw === "string" ? (parseJSON(raw) ?? raw) : raw;
    const action = object(args);
    const wrapped =
      (name === "harness" || name === "inspect") &&
      typeof action.action === "string";
    const actionArguments =
      typeof action.arguments === "string"
        ? (parseJSON(action.arguments) ?? action.arguments)
        : action.arguments;
    return [
      {
        id: String(
          entry.call_id ??
            entry.id ??
            (entry.functionCall ? `gemini-${index}` : ""),
        ),
        name: wrapped
          ? String(action.action)
          : name === "exec_command"
            ? "exec"
            : name,
        providerTool: name,
        arguments: wrapped
          ? (actionArguments ?? {})
          : name === "exec_command"
            ? { ...action, command: action.cmd }
            : args,
        wire,
        network:
          typeof action.network === "boolean" ? action.network : undefined,
        justification:
          typeof action.justification === "string"
            ? action.justification
            : undefined,
      },
    ];
  });
}

export function toolSummary(call?: Pick<ToolCall, "arguments">): string {
  const args = object(call?.arguments);
  if (typeof args.chars === "string" && args.chars)
    return args.chars
      .replaceAll("\u0003", "Ctrl+C")
      .replaceAll("\u0004", "Ctrl+D")
      .replaceAll("\u001b", "Esc")
      .split("\n")[0];
  if (args.eof === true) return "EOF";
  if (typeof args.tool === "string") return args.tool;
  if (typeof args.name === "string") return args.name;
  if (Array.isArray(args.packages))
    return args.packages.filter((item) => typeof item === "string").join(", ");
  for (const key of [
    "command",
    "path",
    "filename",
    "url",
    "title",
    "query",
    "code",
    "explanation",
  ]) {
    if (typeof args[key] === "string") return args[key].split("\n")[0];
  }
  return "";
}

export function correlateToolCalls(messages: Message[]) {
  const callsById = new Map<string, ToolCall>();
  const callsByName = new Map<string, ToolCall>();
  const callsByMessage = new Map<string, ToolCall[]>();
  const resultCalls = new Map<string, ToolCall>();
  const completed = new Set<ToolCall>();
  for (const message of messages) {
    const calls = toolCalls(message);
    callsByMessage.set(message.id, calls);
    for (const call of calls) {
      if (call.id) callsById.set(call.id, call);
      callsByName.set(call.providerTool, call);
      callsByName.set(call.name, call);
    }
    if (message.role !== "tool") continue;
    const call = message.callId
      ? callsById.get(message.callId)
      : callsByName.get(message.toolName);
    if (call) {
      resultCalls.set(message.id, call);
      completed.add(call);
    }
  }
  // Some providers reuse call IDs in later turns. Match in chronological order
  // so an older result cannot mark a new, still-running action as completed.
  return { callsByMessage, resultCalls, completed };
}

export function toolOutcome(content: string) {
  const value = object(parseJSON(content));
  const result = object(value.result ?? value);
  const job = object(result.job ?? result);
  let error = typeof value.error === "string" ? value.error : "";
  if (!error && typeof job.error === "string") error = job.error;
  if (!error && (job.status === "failed" || job.status === "interrupted"))
    error = job.status;
  return {
    error,
    status:
      typeof job.status === "string"
        ? job.status
        : error
          ? "failed"
          : "completed",
    result: "result" in value ? value.result : (parseJSON(content) ?? content),
  };
}
