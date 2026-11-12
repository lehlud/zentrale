import {
  agentFields,
  messageFields,
  type Transcript,
} from "./agent-transcript";
import type {
  Agent,
  ModelDefaults,
  Project,
  UsageSummary,
  SandboxStatus,
} from "./generated";

const compactAgentFields = agentFields.replace(/\s+summary\b/, "");

// One authenticated request also selects the default main agent server-side.
// No job output, approvals, notes, provider catalog or usage aggregate belongs
// on the first-paint critical path. Pagination continues using recentMessages.
export const conversationBootstrapQuery = `query($projectId:String!, $agentId:String) {
  conversationBootstrap(projectId:$projectId, agentId:$agentId) {
    project { id name directory managedWorkspace machineIds poolIds commitName commitEmail gitConnectionId repositoryId repositoryName }
    agents { ${compactAgentFields} }
    agent { ${compactAgentFields} }
    messages { ${messageFields} }
    goal { id agentId objective origin status requestedStatus requestReason sourceInputIds revision continuations blockedAttempts blocker reason evidence createdAt updatedAt }
    agentInputs { id agentId projectId text mode status createdAt deliveredAt attachments { id name mimeType size path machineId } }
    agentRunState { agentId phase error hostKey { host fingerprint publicKey } }
    questions { id title options status answer agentId }
    jobs { id command status error startedAt machineId agentId timeoutMs }
    modelDefaults { main { providerId modelId reasoningLevel } approver { providerId modelId reasoningLevel } }
    machineName sandboxMode
    sandboxStatus { machineId mode level checkedAt error }
  }
}`;

export type ConversationBootstrap = Pick<
  Transcript,
  "messages" | "goal" | "agentInputs" | "agentRunState" | "questions" | "jobs"
> & {
  project: Project;
  agents: Agent[];
  agent: Agent | null;
  modelDefaults: ModelDefaults;
  machineName: string;
  sandboxMode: string;
  sandboxStatus?: SandboxStatus | null;
};

export const emptyUsage: UsageSummary = {
  inputTokens: 0,
  outputTokens: 0,
  cachedTokens: 0,
  cacheInputTokens: 0,
  cacheReadTokens: 0,
  cacheWriteTokens: 0,
  unknownTokenCounts: 0,
  unknownCacheWrites: 0,
  realCost: 0,
  fictionalCost: 0,
  estimated: false,
  unknownPrices: 0,
  requestCount: 0,
};

// Defaults are presentation placeholders only; the detail panel fetches actual
// values when opened. Never merge these defaults over loaded detail data.
export function bootstrapTranscript(
  value: ConversationBootstrap,
): Transcript | undefined {
  if (!value.agent) return undefined;
  return {
    agent: value.agent,
    messages: value.messages,
    goal: value.goal,
    agentInputs: value.agentInputs,
    agentRunState: value.agentRunState,
    questions: value.questions,
    jobs: value.jobs,
    approvals: [],
    notes: [],
    usageSummary: { ...emptyUsage },
  };
}
