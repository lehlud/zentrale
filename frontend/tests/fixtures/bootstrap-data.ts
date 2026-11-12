import { agent, agents, config, project } from "./visual-data";

// Keep fixture content identical across the legacy core query, the compact
// first-paint bootstrap, and the on-demand details query.
export function isTranscriptQuery(query: string) {
  return (
    query.includes("recentMessages") ||
    query.includes("conversationBootstrap(") ||
    query.includes("usageSummary(")
  );
}

export function selectedAgentId(
  variables: Record<string, unknown>,
  fallback = agent.id,
) {
  return variables.id || variables.agentId || fallback;
}

export function bootstrapData(
  query: string,
  data: unknown,
  navigation: { project?: unknown; agents?: unknown[] } = {},
) {
  const core = data as Record<string, unknown>;
  if (!query.includes("conversationBootstrap(")) {
    if (query.includes("usageSummary(") && !query.includes("recentMessages")) {
      return {
        agent: core.agent
          ? {
              id: (core.agent as Record<string, unknown>).id,
              summary: (core.agent as Record<string, unknown>).summary ?? "",
            }
          : null,
        jobs: core.jobs ?? [],
        approvals: core.approvals ?? [],
        notes: core.notes ?? [],
        usageSummary: core.usageSummary,
      };
    }
    return data;
  }
  return {
    conversationBootstrap: {
      project: navigation.project ?? project,
      agents: navigation.agents ?? agents,
      agent: core.agent ?? null,
      messages: core.messages ?? [],
      goal: core.goal ?? null,
      agentInputs: core.agentInputs ?? [],
      agentRunState: core.agentRunState ?? null,
      questions: core.questions ?? [],
      jobs: ((core.jobs ?? []) as Array<Record<string, unknown>>)
        .filter((job) => job.status === "running")
        .slice(0, 100)
        .map(({ output: _output, ...job }) => ({
          ...job,
          timeoutMs: job.timeoutMs ?? 0,
        })),
      modelDefaults: config.modelDefaults,
      machineName: config.machines[0]?.name ?? "",
      sandboxMode: config.machines[0]?.sandbox ?? "",
      sandboxStatus:
        ((core.sandboxStatuses ?? []) as Array<Record<string, unknown>>).find(
          (status) =>
            status.machineId ===
            (core.agent as Record<string, unknown> | undefined)?.machineId,
        ) ?? null,
    },
  };
}
